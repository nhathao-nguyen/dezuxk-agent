package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// WebhookAlertDispatcher triển khai ports.AlertDispatcher với worker Goroutine chạy ngầm không chặn
type WebhookAlertDispatcher struct {
	cfg        config.WebhookAlertConfig
	httpClient *http.Client
	queue      chan domain.AlertPayload
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.Mutex
	closed     bool
}

// NewWebhookAlertDispatcher khởi tạo bộ phát cảnh báo Webhook
func NewWebhookAlertDispatcher(cfg config.WebhookAlertConfig, customClient ...*http.Client) *WebhookAlertDispatcher {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	if len(customClient) > 0 && customClient[0] != nil {
		client = customClient[0]
	}

	ctx, cancel := context.WithCancel(context.Background())
	d := &WebhookAlertDispatcher{
		cfg:        cfg,
		httpClient: client,
		queue:      make(chan domain.AlertPayload, 100),
		ctx:        ctx,
		cancel:     cancel,
	}

	if cfg.IsEnabled() {
		d.wg.Add(1)
		go d.workerLoop()
	}

	return d
}

// Dispatch đưa cảnh báo vào hàng đợi một cách không chặn (Non-blocking)
func (d *WebhookAlertDispatcher) Dispatch(alert domain.AlertPayload) {
	if d == nil || !d.cfg.IsEnabled() {
		return
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()

	select {
	case d.queue <- alert:
	default:
		log.Printf("[Webhook Alert] Hàng đợi cảnh báo đã đầy (100 alerts), bỏ qua cảnh báo: %s (%s)", alert.ErrorType, alert.AccountID)
	}
}

// Close dừng dispatcher và giải phóng tài nguyên
func (d *WebhookAlertDispatcher) Close() error {
	if d == nil {
		return nil
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.mu.Unlock()

	d.cancel()
	close(d.queue)
	d.wg.Wait()
	return nil
}

func (d *WebhookAlertDispatcher) workerLoop() {
	defer d.wg.Done()

	for alert := range d.queue {
		d.sendWithRetry(alert)
	}
}

// sendWithRetry gửi thông báo webhook kèm cơ chế thử lại lũy tiến (Exponential Backoff)
func (d *WebhookAlertDispatcher) sendWithRetry(alert domain.AlertPayload) {
	maxRetries := d.cfg.GetMaxRetries()
	backoff := d.cfg.GetRetryBackoff()

	messageText := d.formatMessage(alert)
	provider := d.cfg.GetProvider()

	url, bodyData, contentType, err := d.buildPayload(provider, messageText, alert)
	if err != nil {
		log.Printf("[Webhook Alert Error] Lỗi tạo payload webhook: %v", err)
		return
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		reqCtx, reqCancel := context.WithTimeout(d.ctx, 10*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(bodyData))
		if err != nil {
			reqCancel()
			log.Printf("[Webhook Alert Error] Lỗi tạo HTTP Request: %v", err)
			return
		}
		req.Header.Set("Content-Type", contentType)

		resp, err := d.httpClient.Do(req)
		reqCancel()

		if err == nil {
			// Đọc và đóng body để tái sử dụng connection
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				// Thành công
				return
			}
			log.Printf("[Webhook Alert Warning] Gửi webhook thất bại (HTTP %d, lần thử %d/%d)", resp.StatusCode, attempt, maxRetries)
		} else {
			log.Printf("[Webhook Alert Warning] Lỗi kết nối webhook (lần thử %d/%d): %v", attempt, maxRetries, err)
		}

		if attempt < maxRetries {
			select {
			case <-d.ctx.Done():
				return
			case <-time.After(backoff):
				backoff *= 2
			}
		}
	}

	log.Printf("[Webhook Alert Failed] Đã hết số lần thử lại (%d lần) cho cảnh báo: %s", maxRetries, alert.ErrorType)
}

func (d *WebhookAlertDispatcher) formatMessage(alert domain.AlertPayload) string {
	tmpl := d.cfg.GetMessageTemplate()
	replacer := strings.NewReplacer(
		"{account_id}", alert.AccountID,
		"{error_type}", alert.ErrorType,
		"{service}", string(alert.Service),
		"{reason}", alert.Reason,
		"{action_required}", alert.ActionRequired,
		"{timestamp}", alert.Timestamp.Format(time.RFC3339),
	)
	return replacer.Replace(tmpl)
}

func (d *WebhookAlertDispatcher) buildPayload(provider string, message string, alert domain.AlertPayload) (targetURL string, body []byte, contentType string, err error) {
	contentType = "application/json"

	switch provider {
	case "telegram":
		targetURL = d.cfg.URL
		if targetURL == "" && d.cfg.Token != "" {
			targetURL = fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", d.cfg.Token)
		}
		if targetURL == "" {
			return "", nil, "", errors.New("thiếu url hoặc token cho provider telegram")
		}

		payload := map[string]any{
			"chat_id": d.cfg.ChatID,
			"text":    message,
		}
		data, err := json.Marshal(payload)
		return targetURL, data, contentType, err

	case "discord":
		targetURL = d.cfg.URL
		if targetURL == "" {
			return "", nil, "", errors.New("thiếu webhook url cho provider discord")
		}
		payload := map[string]any{
			"content": message,
		}
		data, err := json.Marshal(payload)
		return targetURL, data, contentType, err

	case "slack":
		targetURL = d.cfg.URL
		if targetURL == "" {
			return "", nil, "", errors.New("thiếu webhook url cho provider slack")
		}
		payload := map[string]any{
			"text": message,
		}
		data, err := json.Marshal(payload)
		return targetURL, data, contentType, err

	default: // "generic"
		targetURL = d.cfg.URL
		if targetURL == "" {
			return "", nil, "", errors.New("thiếu webhook url cho provider generic")
		}
		payload := map[string]any{
			"account_id":      alert.AccountID,
			"error_type":      alert.ErrorType,
			"service":         alert.Service,
			"reason":          alert.Reason,
			"status_code":     alert.StatusCode,
			"action_required": alert.ActionRequired,
			"timestamp":       alert.Timestamp.Format(time.RFC3339),
			"message":         message,
		}
		data, err := json.Marshal(payload)
		return targetURL, data, contentType, err
	}
}

// Đảm bảo implement đúng ports.AlertDispatcher
var _ ports.AlertDispatcher = (*WebhookAlertDispatcher)(nil)
