package domain

import (
	"errors"
)

type ErrorClass string

const (
	ClassInvalidRequest      ErrorClass = "invalid_request"
	ClassUnauthenticated     ErrorClass = "unauthenticated"
	ClassUnauthorized        ErrorClass = "unauthorized"
	ClassExpired             ErrorClass = "expired"
	ClassRateLimited         ErrorClass = "rate_limited"
	ClassUpstreamRejected    ErrorClass = "upstream_rejected"
	ClassUpstreamUnavailable ErrorClass = "upstream_unavailable"
	ClassSchemaUnexpected    ErrorClass = "schema_unexpected"
	ClassConflict            ErrorClass = "conflict"
)

const (
	OpFlowGetCredits      = "flow.get_credits"
	OpFlowCreateProject   = "flow.create_project"
	OpFlowExtendVideo     = "flow.videos.extend"
	OpFlowUpsample4K      = "flow.videos.upsample_4k"
	OpFlowAudio           = "flow.audio.generate"
	OpFlowVoices          = "flow.voices"
	OpFlowProjects        = "flow.projects"
	OpFlowTrash           = "flow.trash"
	OpFlowPinhole         = "flow.pinhole"
	OpFlowGallery         = "flow.gallery"
	OpGeminiHistory       = "gemini.history"
	OpChatCompletions     = "chat.completions"
	OpImages              = "images.generations"
	OpVideos              = "videos.generations"
	OpSession             = "session"
	OriginNzlxg           = "nzlxg"
	OriginStreamGenerate  = "StreamGenerate"
	OriginStreamChat      = "StreamChat"
	OriginCreateProject   = "jHPbke"
	OriginExtendVideo     = "veo_3_1_extend"
	OriginUpsample4K      = "uW3g7e"
	OriginMusicFX         = "mX9w1"
	OriginVoices          = "Zzl0ze"
	OriginProjects        = "UpteDb"
	OriginTrashMove       = "dK3x9"
	OriginTrashList       = "tB6q8"
	OriginTrashRestore    = "rS4y1"
	OriginProjectDelete   = "mrlkwd"
	OriginHistoryList     = "MaZiqc"
	OriginHistoryDetail   = "cZOhpc"
	OriginHistoryRename   = "PCck7e"
	OriginHistoryDelete   = "VxUbXb"
	OriginHistoryBranch   = "wEb32b"
	OriginPinholeAdd       = "kF8z7b"
	OriginPinholeConnect   = "jE2m9c"
	OriginPinholeDelete    = "dL5p2"
	OriginHandshake       = "handshake"
	FlowCreditSpecVersion = "2026-09-23"
)

// GatewayError là lỗi đã phân lớp. Error() chỉ chứa class và câu an toàn.
type GatewayError struct {
	Class           ErrorClass
	Operation       string
	OriginOperation string
	Service         ServiceKind
	Message         string
	OriginStatus    int
	PublicStatus    int
	Retryable       bool
}

func (e *GatewayError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return string(e.Class)
	}
	return string(e.Class) + ": " + e.Message
}

func (e *GatewayError) HTTPStatus() int {
	if e == nil {
		return 502
	}
	if e.PublicStatus != 0 {
		return e.PublicStatus
	}
	switch e.Class {
	case ClassInvalidRequest:
		return 400
	case ClassUnauthenticated, ClassExpired:
		return 401
	case ClassUnauthorized:
		return 403
	case ClassRateLimited:
		return 429
	case ClassConflict:
		return 409
	case ClassUpstreamUnavailable:
		return 503
	default:
		return 502
	}
}

func (e *GatewayError) WithPublicStatus(code int) *GatewayError {
	if e == nil {
		return nil
	}
	e.PublicStatus = code
	return e
}

func AsGatewayError(err error) (*GatewayError, bool) {
	var ge *GatewayError
	if errors.As(err, &ge) && ge != nil {
		return ge, true
	}
	return nil, false
}

// EnsureGateway giữ lỗi đã phân lớp và là chỗ duy nhất gán tên operation nội bộ cho lỗi codec.
// Lỗi lạ trở thành câu cố định, không kèm chuỗi gốc.
func EnsureGateway(err error, operation string, service ServiceKind) *GatewayError {
	if ge, ok := AsGatewayError(err); ok {
		if ge.Operation == "" {
			ge.Operation = operation
		}
		if ge.Service == "" {
			ge.Service = service
		}
		return ge
	}
	if ce, ok := AsCodecError(err); ok {
		svc := ce.Service
		if svc == "" {
			svc = service
		}
		return &GatewayError{
			Class:           ce.Class,
			Operation:       operation,
			OriginOperation: ce.Origin,
			Service:         svc,
			Message:         ce.Message,
			OriginStatus:    ce.OriginStatus,
			Retryable:       ce.Retryable,
		}
	}
	return &GatewayError{
		Class:     ClassUpstreamRejected,
		Operation: operation,
		Service:   service,
		Message:   "yêu cầu không hoàn thành",
	}
}

func InvalidRequest(operation, origin string, service ServiceKind, message string) *GatewayError {
	return newGateway(ClassInvalidRequest, operation, origin, service, message, 0, false)
}

func Unauthenticated(operation, origin string, service ServiceKind, message string) *GatewayError {
	return newGateway(ClassUnauthenticated, operation, origin, service, message, 0, false)
}

func Expired(operation, origin string, service ServiceKind, message string) *GatewayError {
	return newGateway(ClassExpired, operation, origin, service, message, 401, false)
}

func SchemaUnexpected(operation, origin string, service ServiceKind, message string) *GatewayError {
	return newGateway(ClassSchemaUnexpected, operation, origin, service, message, 200, false)
}

func UpstreamRejected(operation, origin string, service ServiceKind, message string) *GatewayError {
	return newGateway(ClassUpstreamRejected, operation, origin, service, message, 0, false)
}

func UpstreamUnavailable(operation, origin string, service ServiceKind, message string) *GatewayError {
	return newGateway(ClassUpstreamUnavailable, operation, origin, service, message, 0, true)
}

func Conflict(operation, origin string, service ServiceKind, message string) *GatewayError {
	return newGateway(ClassConflict, operation, origin, service, message, 0, false)
}

func newGateway(class ErrorClass, operation, origin string, service ServiceKind, message string, originStatus int, retryable bool) *GatewayError {
	return &GatewayError{
		Class:           class,
		Operation:       operation,
		OriginOperation: origin,
		Service:         service,
		Message:         message,
		OriginStatus:    originStatus,
		Retryable:       retryable,
	}
}

// ClassifyUpstreamStatus map mã HTTP đã quan sát. status dưới 400 trả nil.
// Thân phản hồi không được đưa vào lỗi.
func ClassifyUpstreamStatus(operation, origin string, status int, idempotent bool, service ServiceKind) *GatewayError {
	ce := CodecHTTP(origin, status, idempotent, service)
	if ce == nil {
		return nil
	}
	return newGateway(ce.Class, operation, ce.Origin, ce.Service, ce.Message, ce.OriginStatus, ce.Retryable)
}

// ClassifyTransport đổi lỗi mạng thành lớp upstream_unavailable. Chuỗi lỗi gốc bị bỏ.
func ClassifyTransport(operation, origin string, service ServiceKind, err error) *GatewayError {
	ce := CodecTransport(origin, service, err)
	if ce == nil {
		return nil
	}
	return newGateway(ce.Class, operation, ce.Origin, ce.Service, ce.Message, ce.OriginStatus, ce.Retryable)
}
