package google_test

import (
	"context"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/core/domain"
)

// TestProtocolDrift_FailsLoudlyWithSchemaError verifies that if Google changes
// its response payload structure to an unexpected format, the stream decoder
// fails loudly with CodecSchema, records metrics, and does not silently swallow errors.
func TestProtocolDrift_FailsLoudlyWithSchemaError(t *testing.T) {
	metrics := domain.NewContractMetrics().Bind(domain.OpChatCompletions)

	// Simulated broken or altered upstream response (e.g. Google changed JSON envelope)
	unknownEnvelope := `)]}'
123
[["wrb.fr", null, "{\"unknown_field_123\": [1,2,3], \"changed_schema\": true}"]]
`
	ctx := context.Background()
	reply, err := google.ReadGeminiStreamWithThinking(ctx, strings.NewReader(unknownEnvelope), metrics, nil, nil)

	if err == nil {
		t.Fatalf("expected error on protocol drift / schema change, got nil with reply: %+v", reply)
	}

	codecErr, ok := domain.AsCodecError(err)
	if !ok {
		t.Fatalf("expected domain.CodecError, got %T: %v", err, err)
	}

	if codecErr.Class != domain.ClassSchemaUnexpected {
		t.Errorf("expected ClassSchemaUnexpected, got %v", codecErr.Class)
	}

	snapshot := metrics.Snapshot()
	if snapshot.SchemaUnexpected == 0 {
		t.Errorf("expected SchemaUnexpected count > 0 in metrics, got %d", snapshot.SchemaUnexpected)
	}
}
