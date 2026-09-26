package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

type ChatService struct {
	modelRegistry  *domain.ModelRegistry
	sessionRepo    ports.SessionRepository
	upstream       ports.UpstreamGoogleTransport
	wire           ports.WireCodec
	metrics        *domain.ContractMetrics
	storage        ports.MediaRepository
	visionResolver *VisionResolver
	tokenCounter   *TokenCounter
	failoverConfig config.FailoverConfig
}

func NewChatService(
	mr *domain.ModelRegistry,
	sr ports.SessionRepository,
	up ports.UpstreamGoogleTransport,
	wire ports.WireCodec,
	metrics *domain.ContractMetrics,
) *ChatService {
	return &ChatService{
		modelRegistry: mr,
		sessionRepo:   sr,
		upstream:      up,
		wire:          wire,
		metrics:       metrics,
		tokenCounter:  NewTokenCounter(config.TokensConfig{}),
		failoverConfig: config.FailoverConfig{
			MaxAttempts:     3,
			CoolingDuration: 60 * time.Second,
		},
	}
}

func (s *ChatService) SetStorage(storage ports.MediaRepository) {
	s.storage = storage
}

func (s *ChatService) SetVisionResolver(vr *VisionResolver) {
	s.visionResolver = vr
}

func (s *ChatService) SetTokenCounter(tc *TokenCounter) {
	s.tokenCounter = tc
}

func (s *ChatService) SetFailoverConfig(fc config.FailoverConfig) {
	s.failoverConfig = fc
}

func (s *ChatService) ExecuteChatStream(
	ctx context.Context,
	req *domain.OpenAIChatRequest,
	streamWriter io.Writer,
	flusher func(),
) error {
	modelDesc, err := s.prepareModel(req)
	if err != nil {
		return err
	}
	var flushedToClient bool
	return s.runGeminiFailover(ctx, func(account *domain.ManagedAccount) (bool, error) {
		callErr := s.streamRound(ctx, account, modelDesc, req, streamWriter, flusher, &flushedToClient)
		return !flushedToClient, callErr
	})
}

func (s *ChatService) ExecuteChatSync(
	ctx context.Context,
	req *domain.OpenAIChatRequest,
) (*domain.OpenAIChatResponse, error) {
	modelDesc, err := s.prepareModel(req)
	if err != nil {
		return nil, err
	}
	var resp *domain.OpenAIChatResponse
	err = s.runGeminiFailover(ctx, func(account *domain.ManagedAccount) (bool, error) {
		var callErr error
		resp, callErr = s.syncRound(ctx, account, modelDesc, req)
		return true, callErr
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *ChatService) prepareModel(req *domain.OpenAIChatRequest) (*domain.ModelDescriptor, error) {
	if req == nil || strings.TrimSpace(req.Model) == "" || len(req.Messages) == 0 {
		return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "thiếu hội thoại hoặc mô hình")
	}
	modelDesc, err := s.modelRegistry.MustFind(req.Model)
	if err != nil {
		return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "không có mô hình này")
	}
	if modelDesc.TargetService != domain.ServiceGemini {
		return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "mô hình này không dùng cho chat")
	}
	return modelDesc, nil
}

type failoverAction func(account *domain.ManagedAccount) (canRetry bool, err error)

func (s *ChatService) runGeminiFailover(ctx context.Context, action failoverAction) error {
	maxAttempts := s.failoverConfig.GetMaxAttempts()
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	triedAccounts := make(map[string]struct{})
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		account, err := s.sessionRepo.GetAvailable(ctx, domain.ServiceGemini, 0)
		if err != nil {
			if lastErr != nil {
				return domain.EnsureGateway(lastErr, domain.OpChatCompletions, domain.ServiceGemini)
			}
			if _, ok := domain.AsGatewayError(err); ok {
				return err
			}
			return domain.Unauthenticated(domain.OpChatCompletions, "", domain.ServiceGemini, "chưa có phiên Gemini sẵn sàng").WithPublicStatus(http.StatusServiceUnavailable)
		}

		if _, alreadyTried := triedAccounts[account.ID]; alreadyTried {
			s.sessionRepo.Release(account, nil)
			break
		}
		triedAccounts[account.ID] = struct{}{}

		if !s.sessionRepo.TryWriteLease(account, domain.ServiceGemini) {
			s.sessionRepo.Release(account, nil)
			return domain.Conflict(domain.OpChatCompletions, domain.OriginStreamGenerate, domain.ServiceGemini, "phiên đang bận một tác vụ ghi")
		}

		var canRetry bool
		var callErr error
		callErr = session.RetryAfterRefresh(ctx, s.sessionRepo, account, domain.ServiceGemini, func() error {
			var err error
			canRetry, err = action(account)
			return err
		})

		// 1. Client ngắt kết nối (Client Disconnect)
		if ctx.Err() != nil || errors.Is(callErr, context.Canceled) {
			s.sessionRepo.ReleaseWriteLease(account, domain.ServiceGemini)
			s.sessionRepo.Release(account, nil)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return callErr
		}

		// 2. Thành công
		if callErr == nil {
			s.sessionRepo.ReleaseWriteLease(account, domain.ServiceGemini)
			s.sessionRepo.Release(account, nil)
			return nil
		}

		// 3. Xử lý lỗi
		lastErr = callErr
		class, _, _ := domain.ClassifiedFailure(callErr)

		if class == domain.ClassRateLimited || class == domain.ClassUpstreamUnavailable {
			cooldown := s.failoverConfig.GetCoolingDuration()
			if cooldown <= 0 {
				cooldown = 60 * time.Second
			}
			_ = account.MoveService(domain.ServiceGemini, domain.StateCooling)
			account.CoolService(domain.ServiceGemini, time.Now().Add(cooldown), class)
		}

		s.sessionRepo.ReleaseWriteLease(account, domain.ServiceGemini)
		s.sessionRepo.Release(account, callErr)

		if !canRetry || !isFailoverCandidate(callErr) {
			return domain.EnsureGateway(callErr, domain.OpChatCompletions, domain.ServiceGemini)
		}
	}

	if lastErr != nil {
		return domain.EnsureGateway(lastErr, domain.OpChatCompletions, domain.ServiceGemini)
	}
	return domain.UpstreamUnavailable(domain.OpChatCompletions, "", domain.ServiceGemini, "hết tài khoản khả dụng để hoàn thành yêu cầu")
}

func (s *ChatService) runGemini(ctx context.Context, fn func(*domain.ManagedAccount) error) error {
	return s.runGeminiFailover(ctx, func(account *domain.ManagedAccount) (bool, error) {
		err := fn(account)
		return true, err
	})
}

func isFailoverCandidate(err error) bool {
	if err == nil {
		return false
	}
	class, _, ok := domain.ClassifiedFailure(err)
	if !ok {
		return false
	}
	switch class {
	case domain.ClassRateLimited, domain.ClassUpstreamUnavailable, domain.ClassExpired, domain.ClassUnauthorized:
		return true
	case domain.ClassUpstreamRejected:
		if ge, found := domain.AsGatewayError(err); found {
			if ge.OriginStatus == http.StatusTooManyRequests ||
				ge.OriginStatus == http.StatusServiceUnavailable ||
				ge.OriginStatus == http.StatusGatewayTimeout ||
				ge.OriginStatus == http.StatusBadGateway {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (s *ChatService) streamRound(
	ctx context.Context,
	account *domain.ManagedAccount,
	modelDesc *domain.ModelDescriptor,
	req *domain.OpenAIChatRequest,
	streamWriter io.Writer,
	flusher func(),
	flushedToClient *bool,
) error {
	ctx, cancel := boundStream(s.upstream, ctx)
	defer cancel()
	resp, err := s.postGemini(ctx, account, modelDesc, req)
	if err != nil {
		return err
	}
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	createdTime := time.Now().Unix()
	var conversationID string
	reply, err := s.wire.DematerializeChat(ctx, resp, s.metrics.Bind(domain.OpChatCompletions), func(delta, cID string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if cID != "" {
			conversationID = cID
		}
		chunk := domain.OpenAIChatResponse{
			ID:             "chatcmpl-" + conversationID,
			Object:         "chat.completion.chunk",
			Created:        createdTime,
			Model:          req.Model,
			ConversationID: conversationID,
			Choices: []domain.OpenAIChoice{{
				Index: 0,
				Delta: domain.OpenAIDelta{Content: delta},
			}},
		}
		chunkBytes, _ := json.Marshal(chunk)
		if _, writeErr := fmt.Fprintf(streamWriter, "data: %s\n\n", chunkBytes); writeErr != nil {
			return writeErr
		}
		if flusher != nil {
			flusher()
		}
		if flushedToClient != nil {
			*flushedToClient = true
		}
		return nil
	})
	if conversationID == "" {
		conversationID = reply.ConversationID
	}
	if err != nil {
		return normalizeWireErr(err)
	}

	// Tải và cache media cục bộ cho chunk cuối cùng
	if s.storage != nil && len(reply.MediaURLs) > 0 {
		cookies := ""
		userAgent := ""
		if account != nil {
			if account.Jar != nil {
				cookies = account.Jar.GetCookieHeader(false)
			}
			userAgent = account.UserAgent
		}

		userPrompt := ""
		if len(req.Messages) > 0 {
			userPrompt = req.Messages[len(req.Messages)-1].Content
		}

		var cachedURLs []string
		for _, rawURL := range reply.MediaURLs {
			cachedAsset, cacheErr := s.storage.DownloadAndCacheWithAuth(
				ctx,
				rawURL,
				domain.MediaImagePNG,
				userPrompt,
				req.Model,
				cookies,
				userAgent,
			)
			if cacheErr == nil && cachedAsset != nil && cachedAsset.LocalURL != "" {
				cachedURLs = append(cachedURLs, cachedAsset.LocalURL)
			} else {
				cachedURLs = append(cachedURLs, rawURL)
			}
		}
		reply.MediaURLs = cachedURLs
	}

	var usage *domain.OpenAIUsage
	if s.tokenCounter != nil {
		usage = s.tokenCounter.CalculateUsage(req.Messages, reply)
	}

	stopReason := "stop"
	finalChunk := domain.OpenAIChatResponse{
		ID:             "chatcmpl-" + conversationID,
		Object:         "chat.completion.chunk",
		Created:        createdTime,
		Model:          req.Model,
		ConversationID: conversationID,
		ResponseID:     reply.ResponseID,
		ChoiceID:       reply.ChoiceID,
		Thinking:       reply.ThinkingBlocks,
		Grounding:      reply.Grounding,
		CodeExecutions: reply.CodeExecutions,
		MediaURLs:      reply.MediaURLs,
		Usage:          usage,
		Choices: []domain.OpenAIChoice{
			{
				Index:        0,
				Delta:        domain.OpenAIDelta{},
				FinishReason: &stopReason,
			},
		},
	}
	finalBytes, _ := json.Marshal(finalChunk)
	if _, writeErr := fmt.Fprintf(streamWriter, "data: %s\n\n", finalBytes); writeErr != nil {
		return writeErr
	}
	if _, writeErr := fmt.Fprintf(streamWriter, "data: [DONE]\n\n"); writeErr != nil {
		return writeErr
	}
	if flusher != nil {
		flusher()
	}
	if flushedToClient != nil {
		*flushedToClient = true
	}
	return nil
}

func (s *ChatService) syncRound(
	ctx context.Context,
	account *domain.ManagedAccount,
	modelDesc *domain.ModelDescriptor,
	req *domain.OpenAIChatRequest,
) (*domain.OpenAIChatResponse, error) {
	ctx, cancel := boundStream(s.upstream, ctx)
	defer cancel()
	resp, err := s.postGemini(ctx, account, modelDesc, req)
	if err != nil {
		return nil, err
	}
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	reply, err := s.wire.DematerializeChat(ctx, resp, s.metrics.Bind(domain.OpChatCompletions), nil)
	if err != nil {
		return nil, normalizeWireErr(err)
	}

	// Tải và cache media cục bộ chống lỗi 403 Google CDN và bảo toàn TTL
	if s.storage != nil && len(reply.MediaURLs) > 0 {
		cookies := ""
		userAgent := ""
		if account != nil {
			if account.Jar != nil {
				cookies = account.Jar.GetCookieHeader(false)
			}
			userAgent = account.UserAgent
		}

		userPrompt := ""
		if len(req.Messages) > 0 {
			userPrompt = req.Messages[len(req.Messages)-1].Content
		}

		var cachedURLs []string
		for _, rawURL := range reply.MediaURLs {
			cachedAsset, cacheErr := s.storage.DownloadAndCacheWithAuth(
				ctx,
				rawURL,
				domain.MediaImagePNG,
				userPrompt,
				req.Model,
				cookies,
				userAgent,
			)
			if cacheErr == nil && cachedAsset != nil && cachedAsset.LocalURL != "" {
				cachedURLs = append(cachedURLs, cachedAsset.LocalURL)
				// Thay thế URL gốc bằng LocalURL trong văn bản phản hồi Markdown
				reply.Text = strings.ReplaceAll(reply.Text, rawURL, cachedAsset.LocalURL)
			} else {
				cachedURLs = append(cachedURLs, rawURL)
			}
		}
		reply.MediaURLs = cachedURLs
	}

	var usage *domain.OpenAIUsage
	if s.tokenCounter != nil {
		usage = s.tokenCounter.CalculateUsage(req.Messages, reply)
	}

	stopReason := "stop"
	return &domain.OpenAIChatResponse{
		ID:             "chatcmpl-" + reply.ConversationID,
		Object:         "chat.completion",
		Created:        time.Now().Unix(),
		Model:          req.Model,
		ConversationID: reply.ConversationID,
		ResponseID:     reply.ResponseID,
		ChoiceID:       reply.ChoiceID,
		Thinking:       reply.ThinkingBlocks,
		Grounding:      reply.Grounding,
		CodeExecutions: reply.CodeExecutions,
		MediaURLs:      reply.MediaURLs,
		Usage:          usage,
		Choices: []domain.OpenAIChoice{
			{
				Index: 0,
				Message: domain.OpenAIMessage{
					Role:    "assistant",
					Content: reply.Text,
				},
				FinishReason: &stopReason,
			},
		},
	}, nil
}

func (s *ChatService) postGemini(
	ctx context.Context,
	account *domain.ManagedAccount,
	modelDesc *domain.ModelDescriptor,
	req *domain.OpenAIChatRequest,
) (*http.Response, error) {
	_, lastUserPrompt := domain.FlattenMessages(req.Messages)
	isThinking := strings.Contains(strings.ToLower(req.Model), "thinking")
	if req.Thinking != nil {
		isThinking = *req.Thinking
	}
	isGrounding := true
	if req.SearchGrounding != nil {
		isGrounding = *req.SearchGrounding
	}
	isCodeInterpreter := true
	if req.CodeInterpreter != nil {
		isCodeInterpreter = *req.CodeInterpreter
	}

	modelTier := modelDesc.ModelTierCode
	if isThinking && modelTier < 3 {
		modelTier = 3
	}

	attachments := req.Attachments
	if s.visionResolver != nil {
		resolvedAtts, err := s.visionResolver.ProcessRequestAttachments(ctx, account, req)
		if err != nil {
			return nil, err
		}
		attachments = resolvedAtts
	}

	builder := domain.GeminiPayloadBuilder{
		UserPrompt:            lastUserPrompt,
		Locale:                "en",
		ConversationID:        req.ConversationID,
		ResponseID:            req.ResponseID,
		ChoiceID:              req.ChoiceID,
		ParentResponseID:      req.ParentResponseID,
		ParentChoiceID:        req.ParentChoiceID,
		ContextBlob:           req.ContextBlob,
		ModelTier:             modelTier,
		EnableThinking:        isThinking,
		EnableSearchGrounding: isGrounding,
		EnableCodeExecution:   isCodeInterpreter,
		Attachments:           attachments,
		ClientUUID:            fmt.Sprintf("req_%d", time.Now().UnixNano()),
	}

	if s.wire == nil {
		return nil, domain.CodecRejected(domain.OriginStreamGenerate, domain.ServiceGemini, "không đóng gói được yêu cầu")
	}
	attempt, err := s.wire.MaterializeChat(account, builder)
	if err != nil {
		return nil, err
	}
	reqPath := attempt.Path
	if attempt.TargetHost != "" && !strings.HasPrefix(reqPath, "http") {
		reqPath = attempt.TargetHost + reqPath
	}
	resp, err := s.upstream.DoRequest(
		ctx,
		account,
		modelDesc.TargetService,
		http.MethodPost,
		reqPath,
		strings.NewReader(attempt.Body),
		attempt.ContentType,
	)
	if err != nil {
		return nil, domain.CodecTransport(domain.OriginStreamGenerate, modelDesc.TargetService, err)
	}
	return resp, nil
}

func boundStream(upstream ports.UpstreamGoogleTransport, ctx context.Context) (context.Context, context.CancelFunc) {
	if upstream == nil {
		return ctx, func() {}
	}
	return upstream.BoundStream(ctx)
}

func normalizeWireErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if _, ok := domain.AsCodecError(err); ok {
		return err
	}
	if _, ok := domain.AsGatewayError(err); ok {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.CodecTransport(domain.OriginStreamGenerate, domain.ServiceGemini, err)
	}
	return domain.CodecSchema(domain.OriginStreamGenerate, domain.ServiceGemini, "luồng phản hồi không đúng hợp đồng")
}
