package google

import (
	"io"
	"net/http"

	"dezuxk-gateway/internal/core/domain"
)

// StatusError phân loại HTTP lỗi và bỏ thân phản hồi. Thân có thể chứa cookie hoặc token.
func StatusError(resp *http.Response, origin string, idempotent bool, service domain.ServiceKind) error {
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	}
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	if ce := domain.CodecHTTP(origin, status, idempotent, service); ce != nil {
		return ce
	}
	return domain.CodecRejected(origin, service, "máy chủ gốc trả trạng thái không dùng được")
}
