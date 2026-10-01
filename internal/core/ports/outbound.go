package ports

import (
	"context"
	"io"
	"net/http"

	"dezuxk-gateway/internal/core/domain"
)

// UpstreamGoogleTransport giao diện gửi gói tin mạng lên máy chủ Google
type UpstreamGoogleTransport interface {
	DoRequest(
		ctx context.Context,
		account *domain.ManagedAccount,
		service domain.ServiceKind,
		method string,
		path string,
		body io.Reader,
		contentType string,
	) (*http.Response, error)
	BoundShort(ctx context.Context) (context.Context, context.CancelFunc)
	BoundStream(ctx context.Context) (context.Context, context.CancelFunc)
}

// SessionRepository quản lý lưu trữ và phân phối tài khoản
type SessionRepository interface {
	GetAvailable(ctx context.Context, service domain.ServiceKind, minCredits int) (*domain.ManagedAccount, error)
	Release(account *domain.ManagedAccount, err error)
	ListAll(ctx context.Context) []*domain.ManagedAccount
	Save(ctx context.Context, account *domain.ManagedAccount) error
	FindByID(ctx context.Context, id string) (*domain.ManagedAccount, error)
	RefreshDerived(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) error
	Invalidate(account *domain.ManagedAccount, service domain.ServiceKind)
	TryWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) bool
	ReleaseWriteLease(account *domain.ManagedAccount, service domain.ServiceKind)
	GetAlerts() []domain.SessionAlert
	AddAlert(alert domain.SessionAlert)
	ClearAlerts(accountID string)
}

// SessionAlertNotifier cho phép đăng ký bộ phát cảnh báo Webhook cho Session Repository
type SessionAlertNotifier interface {
	SetAlertDispatcher(dispatcher AlertDispatcher)
}

// AlertDispatcher gửi cảnh báo bất đồng bộ không chặn qua Webhook
type AlertDispatcher interface {
	Dispatch(alert domain.AlertPayload)
	Close() error
}

// KeyRepository quản lý lưu trữ và tra cứu Virtual API Keys trong cơ sở dữ liệu
type KeyRepository interface {
	Save(ctx context.Context, key *domain.VirtualKey) error
	FindByKeyHash(ctx context.Context, keyHash string) (*domain.VirtualKey, error)
	FindByID(ctx context.Context, id string) (*domain.VirtualKey, error)
	ListActive(ctx context.Context) ([]*domain.VirtualKey, error)
	Revoke(ctx context.Context, id string) error
	ConsumeDailyQuota(ctx context.Context, id string, date string) (int, error)
	RecordTokenUsage(ctx context.Context, id string, promptTokens, completionTokens int) error
	GetTokenUsageHistory(ctx context.Context, keyID string, days int) ([]domain.KeyTokenUsage, error)
	GetSystemTokenUsageHistory(ctx context.Context, days int) ([]domain.KeyTokenUsage, error)
}

// DerivedSecretRefresher xoay bí mật dẫn xuất (SNlM0e) của một service. Không login lại.
type DerivedSecretRefresher interface {
	RefreshDerivedSecret(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) error
}

// MediaRepository quản lý lưu trữ tệp cục bộ
type MediaRepository interface {
	SaveAsset(ctx context.Context, asset *domain.MediaAsset, content io.Reader) error
	GetAsset(ctx context.Context, assetID string) (*domain.MediaAsset, io.ReadCloser, error)
	ListAssets(ctx context.Context, kind domain.MediaKind) ([]*domain.MediaAsset, error)
	DeleteExpired(ctx context.Context, maxAgeDays int) (int, error)
	DownloadAndCache(ctx context.Context, remoteURL string, kind domain.MediaKind, prompt string, model string) (*domain.MediaAsset, error)
	DownloadAndCacheWithAuth(ctx context.Context, remoteURL string, kind domain.MediaKind, prompt string, model string, cookies string, userAgent string) (*domain.MediaAsset, error)
	ServeAssetHTTP(w http.ResponseWriter, r *http.Request, assetID string) error
}

// TokenExtractor giao diện trích xuất CSRF SNlM0e và cfb2h
type TokenExtractor interface {
	ExtractTokens(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) (snlm0e string, cfb2h string, err error)
}

// WireCodec pack và unpack StreamGenerate. Lỗi trả về là codec, chưa mang tên operation nội bộ.
type WireCodec interface {
	MaterializeChat(account *domain.ManagedAccount, payload domain.GeminiPayloadBuilder) (domain.OutboundAttempt, error)
	DematerializeChat(ctx context.Context, resp *http.Response, metrics *domain.ContractMetrics, onDelta func(delta, convID string) error) (domain.GeminiReply, error)
	DematerializeChatStream(ctx context.Context, resp *http.Response, metrics *domain.ContractMetrics, onContent func(delta, convID string) error, onReasoning func(delta, convID string) error) (domain.GeminiReply, error)
}

