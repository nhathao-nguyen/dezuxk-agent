package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// KeyRateLimiter quản lý giới hạn tần suất yêu cầu RPM cho từng Virtual API Key
type KeyRateLimiter struct {
	mu      sync.Mutex
	windows map[string]*keyWindow
}

type keyWindow struct {
	windowStart time.Time
	count       int
}

func NewKeyRateLimiter() *KeyRateLimiter {
	limiter := &KeyRateLimiter{
		windows: make(map[string]*keyWindow),
	}
	go limiter.cleanupLoop()
	return limiter
}

func (l *KeyRateLimiter) Allow(keyID string, rpm int) bool {
	if rpm <= 0 {
		return true // Không giới hạn
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	w, exists := l.windows[keyID]
	if !exists || now.Sub(w.windowStart) >= time.Minute {
		l.windows[keyID] = &keyWindow{
			windowStart: now,
			count:       1,
		}
		return true
	}

	if w.count >= rpm {
		return false
	}

	w.count++
	return true
}

func (l *KeyRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	for now := range ticker.C {
		l.mu.Lock()
		for id, w := range l.windows {
			if now.Sub(w.windowStart) >= 2*time.Minute {
				delete(l.windows, id)
			}
		}
		l.mu.Unlock()
	}
}

// KeyService triển khai ports.KeyUseCase
type KeyService struct {
	repo           ports.KeyRepository
	rateLimiter    *KeyRateLimiter
	masterAdminKey string
	adminTokens    []string
}

func NewKeyService(repo ports.KeyRepository, masterAdminKey string, additionalAdminKeys ...string) *KeyService {
	var tokens []string
	for _, k := range additionalAdminKeys {
		k = strings.TrimSpace(k)
		if k != "" {
			tokens = append(tokens, k)
		}
	}
	return &KeyService{
		repo:           repo,
		rateLimiter:    NewKeyRateLimiter(),
		masterAdminKey: strings.TrimSpace(masterAdminKey),
		adminTokens:    tokens,
	}
}

// AddAdminToken bổ sung thêm token/key có quyền quản trị tối cao
func (s *KeyService) AddAdminToken(token string) {
	token = strings.TrimSpace(token)
	if token != "" {
		s.adminTokens = append(s.adminTokens, token)
	}
}

func (s *KeyService) isMasterAdminKey(key string) bool {
	if s.masterAdminKey != "" && key == s.masterAdminKey {
		return true
	}
	for _, tok := range s.adminTokens {
		if tok != "" && key == tok {
			return true
		}
	}
	return false
}



// CreateKey tạo một Virtual API Key mới với hạn ngạch cấu hình
func (s *KeyService) CreateKey(ctx context.Context, req domain.CreateKeyRequest) (*domain.VirtualKeyCreated, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.New("tên khóa (name) là bắt buộc")
	}

	role := strings.ToLower(strings.TrimSpace(req.Role))
	if role == "" {
		role = "user"
	}
	if role != "admin" && role != "user" {
		return nil, errors.New("vai trò (role) không hợp lệ, chỉ chấp nhận 'user' hoặc 'admin'")
	}

	rpm := req.RateLimitRPM
	if rpm < 0 {
		rpm = 60
	}

	dailyQuota := req.DailyQuotaRequests
	if dailyQuota < 0 {
		dailyQuota = 1000
	}

	allowedModels := req.AllowedModels
	if len(allowedModels) == 0 {
		allowedModels = []string{"*"}
	}

	rawKey, err := domain.GenerateRawKey()
	if err != nil {
		return nil, err
	}

	keyID, err := domain.GenerateKeyID()
	if err != nil {
		return nil, err
	}

	keyHash := domain.HashKey(rawKey)
	keyPrefix := domain.MaskKey(rawKey)
	now := time.Now().UTC()

	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		tenantID = "tenant_" + keyID
	}

	scopes := req.Scopes
	if len(scopes) == 0 {
		if role == "admin" {
			scopes = []string{domain.ScopeChat, domain.ScopeResponses, domain.ScopeAgent, domain.ScopeMemory, domain.ScopeBrowser, domain.ScopeShell, domain.ScopeAdmin}
		} else {
			scopes = []string{domain.ScopeChat, domain.ScopeResponses, domain.ScopeAgent, domain.ScopeMemory}
		}
	}

	maxSteps := req.MaxAgentSteps
	if maxSteps <= 0 {
		maxSteps = 25
	}
	maxConcurrent := req.MaxConcurrentRuns
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	runtimeSec := req.MaxToolRuntimeSeconds
	if runtimeSec <= 0 {
		runtimeSec = 60
	}

	requireApproval := true
	if req.RequireApproval != nil {
		requireApproval = *req.RequireApproval
	} else if role == "admin" {
		requireApproval = false
	}

	allowShell := false
	if req.AllowShell != nil {
		allowShell = *req.AllowShell
	} else if role == "admin" {
		allowShell = true
	}

	enforceSandbox := true
	if req.EnforceSandbox != nil {
		enforceSandbox = *req.EnforceSandbox
	} else if role == "admin" {
		enforceSandbox = false
	}

	autoMerge := false
	if req.AutoMergeAllowed != nil {
		autoMerge = *req.AutoMergeAllowed
	} else if role == "admin" {
		autoMerge = true
	}

	vKey := &domain.VirtualKey{
		ID:                    keyID,
		TenantID:              tenantID,
		KeyHash:               keyHash,
		KeyPrefix:             keyPrefix,
		Name:                  name,
		Role:                  role,
		RateLimitRPM:          rpm,
		DailyQuotaRequests:    dailyQuota,
		MaxTokenQuota:         req.MaxTokenQuota,
		UsedToday:             0,
		LastUsedDate:          now.Format("2006-01-02"),
		AllowedModels:         allowedModels,
		Scopes:                scopes,
		AllowedTools:          req.AllowedTools,
		AllowedWorkspaceRoots: req.AllowedWorkspaceRoots,
		MaxAgentSteps:         maxSteps,
		MaxConcurrentRuns:     maxConcurrent,
		MaxToolRuntimeSeconds: runtimeSec,
		RequireApproval:       requireApproval,
		AllowShell:            allowShell,
		EnforceSandbox:        enforceSandbox,
		AutoMergeAllowed:      autoMerge,
		IsActive:              true,
		ExpiresAt:             req.ExpiresAt,
		CreatedAt:             now,
	}

	if err := s.repo.Save(ctx, vKey); err != nil {
		return nil, err
	}

	return &domain.VirtualKeyCreated{
		VirtualKey: *vKey,
		Key:        rawKey,
		RawKey:     rawKey,
	}, nil
}

// ListActiveKeys liệt kê các khóa đang hoạt động
func (s *KeyService) ListActiveKeys(ctx context.Context) ([]*domain.VirtualKey, error) {
	return s.repo.ListActive(ctx)
}

// RevokeKey thu hồi khóa ngay lập tức
func (s *KeyService) RevokeKey(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("id khóa không được để trống")
	}
	return s.repo.Revoke(ctx, id)
}

// ValidateKey xác thực khóa API, kiểm tra hạn ngạch và quyền truy cập mô hình
func (s *KeyService) ValidateKey(ctx context.Context, rawKey string, targetModel string) (*domain.VirtualKey, error) {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" {
		return nil, domain.ErrMissingAPIKey
	}

	if strings.HasPrefix(strings.ToLower(rawKey), "bearer ") {
		rawKey = strings.TrimSpace(rawKey[7:])
	}

	// 1. Kiểm tra nếu khớp với Master Admin Key hoặc Admin Session Token
	if s.isMasterAdminKey(rawKey) {
		return &domain.VirtualKey{
			ID:                 "master",
			TenantID:           "admin_system",
			KeyPrefix:          domain.MaskKey(rawKey),
			Name:               "Master Super Admin",
			Role:               "admin",
			RateLimitRPM:       -1,
			DailyQuotaRequests: -1,
			AllowedModels:      []string{"*"},
			Scopes:             []string{domain.ScopeChat, domain.ScopeResponses, domain.ScopeAgent, domain.ScopeMemory, domain.ScopeBrowser, domain.ScopeShell, domain.ScopeAdmin},
			AllowShell:         true,
			RequireApproval:    false,
			EnforceSandbox:     false,
			AutoMergeAllowed:   true,
			IsActive:           true,
			CreatedAt:          time.Now().UTC(),
		}, nil
	}

	// 2. Băm SHA-256 và tra cứu trong cơ sở dữ liệu
	keyHash := domain.HashKey(rawKey)
	vKey, err := s.repo.FindByKeyHash(ctx, keyHash)
	if err != nil {
		return nil, domain.ErrInvalidAPIKey
	}

	// 3. Kiểm tra trạng thái hoạt động
	if !vKey.IsActive {
		return nil, domain.ErrKeyRevoked
	}

	// 4. Kiểm tra thời hạn hết hạn
	if vKey.IsExpired() {
		return nil, domain.ErrKeyExpired
	}

	// 5. Kiểm tra giới hạn tốc độ RPM (Rate Limit)
	if vKey.RateLimitRPM > 0 && !s.rateLimiter.Allow(vKey.ID, vKey.RateLimitRPM) {
		return nil, domain.ErrRateLimitRPMExceeded
	}

	// 6. Kiểm tra quyền truy cập mô hình (Allowed Models)
	if targetModel != "" && !vKey.IsModelAllowed(targetModel) {
		return nil, domain.ErrModelNotAllowed
	}

	// 7. Kiểm tra hạn ngạch trong ngày (Daily Quota)
	today := time.Now().UTC().Format("2006-01-02")
	if vKey.DailyQuotaRequests > 0 && vKey.LastUsedDate == today && vKey.UsedToday >= vKey.DailyQuotaRequests {
		return nil, domain.ErrDailyQuotaExceeded
	}

	// 8. Kiểm tra hạn ngạch tổng token nếu có thiết lập
	if vKey.MaxTokenQuota > 0 && vKey.TotalTokens >= vKey.MaxTokenQuota {
		return nil, domain.ErrTokenQuotaExceeded
	}

	return vKey, nil
}

// ConsumeQuota tiêu thụ 1 lượt hạn ngạch trong ngày (Quota Decrementor)
func (s *KeyService) ConsumeQuota(ctx context.Context, keyID string) (int, error) {
	if keyID == "master" {
		return -1, nil // Master key không giới hạn
	}
	today := time.Now().UTC().Format("2006-01-02")
	return s.repo.ConsumeDailyQuota(ctx, keyID, today)
}

// RecordTokenUsage ghi nhận số token tiêu thụ cho một Virtual API Key
func (s *KeyService) RecordTokenUsage(ctx context.Context, keyID string, promptTokens, completionTokens int) error {
	if keyID == "" {
		return nil
	}
	return s.repo.RecordTokenUsage(ctx, keyID, promptTokens, completionTokens)
}

// GetTokenUsageHistory lấy lịch sử sử dụng token theo ngày của một khóa
func (s *KeyService) GetTokenUsageHistory(ctx context.Context, keyID string, days int) ([]domain.KeyTokenUsage, error) {
	return s.repo.GetTokenUsageHistory(ctx, keyID, days)
}

// GetSystemTokenUsageHistory lấy lịch sử sử dụng token tổng hợp toàn hệ thống
func (s *KeyService) GetSystemTokenUsageHistory(ctx context.Context, days int) ([]domain.KeyTokenUsage, error) {
	return s.repo.GetSystemTokenUsageHistory(ctx, days)
}

// Đảm bảo implement đúng ports.KeyUseCase
var _ ports.KeyUseCase = (*KeyService)(nil)
