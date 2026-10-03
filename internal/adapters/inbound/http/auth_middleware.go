package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// VirtualKeyAuthMiddleware tạo middleware xác thực Virtual API Keys đa người dùng
func VirtualKeyAuthMiddleware(keyUseCase ports.KeyUseCase) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if keyUseCase == nil {
				next.ServeHTTP(w, r)
				return
			}

			// 1. Trích xuất Token từ header Authorization, x-api-key hoặc Cookie dezuxk_admin_token
			token := r.Header.Get("Authorization")
			if token == "" {
				token = r.Header.Get("x-api-key")
			}
			if token == "" {
				if cookie, err := r.Cookie("dezuxk_admin_token"); err == nil && cookie.Value != "" {
					token = cookie.Value
				}
			}
			if token == "" {
				writeAuthError(w, http.StatusUnauthorized, "missing_api_key", "Thiếu khóa API trong header Authorization (Bearer <key>) hoặc x-api-key.")
				return
			}

			// 2. Trích xuất tên mô hình yêu cầu (nếu là request JSON POST) để đối soát phân quyền
			targetModel := extractTargetModel(r)

			// 3. Xác thực khóa qua KeyUseCase
			vKey, err := keyUseCase.ValidateKey(r.Context(), token, targetModel)
			if err != nil {
				switch {
				case errors.Is(err, domain.ErrMissingAPIKey), errors.Is(err, domain.ErrInvalidAPIKey):
					writeAuthError(w, http.StatusUnauthorized, "invalid_api_key", "Khóa API không hợp lệ hoặc không tồn tại.")
				case errors.Is(err, domain.ErrKeyRevoked):
					writeAuthError(w, http.StatusUnauthorized, "key_revoked", "Khóa API đã bị thu hồi hoặc vô hiệu hóa.")
				case errors.Is(err, domain.ErrKeyExpired):
					writeAuthError(w, http.StatusUnauthorized, "key_expired", "Khóa API đã hết hạn sử dụng.")
				case errors.Is(err, domain.ErrModelNotAllowed):
					writeAuthError(w, http.StatusForbidden, "model_not_allowed", "Khóa API này không có quyền truy cập vào mô hình: "+targetModel)
				case errors.Is(err, domain.ErrRateLimitRPMExceeded):
					writeAuthError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "Đã vượt quá giới hạn tần suất yêu cầu trên phút (RPM) của khóa API.")
				case errors.Is(err, domain.ErrDailyQuotaExceeded):
					writeAuthError(w, http.StatusTooManyRequests, "quota_exceeded", "Đã sử dụng hết hạn ngạch yêu cầu trong ngày (Daily Quota) của khóa API.")
				default:
					writeAuthError(w, http.StatusUnauthorized, "auth_error", err.Error())
				}
				return
			}

			// Nếu endpoint yêu cầu model (/v1/chat/completions, /v1/responses) nhưng khóa bị giới hạn model và không xác định được model:
			isModelEndpoint := (r.Method == http.MethodPost) && (strings.HasPrefix(r.URL.Path, "/v1/chat/completions") || strings.HasPrefix(r.URL.Path, "/v1/responses"))
			if isModelEndpoint && targetModel == "" && !vKey.IsModelAllowed("") {
				writeAuthError(w, http.StatusForbidden, "model_not_allowed", "Khóa API bị giới hạn danh sách mô hình và không thể xác định mô hình đích trong yêu cầu.")
				return
			}

			// 4. Tiêu thụ 1 lượt hạn ngạch ngày (Quota Decrementor) chỉ đối với các tác vụ tính phí (/v1/chat/completions)
			if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/chat/completions") {
				if _, err := keyUseCase.ConsumeQuota(r.Context(), vKey.ID); err != nil {
					if errors.Is(err, domain.ErrDailyQuotaExceeded) {
						writeAuthError(w, http.StatusTooManyRequests, "quota_exceeded", "Đã sử dụng hết hạn ngạch yêu cầu trong ngày của khóa API.")
						return
					}
					writeAuthError(w, http.StatusInternalServerError, "quota_error", "Không thể ghi nhận hạn ngạch.")
					return
				}
			}

			// 5. Đưa thông tin VirtualKey và TenantIdentity vào Request Context để các tầng sau sử dụng
			identity := vKey.ToIdentity()
			ctx := domain.ContextWithVirtualKey(r.Context(), vKey)
			ctx = domain.ContextWithTenantIdentity(ctx, identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireScope kiểm tra xem khóa API hiện tại có scope chỉ định hay không
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := domain.TenantIdentityFromContext(r.Context())
			if !ok {
				vKey := domain.VirtualKeyFromContext(r.Context())
				if vKey != nil {
					id = vKey.ToIdentity()
				} else {
					writeAuthError(w, http.StatusUnauthorized, "unauthorized", "Yêu cầu xác thực khóa API.")
					return
				}
			}

			if !id.HasScope(scope) {
				writeAuthError(w, http.StatusForbidden, "scope_not_allowed", "Khóa API không có quyền truy cập phạm vi (scope): "+scope)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireAdmin kiểm tra quyền quản trị (role == 'admin')
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := domain.TenantIdentityFromContext(r.Context())
		if ok {
			if id.Role == "admin" {
				next.ServeHTTP(w, r)
				return
			}
			writeAuthError(w, http.StatusForbidden, "admin_required", "Yêu cầu quyền quản trị viên (role: admin) để thực hiện thao tác này.")
			return
		}

		vKey := domain.VirtualKeyFromContext(r.Context())
		if vKey == nil {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized", "Yêu cầu xác thực khóa API.")
			return
		}
		if vKey.Role != "admin" {
			writeAuthError(w, http.StatusForbidden, "admin_required", "Yêu cầu quyền quản trị viên (role: admin) để thực hiện thao tác này.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func extractTargetModel(r *http.Request) string {
	// Kiểm tra query parameter
	if m := r.URL.Query().Get("model"); m != "" {
		return strings.TrimSpace(m)
	}

	// Đọc thân JSON (hỗ trợ payload lớn tới 50MB) bằng streaming token scanner không làm cạn kiệt bộ nhớ
	if r.Method == http.MethodPost && strings.Contains(r.Header.Get("Content-Type"), "application/json") && r.Body != nil {
		const maxScan = 50 * 1024 * 1024
		buf := new(bytes.Buffer)
		tee := io.TeeReader(io.LimitReader(r.Body, maxScan), buf)
		model := parseModelFromJSONReader(tee)
		r.Body = io.NopCloser(io.MultiReader(buf, r.Body))
		return model
	}
	return ""
}

func parseModelFromJSONReader(reader io.Reader) string {
	dec := json.NewDecoder(reader)
	t, err := dec.Token()
	if err != nil {
		return ""
	}
	if delim, ok := t.(json.Delim); !ok || delim != '{' {
		return ""
	}

	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return ""
		}
		key, ok := t.(string)
		if !ok {
			return ""
		}
		if key == "model" {
			tVal, err := dec.Token()
			if err != nil {
				return ""
			}
			if strVal, ok := tVal.(string); ok {
				return strings.TrimSpace(strVal)
			}
			return ""
		}
		// Bỏ qua giá trị của key (hỗ trợ object lồng nhau / array lớn tùy ý mà không tốn bộ nhớ)
		if err := skipJSONValue(dec); err != nil {
			return ""
		}
	}
	return ""
}

func skipJSONValue(dec *json.Decoder) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if _, ok := t.(json.Delim); !ok {
		return nil
	}
	depth := 1
	for depth > 0 {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else if d == '}' || d == ']' {
				depth--
			}
		}
	}
	return nil
}

func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "authentication_error",
			"code":    code,
		},
	})
}
