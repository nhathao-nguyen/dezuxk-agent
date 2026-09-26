package domain

import (
	"context"
	"errors"
	"net"
)

// CodecError là lỗi pack/unpack hoặc vận chuyển. Không mang tên operation nội bộ.
type CodecError struct {
	Class        ErrorClass
	Origin       string
	Service      ServiceKind
	Message      string
	OriginStatus int
	Retryable    bool
}

func (e *CodecError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return string(e.Class)
	}
	return string(e.Class) + ": " + e.Message
}

func AsCodecError(err error) (*CodecError, bool) {
	var ce *CodecError
	if errors.As(err, &ce) && ce != nil {
		return ce, true
	}
	return nil, false
}

func CodecExpired(origin string, service ServiceKind, message string) *CodecError {
	return &CodecError{Class: ClassExpired, Origin: origin, Service: service, Message: message, OriginStatus: 401}
}

func CodecSchema(origin string, service ServiceKind, message string) *CodecError {
	return &CodecError{Class: ClassSchemaUnexpected, Origin: origin, Service: service, Message: message, OriginStatus: 200}
}

func CodecRejected(origin string, service ServiceKind, message string) *CodecError {
	return &CodecError{Class: ClassUpstreamRejected, Origin: origin, Service: service, Message: message}
}

// CodecHTTP map mã HTTP đã quan sát. status dưới 400 trả nil.
func CodecHTTP(origin string, status int, idempotent bool, service ServiceKind) *CodecError {
	switch status {
	case 401:
		return &CodecError{Class: ClassExpired, Origin: origin, Service: service, Message: "phiên gốc hết hạn", OriginStatus: status}
	case 403:
		return &CodecError{Class: ClassUpstreamRejected, Origin: origin, Service: service, Message: "máy chủ gốc từ chối yêu cầu", OriginStatus: status}
	case 429:
		return &CodecError{Class: ClassRateLimited, Origin: origin, Service: service, Message: "máy chủ gốc giới hạn tần suất", OriginStatus: status, Retryable: idempotent}
	case 408, 425, 500, 502, 503, 504:
		return &CodecError{Class: ClassUpstreamUnavailable, Origin: origin, Service: service, Message: "máy chủ gốc không phản hồi kịp", OriginStatus: status, Retryable: idempotent}
	default:
		if status >= 500 {
			return &CodecError{Class: ClassUpstreamUnavailable, Origin: origin, Service: service, Message: "máy chủ gốc không phản hồi kịp", OriginStatus: status, Retryable: idempotent}
		}
		if status >= 400 {
			return &CodecError{Class: ClassUpstreamRejected, Origin: origin, Service: service, Message: "máy chủ gốc từ chối yêu cầu", OriginStatus: status}
		}
		return nil
	}
}

// CodecTransport bỏ chuỗi lỗi mạng gốc.
func CodecTransport(origin string, service ServiceKind, err error) *CodecError {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &CodecError{Class: ClassUpstreamUnavailable, Origin: origin, Service: service, Message: "hết thời gian chờ máy chủ gốc", Retryable: true}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return &CodecError{Class: ClassUpstreamUnavailable, Origin: origin, Service: service, Message: "không kết nối được máy chủ gốc", Retryable: true}
	}
	return &CodecError{Class: ClassUpstreamUnavailable, Origin: origin, Service: service, Message: "không kết nối được máy chủ gốc", Retryable: true}
}

// ClassifiedFailure đọc lớp lỗi từ GatewayError hoặc CodecError.
func ClassifiedFailure(err error) (class ErrorClass, service ServiceKind, ok bool) {
	if ge, found := AsGatewayError(err); found {
		return ge.Class, ge.Service, true
	}
	if ce, found := AsCodecError(err); found {
		return ce.Class, ce.Service, true
	}
	return "", "", false
}
