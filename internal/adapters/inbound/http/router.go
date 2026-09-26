package http

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"dezuxk-gateway/internal/adapters/inbound/web"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

type RouterDependencies struct {
	Config            *config.Config
	ModelRegistry     *domain.ModelRegistry
	ChatUseCase       ports.ChatUseCase
	ProfileUseCase    ports.ProfileUseCase
	FlowCreditUseCase ports.FlowCreditUseCase
	MediaUseCase      ports.MediaUseCase
	FlowUseCase       ports.FlowUseCase
	MediaStorage      ports.MediaRepository
	Metrics           *domain.ContractMetrics
	SessionRepo       ports.SessionRepository
	RateLimiter       *IPRateLimiter
	GeminiHistoryUseCase  ports.GeminiHistoryUseCase
	GeminiUploadUseCase   ports.GeminiUploadUseCase
	GeminiCanvasUseCase   ports.GeminiCanvasUseCase
	GeminiQuotaUseCase    ports.GeminiQuotaUseCase
	GeminiFeedbackUseCase ports.GeminiFeedbackUseCase

	KeyUseCase      ports.KeyUseCase
	AlertDispatcher ports.AlertDispatcher
	ResponseCache   *services.ResponseCache
}


func BuildRouter(deps RouterDependencies) http.Handler {
	r := chi.NewRouter()

	// 1. Core Middlewares
	r.Use(middleware.RequestID)
	var trustedProxies []string
	if deps.Config != nil && len(deps.Config.Server.TrustedProxies) > 0 {
		trustedProxies = deps.Config.Server.TrustedProxies
	}
	r.Use(SecureRealIPMiddleware(NewTrustedProxyChecker(trustedProxies)))
	r.Use(middleware.Recoverer)
	if deps.Metrics != nil {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				deps.Metrics.RecordRequest()
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
		limiter = NewIPRateLimiter(maxReqs, time.Duration(windowSecs)*time.Second, trustedProxies)
	}
	r.Use(limiter.Middleware())

	// 3. Dynamic CORS
	allowedOrigins := []string{"*"}
	if deps.Config != nil && len(deps.Config.Server.AllowedOrigins) > 0 {
		allowedOrigins = deps.Config.Server.AllowedOrigins
	}
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link", "Content-Range", "Accept-Ranges"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// 4. Health check endpoints (Tích hợp cảnh báo phiên / Alerts)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		activeModels := 0
		if deps.ModelRegistry != nil {
			activeModels = deps.ModelRegistry.Count()
		}
		status := "ok"
		var alerts []domain.SessionAlert
		if deps.SessionRepo != nil {
			alerts = deps.SessionRepo.GetAlerts()
			if len(alerts) > 0 {
				status = "warning"
			}
		}
		payload := map[string]any{
			"status":        status,
			"models_active": activeModels,
			"alerts":        alerts,
			"timestamp":     time.Now().Format(time.RFC3339),
		}
		if deps.Metrics != nil {
			payload["contract"] = deps.Metrics.Snapshot()
		}
		_ = json.NewEncoder(w).Encode(payload)
	})

	r.Get("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ready":true}`))
	})

	// Phục vụ tệp Media cục bộ với Byte-Range streaming (hỗ trợ tua video)
	if deps.MediaStorage != nil {
		r.Get("/media/{id}", func(w http.ResponseWriter, r *http.Request) {
			id := chi.URLParam(r, "id")
			_ = deps.MediaStorage.ServeAssetHTTP(w, r, id)
		})
	}

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
	var flowHandler *FlowHandler
	if deps.FlowCreditUseCase != nil {
		flowHandler = NewFlowHandler(deps.FlowCreditUseCase, deps.Metrics)
		if deps.ResponseCache != nil {
			flowHandler.SetCache(deps.ResponseCache)
		}
	}

	var adminCfg *config.AdminConfig
	if deps.Config != nil {
		adminCfg = &deps.Config.Admin
	}
	adminHandler := NewAdminHandler(deps.SessionRepo, deps.ModelRegistry, deps.Metrics, deps.ResponseCache, adminCfg)

	// Phục vụ Giao diện Embedded Admin Dashboard tại /admin
	hfs, err := web.GetFileSystem()
	if err == nil {
		r.Get("/admin", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
		})
		r.Handle("/admin/*", http.StripPrefix("/admin", http.FileServer(hfs)))
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

	// 5. Mount /v1 Routes - Khóa chặt chỉ phục vụ các endpoint đã hoàn tất hợp đồng Facade
	r.Route("/v1", func(v1 chi.Router) {

		if deps.KeyUseCase != nil {
			v1.Use(VirtualKeyAuthMiddleware(deps.KeyUseCase))
		} else if deps.Config != nil && deps.Config.Server.APIKey != "" {
			v1.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					authHeader := r.Header.Get("Authorization")
					expected := "Bearer " + deps.Config.Server.APIKey
					if authHeader != expected {
						http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
						return
					}
					next.ServeHTTP(w, r)
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
				admin.Delete("/{id}", adminHandler.HandleRevokeKey)
			})
		}

		// Models & Chat
		if modelHandler != nil {
			v1.Get("/models", modelHandler.HandleListModels)
		}
		if chatHandler != nil {
			v1.Post("/chat/completions", gateOperation(deps, domain.OpChatCompletions, domain.ServiceGemini, true, chatHandler.HandleChatCompletions))
		}

		// Flow Credits (đọc số dư Flow với envelope data/error/meta)
		if flowHandler != nil {
			v1.Get("/flow/credits", gateOperation(deps, domain.OpFlowGetCredits, domain.ServiceFlow, false, flowHandler.HandleGetCredits))
		}

		// Media Generations (gated qua config flag)
		if deps.MediaUseCase != nil {
			mediaHandler := NewMediaHandler(deps.MediaUseCase, deps.Metrics)
			v1.Post("/images/generations", gateOperation(deps, domain.OpImages, domain.ServiceFlow, false, mediaHandler.HandleImage))
			v1.Post("/videos/generations", gateOperation(deps, domain.OpVideos, domain.ServiceFlow, false, mediaHandler.HandleVideo))
		}

		// Profile Management (Quản lý Profile cục bộ)
		if profileHandler != nil {
			v1.Get("/profiles", profileHandler.HandleListProfiles)
			v1.Post("/profiles", profileHandler.HandleCreateProfile)
			v1.Post("/profiles/{id}/launch", profileHandler.HandleLaunchChrome)
			v1.Post("/profiles/{id}/sync", profileHandler.HandleSyncCDP)
			v1.Post("/profiles/{id}/ingest", profileHandler.HandleIngestCookies)
			v1.Put("/profiles/{id}/proxy", profileHandler.HandleSetProxy)
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

		// Flow Studio Endpoints (Quản lý dự án UpteDb, tB6q8, dK3x9, rS4y1, mrlkwd, Nối dài video, Audio, Voices, Gallery)
		var flowUC ports.FlowUseCase
		if deps.FlowUseCase != nil {
			flowUC = deps.FlowUseCase
		} else if f, ok := deps.MediaUseCase.(ports.FlowUseCase); ok {
			flowUC = f
		}
		if flowUC != nil {
			studioHandler := NewFlowStudioHandler(flowUC, deps.Metrics)
			v1.Post("/flow/videos/extend", studioHandler.HandleExtendVideo)
			v1.Post("/flow/videos/upsample-4k", studioHandler.HandleUpsample4K)
			v1.Post("/flow/audio/generate", studioHandler.HandleGenerateAudio)
			v1.Get("/flow/voices", studioHandler.HandleListVoices)
			v1.Get("/flow/projects", studioHandler.HandleListProjects)
			v1.Post("/flow/projects", studioHandler.HandleCreateProject)
			v1.Delete("/flow/projects/{id}", studioHandler.HandleTrashProject)
			v1.Get("/flow/trash", studioHandler.HandleListTrash)
			v1.Post("/flow/trash/{id}/restore", studioHandler.HandleRestoreProject)
			v1.Delete("/flow/trash/{id}", studioHandler.HandleDeleteProject)
			v1.Get("/flow/gallery", studioHandler.HandleListMediaGallery)
		}

		// Alerts API
		if deps.SessionRepo != nil {
			v1.Get("/alerts", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"alerts": deps.SessionRepo.GetAlerts(),
				})
			})
			v1.Post("/alerts/clear", func(w http.ResponseWriter, r *http.Request) {
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
