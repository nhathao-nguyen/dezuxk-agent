package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

type GeminiQuotaService struct {
	sessionRepo ports.SessionRepository
	upstream    ports.UpstreamGoogleTransport
	rpcRegistry *domain.RpcRegistry
}

func NewGeminiQuotaService(
	sr ports.SessionRepository,
	up ports.UpstreamGoogleTransport,
	rpcs *domain.RpcRegistry,
) ports.GeminiQuotaUseCase {
	if rpcs == nil {
		rpcs = domain.DefaultRpcRegistry()
	}
	return &GeminiQuotaService{
		sessionRepo: sr,
		upstream:    up,
		rpcRegistry: rpcs,
	}
}

// GetQuota tra cứu hạn ngạch /usage từ tài khoản Gemini khả dụng trong pool
func (s *GeminiQuotaService) GetQuota(ctx context.Context) (*domain.QuotaInfo, error) {
	if s.sessionRepo == nil || s.upstream == nil {
		return nil, fmt.Errorf("hạ tầng quota chưa được khởi tạo")
	}

	account, err := s.sessionRepo.GetAvailable(ctx, domain.ServiceGemini, 0)
	if err != nil {
		return nil, fmt.Errorf("không có tài khoản Gemini khả dụng: %w", err)
	}
	defer s.sessionRepo.Release(account, nil)

	return s.GetQuotaForAccount(ctx, account)
}

// GetQuotaForAccount tra cứu hạn ngạch /usage từ một tài khoản chỉ định
func (s *GeminiQuotaService) GetQuotaForAccount(ctx context.Context, account *domain.ManagedAccount) (*domain.QuotaInfo, error) {
	if account == nil {
		return nil, fmt.Errorf("tài khoản Gemini rỗng")
	}

	reqPath := "/usage"
	if s.rpcRegistry != nil {
		if ep, ok := s.rpcRegistry.Get("usage"); ok {
			if ep.PathPattern != "" {
				reqPath = ep.PathPattern
			}
			if ep.TargetHost != "" && !strings.HasPrefix(reqPath, "http") {
				reqPath = ep.TargetHost + reqPath
			}
		}
	}

	resp, err := s.upstream.DoRequest(ctx, account, domain.ServiceGemini, http.MethodGet, reqPath, nil, "")
	if err != nil {
		return nil, fmt.Errorf("lỗi kết nối Google /usage: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("lỗi đọc dữ liệu /usage: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Google /usage phản hồi mã %d", resp.StatusCode)
	}

	return domain.ParseQuotaResponse(string(bodyBytes))
}

// GetAccountTier tra cứu thông tin cấp độ gói thuê bao I4z33b từ tài khoản trong pool
func (s *GeminiQuotaService) GetAccountTier(ctx context.Context) (*domain.AccountTierInfo, error) {
	if s.sessionRepo == nil || s.upstream == nil {
		return nil, fmt.Errorf("hạ tầng quota chưa được khởi tạo")
	}

	account, err := s.sessionRepo.GetAvailable(ctx, domain.ServiceGemini, 0)
	if err != nil {
		return nil, fmt.Errorf("không có tài khoản Gemini khả dụng: %w", err)
	}
	defer s.sessionRepo.Release(account, nil)

	return s.GetAccountTierForAccount(ctx, account)
}

// GetAccountTierForAccount tra cứu gói thuê bao I4z33b cho một tài khoản chỉ định
func (s *GeminiQuotaService) GetAccountTierForAccount(ctx context.Context, account *domain.ManagedAccount) (*domain.AccountTierInfo, error) {
	if account == nil {
		return nil, fmt.Errorf("tài khoản Gemini rỗng")
	}

	postBody := `f.req=` + url.QueryEscape(`[[["I4z33b","[]",null,"generic"]]]`)
	if at := account.GetAtToken(domain.ServiceGemini); at != "" {
		postBody += "&at=" + url.QueryEscape(at)
	}

	reqPath := "/_/BardChatUi/data/batchexecute?rpcids=I4z33b"
	if s.rpcRegistry != nil {
		if ep, ok := s.rpcRegistry.Get("I4z33b"); ok {
			if ep.PathPattern != "" {
				reqPath = ep.PathPattern
			}
			if ep.TargetHost != "" && !strings.HasPrefix(reqPath, "http") {
				reqPath = ep.TargetHost + reqPath
			}
		}
	}

	resp, err := s.upstream.DoRequest(
		ctx,
		account,
		domain.ServiceGemini,
		http.MethodPost,
		reqPath,
		strings.NewReader(postBody),
		"application/x-www-form-urlencoded;charset=UTF-8",
	)
	if err != nil {
		return nil, fmt.Errorf("lỗi gọi RPC I4z33b: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("lỗi đọc dữ liệu I4z33b: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("RPC I4z33b phản hồi mã %d", resp.StatusCode)
	}

	return domain.ParseAccountTierResponse(string(bodyBytes))
}
