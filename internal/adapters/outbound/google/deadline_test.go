package google

import (
	"context"
	"testing"
	"time"

	"dezuxk-gateway/internal/config"
)

func TestTransportDeadlines(t *testing.T) {
	upstream := NewGoogleTransportAdapter(&config.Config{Server: config.ServerConfig{
		UpstreamShortTimeout:  20 * time.Second,
		UpstreamStreamTimeout: 5 * time.Minute,
	}})
	parent, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	streamCtx, stopStream := upstream.BoundStream(parent)
	defer stopStream()
	parentDeadline, _ := parent.Deadline()
	streamDeadline, ok := streamCtx.Deadline()
	if !ok || !parentDeadline.Equal(streamDeadline) {
		t.Fatalf("stream deadline %v parent %v", streamDeadline, parentDeadline)
	}

	shortCtx, stopShort := upstream.BoundShort(context.Background())
	defer stopShort()
	shortDeadline, ok := shortCtx.Deadline()
	if !ok || time.Until(shortDeadline) > 25*time.Second {
		t.Fatalf("short deadline %v", shortDeadline)
	}
}

func TestHandshakeUsesShortTimeout(t *testing.T) {
	raw := NewGoogleTokenExtractorAdapter(1500 * time.Millisecond)
	adapter := raw.(*TokenExtractorAdapter)
	if adapter.client.Timeout != 1500*time.Millisecond || adapter.short != 1500*time.Millisecond {
		t.Fatalf("timeout = %s short = %s", adapter.client.Timeout, adapter.short)
	}
	ctx, cancel := adapter.bound(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 2*time.Second {
		t.Fatalf("deadline %v", deadline)
	}
}
