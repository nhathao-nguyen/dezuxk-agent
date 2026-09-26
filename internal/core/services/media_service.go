package services

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

type MediaService struct {
	models             *domain.ModelRegistry
	sessions           ports.SessionRepository
	upstream           ports.UpstreamGoogleTransport
	wire               ports.WireCodec
	flow               ports.FlowClient
	projects           ports.FlowProjectRecorder
	storage            ports.MediaRepository
	metrics            *domain.ContractMetrics
	creditCosts        *domain.FlowCreditCostRegistry
	projectTitlePrefix string
}

func NewMediaService(
	models *domain.ModelRegistry,
	sessions ports.SessionRepository,
	upstream ports.UpstreamGoogleTransport,
	wire ports.WireCodec,
	flow ports.FlowClient,
	projects ports.FlowProjectRecorder,
	metrics *domain.ContractMetrics,
) *MediaService {
	return &MediaService{
		models:   models,
		sessions: sessions,
		upstream: upstream,
		wire:     wire,
		flow:     flow,
		projects: projects,
		metrics:  metrics,
	}
}

// SetStorage gán MediaRepository để tự động tải và cache media cục bộ
func (s *MediaService) SetStorage(storage ports.MediaRepository) {
	s.storage = storage
}

// SetCreditCosts gán registry chi phí tín dụng động nạp từ config/rules engine
func (s *MediaService) SetCreditCosts(costs *domain.FlowCreditCostRegistry) {
	s.creditCosts = costs
}

// SetProjectTitlePrefix gán tiền tố sinh tên project động
func (s *MediaService) SetProjectTitlePrefix(prefix string) {
	s.projectTitlePrefix = prefix
}

func (s *MediaService) resolveCost(operation string, fallback int) int {
	if s.creditCosts != nil {
		return s.creditCosts.GetCost(operation, fallback)
	}
	return fallback
}

func (s *MediaService) resolveProjectTitle(accountID string) string {
	prefix := s.projectTitlePrefix
	if prefix == "" {
		prefix = "Studio"
	}
	ts := time.Now().UTC().Format("20060102-150405")
	if accountID != "" {
		return fmt.Sprintf("%s-%s-%s", prefix, accountID, ts)
	}
	return fmt.Sprintf("%s-%s", prefix, ts)
}

func (s *MediaService) GenerateImage(ctx context.Context, req *domain.ImageGenerationRequest) (*domain.ImageGenerationResult, error) {
	model, err := s.prepare(reqModel(req), domain.CapImage, domain.OpImages)
	if err != nil {
		return nil, err
	}
	if err := validateImage(req); err != nil {
		return nil, err
	}
	aspect := 0
	if req.AspectRatio != "" {
		aspect, _ = domain.VideoAspectCode(req.AspectRatio)
	}
	extracted, err := s.generate(ctx, model, domain.OpImages, domain.FlowMediaInput{
		Prompt:     strings.TrimSpace(req.Prompt),
		ModelID:    model.InternalBackendID,
		AspectCode: aspect,
	})
	if err != nil {
		return nil, err
	}

	finalURL := extracted.URL
	if s.storage != nil && extracted.URL != "" {
		cookies := ""
		userAgent := ""
		if acc, err := s.sessions.GetAvailable(ctx, domain.ServiceFlow, 0); err == nil && acc != nil {
			if acc.Jar != nil {
				cookies = acc.Jar.GetCookieHeader(true)
			}
			userAgent = acc.UserAgent
		}
		if cached, cacheErr := s.storage.DownloadAndCacheWithAuth(ctx, extracted.URL, domain.MediaImagePNG, req.Prompt, model.ID, cookies, userAgent); cacheErr == nil && cached != nil {
			finalURL = cached.LocalURL
		}
	}

	return &domain.ImageGenerationResult{
		URL:            finalURL,
		MimeType:       extracted.MimeType,
		UnmappedFields: extracted.Unmapped,
		SpecVersion:    domain.FlowMediaSpecVersion,
		CreditsBalance: extracted.CreditsBalance,
	}, nil
}

func (s *MediaService) GenerateVideo(ctx context.Context, req *domain.VideoGenerationRequest) (*domain.VideoGenerationResult, error) {
	if req == nil {
		return nil, domain.InvalidRequest(domain.OpVideos, domain.OriginStreamChat, domain.ServiceFlow, "thiếu yêu cầu")
	}
	model, err := s.prepare(req.Model, domain.CapVideo, domain.OpVideos)
	if err != nil {
		return nil, err
	}
	if err := validateVideo(req, model); err != nil {
		return nil, err
	}
	aspect, _ := domain.VideoAspectCode(req.AspectRatio)

	var cameraMotion *domain.CameraMotionConfig
	if req.CameraConfig != nil {
		cameraMotion = req.CameraConfig
	} else if req.Camera != nil {
		cameraMotion = &domain.CameraMotionConfig{
			MotionPreset: domain.PresetCustomVector,
			MotionVector: *req.Camera,
		}
	}

	extracted, err := s.generate(ctx, model, domain.OpVideos, domain.FlowMediaInput{
		Prompt:          strings.TrimSpace(req.Prompt),
		ModelID:         model.InternalBackendID,
		DurationSeconds: req.Duration,
		AspectCode:      aspect,
		Seed:            req.Seed,
		StartImageToken: req.ImageURL,
		CameraMotion:    cameraMotion,
	})
	if err != nil {
		return nil, err
	}

	finalURL := extracted.URL
	if s.storage != nil && extracted.URL != "" {
		cookies := ""
		userAgent := ""
		if acc, err := s.sessions.GetAvailable(ctx, domain.ServiceFlow, 0); err == nil && acc != nil {
			if acc.Jar != nil {
				cookies = acc.Jar.GetCookieHeader(true)
			}
			userAgent = acc.UserAgent
		}
		if cached, cacheErr := s.storage.DownloadAndCacheWithAuth(ctx, extracted.URL, domain.MediaVideoMP4, req.Prompt, model.ID, cookies, userAgent); cacheErr == nil && cached != nil {
			finalURL = cached.LocalURL
		}
	}

	return &domain.VideoGenerationResult{
		URL:            finalURL,
		MimeType:       extracted.MimeType,
		UnmappedFields: extracted.Unmapped,
		SpecVersion:    domain.FlowMediaSpecVersion,
		CreditsBalance: extracted.CreditsBalance,
	}, nil
}

// ExtendVideo nối dài video có sẵn thêm 4s/6s dùng veo_3_1_extend
func (s *MediaService) ExtendVideo(ctx context.Context, req *domain.VideoExtensionRequest) (*domain.VideoGenerationResult, error) {
	if err := domain.ValidateVideoExtension(req); err != nil {
		return nil, domain.InvalidRequest(domain.OpFlowExtendVideo, domain.OriginExtendVideo, domain.ServiceFlow, err.Error())
	}

	modelID := req.Model
	if modelID == "" {
		modelID = "veo-3.1-fast"
	}
	_, err := s.prepare(modelID, domain.CapVideo, domain.OpFlowExtendVideo)
	if err != nil {
		return nil, err
	}

	var extracted domain.MediaExtract
	err = s.withFlow(ctx, domain.OpFlowExtendVideo, func(account *domain.ManagedAccount) error {
		balance, err := s.flow.GetCreditsBalance(ctx, account)
		if err != nil {
			return err
		}
		cost := s.resolveCost(domain.CreditOpVideoExtend4s, 20)
		if req.ExtensionDuration == 6 {
			cost = s.resolveCost(domain.CreditOpVideoExtend6s, 30)
		}
		if balance.Amount < cost {
			return domain.InvalidRequest(domain.OpFlowExtendVideo, domain.OriginExtendVideo, domain.ServiceFlow, fmt.Sprintf("không đủ credit để nối dài video (yêu cầu %d credits)", cost))
		}

		projectID, sessionToken := account.GetFlowMediaSecrets()
		if sessionToken == "" {
			return domain.Unauthenticated(domain.OpFlowExtendVideo, domain.OriginExtendVideo, domain.ServiceFlow, "phiên Flow chưa có session token")
		}
		if projectID == "" {
			projectID, err = s.flow.CreateProject(ctx, account, s.resolveProjectTitle(account.ID))
			if err != nil {
				return err
			}
			account.SetFlowProjectID(projectID)
		}
		if err := s.flow.RegisterSessionLock(ctx, account, projectID); err != nil {
			return err
		}

		reqUUID, err := newMediaRequestID()
		if err != nil {
			return err
		}

		input := domain.FlowMediaInput{
			RequestID:       reqUUID,
			Prompt:          req.Prompt,
			ProjectID:       projectID,
			SessionToken:    sessionToken,
			ModelID:         "veo_3_1_extend",
			DurationSeconds: req.ExtensionDuration,
			CameraMotion:    req.CameraMotion,
			Ingredients: []any{
				map[string]any{
					"ingredient_type":       4,
					"source_video_asset_id": req.SourceVideoAssetID,
				},
			},
		}

		attempt, err := s.wire.MaterializeFlowMedia(account, input)
		if err != nil {
			return err
		}

		callCtx, cancel := boundStream(s.upstream, ctx)
		defer cancel()
		reqPath := attempt.Path
		if attempt.TargetHost != "" && !strings.HasPrefix(reqPath, "http") {
			reqPath = attempt.TargetHost + reqPath
		}
		resp, err := s.upstream.DoRequest(callCtx, account, domain.ServiceFlow, http.MethodPost, reqPath, strings.NewReader(attempt.Body), attempt.ContentType)
		if err != nil {
			return domain.CodecTransport(domain.OriginExtendVideo, domain.ServiceFlow, err)
		}
		defer resp.Body.Close()

		extracted, err = s.wire.DematerializeFlowMedia(resp, s.metrics.Bind(domain.OpFlowExtendVideo))
		if err != nil {
			return err
		}
		if newBal, balErr := s.flow.GetCreditsBalance(ctx, account); balErr == nil {
			extracted.CreditsBalance = &newBal.Amount
			account.CreditsBalance = newBal.Amount
		}
		return nil
	})
	if err != nil {
		return nil, domain.EnsureGateway(err, domain.OpFlowExtendVideo, domain.ServiceFlow)
	}

	finalURL := extracted.URL
	if s.storage != nil && extracted.URL != "" {
		cookies := ""
		userAgent := ""
		if acc, err := s.sessions.GetAvailable(ctx, domain.ServiceFlow, 0); err == nil && acc != nil {
			if acc.Jar != nil {
				cookies = acc.Jar.GetCookieHeader(true)
			}
			userAgent = acc.UserAgent
		}
		if cached, cacheErr := s.storage.DownloadAndCacheWithAuth(ctx, extracted.URL, domain.MediaVideoMP4, req.Prompt, "veo_3_1_extend", cookies, userAgent); cacheErr == nil && cached != nil {
			finalURL = cached.LocalURL
		}
	}

	return &domain.VideoGenerationResult{
		URL:            finalURL,
		MimeType:       extracted.MimeType,
		UnmappedFields: extracted.Unmapped,
		SpecVersion:    domain.FlowMediaSpecVersion,
		CreditsBalance: extracted.CreditsBalance,
	}, nil
}

// UpsampleVideo4K nâng cấp video Veo lên 4K (RPC uW3g7e - trạng thái nghiên cứu)
func (s *MediaService) UpsampleVideo4K(ctx context.Context, req *domain.Upsample4KRequest) (*domain.Upsample4KResponse, error) {
	return nil, domain.UpstreamRejected(domain.OpFlowUpsample4K, domain.OriginUpsample4K, domain.ServiceFlow, "tính năng đang ở mức nghiên cứu (chưa có code gọi upstream)")
}

// GenerateAudio tạo nhạc nền MusicFX (RPC mX9w1 - trạng thái nghiên cứu)
func (s *MediaService) GenerateAudio(ctx context.Context, req domain.FlowMusicRequest) (*domain.FlowMusicResponse, error) {
	return nil, domain.UpstreamRejected(domain.OpFlowAudio, domain.OriginMusicFX, domain.ServiceFlow, "tính năng đang ở mức nghiên cứu (chưa có code gọi upstream)")
}

// ListVoicePersonas lấy danh sách 30 giọng đọc AI trực tiếp qua RPC Zzl0ze từ Google Flow (docs-2 mục character_and_reference.md)
func (s *MediaService) ListVoicePersonas(ctx context.Context) ([]domain.VoicePersona, error) {
	var personas []domain.VoicePersona
	err := s.withFlow(ctx, "flow.voices", func(account *domain.ManagedAccount) error {
		projectID, _ := account.GetFlowMediaSecrets()
		if projectID == "" {
			var err error
			projectID, err = s.flow.CreateProject(ctx, account, s.resolveProjectTitle(account.ID))
			if err != nil {
				return err
			}
			account.SetFlowProjectID(projectID)
			if s.projects != nil {
				_ = s.projects.RememberFlowProject(account.ID, projectID)
			}
		}
		var callErr error
		personas, callErr = s.flow.ListVoicePersonas(ctx, account, projectID)
		return callErr
	})
	if err != nil || len(personas) == 0 {
		return domain.DefaultVoicePersonas(), nil
	}
	return personas, nil
}

// ListProjects lấy danh sách dự án qua RPC UpteDb
func (s *MediaService) ListProjects(ctx context.Context) ([]domain.FlowProject, error) {
	var list []domain.FlowProject
	err := s.withFlow(ctx, domain.OpFlowProjects, func(account *domain.ManagedAccount) error {
		var callErr error
		list, callErr = s.flow.ListProjects(ctx, account)
		return callErr
	})
	return list, err
}

// CreateProject tạo dự án mới qua RPC jHPbke
func (s *MediaService) CreateProject(ctx context.Context, title string) (string, error) {
	var projectID string
	err := s.withFlow(ctx, domain.OpFlowCreateProject, func(account *domain.ManagedAccount) error {
		var callErr error
		projectID, callErr = s.flow.CreateProject(ctx, account, title)
		return callErr
	})
	return projectID, err
}

// MoveProjectToTrash chuyển dự án vào thùng rác qua RPC dK3x9
func (s *MediaService) MoveProjectToTrash(ctx context.Context, projectID string) error {
	return s.withFlow(ctx, domain.OpFlowTrash, func(account *domain.ManagedAccount) error {
		return s.flow.MoveProjectToTrash(ctx, account, projectID)
	})
}

// ListTrash lấy danh sách thùng rác qua RPC tB6q8
func (s *MediaService) ListTrash(ctx context.Context) ([]domain.FlowTrashProject, error) {
	var list []domain.FlowTrashProject
	err := s.withFlow(ctx, domain.OpFlowTrash, func(account *domain.ManagedAccount) error {
		var callErr error
		list, callErr = s.flow.ListTrash(ctx, account)
		return callErr
	})
	return list, err
}

// RestoreProject khôi phục dự án khỏi thùng rác qua RPC rS4y1
func (s *MediaService) RestoreProject(ctx context.Context, projectID string) error {
	return s.withFlow(ctx, domain.OpFlowTrash, func(account *domain.ManagedAccount) error {
		return s.flow.RestoreProject(ctx, account, projectID)
	})
}

// DeleteProjectPermanently xóa vĩnh viễn dự án qua RPC mrlkwd
func (s *MediaService) DeleteProjectPermanently(ctx context.Context, projectID string) error {
	return s.withFlow(ctx, domain.OpFlowProjects, func(account *domain.ManagedAccount) error {
		return s.flow.DeleteProjectPermanently(ctx, account, projectID)
	})
}

// ListMediaGallery truy xuất toàn bộ tác phẩm media đã cache trên máy tính
func (s *MediaService) ListMediaGallery(ctx context.Context, kind domain.MediaKind) ([]*domain.MediaAsset, error) {
	if s.storage == nil {
		return []*domain.MediaAsset{}, nil
	}
	return s.storage.ListAssets(ctx, kind)
}

func reqModel(req *domain.ImageGenerationRequest) string {
	if req == nil {
		return ""
	}
	return req.Model
}

func (s *MediaService) prepare(modelID string, capability domain.ModelCapability, operation string) (*domain.ModelDescriptor, error) {
	if strings.TrimSpace(modelID) == "" {
		return nil, domain.InvalidRequest(operation, domain.OriginStreamChat, domain.ServiceFlow, "thiếu mô hình")
	}
	model, err := s.models.MustFind(modelID)
	if err != nil || !hasCapability(*model, capability) || model.TargetService != domain.ServiceFlow {
		return nil, domain.InvalidRequest(operation, domain.OriginStreamChat, domain.ServiceFlow, "mô hình không dùng cho yêu cầu này")
	}
	return model, nil
}

func hasCapability(model domain.ModelDescriptor, want domain.ModelCapability) bool {
	for _, cap := range model.Capabilities {
		if cap == want {
			return true
		}
	}
	return false
}

func validateImage(req *domain.ImageGenerationRequest) error {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return domain.InvalidRequest(domain.OpImages, domain.OriginStreamChat, domain.ServiceFlow, "thiếu mô tả ảnh")
	}
	if req.N != 0 && req.N != 1 {
		return domain.InvalidRequest(domain.OpImages, domain.OriginStreamChat, domain.ServiceFlow, "spec này chỉ tạo một ảnh")
	}
	return nil
}

func validateVideo(req *domain.VideoGenerationRequest, model *domain.ModelDescriptor) error {
	if strings.TrimSpace(req.Prompt) == "" {
		return domain.InvalidRequest(domain.OpVideos, domain.OriginStreamChat, domain.ServiceFlow, "thiếu mô tả video")
	}
	if !containsInt(model.SupportedDurations, req.Duration) {
		return domain.InvalidRequest(domain.OpVideos, domain.OriginStreamChat, domain.ServiceFlow, "thời lượng không thuộc mô hình")
	}
	if _, ok := domain.VideoAspectCode(req.AspectRatio); !ok || !containsString(model.SupportedAspects, req.AspectRatio) {
		return domain.InvalidRequest(domain.OpVideos, domain.OriginStreamChat, domain.ServiceFlow, "tỷ lệ khung hình không thuộc mô hình")
	}
	if req.CameraConfig != nil {
		if err := req.CameraConfig.Validate(); err != nil {
			return domain.InvalidRequest(domain.OpVideos, domain.OriginStreamChat, domain.ServiceFlow, err.Error())
		}
	}
	return nil
}

func (s *MediaService) generate(ctx context.Context, model *domain.ModelDescriptor, operation string, input domain.FlowMediaInput) (domain.MediaExtract, error) {
	var extracted domain.MediaExtract
	err := s.withFlow(ctx, operation, func(account *domain.ManagedAccount) error {
		var callErr error
		extracted, callErr = s.generateOne(ctx, account, model, operation, input)
		return callErr
	})
	if err != nil {
		err = domain.EnsureGateway(err, operation, domain.ServiceFlow)
	}
	return extracted, err
}

func (s *MediaService) generateOne(ctx context.Context, account *domain.ManagedAccount, model *domain.ModelDescriptor, operation string, input domain.FlowMediaInput) (domain.MediaExtract, error) {
	if s.flow == nil {
		return domain.MediaExtract{}, domain.UpstreamRejected(operation, domain.OriginStreamChat, domain.ServiceFlow, "chưa có client Flow")
	}
	balance, err := s.flow.GetCreditsBalance(ctx, account)
	if err != nil {
		return domain.MediaExtract{}, err
	}
	if model.CreditCostPerUnit > 0 && balance.Amount < model.CreditCostPerUnit {
		return domain.MediaExtract{}, domain.InvalidRequest(operation, domain.OriginStreamChat, domain.ServiceFlow, "không đủ số dư")
	}
	projectID, sessionToken := account.GetFlowMediaSecrets()
	if sessionToken == "" {
		return domain.MediaExtract{}, domain.Unauthenticated(operation, domain.OriginStreamChat, domain.ServiceFlow, "phiên Flow chưa có session state token").WithPublicStatus(http.StatusServiceUnavailable)
	}
	if projectID == "" {
		projectID, err = s.flow.CreateProject(ctx, account, s.resolveProjectTitle(account.ID))
		if err != nil {
			return domain.MediaExtract{}, err
		}
		account.SetFlowProjectID(projectID)
		if s.projects != nil {
			if err := s.projects.RememberFlowProject(account.ID, projectID); err != nil {
				return domain.MediaExtract{}, err
			}
		}
	}
	if err := s.flow.RegisterSessionLock(ctx, account, projectID); err != nil {
		return domain.MediaExtract{}, err
	}
	requestID, err := newMediaRequestID()
	if err != nil {
		return domain.MediaExtract{}, domain.UpstreamRejected(operation, domain.OriginStreamChat, domain.ServiceFlow, "không tạo được mã yêu cầu")
	}
	input.RequestID = requestID
	input.ProjectID = projectID
	input.SessionToken = sessionToken
	if s.wire == nil {
		return domain.MediaExtract{}, domain.CodecRejected(domain.OriginStreamChat, domain.ServiceFlow, "không đóng gói được yêu cầu media")
	}
	attempt, err := s.wire.MaterializeFlowMedia(account, input)
	if err != nil {
		return domain.MediaExtract{}, err
	}
	callCtx, cancel := boundStream(s.upstream, ctx)
	defer cancel()
	reqPath := attempt.Path
	if attempt.TargetHost != "" && !strings.HasPrefix(reqPath, "http") {
		reqPath = attempt.TargetHost + reqPath
	}
	resp, err := s.upstream.DoRequest(callCtx, account, domain.ServiceFlow, http.MethodPost, reqPath, strings.NewReader(attempt.Body), attempt.ContentType)
	if err != nil {
		return domain.MediaExtract{}, domain.CodecTransport(domain.OriginStreamChat, domain.ServiceFlow, err)
	}
	extracted, err := s.wire.DematerializeFlowMedia(resp, s.metrics.Bind(operation))
	if err != nil {
		return domain.MediaExtract{}, err
	}
	if newBal, balErr := s.flow.GetCreditsBalance(ctx, account); balErr == nil {
		extracted.CreditsBalance = &newBal.Amount
		account.CreditsBalance = newBal.Amount
	}
	return extracted, nil
}

func (s *MediaService) withFlow(ctx context.Context, operation string, fn func(*domain.ManagedAccount) error) error {
	account, err := s.sessions.GetAvailable(ctx, domain.ServiceFlow, 0)
	if err != nil {
		if _, ok := domain.AsGatewayError(err); ok {
			return err
		}
		return domain.Unauthenticated(operation, domain.OriginStreamChat, domain.ServiceFlow, "chưa có phiên Flow sẵn sàng").WithPublicStatus(http.StatusServiceUnavailable)
	}
	if !s.sessions.TryWriteLease(account, domain.ServiceFlow) {
		s.sessions.Release(account, nil)
		return domain.Conflict(operation, domain.OriginStreamChat, domain.ServiceFlow, "phiên đang bận một tác vụ ghi")
	}
	defer s.sessions.ReleaseWriteLease(account, domain.ServiceFlow)
	var callErr error
	defer func() { s.sessions.Release(account, callErr) }()
	callErr = session.RetryAfterRefresh(ctx, s.sessions, account, domain.ServiceFlow, func() error {
		return fn(account)
	})
	return callErr
}

func newMediaRequestID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
