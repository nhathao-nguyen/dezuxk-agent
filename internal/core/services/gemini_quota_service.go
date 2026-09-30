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

// GetAccountTierForAccount tra cứu gói thuê bao (ưu tiên otAQ7b, fallback I4z33b) cho một tài khoản chỉ định
func (s *GeminiQuotaService) GetAccountTierForAccount(ctx context.Context, account *domain.ManagedAccount) (*domain.AccountTierInfo, error) {
	if account == nil {
		return nil, fmt.Errorf("tài khoản Gemini rỗng")
	}

	// 1. Thử gọi RPC otAQ7b hiện đại của Google
	atToken := account.GetAtToken(domain.ServiceGemini)
	postBodyOtAQ7b := `f.req=` + url.QueryEscape(`[[["otAQ7b","[]",null,"generic"]]]`)
	if atToken != "" {
		postBodyOtAQ7b += "&at=" + url.QueryEscape(atToken)
	}

	reqPathOtAQ7b := "/_/BardChatUi/data/batchexecute?rpcids=otAQ7b"
	if s.rpcRegistry != nil {
		if ep, ok := s.rpcRegistry.Get("otAQ7b"); ok {
			if ep.PathPattern != "" {
				reqPathOtAQ7b = ep.PathPattern
			}
			if ep.TargetHost != "" && !strings.HasPrefix(reqPathOtAQ7b, "http") {
				reqPathOtAQ7b = ep.TargetHost + reqPathOtAQ7b
			}
		}
	}

	resp, err := s.upstream.DoRequest(
		ctx,
		account,
		domain.ServiceGemini,
		http.MethodPost,
		reqPathOtAQ7b,
		strings.NewReader(postBodyOtAQ7b),
		"application/x-www-form-urlencoded;charset=UTF-8",
	)

	var bodyBytes []byte
	if err == nil && resp != nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			bodyBytes, _ = io.ReadAll(resp.Body)
		}
	}

	// 2. Nếu otAQ7b không thành công, thử fallback sang RPC I4z33b cũ
	if len(bodyBytes) == 0 {
		postBodyI4z33b := `f.req=` + url.QueryEscape(`[[["I4z33b","[]",null,"generic"]]]`)
		if atToken != "" {
			postBodyI4z33b += "&at=" + url.QueryEscape(atToken)
		}

		reqPathI4z33b := "/_/BardChatUi/data/batchexecute?rpcids=I4z33b"
		if s.rpcRegistry != nil {
			if ep, ok := s.rpcRegistry.Get("I4z33b"); ok {
				if ep.PathPattern != "" {
					reqPathI4z33b = ep.PathPattern
				}
				if ep.TargetHost != "" && !strings.HasPrefix(reqPathI4z33b, "http") {
					reqPathI4z33b = ep.TargetHost + reqPathI4z33b
				}
			}
		}

		respFallback, errFallback := s.upstream.DoRequest(
			ctx,
			account,
			domain.ServiceGemini,
			http.MethodPost,
			reqPathI4z33b,
			strings.NewReader(postBodyI4z33b),
			"application/x-www-form-urlencoded;charset=UTF-8",
		)
		if errFallback != nil {
			return nil, fmt.Errorf("lỗi gọi RPC tra cứu tier (otAQ7b & I4z33b): %w", errFallback)
		}
		defer respFallback.Body.Close()

		if respFallback.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("RPC tier phản hồi mã %d", respFallback.StatusCode)
		}
		bodyBytes, _ = io.ReadAll(respFallback.Body)
	}

	tierInfo, err := domain.ParseAccountTierResponse(string(bodyBytes))
	if err != nil {
		return nil, err
	}

	// 3. Đồng bộ cấp độ Tier vào tài khoản và lưu trữ SQLite
	if account != nil {
		if tierInfo.TierCode == "GOOGLE_AI_PRO" || tierInfo.TierCode == "GOOGLE_ONE_AI_PREMIUM" {
			account.Tier = 2 // 2: Pro
		} else if tierInfo.TierCode == "WORKSPACE_ENTERPRISE" {
			account.Tier = 3 // 3: Enterprise / Ultra
		} else {
			account.Tier = 1 // 1: Free
		}
		if s.sessionRepo != nil {
			_ = s.sessionRepo.Save(ctx, account)
		}
	}

	return tierInfo, nil
}
