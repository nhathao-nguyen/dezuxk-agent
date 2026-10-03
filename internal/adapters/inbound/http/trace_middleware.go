package http

import (
	"log"
	"net/http"
	"strings"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

type responseWriterInterceptor struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseWriterInterceptor) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// RequestTraceMiddleware khởi tạo và truyền bá TraceContext xuyên suốt chu kỳ xử lý request
func RequestTraceMiddleware(enableAccessLog bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
			if reqID == "" {
				reqID = domain.GenerateRequestID()
			}

			traceID := strings.TrimSpace(r.Header.Get("X-Trace-ID"))
			if traceID == "" {
				traceID = domain.GenerateTraceID()
			}

			// Gắn vào Response Header cho client theo dõi
			w.Header().Set("X-Request-ID", reqID)
			w.Header().Set("X-Trace-ID", traceID)

			tc := &domain.TraceContext{
				RequestID: reqID,
				TraceID:   traceID,
				StartTime: time.Now(),
			}

			// Lấy tenant nếu context đã có (từ middleware auth)
			if id, ok := domain.TenantIdentityFromContext(r.Context()); ok {
				tc.TenantID = id.TenantID
				tc.KeyID = id.KeyID
			}

			ctx := domain.ContextWithTrace(r.Context(), tc)

			interceptor := &responseWriterInterceptor{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(interceptor, r.WithContext(ctx))

			duration := time.Since(tc.StartTime)

			// Ghi log có cấu trúc nếu bật access log và không phải endpoint health check
			if enableAccessLog && r.URL.Path != "/health" && r.URL.Path != "/ready" && r.URL.Path != "/metrics" {
				tenant := tc.TenantID
				if tenant == "" {
					tenant = "anonymous"
				}
				log.Printf("[ACCESS] %s %s | status: %d | duration: %v | trace_id: %s | tenant: %s",
					r.Method, r.URL.Path, interceptor.statusCode, duration, traceID, tenant)
			}
		})
	}
}
