package http

import (
	"encoding/json"
	"net/http"
	"strings"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"github.com/go-chi/chi/v5"
)

type ProfileHandler struct {
	profileMgr ports.ProfileUseCase
}

func NewProfileHandler(pm ports.ProfileUseCase) *ProfileHandler {
	return &ProfileHandler{profileMgr: pm}
}

func (h *ProfileHandler) HandleListProfiles(w http.ResponseWriter, r *http.Request) {
	profiles := h.profileMgr.ListActiveProfiles()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"profiles": profiles,
		"count":    len(profiles),
	})
}

type CreateProfileRequest struct {
	ID    string `json:"id"`
	Proxy string `json:"proxy,omitempty"`
}

func (h *ProfileHandler) HandleCreateProfile(w http.ResponseWriter, r *http.Request) {
	var req CreateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}

	var prof *domain.Profile
	var err error
	if req.Proxy != "" {
		prof, err = h.profileMgr.CreateProfileWithProxy(req.ID, req.Proxy)
	} else {
		prof, err = h.profileMgr.CreateProfile(req.ID)
	}
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "created",
		"profile": prof,
		"message": "Đã tạo thư mục Profile độc lập cho tài khoản.",
	})
}

func (h *ProfileHandler) HandleLaunchChrome(w http.ResponseWriter, r *http.Request) {
	profileID := chi.URLParam(r, "id")
	if profileID == "" {
		http.Error(w, `{"error":"profile id is required"}`, http.StatusBadRequest)
		return
	}

	err := h.profileMgr.LaunchChromeForProfile(profileID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "launched",
		"profile_id": profileID,
		"message":    "Chrome đã mở với profile riêng. Hãy hoàn tất đăng nhập Google trong cửa sổ Chrome đó.",
	})
}

func (h *ProfileHandler) HandleSyncCDP(w http.ResponseWriter, r *http.Request) {
	profileID := chi.URLParam(r, "id")
	if profileID == "" {
		http.Error(w, `{"error":"profile id is required"}`, http.StatusBadRequest)
		return
	}

	acc, err := h.profileMgr.SyncCookiesFromCDP(r.Context(), profileID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "activated",
		"profile_id": profileID,
		"email":      acc.Email,
		"has_flow":   acc.Jar.HasKey("OSID"),
		"has_gemini": acc.Jar.HasKey("__Secure-1PSID"),
		"message":    "Đồng bộ cookie từ Chrome thành công! Dữ liệu các mô hình tương ứng đã bắt đầu đổ ra Gateway.",
	})
}

type IngestCookiesRequest struct {
	Email     string          `json:"email"`
	Cookies   json.RawMessage `json:"cookies"`
	CookieStr string          `json:"cookie_str"`
	UserAgent string          `json:"user_agent"`
	Proxy     string          `json:"proxy,omitempty"`
}

func (h *ProfileHandler) HandleIngestCookies(w http.ResponseWriter, r *http.Request) {
	profileID := chi.URLParam(r, "id")
	if profileID == "" {
		http.Error(w, `{"error":"profile id is required"}`, http.StatusBadRequest)
		return
	}

	var req IngestCookiesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}

	cookieMap := make(map[string]string)

	// 1. Thử parse dạng Map: {"OSID":"...", "__Secure-1PSID":"..."}
	if len(req.Cookies) > 0 {
		var asMap map[string]string
		if err := json.Unmarshal(req.Cookies, &asMap); err == nil && len(asMap) > 0 {
			cookieMap = asMap
		} else {
			// 2. Thử parse dạng CDP Array: [{"name":"OSID", "value":"..."}, ...]
			var asArray []struct {
				Name   string `json:"name"`
				Value  string `json:"value"`
				Domain string `json:"domain"`
			}
			if err := json.Unmarshal(req.Cookies, &asArray); err == nil && len(asArray) > 0 {
				// 1. Ưu tiên các cookie thuộc chính xác google.com, gemini.google.com, flow.google.com
				for _, c := range asArray {
					d := strings.TrimPrefix(c.Domain, ".")
					if d == "google.com" || d == "gemini.google.com" || d == "flow.google.com" {
						cookieMap[c.Name] = c.Value
					}
				}
				// 2. Nạp thêm các cookie phụ nếu chưa có và không thuộc domain địa phương .vn
				for _, c := range asArray {
					if _, exists := cookieMap[c.Name]; !exists && strings.Contains(c.Domain, "google.com") && !strings.Contains(c.Domain, ".vn") {
						cookieMap[c.Name] = c.Value
					}
				}
			}
		}
	}

	// 3. Thử parse chuỗi cookie thô: "OSID=...; __Secure-1PSID=..."
	if len(cookieMap) == 0 && req.CookieStr != "" {
		parts := strings.Split(req.CookieStr, ";")
		for _, p := range parts {
			kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
			if len(kv) == 2 {
				cookieMap[kv[0]] = kv[1]
			}
		}
	}

	if len(cookieMap) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "không tìm thấy cookie hợp lệ trong payload (chấp nhận map, array CDP hoặc chuỗi cookie_str)"})
		return
	}

	var acc *domain.ManagedAccount
	var err error
	if req.Proxy != "" {
		acc, err = h.profileMgr.IngestLiveCookiesWithProxy(r.Context(), profileID, req.Email, cookieMap, req.UserAgent, req.Proxy)
	} else {
		acc, err = h.profileMgr.IngestLiveCookies(r.Context(), profileID, req.Email, cookieMap, req.UserAgent)
	}
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "activated",
		"profile_id": profileID,
		"email":      acc.Email,
		"proxy":      acc.GetProxy(),
		"has_flow":   acc.Jar.HasKey("OSID"),
		"has_gemini": acc.Jar.HasKey("__Secure-1PSID"),
		"message":    "Nạp cookie thành công! Dữ liệu các mô hình tương ứng đã bắt đầu đổ ra Gateway.",
	})
}

type SetProxyRequest struct {
	Proxy string `json:"proxy"`
}

func (h *ProfileHandler) HandleSetProxy(w http.ResponseWriter, r *http.Request) {
	profileID := chi.URLParam(r, "id")
	if profileID == "" {
		http.Error(w, `{"error":"profile id is required"}`, http.StatusBadRequest)
		return
	}

	var req SetProxyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}

	if err := h.profileMgr.SetProfileProxy(profileID, req.Proxy); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "updated",
		"profile_id": profileID,
		"proxy":      req.Proxy,
		"message":    "Cập nhật Proxy cho Profile thành công.",
	})
}
