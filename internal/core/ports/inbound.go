package ports

import (
	"context"
	"io"

	"dezuxk-gateway/internal/core/domain"
)

// ChatUseCase giao diện xử lý trò chuyện chuẩn OpenAI
type ChatUseCase interface {
	ExecuteChatStream(
		ctx context.Context,
		req *domain.OpenAIChatRequest,
		streamWriter io.Writer,
		flusher func(),
	) error

	ExecuteChatSync(
		ctx context.Context,
		req *domain.OpenAIChatRequest,
	) (*domain.OpenAIChatResponse, error)
}

// ModelUseCase giao diện tra cứu danh mục mô hình
type ModelUseCase interface {
	ListModels(ctx context.Context) []domain.ModelDescriptor
	GetModel(ctx context.Context, modelID string) (*domain.ModelDescriptor, error)
}

// AccountUseCase giao diện quản lý danh tính tài khoản
type AccountUseCase interface {
	ListAccounts(ctx context.Context) []*domain.ManagedAccount
	AddAccount(ctx context.Context, email string, cookies map[string]string, userAgent string) error
	RefreshAccount(ctx context.Context, accountID string) error
}

// GeminiHistoryUseCase giao diện quản lý lịch sử hội thoại Gemini (MaZiqc, cZOhpc, PCck7e, VxUbXb, wEb32b)
type GeminiHistoryUseCase interface {
	ListConversations(ctx context.Context, limit int) ([]domain.ConversationSummary, string, error)
	GetConversationDetail(ctx context.Context, convID string) (*domain.ConversationTree, error)
	RenameConversation(ctx context.Context, convID, newTitle string) error
	DeleteConversation(ctx context.Context, convID string) error
	SwitchBranch(ctx context.Context, convID, respID, choiceID string) error
}

// GeminiUploadUseCase giao diện tải lên tệp đa phương thức Resumable SCOTTY
type GeminiUploadUseCase interface {
	UploadFile(ctx context.Context, fileName, mimeType string, content io.Reader, size int64) (storageToken string, err error)
	UploadFileWithAccount(ctx context.Context, account *domain.ManagedAccount, fileName, mimeType string, content io.Reader, size int64) (storageToken string, err error)
}

// GeminiCanvasUseCase giao diện tương tác với Canvas Artifacts (tVk3Sc, sA4a8, H8s0fe)
type GeminiCanvasUseCase interface {
	CreateCanvas(ctx context.Context, convID, title, contentType, initialContent string) (*domain.CanvasArtifact, error)
	UpdateDelta(ctx context.Context, canvasID string, baseVersion int, diffOps []domain.CanvasDiffOp) (*domain.CanvasArtifact, error)
	Publish(ctx context.Context, canvasID string, visibilityCode int, allowFork bool) (string, error)
}

// GeminiQuotaUseCase giao diện tra cứu hạn ngạch /usage và tier I4z33b
type GeminiQuotaUseCase interface {
	GetQuota(ctx context.Context) (*domain.QuotaInfo, error)
	GetAccountTier(ctx context.Context) (*domain.AccountTierInfo, error)
	GetQuotaForAccount(ctx context.Context, account *domain.ManagedAccount) (*domain.QuotaInfo, error)
	GetAccountTierForAccount(ctx context.Context, account *domain.ManagedAccount) (*domain.AccountTierInfo, error)
}

// GeminiFeedbackUseCase giao diện gửi đánh giá Thumbs up/down
type GeminiFeedbackUseCase interface {
	SendFeedback(ctx context.Context, req *domain.FeedbackRequest) error
}

// ProfileUseCase giao diện quản lý profile cục bộ
type ProfileUseCase interface {
	ListActiveProfiles() []*domain.Profile
	CreateProfile(profileID string) (*domain.Profile, error)
	CreateProfileWithProxy(profileID string, proxy string) (*domain.Profile, error)
	SetProfileProxy(profileID string, proxy string) error
	LaunchChromeForProfile(profileID string) error
	SyncCookiesFromCDP(ctx context.Context, profileID string) (*domain.ManagedAccount, error)
	IngestLiveCookies(ctx context.Context, profileID, email string, cookies map[string]string, userAgent string) (*domain.ManagedAccount, error)
	IngestLiveCookiesWithProxy(ctx context.Context, profileID, email string, cookies map[string]string, userAgent string, proxy string) (*domain.ManagedAccount, error)
	SetProfileQuota(profileID string, quota string)
}

// KeyUseCase giao diện quản lý và xác thực Virtual API Keys
type KeyUseCase interface {
	CreateKey(ctx context.Context, req domain.CreateKeyRequest) (*domain.VirtualKeyCreated, error)
	ListActiveKeys(ctx context.Context) ([]*domain.VirtualKey, error)
	RevokeKey(ctx context.Context, id string) error
	ValidateKey(ctx context.Context, rawKey string, targetModel string) (*domain.VirtualKey, error)
	ConsumeQuota(ctx context.Context, keyID string) (int, error)
}
