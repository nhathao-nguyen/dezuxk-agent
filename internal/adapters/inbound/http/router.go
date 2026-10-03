package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"dezuxk-gateway/internal/adapters/inbound/web"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services"
	"dezuxk-gateway/internal/version"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

// ReadinessManager quản lý trạng thái sẵn sàng (Readiness State) của AI Gateway
type ReadinessManager struct {
	ready atomic.Bool
}

func NewReadinessManager() *ReadinessManager {
	rm := &ReadinessManager{}
	rm.ready.Store(false)
	return rm
}

func (rm *ReadinessManager) SetReady(ready bool) {
	rm.ready.Store(ready)
}

func (rm *ReadinessManager) IsReady() bool {
	return rm.ready.Load()
}

type RouterDependencies struct {
	Config                *config.Config
	ModelRegistry         *domain.ModelRegistry
	ChatUseCase           ports.ChatUseCase
	ProfileUseCase        ports.ProfileUseCase
	MediaStorage          ports.MediaRepository
	Metrics               *domain.ContractMetrics
	SessionRepo           ports.SessionRepository
	RateLimiter           *IPRateLimiter
	GeminiHistoryUseCase  ports.GeminiHistoryUseCase
	GeminiUploadUseCase   ports.GeminiUploadUseCase
	GeminiCanvasUseCase   ports.GeminiCanvasUseCase
	GeminiQuotaUseCase    ports.GeminiQuotaUseCase
	GeminiFeedbackUseCase ports.GeminiFeedbackUseCase

	KeyUseCase      ports.KeyUseCase
	AlertDispatcher ports.AlertDispatcher
	ResponseCache   *services.ResponseCache

	AgentRunner           ports.AgentRunner
	GraphRunner           ports.GraphWorkflowRunner
	ToolRegistry          ports.ToolRegistry
	CheckpointRepo        ports.CheckpointRepository
	MemoryService         ports.MemoryService
	SubagentSupervisor    ports.SubagentSupervisor
	AgentJobService       ports.AgentJobService
	AgentRunRepo          ports.AgentRunRepository
	ReadinessManager      *ReadinessManager
	ClusterClient         ports.DistributedClusterClient
	ModelDiscoveryService *services.ModelDiscoveryService
	ModelCatalogRepo      ports.ModelCatalogRepository
	TenantSettingsRepo    ports.TenantRuntimeSettingsRepository
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func BuildRouter(deps RouterDependencies) http.Handler {
	r := chi.NewRouter()

	// 1. Core Middlewares
	enableLog := false
	if deps.Config != nil && deps.Config.Server.EnableRequestLog {
		enableLog = true
	}
	r.Use(RequestTraceMiddleware(enableLog))
	r.Use(MaxBodySizeMiddleware(50 * 1024 * 1024)) // 50MB trần tối đa cho toàn bộ requests

	// Node identification header for observability & cluster test verification (Instruction 43)
	nodeID := ""
	if deps.Config != nil {
		nodeID = deps.Config.Cluster.GetNodeID()
	}
	if nodeID != "" {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Dezuxk-Node-ID", nodeID)
				next.ServeHTTP(w, r)
			})
		})
	}

	var trustedProxies []string
	if deps.Config != nil && len(deps.Config.Server.TrustedProxies) > 0 {
		trustedProxies = deps.Config.Server.TrustedProxies
	}
	r.Use(SecureRealIPMiddleware(NewTrustedProxyChecker(trustedProxies)))
	r.Use(middleware.Recoverer)
	if deps.Metrics != nil {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.Path
				// Chỉ ghi nhận vào ContractMetrics đối với các yêu cầu AI Gateway thực tế (OpenAI / Gemini Facade),
				// loại trừ các yêu cầu phục vụ giao diện Web Dashboard, kiểm tra sức khỏe và API quản trị nội bộ.
				if strings.HasPrefix(path, "/v1/") &&
					!strings.HasPrefix(path, "/v1/admin") &&
					!strings.HasPrefix(path, "/v1/profiles") &&
					path != "/v1/alerts" {
					deps.Metrics.RecordRequest()
					deps.Metrics.IncActiveRequests()
					start := time.Now()
					defer func() {
						deps.Metrics.DecActiveRequests()
						deps.Metrics.RecordRequestDuration(time.Since(start), r.Method, path, "2xx")
					}()
				}
				next.ServeHTTP(w, r)
			})
		})
	}
	if deps.Config != nil && deps.Config.Server.EnableRequestLog {

		r.Use(middleware.Logger)
	}

	// 2. Anti-Spam Rate Limiter (bảo vệ tài khoản và chống IP ban từ Google)
	limiter := deps.RateLimiter
	if limiter == nil {
		maxReqs := 120
		windowSecs := 60
		if deps.Config != nil {
			if deps.Config.Server.RateLimit.MaxRequests > 0 {
				maxReqs = deps.Config.Server.RateLimit.MaxRequests
			}
			if deps.Config.Server.RateLimit.WindowSeconds > 0 {
				windowSecs = deps.Config.Server.RateLimit.WindowSeconds
			}
		}
		localPrefilterReqs := maxReqs
		if deps.Config != nil && deps.Config.Distributed.Enabled {
			localPrefilterReqs = maxReqs * 20
			if localPrefilterReqs < 600 {
				localPrefilterReqs = 600
			}
		}
		limiter = NewIPRateLimiter(localPrefilterReqs, time.Duration(windowSecs)*time.Second, trustedProxies)
	}
	r.Use(func(next http.Handler) http.Handler {
		limiterHandler := limiter.Middleware(deps.Metrics)(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if path == "/health" || path == "/ready" || path == "/metrics" || path == "/version" {
				next.ServeHTTP(w, r)
				return
			}
			limiterHandler.ServeHTTP(w, r)
		})
	})

	// 3. Dynamic CORS Hardening
	allowedOrigins := []string{"*"}
	allowCredentials := false
	if deps.Config != nil && len(deps.Config.Server.AllowedOrigins) > 0 {
		allowedOrigins = deps.Config.Server.AllowedOrigins
		allowCredentials = true
		for _, o := range allowedOrigins {
			if strings.TrimSpace(o) == "*" {
				allowCredentials = false
				break
			}
		}
	}
	if deps.Config != nil && deps.Config.IsProduction() {
		var cleanOrigins []string
		for _, o := range allowedOrigins {
			if strings.TrimSpace(o) != "*" && strings.TrimSpace(o) != "" {
				cleanOrigins = append(cleanOrigins, strings.TrimSpace(o))
			}
		}
		allowedOrigins = cleanOrigins
		allowCredentials = len(cleanOrigins) > 0
	}
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: allowedOrigins,
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{
			"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-API-Key", "Idempotency-Key",
			"OpenAI-Beta", "OpenAI-Organization", "X-Request-Id",
			"X-Stainless-Lang", "X-Stainless-Package-Version", "X-Stainless-OS", "X-Stainless-Arch", "X-Stainless-Runtime",
		},
		ExposedHeaders:   []string{"Link", "Content-Range", "Accept-Ranges", "Retry-After", "X-Request-Id"},
		AllowCredentials: allowCredentials,
		MaxAge:           300,
	}))

	// 4. Prometheus Metrics Endpoint (Bảo vệ chống rò rỉ dữ liệu vận hành ra public internet)
	metricsExporter := NewPrometheusMetricsExporter(deps.SessionRepo, deps.ModelRegistry, deps.AgentRunRepo, deps.Metrics)
	r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if deps.Config != nil && deps.Config.IsProduction() {
			authorized := false
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
				if (deps.Config.Server.MetricsToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(deps.Config.Server.MetricsToken)) == 1) ||
					(deps.Config.Server.APIKey != "" && subtle.ConstantTimeCompare([]byte(token), []byte(deps.Config.Server.APIKey)) == 1) ||
					(deps.Config.Admin.SessionToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(deps.Config.Admin.SessionToken)) == 1) {
					authorized = true
				}
			}
			peerIP := r.RemoteAddr
			if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				peerIP = host
			}
			if peerIP == "127.0.0.1" || peerIP == "::1" || (limiter != nil && limiter.trustedChecker != nil && limiter.trustedChecker.IsTrusted(peerIP)) {
				authorized = true
			}

			if !authorized {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("Unauthorized: /metrics endpoint requires authentication or trusted network access in production\n"))
				return
			}
		}
		metricsExporter.ServeHTTP(w, r)
	})

	// 5. Health Check (Liveness Probe - Kiểm tra tiến trình sống còn thuần túy)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
		})
	})

	// 6. Readiness Check (Readiness Probe - Kiểm tra khả năng phục vụ lưu lượng thực tế)
	r.Get("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		checks := make(map[string]string)
		isReady := true

		if deps.ReadinessManager != nil && !deps.ReadinessManager.IsReady() {
			checks["gateway"] = "server is starting up or shutting down"
			isReady = false
		} else {
			checks["gateway"] = "ok"
		}

		isTestMode := os.Getenv("DEZUXK_TEST_MODE") == "true" && (deps.Config == nil || !deps.Config.IsProduction())
		isProd := deps.Config != nil && deps.Config.IsProduction()

		if deps.ModelRegistry == nil || deps.ModelRegistry.Count() == 0 {
			if isTestMode {
				checks["models"] = "ok (test_mode)"
			} else {
				checks["models"] = "no active models registered"
				isReady = false
			}
		} else if isProd {
			hasChatModel := false
			for _, m := range deps.ModelRegistry.List() {
				if m.HasCapability(domain.CapChat) {
					hasChatModel = true
					break
				}
			}
			if !hasChatModel {
				checks["models"] = "no active chat models registered"
				isReady = false
			} else {
				checks["models"] = "ok"
			}
		} else {
			checks["models"] = "ok"
		}

		// 1. Kiểm tra cơ sở dữ liệu lưu trữ bắt buộc (PostgreSQL / SQLite)
		if deps.SessionRepo == nil {
			checks["storage"] = "storage repository not initialized"
			isReady = false
			if deps.Metrics != nil {
				deps.Metrics.RecordDependencyHealth("postgres", false)
			}
		} else if pinger, ok := deps.SessionRepo.(interface{ Ping(context.Context) error }); ok {
			pingCtx, pingCancel := context.WithTimeout(r.Context(), 1*time.Second)
			if err := pinger.Ping(pingCtx); err != nil {
				log.Printf("[Readiness] Database ping error: %v", err)
				checks["storage"] = "unavailable"
				isReady = false
				if deps.Metrics != nil {
					deps.Metrics.RecordDependencyHealth("postgres", false)
				}
			} else {
				checks["storage"] = "ok"
				if deps.Metrics != nil {
					deps.Metrics.RecordDependencyHealth("postgres", true)
				}
			}
			pingCancel()
		} else {
			if deps.Config != nil && deps.Config.Storage.Driver == "postgres" {
				checks["storage"] = "unavailable"
				isReady = false
				if deps.Metrics != nil {
					deps.Metrics.RecordDependencyHealth("postgres", false)
				}
			} else {
				checks["storage"] = "ok"
			}
		}

		// Kiểm tra trạng thái tài khoản khả dụng nếu có session repo
		if deps.SessionRepo != nil {
			accounts := deps.SessionRepo.ListAll(r.Context())
			hasUsable := false
			for _, acc := range accounts {
				if acc != nil && acc.GetHealthStatus() != domain.HealthStatusUnavailable {
					hasUsable = true
					break
				}
			}
			if !hasUsable {
				if isTestMode {
					checks["accounts"] = "ok (test_mode)"
				} else {
					checks["accounts"] = "all upstream accounts are degraded or unavailable"
					isReady = false
				}
			} else {
				checks["accounts"] = "ok"
			}
		} else {
			if isTestMode {
				checks["accounts"] = "ok (test_mode)"
			} else {
				checks["accounts"] = "session repository not initialized"
				isReady = false
			}
		}

		// 2. Kiểm tra kết nối Redis trong môi trường phân tán nếu có
		if deps.ClusterClient != nil {
			pingCtx, pingCancel := context.WithTimeout(r.Context(), 1*time.Second)
			if err := deps.ClusterClient.Ping(pingCtx); err != nil {
				log.Printf("[Readiness] Redis ping error: %v", err)
				checks["redis"] = "unavailable"
				isReady = false
				if deps.Metrics != nil {
					deps.Metrics.RecordDependencyHealth("redis", false)
				}
			} else {
				checks["redis"] = "ok"
				if deps.Metrics != nil {
					deps.Metrics.RecordDependencyHealth("redis", true)
				}
			}
			pingCancel()
		} else if deps.Config != nil && deps.Config.Distributed.Enabled {
			checks["redis"] = "unavailable"
			isReady = false
			if deps.Metrics != nil {
				deps.Metrics.RecordDependencyHealth("redis", false)
			}
		}

		// 3. Kiểm tra kết nối S3/MinIO Shared Media nếu có
		if deps.MediaStorage != nil {
			if pinger, ok := deps.MediaStorage.(interface{ Ping(context.Context) error }); ok {
				pingCtx, pingCancel := context.WithTimeout(r.Context(), 1*time.Second)
				if err := pinger.Ping(pingCtx); err != nil {
					log.Printf("[Readiness] Media storage ping error: %v", err)
					checks["media_storage"] = "unavailable"
					isReady = false
					if deps.Metrics != nil {
						deps.Metrics.RecordDependencyHealth("s3", false)
					}
				} else {
					checks["media_storage"] = "ok"
					if deps.Metrics != nil {
						deps.Metrics.RecordDependencyHealth("s3", true)
					}
				}
				pingCancel()
			}
		} else if deps.Config != nil && deps.Config.Media.GetDriver() == "s3" {
			checks["media_storage"] = "unavailable"
			isReady = false
			if deps.Metrics != nil {
				deps.Metrics.RecordDependencyHealth("s3", false)
			}
		}

		status := http.StatusOK
		statusStr := "ready"
		if !isReady {
			status = http.StatusServiceUnavailable
			statusStr = "not_ready"
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":     isReady,
			"status":    statusStr,
			"checks":    checks,
			"timestamp": time.Now().Format(time.RFC3339),
		})
	})

	// 7. Version Endpoint (SemVer & Release Metadata - zero secrets)
	r.Get("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(version.GetInfo())
	})

	// Web Admin UI Dashboard (Embedded SPA)
	webHandler := http.StripPrefix("/admin", web.Handler())
	r.Get("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})
	r.Get("/admin/*", func(w http.ResponseWriter, r *http.Request) {
		webHandler.ServeHTTP(w, r)
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})

	// 4. Handlers
	var modelHandler *ModelHandler
	if deps.ModelRegistry != nil {
		modelHandler = NewModelHandler(deps.ModelRegistry)
	}
	var chatHandler *ChatHandler
	if deps.ChatUseCase != nil {
		chatHandler = NewChatHandler(deps.ChatUseCase, deps.ModelRegistry, deps.Metrics)
		if deps.ResponseCache != nil {
			chatHandler.SetCache(deps.ResponseCache)
		}
	}
	var profileHandler *ProfileHandler
	if deps.ProfileUseCase != nil {
		profileHandler = NewProfileHandler(deps.ProfileUseCase)
	}

	var adminCfg *config.AdminConfig
	if deps.Config != nil {
		adminCfg = &deps.Config.Admin
	}
	adminHandler := NewAdminHandler(deps.SessionRepo, deps.ModelRegistry, deps.Metrics, deps.ResponseCache, adminCfg)
	if deps.KeyUseCase != nil {
		adminHandler.SetKeyUseCase(deps.KeyUseCase)
	}
	if deps.ProfileUseCase != nil {
		adminHandler.SetProfileUseCase(deps.ProfileUseCase)
	}

	// Admin Authentication & Overview APIs
	r.Post("/v1/admin/auth/login", adminHandler.HandleLogin)
	r.Post("/v1/admin/auth/logout", adminHandler.HandleLogout)
	r.Route("/v1/admin/overview", func(ov chi.Router) {
		if adminCfg != nil && adminCfg.IsEnabled() {
			ov.Use(AdminAuthMiddleware(*adminCfg))
		}
		ov.Get("/", adminHandler.HandleOverview)
	})

	// Admin Runtime Management APIs (Requirement 29 & 65)
	runtimeAdminHandler := NewAdminRuntimeHandler(deps.ModelDiscoveryService, deps.ModelCatalogRepo, deps.SessionRepo, deps.ModelRegistry, deps.Metrics)
	mountRuntimeAdmin := func(rt chi.Router) {
		if adminCfg != nil && adminCfg.IsEnabled() {
			rt.Use(AdminAuthMiddleware(*adminCfg))
		}
		rt.Post("/models/refresh", runtimeAdminHandler.HandleRefresh)
		rt.Get("/models", runtimeAdminHandler.HandleListRuntimeModels)
		rt.Get("/accounts", runtimeAdminHandler.HandleListRuntimeAccounts)
		rt.Get("/catalog/status", runtimeAdminHandler.HandleCatalogStatus)
	}
	r.Route("/admin/runtime", mountRuntimeAdmin)
	r.Route("/v1/admin/runtime", mountRuntimeAdmin)

	// 5. Mount /v1 Routes - Khóa chặt chỉ phục vụ các endpoint đã hoàn tất hợp đồng Facade
	r.Route("/v1", func(v1 chi.Router) {

		if deps.KeyUseCase != nil {
			v1.Use(VirtualKeyAuthMiddleware(deps.KeyUseCase))
		} else if deps.Config != nil && deps.Config.Server.APIKey != "" {
			v1.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					authHeader := r.Header.Get("Authorization")
					expected := "Bearer " + deps.Config.Server.APIKey
					if subtle.ConstantTimeCompare([]byte(authHeader), []byte(expected)) != 1 {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusUnauthorized)
						_ = json.NewEncoder(w).Encode(map[string]any{
							"error": map[string]any{
								"message": "Sai API Key xác thực",
								"type":    "authentication_error",
								"code":    "invalid_api_key",
							},
						})
						return
					}
					identity := domain.DefaultAdminIdentity()
					ctx := domain.ContextWithTenantIdentity(r.Context(), identity)
					next.ServeHTTP(w, r.WithContext(ctx))
				})
			})
		} else {
			// Trong production mode, tuyệt đối không cho phép fallback sang DefaultInternalIdentity
			if deps.Config != nil && deps.Config.IsProduction() {
				v1.Use(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusUnauthorized)
						_ = json.NewEncoder(w).Encode(map[string]any{
							"error": map[string]any{
								"message": "Authentication required. Server is running in production mode.",
								"type":    "authentication_error",
								"code":    "api_key_required",
							},
						})
					})
				})
			} else {
				// Môi trường dev/test không cấu hình khóa xác thực: gán DefaultInternalIdentity
				v1.Use(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						identity := domain.DefaultInternalIdentity()
						ctx := domain.ContextWithTenantIdentity(r.Context(), identity)
						next.ServeHTTP(w, r.WithContext(ctx))
					})
				})
			}
		}

		// Layer B — Post-auth limiter: rate-limit và concurrency limit theo tenant, key, endpoint, model (Redis authority trong multi-node)
		if limiter != nil {
			v1.Use(AuthenticatedRateLimitMiddleware(limiter, limiter.LocalRateLimiter.ExtractClientIP, deps.Metrics))
		}

		// Phục vụ tệp Media qua API chuẩn GET /v1/media/{id} và tải lên qua POST /v1/media
		if deps.MediaStorage != nil {
			v1.Get("/media/{id}", func(w http.ResponseWriter, r *http.Request) {
				id := chi.URLParam(r, "id")
				_ = deps.MediaStorage.ServeAssetHTTP(w, r, id)
			})

			v1.Post("/media", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")

				tenantID := "default"
				if id, ok := domain.TenantIdentityFromContext(r.Context()); ok && id.TenantID != "" {
					tenantID = id.TenantID
				}

				var (
					assetID  = fmt.Sprintf("media_%d_%s", time.Now().UnixNano(), hex.EncodeToString(randBytes(4)))
					fileName = "upload.bin"
					kind     = domain.MediaKind("application/octet-stream")
					isPublic = false
					reader   io.Reader
				)

				ct := r.Header.Get("Content-Type")
				if strings.HasPrefix(ct, "multipart/form-data") {
					if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid multipart form: " + err.Error()})
						return
					}
					file, handler, err := r.FormFile("file")
					if err != nil {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]any{"error": "field 'file' is required"})
						return
					}
					defer file.Close()

					fileName = handler.Filename
					if explicitID := strings.TrimSpace(r.FormValue("id")); explicitID != "" {
						assetID = explicitID
					}
					if explicitKind := strings.TrimSpace(r.FormValue("kind")); explicitKind != "" {
						kind = domain.MediaKind(explicitKind)
					} else if hct := handler.Header.Get("Content-Type"); hct != "" {
						kind = domain.MediaKind(hct)
					}
					if r.FormValue("is_public") == "true" {
						isPublic = true
					}
					reader = file
				} else if strings.Contains(ct, "application/json") {
					var body struct {
						ID         string `json:"id"`
						FileName   string `json:"file_name"`
						MimeType   string `json:"mime_type"`
						Kind       string `json:"kind"`
						DataBase64 string `json:"data_base64"`
						IsPublic   bool   `json:"is_public"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DataBase64 == "" {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid json payload or empty data_base64"})
						return
					}
					decoded, err := base64.StdEncoding.DecodeString(body.DataBase64)
					if err != nil {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid base64 encoding"})
						return
					}
					if body.ID != "" {
						assetID = body.ID
					}
					if body.FileName != "" {
						fileName = body.FileName
					}
					if body.Kind != "" {
						kind = domain.MediaKind(body.Kind)
					} else if body.MimeType != "" {
						kind = domain.MediaKind(body.MimeType)
					}
					isPublic = body.IsPublic
					reader = bytes.NewReader(decoded)
				} else {
					if explicitID := r.Header.Get("X-Media-ID"); explicitID != "" {
						assetID = explicitID
					}
					if explicitName := r.Header.Get("X-File-Name"); explicitName != "" {
						fileName = explicitName
					}
					if ct != "" {
						kind = domain.MediaKind(ct)
					}
					if r.Header.Get("X-Is-Public") == "true" {
						isPublic = true
					}
					rawBytes, err := io.ReadAll(r.Body)
					if err != nil {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to read request body: " + err.Error()})
						return
					}
					reader = bytes.NewReader(rawBytes)
				}

				asset := &domain.MediaAsset{
					ID:        assetID,
					FileName:  fileName,
					Kind:      kind,
					TenantID:  tenantID,
					IsPublic:  isPublic,
					CreatedAt: time.Now(),
				}

				if err := deps.MediaStorage.SaveAsset(r.Context(), asset, reader); err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to save media asset: " + err.Error()})
					return
				}

				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":        asset.ID,
					"file_name": asset.FileName,
					"kind":      string(asset.Kind),
					"tenant_id": asset.TenantID,
					"is_public": asset.IsPublic,
					"url":       "/v1/media/" + asset.ID,
				})
			})
		}

		// Quản trị nội bộ Virtual API Keys
		if deps.KeyUseCase != nil {
			adminHandler := NewAdminKeyHandler(deps.KeyUseCase)
			v1.Route("/admin/keys", func(admin chi.Router) {
				admin.Use(RequireAdmin)
				admin.Post("/", adminHandler.HandleCreateKey)
				admin.Get("/", adminHandler.HandleListKeys)
				admin.Get("/usage/history", adminHandler.HandleGetSystemUsageHistory)
				admin.Get("/{id}/usage", adminHandler.HandleGetKeyUsageHistory)
				admin.Delete("/{id}", adminHandler.HandleRevokeKey)
			})
		}

		// Models & Chat
		if modelHandler != nil {
			v1.Get("/models", modelHandler.HandleListModels)
		}
		if chatHandler != nil {
			v1.With(RequireScope(domain.ScopeChat)).Post("/chat/completions", gateOperation(deps, domain.OpChatCompletions, domain.ServiceGemini, true, chatHandler.HandleChatCompletions))
			v1.With(RequireScope(domain.ScopeResponses)).Post("/responses", gateOperation(deps, domain.OpChatCompletions, domain.ServiceGemini, true, chatHandler.HandleResponses))
		}

		// Profile Management (Quản lý Profile cục bộ - Yêu cầu quyền Quản trị viên)
		if profileHandler != nil {
			v1.Route("/profiles", func(prof chi.Router) {
				prof.Use(RequireAdmin)
				prof.Get("/", profileHandler.HandleListProfiles)
				prof.Post("/", profileHandler.HandleCreateProfile)
				prof.Post("/{id}/launch", profileHandler.HandleLaunchChrome)
				prof.Post("/{id}/sync", profileHandler.HandleSyncCDP)
				prof.Post("/{id}/ingest", profileHandler.HandleIngestCookies)
				prof.Put("/{id}/proxy", profileHandler.HandleSetProxy)
			})
		}

		// Gemini History & Advanced Endpoints (Lịch sử chat, Quota, Canvas, Upload, Feedback)
		if deps.GeminiHistoryUseCase != nil || deps.GeminiQuotaUseCase != nil || deps.GeminiCanvasUseCase != nil || deps.GeminiUploadUseCase != nil || deps.GeminiFeedbackUseCase != nil {
			geminiHandler := NewGeminiHandler(
				deps.GeminiHistoryUseCase,
				deps.GeminiUploadUseCase,
				deps.GeminiCanvasUseCase,
				deps.GeminiQuotaUseCase,
				deps.GeminiFeedbackUseCase,
				deps.Metrics,
			)
			if deps.GeminiHistoryUseCase != nil {
				v1.Get("/gemini/conversations", geminiHandler.HandleListConversations)
				v1.Get("/gemini/conversations/{id}", geminiHandler.HandleGetConversation)
				v1.Post("/gemini/conversations/{id}/rename", geminiHandler.HandleRenameConversation)
				v1.Delete("/gemini/conversations/{id}", geminiHandler.HandleDeleteConversation)
				v1.Post("/gemini/conversations/{id}/branch", geminiHandler.HandleSwitchBranch)
			}
			if deps.GeminiUploadUseCase != nil {
				v1.Post("/gemini/upload", geminiHandler.HandleUpload)
			}
			if deps.GeminiQuotaUseCase != nil {
				v1.Get("/gemini/usage", geminiHandler.HandleGetUsage)
				v1.Get("/gemini/account-tier", geminiHandler.HandleGetAccountTier)
			}
			if deps.GeminiFeedbackUseCase != nil {
				v1.Post("/gemini/feedback", geminiHandler.HandleFeedback)
			}
			if deps.GeminiCanvasUseCase != nil {
				v1.Post("/gemini/canvas", geminiHandler.HandleCreateCanvas)
				v1.Post("/gemini/canvas/{id}/delta", geminiHandler.HandleUpdateCanvasDelta)
				v1.Post("/gemini/canvas/{id}/publish", geminiHandler.HandlePublishCanvas)
			}
		}

		// Autonomous Agent Engine Endpoints
		if deps.AgentRunner != nil || deps.GraphRunner != nil {
			agentHandler := NewAgentHandler(deps.AgentRunner, deps.GraphRunner, deps.ToolRegistry, deps.CheckpointRepo, deps.MemoryService)
			if deps.SubagentSupervisor != nil {
				agentHandler.SetSubagentSupervisor(deps.SubagentSupervisor)
			}
			if deps.AgentJobService != nil && deps.AgentRunRepo != nil {
				agentHandler.SetJobService(deps.AgentJobService, deps.AgentRunRepo)
			}
			v1.Route("/agent", func(ag chi.Router) {
				ag.Use(RequireScope(domain.ScopeAgent))
				ag.Post("/run", agentHandler.HandleRun)
				ag.Post("/run/stream", agentHandler.HandleRunStream)
				ag.Post("/runs", agentHandler.HandleCreateRun)
				ag.Get("/runs", agentHandler.HandleListRuns)
				ag.Get("/runs/{id}", agentHandler.HandleGetRun)
				ag.Post("/runs/{id}/cancel", agentHandler.HandleCancelRun)
				ag.Post("/runs/{id}/resume", agentHandler.HandleResumeRun)
				ag.Get("/runs/{id}/events", agentHandler.HandleStreamRunEvents)
				ag.Get("/tools", agentHandler.HandleListTools)
				ag.Get("/checkpoints/{taskId}", agentHandler.HandleGetCheckpoints)
				ag.Get("/subagents", agentHandler.HandleListSubagents)
				ag.Post("/subagents/run", agentHandler.HandleInvokeSubagent)
				ag.Route("/sandbox", func(sb chi.Router) {
					sb.Get("/diff/{taskId}", agentHandler.HandleSandboxDiff)
					sb.Post("/merge", agentHandler.HandleSandboxMerge)
					sb.Post("/rollback", agentHandler.HandleSandboxRollback)
				})
				ag.Route("/memory", func(mem chi.Router) {
					mem.Use(RequireScope(domain.ScopeMemory))
					mem.Get("/core", agentHandler.HandleGetCoreMemory)
					mem.Post("/core", agentHandler.HandleUpdateCoreMemory)
					mem.Get("/search", agentHandler.HandleSearchArchival)
					mem.Post("/store", agentHandler.HandleStoreArchival)
				})
			})
		}

		// Alerts API (Yêu cầu quyền Quản trị viên vì alerts chứa thông tin tài khoản / phiên hệ thống nhạy cảm)
		if deps.SessionRepo != nil {
			v1.With(RequireAdmin).Get("/alerts", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"alerts": deps.SessionRepo.GetAlerts(),
				})
			})
			v1.With(RequireAdmin).Post("/alerts/clear", func(w http.ResponseWriter, r *http.Request) {
				accountID := r.URL.Query().Get("account_id")
				deps.SessionRepo.ClearAlerts(accountID)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": true,
					"message": "Đã xóa danh sách cảnh báo.",
				})
			})
		}
	})

	walkFunc := func(method string, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		route = strings.Replace(route, "/*/", "/", -1)
		log.Printf("[Route] %-6s %s", method, route)
		return nil
	}
	_ = chi.Walk(r, walkFunc)

	return r
}

func gateOperation(deps RouterDependencies, operation string, service domain.ServiceKind, chat bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Config != nil && !deps.Config.Operations.Enabled(operation) {
			err := domain.UpstreamRejected(operation, "", service, "operation đang tắt").WithPublicStatus(http.StatusServiceUnavailable)
			if chat {
				writeChatError(w, r, deps.Metrics, err, false)
				return
			}
			writeOperationError(w, r, deps.Metrics, err, operation, service)
			return
		}
		next(w, r)
	}
}
