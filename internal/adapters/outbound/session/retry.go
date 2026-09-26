package session

import (
	"context"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// RetryAfterRefresh chạy call một lần. Nếu lỗi là hết phiên thì xoay bí mật đúng một flight rồi gọi lại một lần.
// Lần thứ hai vẫn hết phiên thì đánh invalid và dừng.
func RetryAfterRefresh(ctx context.Context, repo ports.SessionRepository, account *domain.ManagedAccount, service domain.ServiceKind, call func() error) error {
	err := call()
	if !needsSecretRefresh(err) {
		return err
	}
	if rerr := repo.RefreshDerived(ctx, account, service); rerr != nil {
		return rerr
	}
	err = call()
	if needsSecretRefresh(err) {
		repo.Invalidate(account, service)
	}
	return err
}

func needsSecretRefresh(err error) bool {
	class, _, ok := domain.ClassifiedFailure(err)
	return ok && class == domain.ClassExpired
}
