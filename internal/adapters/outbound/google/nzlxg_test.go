package google_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/core/domain"
)

const nzlxgInnerVector = `[1050, 1, 2, 2, null, 1050]`

func TestParseNzlxgBalance_Vector(t *testing.T) {
	metrics := domain.NewContractMetrics()
	balance, err := google.ParseNzlxgBalance(nzlxgInnerVector, metrics)
	if err != nil {
		t.Fatalf("parse vector: %v", err)
	}
	if balance.Amount != 1050 || balance.UnmappedFields != 5 {
		t.Fatalf("balance = %+v", balance)
	}
	if metrics.Snapshot().UnmappedFields != 5 {
		t.Fatalf("unmapped metric = %d", metrics.Snapshot().UnmappedFields)
	}
}

func TestParseNzlxgBalance_RejectsBadBalance(t *testing.T) {
	cases := []string{
		``,
		`[]`,
		`null`,
		`{}`,
		`[null, 1050]`,
		`["1050"]`,
		`[-1]`,
		`[1.5]`,
		`["SUPERSECRET"]`,
	}
	for _, inner := range cases {
		metrics := domain.NewContractMetrics()
		_, err := google.ParseNzlxgBalance(inner, metrics)
		if err == nil {
			t.Fatalf("expected schema error for %s", inner)
		}
		ce, ok := domain.AsCodecError(err)
		if !ok || ce.Class != domain.ClassSchemaUnexpected || ce.Origin != domain.OriginNzlxg {
			t.Fatalf("class for %s = %v", inner, err)
		}
		if strings.Contains(err.Error(), "SUPERSECRET") {
			t.Fatalf("secret leaked in error: %s", err.Error())
		}
		if metrics.Snapshot().SchemaUnexpected != 1 {
			t.Fatalf("schema metric for %s = %d", inner, metrics.Snapshot().SchemaUnexpected)
		}
	}
}

func TestParseNzlxgBalance_ExtraFieldStaysUnmapped(t *testing.T) {
	balance, err := google.ParseNzlxgBalance(`[1050, {"new":true}]`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if balance.Amount != 1050 || balance.UnmappedFields != 1 {
		t.Fatalf("balance = %+v", balance)
	}
}

func TestEncodeNzlxg_SeparatesTokenFromEnvelope(t *testing.T) {
	m := google.NewMarshaller()
	first, err := m.EncodeBatchexecute("nzlxg", []any{}, "at-token-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.EncodeBatchexecute("nzlxg", []any{}, "at-token-b")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("changing at must change the form body")
	}
	same, err := m.EncodeBatchexecute("nzlxg", []any{}, "at-token-a")
	if err != nil {
		t.Fatal(err)
	}
	if first != same {
		t.Fatal("same at and payload must pack the same body")
	}

	values, err := url.ParseQuery(first)
	if err != nil {
		t.Fatal(err)
	}
	if values.Get("at") != "at-token-a" {
		t.Fatal("at form field mismatch")
	}
	freq := values.Get("f.req")
	if strings.Contains(freq, "at-token-a") {
		t.Fatal("at leaked into f.req")
	}
	var got [][][]any
	if err := json.Unmarshal([]byte(freq), &got); err != nil {
		t.Fatalf("f.req: %v", err)
	}
	want := [][][]any{
		{{"nzlxg", "[]", nil, "generic"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("envelope = %s", freq)
	}
}

func TestGetCreditsBalance_DropsSecretBody(t *testing.T) {
	const secret = "SUPERSECRET_COOKIE"
	transport := &mockTransport{
		doRequestFunc: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader("Set-Cookie: " + secret + "; SNlM0e=" + secret)),
			}, nil
		},
	}
	client := google.NewFlowClientAdapter(transport, domain.DefaultRpcRegistry(), domain.NewContractMetrics())
	acc := &domain.ManagedAccount{ID: "test", FlowSNlM0e: "at-value"}
	_, err := client.GetCreditsBalance(context.Background(), acc)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "at-value") {
		t.Fatalf("secret leaked: %s", err.Error())
	}
	ce, ok := domain.AsCodecError(err)
	if !ok || ce.Class != domain.ClassExpired || ce.OriginStatus != 401 || ce.Origin != domain.OriginNzlxg {
		t.Fatalf("error = %#v", err)
	}
}
