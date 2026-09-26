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

type GeminiHistoryService struct {
	sessionRepo ports.SessionRepository
	upstream    ports.UpstreamGoogleTransport
	rpcRegistry *domain.RpcRegistry
	metrics     *domain.ContractMetrics
}

func NewGeminiHistoryService(
	sr ports.SessionRepository,
	up ports.UpstreamGoogleTransport,
	rpcs *domain.RpcRegistry,
	metrics *domain.ContractMetrics,
) ports.GeminiHistoryUseCase {
	if rpcs == nil {
		rpcs = domain.DefaultRpcRegistry()
	}
	return &GeminiHistoryService{
		sessionRepo: sr,
		upstream:    up,
		rpcRegistry: rpcs,
		metrics:     metrics,
	}
}

func (s *GeminiHistoryService) doBatchExecute(ctx context.Context, op string, originOp string, reqPayload string, isWrite bool) (string, error) {
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

// ListConversations lấy danh sách hội thoại qua RPC MaZiqc
func (s *GeminiHistoryService) ListConversations(ctx context.Context, limit int) ([]domain.ConversationSummary, string, error) {
	reqPayload, err := domain.BuildHistoryListRequest(limit)
	if err != nil {
		return nil, "", domain.InvalidRequest(domain.OpGeminiHistory, domain.OriginHistoryList, domain.ServiceGemini, err.Error())
	}
	raw, err := s.doBatchExecute(ctx, domain.OpGeminiHistory, domain.OriginHistoryList, reqPayload, false)
	if err != nil {
		return nil, "", err
	}
	return domain.ParseHistoryListResponse(raw)
}

// GetConversationDetail lấy chi tiết cây hội thoại qua RPC cZOhpc
func (s *GeminiHistoryService) GetConversationDetail(ctx context.Context, convID string) (*domain.ConversationTree, error) {
	reqPayload, err := domain.BuildHistoryDetailRequest(convID)
	if err != nil {
		return nil, domain.InvalidRequest(domain.OpGeminiHistory, domain.OriginHistoryDetail, domain.ServiceGemini, err.Error())
	}
	raw, err := s.doBatchExecute(ctx, domain.OpGeminiHistory, domain.OriginHistoryDetail, reqPayload, false)
	if err != nil {
		return nil, err
	}
	return domain.ParseHistoryDetailResponse(convID, raw)
}

// RenameConversation đổi tên hội thoại qua RPC PCck7e
func (s *GeminiHistoryService) RenameConversation(ctx context.Context, convID, newTitle string) error {
	reqPayload, err := domain.BuildRenameConversationRequest(convID, newTitle)
	if err != nil {
		return domain.InvalidRequest(domain.OpGeminiHistory, domain.OriginHistoryRename, domain.ServiceGemini, err.Error())
	}
	raw, err := s.doBatchExecute(ctx, domain.OpGeminiHistory, domain.OriginHistoryRename, reqPayload, true)
	if err != nil {
		return err
	}
	_, err = domain.ParseRenameConversationResponse(raw)
	return err
}

// DeleteConversation xóa hội thoại qua RPC VxUbXb
func (s *GeminiHistoryService) DeleteConversation(ctx context.Context, convID string) error {
	reqPayload, err := domain.BuildDeleteConversationRequest(convID)
	if err != nil {
		return domain.InvalidRequest(domain.OpGeminiHistory, domain.OriginHistoryDelete, domain.ServiceGemini, err.Error())
	}
	raw, err := s.doBatchExecute(ctx, domain.OpGeminiHistory, domain.OriginHistoryDelete, reqPayload, true)
	if err != nil {
		return err
	}
	_, err = domain.ParseDeleteConversationResponse(raw)
	return err
}

// SwitchBranch đổi nhánh rẽ câu trả lời đang hoạt động qua RPC wEb32b
func (s *GeminiHistoryService) SwitchBranch(ctx context.Context, convID, respID, choiceID string) error {
	reqPayload, err := domain.BuildBranchSwitchRequest(convID, respID, choiceID)
	if err != nil {
		return domain.InvalidRequest(domain.OpGeminiHistory, domain.OriginHistoryBranch, domain.ServiceGemini, err.Error())
	}
	raw, err := s.doBatchExecute(ctx, domain.OpGeminiHistory, domain.OriginHistoryBranch, reqPayload, true)
	if err != nil {
		return err
	}
	_, err = domain.ParseBranchSwitchResponse(raw)
	return err
}
