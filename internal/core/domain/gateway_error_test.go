package domain_test

import (
	"fmt"
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestClassifyUpstreamStatus(t *testing.T) {
	expired := domain.ClassifyUpstreamStatus(domain.OpFlowGetCredits, domain.OriginNzlxg, 401, true, domain.ServiceFlow)
	if expired.Class != domain.ClassExpired || expired.Retryable || expired.HTTPStatus() != 401 {
		t.Fatalf("401 = %#v", expired)
	}
	limited := domain.ClassifyUpstreamStatus(domain.OpFlowGetCredits, domain.OriginNzlxg, 429, true, domain.ServiceFlow)
	if limited.Class != domain.ClassRateLimited || !limited.Retryable || limited.HTTPStatus() != 429 {
		t.Fatalf("429 = %#v", limited)
	}
	writeLimited := domain.ClassifyUpstreamStatus(domain.OpChatCompletions, domain.OriginStreamGenerate, 429, false, domain.ServiceGemini)
	if writeLimited.Retryable {
		t.Fatal("non-idempotent 429 must not be retryable")
	}
	rejected := domain.ClassifyUpstreamStatus(domain.OpFlowGetCredits, domain.OriginNzlxg, 403, true, domain.ServiceFlow)
	if rejected.Class != domain.ClassUpstreamRejected || rejected.Retryable {
		t.Fatalf("403 = %#v", rejected)
	}
	down := domain.ClassifyUpstreamStatus(domain.OpFlowGetCredits, domain.OriginNzlxg, 503, true, domain.ServiceFlow)
	if down.Class != domain.ClassUpstreamUnavailable || !down.Retryable || down.HTTPStatus() != 503 {
		t.Fatalf("503 = %#v", down)
	}
	if domain.ClassifyUpstreamStatus(domain.OpFlowGetCredits, domain.OriginNzlxg, 200, true, domain.ServiceFlow) != nil {
		t.Fatal("200 must not be an error")
	}
}

func TestEnsureGatewayHidesRawError(t *testing.T) {
	raw := fmt.Errorf("cookie SUPERSECRET")
	ge := domain.EnsureGateway(raw, domain.OpFlowGetCredits, domain.ServiceFlow)
	if strings.Contains(ge.Error(), "SUPERSECRET") {
		t.Fatalf("leaked: %s", ge.Error())
	}
	if ge.Class != domain.ClassUpstreamRejected {
		t.Fatalf("class = %s", ge.Class)
	}
}

func TestEnsureGatewayNamesCodecError(t *testing.T) {
	err := domain.CodecHTTP(domain.OriginNzlxg, 401, true, domain.ServiceFlow)
	ge := domain.EnsureGateway(err, domain.OpFlowGetCredits, domain.ServiceFlow)
	if ge.Operation != domain.OpFlowGetCredits || ge.OriginOperation != domain.OriginNzlxg || ge.Class != domain.ClassExpired {
		t.Fatalf("named = %#v", ge)
	}
}

func TestSchemaUnexpectedStatus(t *testing.T) {
	ge := domain.SchemaUnexpected(domain.OpFlowGetCredits, domain.OriginNzlxg, domain.ServiceFlow, "phản hồi số dư không đúng hợp đồng")
	if ge.HTTPStatus() != 502 || ge.Retryable {
		t.Fatalf("schema = %#v", ge)
	}
}
