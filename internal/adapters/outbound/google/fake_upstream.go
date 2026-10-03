package google

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// FakeUpstreamTransport cung cấp mô phỏng upstream Gemini cho kiểm thử cụm phân tán và CI
type FakeUpstreamTransport struct{}

var _ ports.UpstreamGoogleTransport = (*FakeUpstreamTransport)(nil)

func NewFakeUpstreamTransport() *FakeUpstreamTransport {
	return &FakeUpstreamTransport{}
}

func (f *FakeUpstreamTransport) DoRequest(
	ctx context.Context,
	account *domain.ManagedAccount,
	service domain.ServiceKind,
	method string,
	path string,
	body io.Reader,
	contentType string,
) (*http.Response, error) {
	mockChunk := `)]}'

160
[["wrb.fr","assistant.lamda.BardFrontendService","[null,[\"c_cluster_123456\",\"r_cluster_987654\"],null,null,[[\"rc_cluster_001\",[\"Xin chào từ Fake Gemini Upstream! Cụm Gateway Multi-Node đã sẵn sàng phục vụ.\"]]]]"]]
`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/plain;charset=UTF-8"},
		},
		Body: io.NopCloser(strings.NewReader(mockChunk)),
	}, nil
}

func (f *FakeUpstreamTransport) BoundShort(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 10*time.Second)
}

func (f *FakeUpstreamTransport) BoundStream(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 30*time.Second)
}
