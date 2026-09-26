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

type GeminiFeedbackService struct {
	sessionRepo ports.SessionRepository
	upstream    ports.UpstreamGoogleTransport
	rpcRegistry *domain.RpcRegistry
}

func NewGeminiFeedbackService(
	sr ports.SessionRepository,
	up ports.UpstreamGoogleTransport,
	rpcs *domain.RpcRegistry,
) ports.GeminiFeedbackUseCase {
	if rpcs == nil {
		rpcs = domain.DefaultRpcRegistry()
	}
	return &GeminiFeedbackService{
		sessionRepo: sr,
		upstream:    up,
		rpcRegistry: rpcs,
	}
}

func (s *GeminiFeedbackService) doBatchExecute(ctx context.Context, op, originOp, reqPayload string) (string, error) {
	account, err := s.sessionRepo.GetAvailable(ctx, domain.ServiceGemini, 0)
	if err != nil {
		return "", domain.Unauthenticated(op, originOp, domain.ServiceGemini, fmt.Sprintf("không có tài khoản Gemini khả dụng: %v", err))
	}
	var callErr error
	defer func() {
		s.sessionRepo.Release(account, callErr)
	}()

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

// SendFeedback gửi đánh giá chất lượng câu trả lời lên máy chủ Gemini qua RPC uP80Sb
func (s *GeminiFeedbackService) SendFeedback(ctx context.Context, req *domain.FeedbackRequest) error {
	if req == nil {
		return domain.InvalidRequest("uP80Sb", "uP80Sb", domain.ServiceGemini, "payload feedback rỗng")
	}

	reqPayload, err := domain.BuildFeedbackRequest(
		req.ConversationID,
		req.ResponseID,
		req.ChoiceID,
		req.Rating,
		req.Reasons,
		req.Comment,
		req.Locale,
	)
	if err != nil {
		return domain.InvalidRequest("uP80Sb", "uP80Sb", domain.ServiceGemini, err.Error())
	}

	raw, err := s.doBatchExecute(ctx, "uP80Sb", "uP80Sb", reqPayload)
	if err != nil {
		return err
	}

	_, err = domain.ParseFeedbackResponse(raw)
	if err != nil {
		return domain.CodecSchema("uP80Sb", domain.ServiceGemini, fmt.Sprintf("phân tích phản hồi feedback thất bại: %v", err))
	}

	return nil
}
