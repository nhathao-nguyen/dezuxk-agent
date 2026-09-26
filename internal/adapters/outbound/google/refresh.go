package google

import (
	"context"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// DerivedSecretRefresher lấy lại SNlM0e từ trang đã đăng nhập. Không mở Chrome và không login.
type DerivedSecretRefresher struct {
	extractor ports.TokenExtractor
}

func NewDerivedSecretRefresher(extractor ports.TokenExtractor) *DerivedSecretRefresher {
	return &DerivedSecretRefresher{extractor: extractor}
}

func (r *DerivedSecretRefresher) RefreshDerivedSecret(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) error {
	if r == nil || r.extractor == nil {
		return domain.CodecExpired(domain.OriginHandshake, service, "chưa có đường xoay bí mật dẫn xuất")
	}
	sn, _, err := r.extractor.ExtractTokens(ctx, account, service)
	if err != nil {
		if _, ok := domain.AsCodecError(err); ok {
			return err
		}
		if ge, ok := domain.AsGatewayError(err); ok {
			return ge
		}
		return domain.CodecTransport(domain.OriginHandshake, service, err)
	}
	if sn == "" {
		return domain.CodecExpired(domain.OriginHandshake, service, "không lấy lại được bí mật dẫn xuất")
	}
	account.SetAtToken(service, sn)
	return nil
}
