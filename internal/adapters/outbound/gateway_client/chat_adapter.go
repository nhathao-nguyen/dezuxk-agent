package gateway_client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// HTTPChatAdapter cài đặt ports.ChatUseCase bằng cách gọi tới Dezuxk Gateway HTTP Server (/v1)
type HTTPChatAdapter struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewHTTPChatAdapter khởi tạo HTTPChatAdapter
func NewHTTPChatAdapter(baseURL, apiKey string, timeout time.Duration) *HTTPChatAdapter {
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL = baseURL + "/v1"
	}
	return &HTTPChatAdapter{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

var _ ports.ChatUseCase = (*HTTPChatAdapter)(nil)

func (c *HTTPChatAdapter) ExecuteChatStream(
	ctx context.Context,
	req *domain.OpenAIChatRequest,
	streamWriter io.Writer,
	flusher func(),
) error {
	return fmt.Errorf("ExecuteChatStream chưa cần thiết cho HTTPChatAdapter nội bộ")
}

func (c *HTTPChatAdapter) ExecuteChatSync(
	ctx context.Context,
	req *domain.OpenAIChatRequest,
) (*domain.OpenAIChatResponse, error) {
	endpoint := c.baseURL + "/chat/completions"

	// Đảm bảo stream = false
	reqCopy := *req
	reqCopy.Stream = false

	bodyBytes, err := json.Marshal(reqCopy)
	if err != nil {
		return nil, fmt.Errorf("không thể mã hóa JSON request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("không thể tạo HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("lỗi kết nối tới Dezuxk Gateway tại %s: %w (Hãy đảm bảo Dezuxk Server đang chạy)", endpoint, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("lỗi đọc dữ liệu phản hồi từ Gateway: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gateway trả về mã lỗi HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var chatResp domain.OpenAIChatResponse
	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return nil, fmt.Errorf("lỗi giải mã JSON phản hồi từ Gateway: %w (Nội dung: %s)", err, string(respBytes))
	}

	return &chatResp, nil
}
