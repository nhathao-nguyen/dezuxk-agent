package domain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidAPIKey        = errors.New("khóa API không hợp lệ")
	ErrMissingAPIKey        = errors.New("thiếu khóa API")
	ErrKeyRevoked           = errors.New("khóa API đã bị thu hồi hoặc vô hiệu hóa")
	ErrKeyExpired           = errors.New("khóa API đã hết hạn sử dụng")
	ErrModelNotAllowed      = errors.New("mô hình không được cấp phép cho khóa API này")
	ErrRateLimitRPMExceeded = errors.New("vượt quá giới hạn tốc độ RPM của khóa API")
	ErrDailyQuotaExceeded   = errors.New("vượt quá hạn ngạch yêu cầu trong ngày của khóa API")
	ErrAdminRequired        = errors.New("yêu cầu quyền quản trị (admin)")
)

// VirtualKey đại diện cho một khóa API ảo phân quyền trong hệ thống Dezuxk Gateway
type VirtualKey struct {
	ID                 string     `json:"id"`
	KeyHash            string     `json:"-"`
	KeyPrefix          string     `json:"key_prefix"`
	Name               string     `json:"name"`
	Role               string     `json:"role"` // "admin" hoặc "user"
	RateLimitRPM       int        `json:"rate_limit_rpm"`
	DailyQuotaRequests int        `json:"daily_quota_requests"`
	UsedToday          int        `json:"used_today"`
	LastUsedDate       string     `json:"last_used_date"`
	AllowedModels      []string   `json:"allowed_models"`
	AllowedModelsJSON  string     `json:"-"`
	IsActive           bool       `json:"is_active"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

// IsModelAllowed kiểm tra xem mô hình có nằm trong danh sách được phép hay không
func (k *VirtualKey) IsModelAllowed(model string) bool {
	if k == nil {
		return false
	}
	if len(k.AllowedModels) == 0 {
		return true
	}
	model = strings.TrimSpace(strings.ToLower(model))
	for _, m := range k.AllowedModels {
		m = strings.TrimSpace(strings.ToLower(m))
		if m == "*" || m == model {
			return true
		}
	}
	return false
}

// IsExpired kiểm tra khóa có quá hạn hay không
func (k *VirtualKey) IsExpired() bool {
	if k == nil {
		return true
	}
	if k.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*k.ExpiresAt)
}

// RemainingQuota trả về số lượt yêu cầu còn lại trong ngày (trả về -1 nếu không giới hạn)
func (k *VirtualKey) RemainingQuota() int {
	if k == nil || k.DailyQuotaRequests <= 0 {
		return -1 // Không giới hạn
	}
	today := time.Now().UTC().Format("2006-01-02")
	if k.LastUsedDate != today {
		return k.DailyQuotaRequests
	}
	rem := k.DailyQuotaRequests - k.UsedToday
	if rem < 0 {
		return 0
	}
	return rem
}

type CreateKeyRequest struct {
	Name               string     `json:"name"`
	Role               string     `json:"role"`
	RateLimitRPM       int        `json:"rate_limit_rpm"`
	DailyQuotaRequests int        `json:"daily_quota_requests"`
	AllowedModels      []string   `json:"allowed_models"`
	ExpiresAt          *time.Time `json:"expires_at"`
}

type VirtualKeyCreated struct {
	VirtualKey
	Key string `json:"key"` // Khóa thô dạng 'sk-dez-...', chỉ hiển thị 1 lần duy nhất khi tạo
}

// HashKey băm khóa bí mật bằng SHA-256
func HashKey(rawKey string) string {
	rawKey = strings.TrimSpace(rawKey)
	hash := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(hash[:])
}

// GenerateRawKey tạo khóa thô an toàn ngẫu nhiên dạng sk-dez-<32 hex chars>
func GenerateRawKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("sk-dez-%s", hex.EncodeToString(b)), nil
}

// GenerateKeyID tạo định danh ngẫu nhiên dạng vk_<16 hex chars>
func GenerateKeyID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("vk_%s", hex.EncodeToString(b)), nil
}

// MaskKey tạo chuỗi hiển thị an toàn (e.g. sk-dez-123...456)
func MaskKey(rawKey string) string {
	if len(rawKey) <= 12 {
		return "sk-dez-***"
	}
	return rawKey[:10] + "..." + rawKey[len(rawKey)-4:]
}

type virtualKeyContextKey struct{}

func ContextWithVirtualKey(ctx context.Context, key *VirtualKey) context.Context {
	return context.WithValue(ctx, virtualKeyContextKey{}, key)
}

func VirtualKeyFromContext(ctx context.Context) *VirtualKey {
	if v := ctx.Value(virtualKeyContextKey{}); v != nil {
		if k, ok := v.(*VirtualKey); ok {
			return k
		}
	}
	return nil
}
