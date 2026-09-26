package http

import (
	"encoding/json"
	"net/http"

	"dezuxk-gateway/internal/core/domain"

	"github.com/go-chi/chi/v5/middleware"
)

func writeChatError(w http.ResponseWriter, r *http.Request, metrics *domain.ContractMetrics, err error, streamed bool) {
	ge := domain.EnsureGateway(err, domain.OpChatCompletions, domain.ServiceGemini)
	if metrics != nil {
		metrics.Bind(ge.Operation).AddClass(ge.Class)
	}
	payload := map[string]any{
		"error": map[string]string{
			"message": ge.Message,
			"type":    string(ge.Class),
			"code":    string(ge.Class),
		},
		"meta": errorMeta(r, ge),
	}
	if streamed {
		encoded, _ := json.Marshal(payload)
		_, _ = w.Write([]byte("data: " + string(encoded) + "\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ge.HTTPStatus())
	_ = json.NewEncoder(w).Encode(payload)
}

func writeOperationError(w http.ResponseWriter, r *http.Request, metrics *domain.ContractMetrics, err error, operation string, service domain.ServiceKind) {
	ge := domain.EnsureGateway(err, operation, service)
	if metrics != nil {
		metrics.Bind(ge.Operation).AddClass(ge.Class)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ge.HTTPStatus())
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  nil,
		"error": map[string]string{"class": string(ge.Class), "message": ge.Message},
		"meta":  errorMeta(r, ge),
	})
}

func writeFlowError(w http.ResponseWriter, r *http.Request, metrics *domain.ContractMetrics, err error) {
	writeOperationError(w, r, metrics, err, domain.OpFlowGetCredits, domain.ServiceFlow)
}

func writeFlowCredits(w http.ResponseWriter, r *http.Request, accountID string, balance domain.FlowCreditBalance) {
	meta := map[string]any{
		"operation":       domain.OpFlowGetCredits,
		"retryable":       false,
		"unmapped_fields": balance.UnmappedFields,
		"spec_version":    balance.SpecVersion,
		"debug": map[string]any{
			"origin_operation": domain.OriginNzlxg,
		},
	}
	if id := middleware.GetReqID(r.Context()); id != "" {
		meta["correlation_id"] = id
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{
			"account_id":    accountID,
			"total_credits": balance.Amount,
		},
		"error": nil,
		"meta":  meta,
	})
}

func errorMeta(r *http.Request, ge *domain.GatewayError) map[string]any {
	meta := map[string]any{
		"operation": ge.Operation,
		"retryable": ge.Retryable,
	}
	debug := map[string]any{}
	if ge.OriginOperation != "" {
		debug["origin_operation"] = ge.OriginOperation
	}
	if ge.OriginStatus != 0 {
		debug["origin_status"] = ge.OriginStatus
	}
	if len(debug) > 0 {
		meta["debug"] = debug
	}
	if r != nil {
		if id := middleware.GetReqID(r.Context()); id != "" {
			meta["correlation_id"] = id
		}
	}
	return meta
}
