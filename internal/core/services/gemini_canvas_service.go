package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

type GeminiCanvasService struct {
	sessionRepo ports.SessionRepository
	upstream    ports.UpstreamGoogleTransport
	rpcRegistry *domain.RpcRegistry
}

func NewGeminiCanvasService(
	sr ports.SessionRepository,
	up ports.UpstreamGoogleTransport,
	rpcs *domain.RpcRegistry,
) ports.GeminiCanvasUseCase {
	if rpcs == nil {
		rpcs = domain.DefaultRpcRegistry()
	}
	return &GeminiCanvasService{
		sessionRepo: sr,
		upstream:    up,
		rpcRegistry: rpcs,
	}
}

func (s *GeminiCanvasService) doBatchExecute(ctx context.Context, op, originOp, reqPayload string, isWrite bool) (string, error) {
	account, err := s.sessionRepo.GetAvailable(ctx, domain.ServiceGemini, 0)
	if err != nil {
		return "", domain.Unauthenticated(op, originOp, domain.ServiceGemini, fmt.Sprintf("không có tài khoản Gemini khả dụng: %v", err))
	}
	var callErr error
	defer func() {
		s.sessionRepo.Release(account, callErr)
	}()

	if isWrite {
		if !s.sessionRepo.TryWriteLease(account, domain.ServiceGemini) {
			return "", domain.Conflict(op, originOp, domain.ServiceGemini, "phiên Gemini đang bận một tác vụ ghi khác")
		}
		defer s.sessionRepo.ReleaseWriteLease(account, domain.ServiceGemini)
	}

	path := "/_/BardChatUi/data/batchexecute?rpcids=" + originOp
	if s.rpcRegistry != nil {
		if ep, ok := s.rpcRegistry.Get(originOp); ok {
			if ep.PathPattern != "" {
				path = ep.PathPattern
			}
			if ep.TargetHost != "" && !strings.HasPrefix(path, "http") {
				path = ep.TargetHost + path
			}
		}
	}

	var respBody string
	callErr = session.RetryAfterRefresh(ctx, s.sessionRepo, account, domain.ServiceGemini, func() error {
		postBody := `f.req=` + url.QueryEscape(reqPayload)
		if at := account.GetAtToken(domain.ServiceGemini); at != "" {
			postBody += "&at=" + url.QueryEscape(at)
		}

		callCtx, cancel := s.upstream.BoundShort(ctx)
		defer cancel()

		resp, err := s.upstream.DoRequest(
			callCtx,
			account,
			domain.ServiceGemini,
			http.MethodPost,
			path,
			strings.NewReader(postBody),
			"application/x-www-form-urlencoded;charset=UTF-8",
		)
		if err != nil {
			return domain.CodecTransport(originOp, domain.ServiceGemini, err)
		}
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return domain.CodecTransport(originOp, domain.ServiceGemini, err)
		}
		if resp.StatusCode != http.StatusOK {
			return domain.ClassifyUpstreamStatus(op, originOp, resp.StatusCode, false, domain.ServiceGemini)
		}
		respBody = string(bodyBytes)
		return nil
	})
	if callErr != nil {
		return "", domain.EnsureGateway(callErr, op, domain.ServiceGemini)
	}
	return respBody, nil
}

// CreateCanvas khởi tạo một Artifact mới trong không gian làm việc Gemini Canvas qua RPC tVk3Sc
func (s *GeminiCanvasService) CreateCanvas(
	ctx context.Context,
	convID, title, contentType, initialContent string,
) (*domain.CanvasArtifact, error) {
	reqPayload, err := domain.BuildCreateCanvasRequest(convID, title, contentType, initialContent)
	if err != nil {
		return nil, domain.InvalidRequest("tVk3Sc", "tVk3Sc", domain.ServiceGemini, err.Error())
	}

	raw, err := s.doBatchExecute(ctx, "tVk3Sc", "tVk3Sc", reqPayload, true)
	if err != nil {
		return nil, err
	}

	canvasID, version, err := domain.ParseCreateCanvasResponse(raw)
	if err != nil {
		return nil, domain.CodecSchema("tVk3Sc", domain.ServiceGemini, fmt.Sprintf("giải mã phản hồi tạo canvas thất bại: %v", err))
	}

	return &domain.CanvasArtifact{
		CanvasID:       canvasID,
		ConversationID: convID,
		Title:          title,
		ContentType:    contentType,
		Content:        initialContent,
		Version:        version,
	}, nil
}

// UpdateDelta cập nhật từng dòng thay đổi (diff) của tài liệu Canvas qua RPC sA4a8
func (s *GeminiCanvasService) UpdateDelta(
	ctx context.Context,
	canvasID string,
	baseVersion int,
	diffOps []domain.CanvasDiffOp,
) (*domain.CanvasArtifact, error) {
	reqPayload, err := domain.BuildUpdateCanvasDeltaRequest(canvasID, baseVersion, diffOps)
	if err != nil {
		return nil, domain.InvalidRequest("sA4a8", "sA4a8", domain.ServiceGemini, err.Error())
	}

	raw, err := s.doBatchExecute(ctx, "sA4a8", "sA4a8", reqPayload, true)
	if err != nil {
		return nil, err
	}

	_, newVersion, _, err := domain.ParseUpdateCanvasDeltaResponse(raw)
	if err != nil {
		return nil, domain.CodecSchema("sA4a8", domain.ServiceGemini, fmt.Sprintf("giải mã phản hồi delta canvas thất bại: %v", err))
	}

	return &domain.CanvasArtifact{
		CanvasID: canvasID,
		Version:  newVersion,
		DiffOps:  diffOps,
	}, nil
}

// Publish xuất bản liên kết công khai của tài liệu Canvas qua RPC H8s0fe
func (s *GeminiCanvasService) Publish(
	ctx context.Context,
	canvasID string,
	visibilityCode int,
	allowFork bool,
) (string, error) {
	reqPayload, err := domain.BuildPublishCanvasRequest(canvasID, visibilityCode, allowFork)
	if err != nil {
		return "", domain.InvalidRequest("H8s0fe", "H8s0fe", domain.ServiceGemini, err.Error())
	}

	raw, err := s.doBatchExecute(ctx, "H8s0fe", "H8s0fe", reqPayload, true)
	if err != nil {
		return "", err
	}

	shareURL, err := domain.ParsePublishCanvasResponse(raw)
	if err != nil {
		return "", domain.CodecSchema("H8s0fe", domain.ServiceGemini, fmt.Sprintf("giải mã phản hồi publish canvas thất bại: %v", err))
	}

	return shareURL, nil
}
