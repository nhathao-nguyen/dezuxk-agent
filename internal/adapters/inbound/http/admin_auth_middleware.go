package http

import (
	"encoding/base64"
	"net/http"
	"strings"

	"dezuxk-gateway/internal/config"
)

// AdminAuthMiddleware bảo vệ các tuyến đường quản trị qua Cookie, Bearer Token hoặc Basic Auth
func AdminAuthMiddleware(adminCfg config.AdminConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !adminCfg.IsEnabled() {
				http.Error(w, `{"error":"forbidden","message":"trang quản trị đã bị vô hiệu hóa"}`, http.StatusForbidden)
				return
			}

			expectedToken := adminCfg.GetSessionToken()
			expectedUser := adminCfg.GetUsername()
			expectedPass := adminCfg.GetPassword()

			// 1. Kiểm tra Cookie dezuxk_admin_token
			if cookie, err := r.Cookie("dezuxk_admin_token"); err == nil && cookie.Value != "" {
				if cookie.Value == expectedToken {
					next.ServeHTTP(w, r)
					return
				}
			}

			// 2. Kiểm tra Header Authorization
			authHeader := r.Header.Get("Authorization")
			if authHeader != "" {
				// 2a. Bearer Token
				if strings.HasPrefix(authHeader, "Bearer ") {
					token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
					if token == expectedToken {
						next.ServeHTTP(w, r)
						return
					}
				}

				// 2b. Basic Auth
				if strings.HasPrefix(authHeader, "Basic ") {
					encoded := strings.TrimSpace(strings.TrimPrefix(authHeader, "Basic "))
					decoded, err := base64.StdEncoding.DecodeString(encoded)
					if err == nil {
						parts := strings.SplitN(string(decoded), ":", 2)
						if len(parts) == 2 && parts[0] == expectedUser && parts[1] == expectedPass {
							next.ServeHTTP(w, r)
							return
						}
					}
				}
			}

			w.Header().Set("WWW-Authenticate", `Basic realm="Dezuxk Admin"`)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized","message":"yêu cầu xác thực quyền quản trị viên"}`))
		})
	}
}
