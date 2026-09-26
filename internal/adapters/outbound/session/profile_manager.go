package session

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"

	"github.com/gorilla/websocket"
)

type ProfileManager struct {
	mu            sync.RWMutex
	baseDir       string
	chromeBin     string
	cdpPortStart  int
	profiles      map[string]*domain.Profile
	sessionRepo   ports.SessionRepository
	modelRegistry *domain.ModelRegistry
	extractor     ports.TokenExtractor
	flowClient    ports.FlowClient
	client        *http.Client
	vault         *Vault
}

type StoredProfileSession struct {
	ProfileID        string            `json:"profile_id"`
	Email            string            `json:"email"`
	Proxy            string            `json:"proxy,omitempty"`
	Cookies          map[string]string `json:"cookies,omitempty"`
	EncryptedCookies string            `json:"encrypted_cookies,omitempty"`
	FlowSNlM0e       string            `json:"flow_sn_token,omitempty"`
	GeminiSNlM0e     string            `json:"gemini_sn_token,omitempty"`
	FlowProjectID    string            `json:"flow_project_id,omitempty"`
	FlowSessionToken string            `json:"flow_session_token,omitempty"`
	UserAgent        string            `json:"user_agent"`
	GeminiQuota      string            `json:"gemini_quota,omitempty"`
	FlowCredits      int               `json:"flow_credits,omitempty"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

func NewProfileManager(
	cfg *config.Config,
	sr ports.SessionRepository,
	mr *domain.ModelRegistry,
	ext ports.TokenExtractor,
	fc ports.FlowClient,
	v ...*Vault,
) (*ProfileManager, error) {
	if err := os.MkdirAll(cfg.Profiles.BaseDir, 0755); err != nil {
		return nil, fmt.Errorf("không thể tạo thư mục profiles tại %s: %w", cfg.Profiles.BaseDir, err)
	}

	var vault *Vault
	if len(v) > 0 && v[0] != nil {
		vault = v[0]
	} else {
		vault = NewVault(ResolveMasterKey(""))
	}

	pm := &ProfileManager{
		baseDir:       cfg.Profiles.BaseDir,
		chromeBin:     resolveChromeBinary(cfg.Profiles.ChromeBinary),
		cdpPortStart:  cfg.Profiles.CDPPortStart,
		profiles:      make(map[string]*domain.Profile),
		sessionRepo:   sr,
		modelRegistry: mr,
		extractor:     ext,
		flowClient:    fc,
		client:        &http.Client{Timeout: 5 * time.Second},
		vault:         vault,
	}

	return pm, nil
}

// resolveChromeBinary tự động phát hiện đường dẫn Chrome trên máy tính nếu cấu hình không tồn tại
func resolveChromeBinary(configured string) string {
	if configured != "" {
		if _, err := os.Stat(configured); err == nil {
			return configured
		}
	}
	candidates := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		filepath.Join(os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`),
		filepath.Join(os.Getenv("PROGRAMFILES"), `Google\Chrome\Application\chrome.exe`),
		filepath.Join(os.Getenv("PROGRAMFILES(X86)"), `Google\Chrome\Application\chrome.exe`),
	}
	for _, c := range candidates {
		if c != "" {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	if path, err := exec.LookPath("chrome"); err == nil {
		return path
	}
	if path, err := exec.LookPath("google-chrome"); err == nil {
		return path
	}
	return configured
}

// ScanAndDiscover quét toàn bộ các thư mục profile trong base_dir khi khởi động
func (pm *ProfileManager) ScanAndDiscover(ctx context.Context) ([]*domain.Profile, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	entries, err := os.ReadDir(pm.baseDir)
	if err != nil {
		return nil, err
	}

	var discovered []*domain.Profile
	portOffset := 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		profileID := entry.Name()
		profileDir := filepath.Join(pm.baseDir, profileID)
		cdpPort := pm.cdpPortStart + portOffset
		portOffset++

		prof := &domain.Profile{
			ID:         profileID,
			Dir:        profileDir,
			CDPPort:    cdpPort,
			IsLoggedIn: false,
			LastActive: time.Now(),
		}

		// Kiểm tra xem profile này đã có session.json lưu trước đó không
		sessionFile := filepath.Join(profileDir, "session.json")
		if data, err := os.ReadFile(sessionFile); err == nil {
			var sess StoredProfileSession
			if err := json.Unmarshal(data, &sess); err == nil {
				cookies := sess.Cookies
				if sess.EncryptedCookies != "" && pm.vault != nil {
					if dec, dErr := pm.vault.Decrypt(sess.EncryptedCookies); dErr == nil {
						var decMap map[string]string
						if jErr := json.Unmarshal(dec, &decMap); jErr == nil && len(decMap) > 0 {
							cookies = decMap
						}
					}
				}
				if len(cookies) > 0 {
					prof.Email = sess.Email
					prof.Proxy = sess.Proxy
					// Nạp session vào hệ thống
					jar := domain.NewCookieJar(cookies)
					hasGemini := jar.HasKey("__Secure-1PSID") && jar.HasKey("__Secure-1PSIDTS")
					hasFlow := jar.HasKey("OSID") && jar.HasKey("__Secure-OSID")

					if hasGemini || hasFlow {
						acc := &domain.ManagedAccount{
							ID:               profileID,
							Email:            sess.Email,
							ProxyURL:         sess.Proxy,
							Jar:              jar,
							FlowSNlM0e:       sess.FlowSNlM0e,
							GeminiSNlM0e:     sess.GeminiSNlM0e,
							FlowProjectID:    sess.FlowProjectID,
							FlowSessionToken: sess.FlowSessionToken,
							UserAgent:        sess.UserAgent,
							IsHealthy:        true,
							LastRefresh:      time.Now(),
						}

						_ = pm.sessionRepo.Save(ctx, acc)
						prof.IsLoggedIn = true
						prof.HasGemini = hasGemini
						prof.HasFlow = hasFlow
						prof.FlowCredits = sess.FlowCredits
						if hasGemini {
							if sess.GeminiQuota != "" {
								prof.GeminiQuota = sess.GeminiQuota
							} else {
								prof.GeminiQuota = "100%"
							}
						}

						// Danh mục cục bộ. Số dư và SNlM0e thiếu để lần request đầu xoay hoặc đọc.
						if hasGemini {
							pm.modelRegistry.ActivateServiceModels(domain.ServiceGemini, domain.GetGeminiCatalog())
						}
						if hasFlow {
							pm.modelRegistry.ActivateServiceModels(domain.ServiceFlow, domain.GetFlowCatalog())
						}
					}
				}
			}
		}

		pm.profiles[profileID] = prof
		discovered = append(discovered, prof)
	}

	return discovered, nil
}

// CreateProfile tạo một thư mục Profile riêng biệt cho tài khoản mới
func (pm *ProfileManager) CreateProfile(profileID string) (*domain.Profile, error) {
	return pm.CreateProfileWithProxy(profileID, "")
}

// CreateProfileWithProxy tạo một thư mục Profile với proxy cấu hình sẵn
func (pm *ProfileManager) CreateProfileWithProxy(profileID string, proxy string) (*domain.Profile, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return nil, fmt.Errorf("profile_id không được để trống")
	}

	if _, exists := pm.profiles[profileID]; exists {
		return nil, fmt.Errorf("profile %q đã tồn tại", profileID)
	}

	profileDir := filepath.Join(pm.baseDir, profileID)
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		return nil, fmt.Errorf("không thể tạo thư mục profile %s: %w", profileDir, err)
	}

	cdpPort := pm.cdpPortStart + len(pm.profiles)
	prof := &domain.Profile{
		ID:         profileID,
		Dir:        profileDir,
		CDPPort:    cdpPort,
		Proxy:      strings.TrimSpace(proxy),
		IsLoggedIn: false,
		LastActive: time.Now(),
	}

	pm.profiles[profileID] = prof
	return prof, nil
}

// SetProfileProxy cập nhật cấu hình proxy cho profile
func (pm *ProfileManager) SetProfileProxy(profileID string, proxy string) error {
	pm.mu.Lock()
	prof, ok := pm.profiles[profileID]
	if !ok {
		pm.mu.Unlock()
		return fmt.Errorf("profile %q không tồn tại", profileID)
	}
	prof.Proxy = strings.TrimSpace(proxy)
	pm.mu.Unlock()

	if acc, err := pm.sessionRepo.FindByID(context.Background(), profileID); err == nil && acc != nil {
		acc.SetProxy(proxy)
		_ = pm.sessionRepo.Save(context.Background(), acc)
	}

	sessionPath := filepath.Join(pm.baseDir, profileID, "session.json")
	if raw, err := os.ReadFile(sessionPath); err == nil {
		var stored StoredProfileSession
		if json.Unmarshal(raw, &stored) == nil {
			stored.Proxy = strings.TrimSpace(proxy)
			stored.UpdatedAt = time.Now()
			if enc, err := json.MarshalIndent(stored, "", "  "); err == nil {
				_ = os.WriteFile(sessionPath, enc, 0600)
			}
		}
	}
	return nil
}

// LaunchChromeForProfile mở Chrome với thư mục profile riêng biệt
func (pm *ProfileManager) LaunchChromeForProfile(profileID string) error {
	pm.mu.Lock()
	prof, ok := pm.profiles[profileID]
	if !ok {
		profileDir := filepath.Join(pm.baseDir, profileID)
		_ = os.MkdirAll(profileDir, 0755)
		prof = &domain.Profile{
			ID:         profileID,
			Dir:        profileDir,
			CDPPort:    pm.cdpPortStart + len(pm.profiles),
			IsLoggedIn: false,
			LastActive: time.Now(),
		}
		pm.profiles[profileID] = prof
	}
	pm.mu.Unlock()

	absDir, _ := filepath.Abs(prof.Dir)
	args := []string{
		fmt.Sprintf("--user-data-dir=%s", absDir),
		fmt.Sprintf("--remote-debugging-port=%d", prof.CDPPort),
		"--no-first-run",
		"--no-default-browser-check",
	}
	if prof.Proxy != "" {
		args = append(args, fmt.Sprintf("--proxy-server=%s", prof.Proxy))
	}
	args = append(args, "https://accounts.google.com")

	cmd := exec.Command(pm.chromeBin, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("không thể khởi chạy Chrome cho profile %s: %w", profileID, err)
	}

	return nil
}

// SyncCookiesFromCDP kết nối vào Chrome qua CDP WebSocket, đọc cookies tự động và kích hoạt mô hình
func (pm *ProfileManager) SyncCookiesFromCDP(ctx context.Context, profileID string) (*domain.ManagedAccount, error) {
	pm.mu.RLock()
	prof, ok := pm.profiles[profileID]
	pm.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("profile %q không tồn tại", profileID)
	}

	versionURL := fmt.Sprintf("http://127.0.0.1:%d/json/version", prof.CDPPort)
	resp, err := pm.client.Get(versionURL)
	if err != nil {
		return nil, fmt.Errorf("chrome profile %s chưa bật trên cổng %d: %w", profileID, prof.CDPPort, err)
	}
	defer resp.Body.Close()

	var verData struct {
		UserAgent            string `json:"User-Agent"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&verData); err != nil {
		return nil, fmt.Errorf("lỗi đọc thông tin CDP: %w", err)
	}

	if verData.WebSocketDebuggerURL == "" {
		return nil, fmt.Errorf("không tìm thấy webSocketDebuggerUrl từ Chrome CDP trên cổng %d", prof.CDPPort)
	}

	// Kết nối WebSocket tới Chrome CDP
	wsDialer := websocket.DefaultDialer
	wsConn, _, err := wsDialer.DialContext(ctx, verData.WebSocketDebuggerURL, nil)
	if err != nil {
		return nil, fmt.Errorf("kết nối WebSocket CDP thất bại: %w", err)
	}
	defer wsConn.Close()

	// Gửi lệnh Storage.getCookies để lấy toàn bộ cookies
	reqMsg := map[string]interface{}{
		"id":     1,
		"method": "Storage.getCookies",
	}
	if err := wsConn.WriteJSON(reqMsg); err != nil {
		return nil, fmt.Errorf("gửi lệnh Storage.getCookies thất bại: %w", err)
	}

	type cdpCookie struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Domain string `json:"domain"`
	}
	type cdpResponse struct {
		ID     int `json:"id"`
		Result struct {
			Cookies []cdpCookie `json:"cookies"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	var cdpResp cdpResponse
	if err := wsConn.ReadJSON(&cdpResp); err != nil {
		return nil, fmt.Errorf("đọc cookies từ Chrome CDP thất bại: %w", err)
	}

	if cdpResp.Error != nil {
		return nil, fmt.Errorf("CDP trả về lỗi: %s", cdpResp.Error.Message)
	}

	cookieMap := make(map[string]string)
	for _, c := range cdpResp.Result.Cookies {
		d := strings.TrimPrefix(c.Domain, ".")
		if d == "google.com" || d == "gemini.google.com" || d == "flow.google.com" {
			cookieMap[c.Name] = c.Value
		}
	}
	for _, c := range cdpResp.Result.Cookies {
		if _, exists := cookieMap[c.Name]; !exists && strings.Contains(c.Domain, "google.com") && !strings.Contains(c.Domain, ".vn") {
			cookieMap[c.Name] = c.Value
		}
	}

	email := prof.Email
	if email == "" {
		email = profileID + "@google.user"
	}

	return pm.IngestLiveCookies(ctx, profileID, email, cookieMap, verData.UserAgent)
}

// IngestLiveCookies nạp trực tiếp danh sách Cookie đã trích xuất từ Chrome vào Profile
func (pm *ProfileManager) IngestLiveCookies(
	ctx context.Context,
	profileID string,
	email string,
	rawCookies map[string]string,
	userAgent string,
) (*domain.ManagedAccount, error) {
	return pm.IngestLiveCookiesWithProxy(ctx, profileID, email, rawCookies, userAgent, "")
}

// IngestLiveCookiesWithProxy nạp trực tiếp danh sách Cookie kèm cấu hình Proxy cho Profile
func (pm *ProfileManager) IngestLiveCookiesWithProxy(
	ctx context.Context,
	profileID string,
	email string,
	rawCookies map[string]string,
	userAgent string,
	proxy string,
) (*domain.ManagedAccount, error) {
	jar := domain.NewCookieJar(rawCookies)

	hasGemini := jar.HasKey("__Secure-1PSID") && jar.HasKey("__Secure-1PSIDTS")
	hasFlow := jar.HasKey("OSID") && jar.HasKey("__Secure-OSID")

	if !hasGemini && !hasFlow {
		return nil, fmt.Errorf("chưa đủ cookie xác thực Google! Cần __Secure-1PSID (Gemini) hoặc OSID (Flow). Hãy hoàn tất đăng nhập Google trên Chrome rồi thử lại.")
	}

	pm.mu.RLock()
	prof, hasProf := pm.profiles[profileID]
	pm.mu.RUnlock()

	effectiveProxy := strings.TrimSpace(proxy)
	if effectiveProxy == "" && hasProf && prof.Proxy != "" {
		effectiveProxy = prof.Proxy
	}

	account := &domain.ManagedAccount{
		ID:          profileID,
		Email:       email,
		ProxyURL:    effectiveProxy,
		Jar:         jar,
		UserAgent:   userAgent,
		IsHealthy:   true,
		LastRefresh: time.Now(),
	}

	// Bắt tay lấy token CSRF SNlM0e nếu có dịch vụ tương ứng
	if val, ok := rawCookies["SNlM0e"]; ok && val != "" {
		account.GeminiSNlM0e = val
		account.FlowSNlM0e = val
	}

	if hasFlow && account.FlowSNlM0e == "" {
		snFlow, _, err := pm.extractor.ExtractTokens(ctx, account, domain.ServiceFlow)
		if err == nil {
			account.FlowSNlM0e = snFlow
			log.Printf("[Profile %s] Trích xuất thành công Flow SNlM0e", profileID)
		} else {
			log.Printf("[Profile %s] Lỗi trích xuất Flow SNlM0e: %v", profileID, err)
		}
	}
	if hasGemini && account.GeminiSNlM0e == "" {
		snGemini, _, err := pm.extractor.ExtractTokens(ctx, account, domain.ServiceGemini)
		if err == nil {
			account.GeminiSNlM0e = snGemini
			log.Printf("[Profile %s] Trích xuất thành công Gemini SNlM0e", profileID)
		} else {
			log.Printf("[Profile %s] Lỗi trích xuất Gemini SNlM0e: %v", profileID, err)
		}
	}

	// Giữ project id và session token đã ghi ở lần chạy trước nếu file còn.
	if oldRaw, readErr := os.ReadFile(filepath.Join(pm.baseDir, profileID, "session.json")); readErr == nil {
		var prev StoredProfileSession
		if json.Unmarshal(oldRaw, &prev) == nil {
			if prev.FlowProjectID != "" {
				account.SetFlowProjectID(prev.FlowProjectID)
			}
			if prev.FlowSessionToken != "" {
				account.SetFlowSessionToken(prev.FlowSessionToken)
			}
			if effectiveProxy == "" && prev.Proxy != "" {
				effectiveProxy = prev.Proxy
				account.SetProxy(effectiveProxy)
			}
		}
	}

	// Lưu vào bộ nhớ Session Repository
	if err := pm.sessionRepo.Save(ctx, account); err != nil {
		return nil, err
	}

	// Lưu vào file session.json trong thư mục profile riêng biệt
	profileDir := filepath.Join(pm.baseDir, profileID)
	_ = os.MkdirAll(profileDir, 0755)
	projectID, sessionToken := account.GetFlowMediaSecrets()

	// Đổ dữ liệu ra Model Registry tương ứng khi đã đăng nhập thành công
	if hasGemini {
		pm.modelRegistry.ActivateServiceModels(domain.ServiceGemini, domain.GetGeminiCatalog())
	}
	if hasFlow {
		pm.modelRegistry.ActivateServiceModels(domain.ServiceFlow, domain.GetFlowCatalog())
		if pm.flowClient != nil {
			credits, err := pm.readFlowCredits(ctx, account)
			if err == nil {
				account.CreditsBalance = credits
				log.Printf("[Profile %s] Nạp cookies: Đồng bộ Flow Credits thành công (%d credits)", profileID, credits)
			}
			if activeMap, modelErr := pm.flowClient.GetActiveModels(ctx, account); modelErr == nil && len(activeMap) > 0 {
				pm.modelRegistry.UpdateBackendStatus(domain.ServiceFlow, activeMap)
				log.Printf("[Profile %s] Đồng bộ ma trận mô hình Flow (HTrJv/yBhWQ) thành công: %d mô hình online", profileID, len(activeMap))
			}
		}
	}

	// Mã hóa cookies trước khi ghi vào session.json
	var encCookies string
	if pm.vault != nil {
		if rawBytes, err := json.Marshal(rawCookies); err == nil {
			if enc, err := pm.vault.Encrypt(rawBytes); err == nil {
				encCookies = enc
			}
		}
	}

	// Lưu phiên an toàn vào session.json với số dư credit và quota thực tế
	storedSess := StoredProfileSession{
		ProfileID:        profileID,
		Email:            email,
		Proxy:            effectiveProxy,
		EncryptedCookies: encCookies,
		FlowSNlM0e:       account.FlowSNlM0e,
		GeminiSNlM0e:     account.GeminiSNlM0e,
		FlowProjectID:    projectID,
		FlowSessionToken: sessionToken,
		UserAgent:        userAgent,
		GeminiQuota:      "",
		FlowCredits:      account.CreditsBalance,
		UpdatedAt:        time.Now(),
	}
	if encCookies == "" {
		storedSess.Cookies = rawCookies
	}
	if data, err := json.MarshalIndent(storedSess, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(profileDir, "session.json"), data, 0600)
	}

	// Cập nhật trạng thái Profile
	pm.mu.Lock()
	geminiQuota := ""
	if p, ok := pm.profiles[profileID]; ok {
		p.Email = email
		p.Proxy = effectiveProxy
		p.IsLoggedIn = true
		p.HasGemini = hasGemini
		p.HasFlow = hasFlow
		if p.GeminiQuota != "" {
			geminiQuota = p.GeminiQuota
		}
		p.GeminiQuota = geminiQuota
		p.FlowCredits = account.CreditsBalance
		p.LastActive = time.Now()
	} else {
		pm.profiles[profileID] = &domain.Profile{
			ID:          profileID,
			Dir:         profileDir,
			Email:       email,
			Proxy:       effectiveProxy,
			CDPPort:     pm.cdpPortStart + len(pm.profiles),
			IsLoggedIn:  true,
			HasGemini:   hasGemini,
			HasFlow:     hasFlow,
			GeminiQuota: geminiQuota,
			FlowCredits: account.CreditsBalance,
			LastActive:  time.Now(),
		}
	}
	pm.mu.Unlock()

	return account, nil
}

func (pm *ProfileManager) RememberFlowProject(accountID, projectID string) error {
	if accountID == "" || projectID == "" {
		return domain.InvalidRequest(domain.OpSession, domain.OriginCreateProject, domain.ServiceFlow, "thiếu định danh dự án")
	}
	if acc, err := pm.sessionRepo.FindByID(context.Background(), accountID); err == nil && acc != nil {
		acc.SetFlowProjectID(projectID)
	}
	path := filepath.Join(pm.baseDir, accountID, "session.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var stored StoredProfileSession
	if err := json.Unmarshal(raw, &stored); err != nil {
		return err
	}
	stored.FlowProjectID = projectID
	stored.UpdatedAt = time.Now()
	encoded, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0600)
}

func (pm *ProfileManager) readFlowCredits(ctx context.Context, account *domain.ManagedAccount) (int, error) {
	var balance domain.FlowCreditBalance
	err := RetryAfterRefresh(ctx, pm.sessionRepo, account, domain.ServiceFlow, func() error {
		var callErr error
		balance, callErr = pm.flowClient.GetCreditsBalance(ctx, account)
		return callErr
	})
	if err != nil {
		return 0, err
	}
	return balance.Amount, nil
}

// ReadProfileCredits đọc số dư credits trực tiếp từ Google Flow cho profile
func (pm *ProfileManager) ReadProfileCredits(ctx context.Context, profileID string) (int, error) {
	pm.mu.RLock()
	prof, ok := pm.profiles[profileID]
	pm.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("profile %q không tồn tại", profileID)
	}

	acc, err := pm.sessionRepo.FindByID(ctx, profileID)
	if err != nil || acc == nil {
		return 0, fmt.Errorf("chưa có phiên đăng nhập cho profile %s", profileID)
	}

	credits, err := pm.readFlowCredits(ctx, acc)
	if err != nil {
		return 0, err
	}

	acc.CreditsBalance = credits
	_ = pm.sessionRepo.Save(ctx, acc)

	pm.mu.Lock()
	prof.FlowCredits = credits
	pm.mu.Unlock()

	// Cập nhật session.json
	sessionPath := filepath.Join(pm.baseDir, profileID, "session.json")
	if data, readErr := os.ReadFile(sessionPath); readErr == nil {
		var stored StoredProfileSession
		if json.Unmarshal(data, &stored) == nil {
			stored.FlowCredits = credits
			stored.UpdatedAt = time.Now()
			if enc, mErr := json.MarshalIndent(stored, "", "  "); mErr == nil {
				_ = os.WriteFile(sessionPath, enc, 0600)
			}
		}
	}

	return credits, nil
}

// SetProfileQuota cập nhật thông tin hạn ngạch Gemini cho profile và lưu session.json
func (pm *ProfileManager) SetProfileQuota(profileID string, quota string) {
	pm.mu.Lock()
	if prof, ok := pm.profiles[profileID]; ok {
		prof.GeminiQuota = quota
	}
	pm.mu.Unlock()

	sessionPath := filepath.Join(pm.baseDir, profileID, "session.json")
	if data, readErr := os.ReadFile(sessionPath); readErr == nil {
		var stored StoredProfileSession
		if json.Unmarshal(data, &stored) == nil {
			stored.GeminiQuota = quota
			stored.UpdatedAt = time.Now()
			if enc, mErr := json.MarshalIndent(stored, "", "  "); mErr == nil {
				_ = os.WriteFile(sessionPath, enc, 0600)
			}
		}
	}
}

func (pm *ProfileManager) ListActiveProfiles() []*domain.Profile {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	list := make([]*domain.Profile, 0, len(pm.profiles))
	for _, p := range pm.profiles {
		list = append(list, p)
	}
	return list
}

// SyncFlowModelMatrix đồng bộ trạng thái trực tuyến các cụm GPU qua RPC HTrJv / yBhWQ
func (pm *ProfileManager) SyncFlowModelMatrix(ctx context.Context, account *domain.ManagedAccount) error {
	if pm.flowClient == nil || account == nil || !account.ServiceReady(domain.ServiceFlow) {
		return nil
	}
	activeMap, err := pm.flowClient.GetActiveModels(ctx, account)
	if err != nil {
		return err
	}
	if len(activeMap) > 0 {
		pm.modelRegistry.UpdateBackendStatus(domain.ServiceFlow, activeMap)
	}
	return nil
}
