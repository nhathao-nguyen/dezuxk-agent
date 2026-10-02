package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"

	"github.com/gorilla/websocket"
)

// BrowserCDPController kết nối trực tiếp vào Chrome qua DevTools Protocol WebSocket duy nhất (Persistent Session)
type BrowserCDPController struct {
	cdpPort    int
	httpClient *http.Client
	mu         sync.Mutex
	wsConn     *websocket.Conn
	reqID      int64
}

// NewBrowserCDPController khởi tạo controller điều khiển Chrome
func NewBrowserCDPController(cdpPort int) *BrowserCDPController {
	if cdpPort <= 0 {
		cdpPort = 9222
	}
	return &BrowserCDPController{
		cdpPort: cdpPort,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c *BrowserCDPController) getWebSocketURL(ctx context.Context) (string, error) {
	versionURL := fmt.Sprintf("http://127.0.0.1:%d/json/version", c.cdpPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("không thể kết nối Chrome CDP tại cổng %d. Vui lòng đảm bảo Chrome đang chạy với cờ '--remote-debugging-port=%d': %w", c.cdpPort, c.cdpPort, err)
	}
	defer resp.Body.Close()

	var data struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("lỗi đọc JSON từ Chrome CDP: %w", err)
	}

	if data.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("Chrome CDP không trả về webSocketDebuggerUrl")
	}

	return data.WebSocketDebuggerURL, nil
}

func (c *BrowserCDPController) connectWS(ctx context.Context) (*websocket.Conn, error) {
	wsURL, err := c.getWebSocketURL(ctx)
	if err != nil {
		return nil, err
	}

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 3 * time.Second
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("không thể kết nối WebSocket tới CDP: %w", err)
	}
	return conn, nil
}

// executeCDPCommand gửi lệnh JSON-RPC 2.0 tới Chrome CDP qua WebSocket với cơ chế Persistent Connection & Auto-Reconnect
func (c *BrowserCDPController) executeCDPCommand(ctx context.Context, method string, params map[string]interface{}) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Thử gửi lệnh qua kết nối hiện tại; nếu đứt kết nối, tự động kết nối lại 1 lần
	for attempt := 0; attempt < 2; attempt++ {
		if c.wsConn == nil {
			conn, err := c.connectWS(ctx)
			if err != nil {
				return nil, err
			}
			c.wsConn = conn
		}

		id := atomic.AddInt64(&c.reqID, 1)
		reqObj := map[string]interface{}{
			"id":     id,
			"method": method,
			"params": params,
		}

		_ = c.wsConn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := c.wsConn.WriteJSON(reqObj); err != nil {
			_ = c.wsConn.Close()
			c.wsConn = nil
			if attempt == 0 {
				continue
			}
			return nil, fmt.Errorf("lỗi gửi lệnh tới CDP: %w", err)
		}

		// Chờ kết quả phản hồi
		_ = c.wsConn.SetReadDeadline(time.Now().Add(30 * time.Second))
		var readErr error
		for {
			var resp struct {
				ID     int64           `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := c.wsConn.ReadJSON(&resp); err != nil {
				readErr = err
				_ = c.wsConn.Close()
				c.wsConn = nil
				break
			}

			if resp.ID == id {
				if resp.Error != nil {
					return nil, fmt.Errorf("CDP lỗi (%d): %s", resp.Error.Code, resp.Error.Message)
				}
				return resp.Result, nil
			}
		}

		if readErr != nil && attempt == 0 {
			continue
		}
		if readErr != nil {
			return nil, fmt.Errorf("lỗi đọc phản hồi từ CDP: %w", readErr)
		}
	}

	return nil, fmt.Errorf("không thể thực thi lệnh CDP sau khi thử kết nối lại")
}

// Close giải phóng kết nối WebSocket tới Chrome CDP
func (c *BrowserCDPController) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.wsConn != nil {
		_ = c.wsConn.Close()
		c.wsConn = nil
	}
}

// -------------------------------------------------------------
// Browser Tools
// -------------------------------------------------------------

// BrowserNavigateTool mở URL trên trình duyệt
type BrowserNavigateTool struct {
	ctrl *BrowserCDPController
}

func NewBrowserNavigateTool(ctrl *BrowserCDPController) *BrowserNavigateTool {
	return &BrowserNavigateTool{ctrl: ctrl}
}

func (t *BrowserNavigateTool) Name() string { return "browser_navigate" }
func (t *BrowserNavigateTool) Description() string {
	return "Điều khiển trình duyệt Chrome mở một đường dẫn URL web thời gian thực thông qua Chrome CDP."
}
func (t *BrowserNavigateTool) Permission() domain.PermissionLevel { return domain.PermissionSafe }

func (t *BrowserNavigateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {
				"type": "string",
				"description": "Địa chỉ URL đầy đủ cần duyệt (ví dụ: https://example.com)"
			}
		},
		"required": ["url"]
	}`)
}

func (t *BrowserNavigateTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("tham số JSON không hợp lệ: %w", err)
	}
	if !strings.HasPrefix(args.URL, "http://") && !strings.HasPrefix(args.URL, "https://") {
		args.URL = "https://" + args.URL
	}

	_, err := t.ctrl.executeCDPCommand(ctx, "Page.navigate", map[string]interface{}{"url": args.URL})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("✓ Trình duyệt đã chuyển hướng thành công tới: %s", args.URL), nil
}

// BrowserEvaluateTool thực thi JavaScript trong ngữ cảnh trang
type BrowserEvaluateTool struct {
	ctrl *BrowserCDPController
}

func NewBrowserEvaluateTool(ctrl *BrowserCDPController) *BrowserEvaluateTool {
	return &BrowserEvaluateTool{ctrl: ctrl}
}

func (t *BrowserEvaluateTool) Name() string { return "browser_evaluate" }
func (t *BrowserEvaluateTool) Description() string {
	return "Thực thi một biểu thức JavaScript trong ngữ cảnh trang web hiện tại để trích xuất dữ liệu, đọc DOM hoặc kiểm tra trạng thái trang."
}
func (t *BrowserEvaluateTool) Permission() domain.PermissionLevel { return domain.PermissionSafe }

func (t *BrowserEvaluateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"expression": {
				"type": "string",
				"description": "Biểu thức JS cần thực thi (ví dụ: 'document.title', 'document.body.innerText.substring(0, 1000)')"
			}
		},
		"required": ["expression"]
	}`)
}

func (t *BrowserEvaluateTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("tham số JSON không hợp lệ: %w", err)
	}

	raw, err := t.ctrl.executeCDPCommand(ctx, "Runtime.evaluate", map[string]interface{}{
		"expression":    args.Expression,
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}

	var evalRes struct {
		Result struct {
			Type  string      `json:"type"`
			Value interface{} `json:"value"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &evalRes)

	valBytes, _ := json.MarshalIndent(evalRes.Result.Value, "", "  ")
	return fmt.Sprintf("Kết quả đánh giá JS:\n%s", string(valBytes)), nil
}

// BrowserScreenshotTool chụp ảnh màn hình trang web
type BrowserScreenshotTool struct {
	ctrl       *BrowserCDPController
	storageDir string
}

func NewBrowserScreenshotTool(ctrl *BrowserCDPController, storageDir string) *BrowserScreenshotTool {
	if storageDir == "" {
		storageDir = filepath.Join("storage", "media")
	}
	return &BrowserScreenshotTool{
		ctrl:       ctrl,
		storageDir: storageDir,
	}
}

func (t *BrowserScreenshotTool) Name() string { return "browser_screenshot" }
func (t *BrowserScreenshotTool) Description() string {
	return "Chụp ảnh màn hình trang web hiện tại qua Chrome CDP và lưu lại tệp hình ảnh PNG trên đĩa cứng để kiểm tra giao diện trực quan."
}
func (t *BrowserScreenshotTool) Permission() domain.PermissionLevel { return domain.PermissionSafe }

func (t *BrowserScreenshotTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"format": {
				"type": "string",
				"enum": ["png", "jpeg"],
				"description": "Định dạng ảnh (mặc định: png)"
			}
		}
	}`)
}

func (t *BrowserScreenshotTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	raw, err := t.ctrl.executeCDPCommand(ctx, "Page.captureScreenshot", map[string]interface{}{"format": "png"})
	if err != nil {
		return "", err
	}

	var res struct {
		Data string `json:"data"` // Base64 encoded image
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("lỗi giải mã dữ liệu ảnh từ CDP: %w", err)
	}

	imgBytes, err := base64.StdEncoding.DecodeString(res.Data)
	if err != nil {
		return "", fmt.Errorf("lỗi decode base64: %w", err)
	}

	_ = os.MkdirAll(t.storageDir, 0755)
	filename := fmt.Sprintf("screenshot_%d.png", time.Now().UnixNano())
	filePath := filepath.Join(t.storageDir, filename)

	if err := os.WriteFile(filePath, imgBytes, 0644); err != nil {
		return "", fmt.Errorf("không thể lưu tệp ảnh: %w", err)
	}

	return fmt.Sprintf("✓ Đã chụp ảnh màn hình thành công (%d bytes), lưu tại: %s", len(imgBytes), filePath), nil
}

// RegisterBrowserTools đăng ký các công cụ duyệt web Chrome CDP vào ToolRegistry
func RegisterBrowserTools(registry ports.ToolRegistry, cdpPort int, storageDir string) {
	ctrl := NewBrowserCDPController(cdpPort)
	registry.RegisterTool(NewBrowserNavigateTool(ctrl))
	registry.RegisterTool(NewBrowserEvaluateTool(ctrl))
	registry.RegisterTool(NewBrowserScreenshotTool(ctrl, storageDir))
}
