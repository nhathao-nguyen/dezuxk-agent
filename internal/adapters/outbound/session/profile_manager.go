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
	"regexp"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"

	"github.com/gorilla/websocket"
)

var validProfileIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateProfileID kiểm tra tính hợp lệ của profile ID và phòng chống tấn công Path Traversal
func ValidateProfileID(profileID string) error {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return fmt.Errorf("tên profile không được để trống")
	}
	if !validProfileIDRegex.MatchString(profileID) || strings.Contains(profileID, "..") || strings.ContainsAny(profileID, "/\\") {
		return fmt.Errorf("tên profile không hợp lệ: chỉ chấp nhận chữ cái, số, gạch dưới và gạch nối")
	}
	return nil
}

type ProfileManager struct {
	mu            sync.RWMutex
	baseDir       string
	chromeBin     string
	cdpPortStart  int
	profiles      map[string]*domain.Profile
	sessionRepo   ports.SessionRepository
	modelRegistry *domain.ModelRegistry
	extractor     ports.TokenExtractor
	client        *http.Client
	vault         *Vault
	quotaUseCase  ports.GeminiQuotaUseCase
}

func (pm *ProfileManager) SetQuotaUseCase(qu ports.GeminiQuotaUseCase) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.quotaUseCase = qu
}

type StoredProfileSession struct {
	ProfileID        string            `json:"profile_id"`
	Email            string            `json:"email"`
	Proxy            string            `json:"proxy,omitempty"`
	Cookies          map[string]string `json:"cookies,omitempty"`
	EncryptedCookies string            `json:"encrypted_cookies,omitempty"`
	GeminiSNlM0e     string            `json:"gemini_sn_token,omitempty"`
	UserAgent        string            `json:"user_agent"`
	GeminiQuota      string            `json:"gemini_quota,omitempty"`
	Tier             int               `json:"tier,omitempty"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

func NewProfileManager(
	cfg *config.Config,
	sr ports.SessionRepository,
	mr *domain.ModelRegistry,
	ext ports.TokenExtractor,
	v ...*Vault,
) (*ProfileManager, error) {
	absBaseDir, err := filepath.Abs(cfg.Profiles.BaseDir)
	if err != nil {
		absBaseDir = cfg.Profiles.BaseDir
	}
	if err := os.MkdirAll(absBaseDir, 0755); err != nil {
		return nil, fmt.Errorf("không thể tạo thư mục profiles tại %s: %w", absBaseDir, err)
	}

	var vault *Vault
	if len(v) > 0 && v[0] != nil {
		vault = v[0]
	} else {
		vault = NewVault(ResolveMasterKey(""))
	}

	pm := &ProfileManager{
		baseDir:       absBaseDir,
		chromeBin:     resolveChromeBinary(cfg.Profiles.ChromeBinary),
		cdpPortStart:  cfg.Profiles.CDPPortStart,
		profiles:      make(map[string]*domain.Profile),
		sessionRepo:   sr,
		modelRegistry: mr,
		extractor:     ext,
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
		"/usr/bin/google-chrome",
		"/usr/bin/chromium-browser",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "chrome"
}

// ScanAndDiscover quét toàn bộ thư mục profiles/ và nạp các profile đã có
func (pm *ProfileManager) ScanAndDiscover(ctx context.Context) ([]*domain.Profile, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	entries, err := os.ReadDir(pm.baseDir)
	if err != nil {
		return nil, fmt.Errorf("không thể đọc thư mục %s: %w", pm.baseDir, err)
	}

	var discovered []*domain.Profile
	portCounter := 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		profileID := entry.Name()
		profileDir := filepath.Join(pm.baseDir, profileID)
		cdpPort := pm.cdpPortStart + portCounter
		portCounter++

		prof := &domain.Profile{
			ID:         profileID,
			Dir:        profileDir,
			CDPPort:    cdpPort,
			IsLoggedIn: false,
		}

		sessionPath := filepath.Join(profileDir, "session.json")
		if data, err := os.ReadFile(sessionPath); err == nil {
			var sess StoredProfileSession
			if err := json.Unmarshal(data, &sess); err == nil {
				prof.Email = sess.Email
				prof.Proxy = sess.Proxy
				cookies := sess.Cookies
				if sess.EncryptedCookies != "" && pm.vault != nil {
					if decBytes, decErr := pm.vault.Decrypt(sess.EncryptedCookies); decErr == nil {
						var decMap map[string]string
						if json.Unmarshal(decBytes, &decMap) == nil {
							cookies = decMap
						}
					}
				}

				// Tự động nâng cấp mã hóa Vault AES-256-GCM nếu file session trên đĩa còn lưu cookie dạng plaintext
				if sess.EncryptedCookies == "" && len(sess.Cookies) > 0 && pm.vault != nil {
					if rawBytes, err := json.Marshal(sess.Cookies); err == nil {
						if enc, err := pm.vault.Encrypt(rawBytes); err == nil {
							sess.EncryptedCookies = enc
							sess.Cookies = nil // Loại bỏ cookie plaintext khỏi đĩa
							sess.UpdatedAt = time.Now()
							if updatedData, err := json.MarshalIndent(sess, "", "  "); err == nil {
								_ = os.WriteFile(sessionPath, updatedData, 0600)
								log.Printf("[Profile %s] Đã tự động nâng cấp mã hóa Vault AES-256-GCM cho session.json trên đĩa", profileID)
							}
						}
					}
				}

				if len(cookies) > 0 {
					prof.Proxy = sess.Proxy
					jar := domain.NewCookieJar(cookies)
					hasGemini := jar.HasKey("__Secure-1PSID") && jar.HasKey("__Secure-1PSIDTS")

					if hasGemini {
						tier := sess.Tier
						if tier == 0 {
							tier = 1
						}
						acc := &domain.ManagedAccount{
							ID:           profileID,
							Email:        sess.Email,
							ProxyURL:     sess.Proxy,
							Jar:          jar,
							GeminiSNlM0e: sess.GeminiSNlM0e,
							UserAgent:    sess.UserAgent,
							Tier:         tier,
							IsHealthy:    true,
							LastRefresh:  time.Now(),
						}

						_ = pm.sessionRepo.Save(ctx, acc)
						prof.IsLoggedIn = true
						prof.HasGemini = true
						if sess.GeminiQuota != "" {
							prof.GeminiQuota = sess.GeminiQuota
						} else {
							prof.GeminiQuota = "100%"
						}

						pm.modelRegistry.ActivateServiceModels(domain.ServiceGemini, domain.GetGeminiCatalog())
					}
				}
			}
		}

		pm.profiles[profileID] = prof
		discovered = append(discovered, prof)
	}

	return discovered, nil
}

// CreateProfile khởi tạo một thư mục Profile Chrome độc lập mới
func (pm *ProfileManager) CreateProfile(profileID string) (*domain.Profile, error) {
	return pm.CreateProfileWithProxy(profileID, "")
}

// CreateProfileWithProxy khởi tạo profile với tùy chọn Proxy mạng
func (pm *ProfileManager) CreateProfileWithProxy(profileID string, proxy string) (*domain.Profile, error) {
	if err := ValidateProfileID(profileID); err != nil {
		return nil, err
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if p, exists := pm.profiles[profileID]; exists {
		if proxy != "" {
			p.Proxy = proxy
		}
		return p, nil
	}

	profileDir := filepath.Join(pm.baseDir, profileID)
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		return nil, fmt.Errorf("không thể tạo thư mục profile tại %s: %w", profileDir, err)
	}

	cdpPort := pm.cdpPortStart + len(pm.profiles)
	prof := &domain.Profile{
		ID:         profileID,
		Dir:        profileDir,
		CDPPort:    cdpPort,
		Proxy:      proxy,
		IsLoggedIn: false,
	}

	pm.profiles[profileID] = prof
	return prof, nil
}

// SetProfileProxy cập nhật Proxy cho một Profile cụ thể
func (pm *ProfileManager) SetProfileProxy(profileID string, proxy string) error {
	if err := ValidateProfileID(profileID); err != nil {
		return err
	}

	pm.mu.Lock()
	prof, ok := pm.profiles[profileID]
	if !ok {
		pm.mu.Unlock()
		return fmt.Errorf("profile %q không tồn tại", profileID)
	}
	prof.Proxy = proxy
	pm.mu.Unlock()

	sessionPath := filepath.Join(pm.baseDir, profileID, "session.json")
	if data, err := os.ReadFile(sessionPath); err == nil {
		var sess StoredProfileSession
		if json.Unmarshal(data, &sess) == nil {
			sess.Proxy = proxy
			sess.UpdatedAt = time.Now()
			if enc, mErr := json.MarshalIndent(sess, "", "  "); mErr == nil {
				_ = os.WriteFile(sessionPath, enc, 0600)
			}
		}
	}

	acc, err := pm.sessionRepo.FindByID(context.Background(), profileID)
	if err == nil && acc != nil {
		acc.SetProxy(proxy)
		_ = pm.sessionRepo.Save(context.Background(), acc)
	}

	return nil
}

// LaunchChromeForProfile mở trình duyệt Chrome cho Profile này với cổng CDP riêng
func (pm *ProfileManager) LaunchChromeForProfile(profileID string) error {
	if err := ValidateProfileID(profileID); err != nil {
		return err
	}

	pm.mu.RLock()
	prof, ok := pm.profiles[profileID]
	pm.mu.RUnlock()

	if !ok {
		return fmt.Errorf("profile %q không tồn tại", profileID)
	}

	userDataDir := prof.Dir
	if absDir, err := filepath.Abs(userDataDir); err == nil {
		userDataDir = absDir
	}
	cdpPort := prof.CDPPort

	// Nếu Chrome đã đang chạy và phản hồi trên cổng CDP này, không cần khởi chạy lại
	versionURL := fmt.Sprintf("http://127.0.0.1:%d/json/version", cdpPort)
	if resp, err := pm.client.Get(versionURL); err == nil {
		_ = resp.Body.Close()
		log.Printf("[Profile %s] Chrome đã đang chạy trên cổng CDP %d", profileID, cdpPort)
		return nil
	}

	args := []string{
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		fmt.Sprintf("--remote-debugging-port=%d", cdpPort),
		"--remote-allow-origins=*",
		"--new-window",
		"--start-maximized",
		"--window-position=50,50",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-blink-features=AutomationControlled",
	}

	if prof.Proxy != "" {
		args = append(args, fmt.Sprintf("--proxy-server=%s", prof.Proxy))
	}

	args = append(args, "https://gemini.google.com/app")

	cmd := exec.Command(pm.chromeBin, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("không thể khởi chạy Chrome cho profile %s: %w", profileID, err)
	}

	log.Printf("[Profile %s] Đã mở Chrome thành công (CDP Port: %d, DataDir: %s)", profileID, cdpPort, userDataDir)
	return nil
}

// SyncCookiesFromCDP kết nối vào Chrome qua CDP WebSocket và trích xuất cookie trực tiếp từ RAM trình duyệt
func (pm *ProfileManager) SyncCookiesFromCDP(ctx context.Context, profileID string) (*domain.ManagedAccount, error) {
	if err := ValidateProfileID(profileID); err != nil {
		return nil, err
	}

	pm.mu.RLock()
	prof, ok := pm.profiles[profileID]
	pm.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("profile %q không tồn tại", profileID)
	}

	versionURL := fmt.Sprintf("http://127.0.0.1:%d/json/version", prof.CDPPort)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	resp, err := pm.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("không thể kết nối tới Chrome CDP tại cổng %d: %w", prof.CDPPort, err)
	}
	defer resp.Body.Close()

	var verData struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
		UserAgent            string `json:"User-Agent"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&verData); err != nil {
		return nil, fmt.Errorf("lỗi đọc JSON từ /json/version: %w", err)
	}

	if verData.WebSocketDebuggerURL == "" {
		return nil, fmt.Errorf("không tìm thấy webSocketDebuggerUrl từ CDP")
	}

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 3 * time.Second
	wsConn, _, err := dialer.DialContext(ctx, verData.WebSocketDebuggerURL, nil)
	if err != nil {
		return nil, fmt.Errorf("không thể kết nối WebSocket tới CDP: %w", err)
	}
	defer wsConn.Close()

	type cdpCookie struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Domain string `json:"domain"`
		Path   string `json:"path"`
	}

	type cdpResponse struct {
		ID     int `json:"id"`
		Result struct {
			Cookies []cdpCookie `json:"cookies"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}

	// Thử Storage.getCookies trước (chuẩn Chrome CDP hiện đại), nếu không có thì fallback Network.getAllCookies
	var cdpResp cdpResponse
	reqID := 1
	methods := []string{"Storage.getCookies", "Network.getAllCookies"}
	var lastErr error

	for _, method := range methods {
		cdpReq := map[string]interface{}{
			"id":     reqID,
			"method": method,
			"params": map[string]interface{}{},
		}
		reqID++
		if err := wsConn.WriteJSON(cdpReq); err != nil {
			lastErr = fmt.Errorf("gửi lệnh %s thất bại: %w", method, err)
			continue
		}
		var resp cdpResponse
		if err := wsConn.ReadJSON(&resp); err != nil {
			lastErr = fmt.Errorf("đọc cookies từ Chrome CDP (%s) thất bại: %w", method, err)
			continue
		}
		if resp.Error != nil {
			lastErr = fmt.Errorf("CDP trả về lỗi (%s): %s", method, resp.Error.Message)
			continue
		}
		cdpResp = resp
		lastErr = nil
		break
	}

	if lastErr != nil && len(cdpResp.Result.Cookies) == 0 {
		return nil, lastErr
	}

	cookieMap := make(map[string]string)
	for _, c := range cdpResp.Result.Cookies {
		d := strings.TrimPrefix(c.Domain, ".")
		if d == "google.com" || d == "gemini.google.com" {
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

// IngestLiveCookiesWithProxy nạp trực tiếp Cookie kèm tùy chọn Proxy
func (pm *ProfileManager) IngestLiveCookiesWithProxy(
	ctx context.Context,
	profileID string,
	email string,
	rawCookies map[string]string,
	userAgent string,
	proxy string,
) (*domain.ManagedAccount, error) {
	if err := ValidateProfileID(profileID); err != nil {
		return nil, err
	}

	jar := domain.NewCookieJar(rawCookies)

	hasGemini := jar.HasKey("__Secure-1PSID") && jar.HasKey("__Secure-1PSIDTS")
	if !hasGemini {
		return nil, fmt.Errorf("chưa đủ cookie xác thực Google! Cần __Secure-1PSID và __Secure-1PSIDTS cho Gemini. Hãy hoàn tất đăng nhập Google trên Chrome rồi thử lại.")
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
	}

	if account.GeminiSNlM0e == "" {
		snGemini, _, err := pm.extractor.ExtractTokens(ctx, account, domain.ServiceGemini)
		if err == nil {
			account.GeminiSNlM0e = snGemini
			log.Printf("[Profile %s] Trích xuất thành công Gemini SNlM0e", profileID)
		} else {
			log.Printf("[Profile %s] Lỗi trích xuất Gemini SNlM0e: %v", profileID, err)
		}
	}

	// Tự động truy vấn và xác thực Tier thực tế từ Google (Pro / Ultra / Free) ngay khi đồng bộ
	account.Tier = 1
	pm.mu.RLock()
	qu := pm.quotaUseCase
	pm.mu.RUnlock()

	if qu != nil {
		qCtx, qCancel := context.WithTimeout(ctx, 15*time.Second)
		tierInfo, err := qu.GetAccountTierForAccount(qCtx, account)
		qCancel()
		if err != nil {
			log.Printf("[Profile %s Warning] Không thể tra cứu Tier từ Google: %v", profileID, err)
		} else if tierInfo != nil {
			if tierInfo.TierCode == "GOOGLE_AI_PRO" || tierInfo.TierCode == "GOOGLE_ONE_AI_PREMIUM" {
				account.Tier = 2
			} else if tierInfo.TierCode == "WORKSPACE_ENTERPRISE" {
				account.Tier = 3
			}
			log.Printf("[Profile %s] Đã tự động xác thực cấp độ gói từ Google: %s (Tier %d)", profileID, tierInfo.TierCode, account.Tier)
		}
	}

	// Lưu vào bộ nhớ Session Repository
	if err := pm.sessionRepo.Save(ctx, account); err != nil {
		return nil, err
	}

	// Lưu vào file session.json trong thư mục profile riêng biệt
	profileDir := filepath.Join(pm.baseDir, profileID)
	_ = os.MkdirAll(profileDir, 0755)

	pm.modelRegistry.ActivateServiceModels(domain.ServiceGemini, domain.GetGeminiCatalog())

	// Mã hóa cookies trước khi ghi vào session.json
	var encCookies string
	if pm.vault != nil {
		if rawBytes, err := json.Marshal(rawCookies); err == nil {
			if enc, err := pm.vault.Encrypt(rawBytes); err == nil {
				encCookies = enc
			}
		}
	}

	// Lưu phiên an toàn vào session.json
	storedSess := StoredProfileSession{
		ProfileID:        profileID,
		Email:            email,
		Proxy:            effectiveProxy,
		EncryptedCookies: encCookies,
		GeminiSNlM0e:     account.GeminiSNlM0e,
		UserAgent:        userAgent,
		GeminiQuota:      "",
		Tier:             account.Tier,
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
		if p.GeminiQuota != "" {
			geminiQuota = p.GeminiQuota
		}
		p.GeminiQuota = geminiQuota
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
			GeminiQuota: geminiQuota,
			LastActive:  time.Now(),
		}
	}
	pm.mu.Unlock()

	return account, nil
}

// SetProfileQuota cập nhật thông tin hạn ngạch Gemini cho profile và lưu session.json
func (pm *ProfileManager) SetProfileQuota(profileID string, quota string) {
	if err := ValidateProfileID(profileID); err != nil {
		return
	}

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
