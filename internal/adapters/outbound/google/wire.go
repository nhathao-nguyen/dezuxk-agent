package google

import (
	"context"
	"net/http"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

const formContentType = "application/x-www-form-urlencoded;charset=UTF-8"

// WireAdapter pack và unpack chat Gemini và media Flow.
type WireAdapter struct {
	rpcs       *domain.RpcRegistry
	marshaller *Marshaller
}

func NewWireAdapter(rpcs *domain.RpcRegistry) ports.WireCodec {
	if rpcs == nil {
		rpcs = domain.DefaultRpcRegistry()
	}
	return &WireAdapter{rpcs: rpcs, marshaller: NewMarshaller()}
}

func (w *WireAdapter) MaterializeChat(account *domain.ManagedAccount, payload domain.GeminiPayloadBuilder) (domain.OutboundAttempt, error) {
	if account == nil || account.GetAtToken(domain.ServiceGemini) == "" {
		return domain.OutboundAttempt{}, domain.CodecExpired(domain.OriginStreamGenerate, domain.ServiceGemini, "phiên chưa có bí mật dẫn xuất")
	}
	slots, err := payload.BuildArray()
	if err != nil {
		return domain.OutboundAttempt{}, domain.CodecRejected(domain.OriginStreamGenerate, domain.ServiceGemini, "không đóng gói được yêu cầu")
	}
	body, err := w.marshaller.EncodeStreamGenerate(slots, account.GetAtToken(domain.ServiceGemini))
	if err != nil {
		return domain.OutboundAttempt{}, domain.CodecRejected(domain.OriginStreamGenerate, domain.ServiceGemini, "không đóng gói được yêu cầu")
	}
	rpc, err := w.rpcs.MustFind("StreamGenerate")
	if err != nil {
		return domain.OutboundAttempt{}, domain.CodecRejected(domain.OriginStreamGenerate, domain.ServiceGemini, "không có đường dẫn chat")
	}
	return domain.OutboundAttempt{Path: rpc.PathPattern, Body: body, ContentType: formContentType, TargetHost: rpc.TargetHost}, nil
}

func (w *WireAdapter) DematerializeChat(ctx context.Context, resp *http.Response, metrics *domain.ContractMetrics, onDelta func(delta, convID string) error) (domain.GeminiReply, error) {
	if resp == nil || resp.Body == nil {
		return domain.GeminiReply{}, domain.CodecRejected(domain.OriginStreamGenerate, domain.ServiceGemini, "không có phản hồi chat")
	}
	if resp.StatusCode != http.StatusOK {
		return domain.GeminiReply{}, StatusError(resp, domain.OriginStreamGenerate, false, domain.ServiceGemini)
	}
	return ReadGeminiStream(ctx, resp.Body, metrics, onDelta)
}
