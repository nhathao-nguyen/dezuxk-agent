package google_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/core/domain"
)

type mockDiscoveryTransport struct {
	responseBody string
	statusCode   int
}

func (m *mockDiscoveryTransport) DoRequest(
	ctx context.Context,
	account *domain.ManagedAccount,
	service domain.ServiceKind,
	method string,
	path string,
	body io.Reader,
	contentType string,
) (*http.Response, error) {
	code := m.statusCode
	if code == 0 {
		code = http.StatusOK
	}
	return &http.Response{
		StatusCode: code,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(m.responseBody)),
	}, nil
}

func (m *mockDiscoveryTransport) BoundShort(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}

func (m *mockDiscoveryTransport) BoundStream(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}

func TestModelDiscovery_UnknownNewModel(t *testing.T) {
	// Synthetic upstream returns "gemini-new-experimental" and "Gemini Ultra Experimental"
	// neither exists anywhere in static constants!
	upstreamPayload := `)]}'

180
[["wrb.fr","otAQ7b","[[[\"mode_exp_999\",\"Gemini New Experimental\",2],[\"mode_ultra_777\",\"Gemini Ultra Thinking\",3]]]",null,null,null,"generic"]]
`
	transport := &mockDiscoveryTransport{responseBody: upstreamPayload}
	provider := google.NewGoogleModelDiscoveryProvider(transport, nil)

	account := &domain.ManagedAccount{
		ID:   "test-acc",
		Tier: 2,
	}

	models, err := provider.DiscoverModels(context.Background(), account)
	if err != nil {
		t.Fatalf("DiscoverModels failed: %v", err)
	}

	if len(models) != 2 {
		t.Fatalf("expected 2 discovered models, got %d", len(models))
	}

	m1 := models[0]
	if m1.ID != "gemini-gemini-new-experimental" && m1.ID != "gemini-new-experimental" {
		t.Errorf("unexpected canonical ID: %s", m1.ID)
	}
	if m1.DisplayName != "Gemini New Experimental" {
		t.Errorf("unexpected display name: %s", m1.DisplayName)
	}
	if !m1.IsActive {
		t.Errorf("model should be active")
	}

	m2 := models[1]
	if m2.DisplayName != "Gemini Ultra Thinking" {
		t.Errorf("unexpected display name: %s", m2.DisplayName)
	}
	// Thinking capability should be inferred automatically from "Thinking" in the name
	hasThinking := false
	for _, c := range m2.Capabilities {
		if c == domain.CapThinking {
			hasThinking = true
			break
		}
	}
	if !hasThinking {
		t.Errorf("expected CapThinking capability to be inferred for %s", m2.DisplayName)
	}
}

func TestModelDiscovery_FreeVsProAccount(t *testing.T) {
	// Upstream returns generic tier response
	freePayload := `)]}'

[["wrb.fr","otAQ7b","[[[\"8c46e95b1a07cecc\",\"3.5 Flash-Lite\",1],[\"56fdd199312815e2\",\"3.8 Flash\",1]]]",null,null,null,"generic"]]
`
	transport := &mockDiscoveryTransport{responseBody: freePayload}
	provider := google.NewGoogleModelDiscoveryProvider(transport, nil)

	accountFree := &domain.ManagedAccount{ID: "free-acc", Tier: 1}
	modelsFree, err := provider.DiscoverModels(context.Background(), accountFree)
	if err != nil {
		t.Fatalf("DiscoverModels free failed: %v", err)
	}

	for _, m := range modelsFree {
		if strings.Contains(strings.ToLower(m.DisplayName), "pro") {
			t.Errorf("free account should not discover pro models: %s", m.DisplayName)
		}
	}
}
