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

// TenantBrowserSession lưu ngữ cảnh phiên trình duyệt cách ly của từng Tenant
type TenantBrowserSession struct {
	TenantID         string
	BrowserContextID string
	TargetID         string
	SessionID        string
	CreatedAt        time.Time
	LastActive       time.Time
}

// BrowserCDPController kết nối trực tiếp vào Chrome qua DevTools Protocol WebSocket duy nhất
// và quản lý các Incognito BrowserContext độc lập cho từng Tenant
type BrowserCDPController struct {
	cdpPort               int
	httpClient            *http.Client
	mu                    sync.Mutex
	wsConn                *websocket.Conn
	reqID                 int64
	sessions              map[string]*TenantBrowserSession // tenant_id -> session
	activePerTenant       map[string]int
	maxConcurrentSessions int
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
		sessions:              make(map[string]*TenantBrowserSession),
		activePerTenant:       make(map[string]int),
		maxConcurrentSessions: 3,
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

// executeCDPCommandWithSession gửi lệnh JSON-RPC 2.0 tới Chrome CDP, có hỗ trợ gắn sessionId cho tab riêng biệt
func (c *BrowserCDPController) executeCDPCommandWithSession(ctx context.Context, sessionID, method string, params map[string]interface{}) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

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
		if sessionID != "" {
			reqObj["sessionId"] = sessionID
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

		if attempt == 0 {
			continue
		}
		return nil, fmt.Errorf("lỗi đọc phản hồi từ CDP: %w", readErr)
	}

	return nil, fmt.Errorf("không thể thực thi lệnh CDP sau khi thử kết nối lại")
}

func (c *BrowserCDPController) executeCDPCommand(ctx context.Context, method string, params map[string]interface{}) (json.RawMessage, error) {
	return c.executeCDPCommandWithSession(ctx, "", method, params)
}

// GetOrCreateTenantSession lấy hoặc khởi tạo một BrowserContext và Session độc lập cho Tenant
func (c *BrowserCDPController) GetOrCreateTenantSession(ctx context.Context, tenantID string) (*TenantBrowserSession, error) {
	if strings.TrimSpace(tenantID) == "" {
		tenantID = "default"
	}

	c.mu.Lock()
	if sess, ok := c.sessions[tenantID]; ok {
		sess.LastActive = time.Now()
		c.mu.Unlock()
		return sess, nil
	}

	if c.activePerTenant[tenantID] >= c.maxConcurrentSessions {
		c.mu.Unlock()
		return nil, fmt.Errorf("vượt quá hạn mức số phiên trình duyệt đồng thời cho tenant %q (tối đa %d)", tenantID, c.maxConcurrentSessions)
	}
	c.mu.Unlock()

	// 1. Tạo isolated incognito browser context
	resCtx, err := c.executeCDPCommand(ctx, "Target.createBrowserContext", map[string]interface{}{})
	if err != nil {
		return nil, fmt.Errorf("lỗi tạo isolated browser context: %w", err)
	}
	var ctxData struct {
		BrowserContextID string `json:"browserContextId"`
	}
	if err := json.Unmarshal(resCtx, &ctxData); err != nil || ctxData.BrowserContextID == "" {
		return nil, fmt.Errorf("Chrome CDP không trả về browserContextId: %v", err)
	}

	// 2. Tạo target tab trong context đó
	resTarget, err := c.executeCDPCommand(ctx, "Target.createTarget", map[string]interface{}{
		"url":              "about:blank",
		"browserContextId": ctxData.BrowserContextID,
	})
	if err != nil {
		_, _ = c.executeCDPCommand(ctx, "Target.disposeBrowserContext", map[string]interface{}{"browserContextId": ctxData.BrowserContextID})
		return nil, fmt.Errorf("lỗi tạo tab mới trong browser context: %w", err)
	}
	var targetData struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(resTarget, &targetData); err != nil || targetData.TargetID == "" {
		_, _ = c.executeCDPCommand(ctx, "Target.disposeBrowserContext", map[string]interface{}{"browserContextId": ctxData.BrowserContextID})
		return nil, fmt.Errorf("Chrome CDP không trả về targetId: %v", err)
	}

	// 3. Đính kèm target để lấy sessionId
	resAttach, err := c.executeCDPCommand(ctx, "Target.attachToTarget", map[string]interface{}{
		"targetId": targetData.TargetID,
		"flatten":  true,
	})
	if err != nil {
		_, _ = c.executeCDPCommand(ctx, "Target.closeTarget", map[string]interface{}{"targetId": targetData.TargetID})
		_, _ = c.executeCDPCommand(ctx, "Target.disposeBrowserContext", map[string]interface{}{"browserContextId": ctxData.BrowserContextID})
		return nil, fmt.Errorf("lỗi đính kèm sessionId: %w", err)
	}
	var attachData struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(resAttach, &attachData); err != nil || attachData.SessionID == "" {
		_, _ = c.executeCDPCommand(ctx, "Target.closeTarget", map[string]interface{}{"targetId": targetData.TargetID})
		_, _ = c.executeCDPCommand(ctx, "Target.disposeBrowserContext", map[string]interface{}{"browserContextId": ctxData.BrowserContextID})
		return nil, fmt.Errorf("Chrome CDP không trả về sessionId: %v", err)
	}

	session := &TenantBrowserSession{
		TenantID:         tenantID,
		BrowserContextID: ctxData.BrowserContextID,
		TargetID:         targetData.TargetID,
		SessionID:        attachData.SessionID,
		CreatedAt:        time.Now(),
		LastActive:       time.Now(),
	}

	c.mu.Lock()
	c.sessions[tenantID] = session
	c.activePerTenant[tenantID]++
	c.mu.Unlock()

	return session, nil
}

// CloseTenantSession dọn dẹp và hủy toàn bộ cookie, bộ nhớ đệm, tab của Tenant
func (c *BrowserCDPController) CloseTenantSession(ctx context.Context, tenantID string) error {
	c.mu.Lock()
	sess, ok := c.sessions[tenantID]
	if !ok {
		c.mu.Unlock()
		return nil
	}
	delete(c.sessions, tenantID)
	if c.activePerTenant[tenantID] > 0 {
		c.activePerTenant[tenantID]--
	}
	c.mu.Unlock()

	_, _ = c.executeCDPCommand(ctx, "Target.closeTarget", map[string]interface{}{"targetId": sess.TargetID})
	_, err := c.executeCDPCommand(ctx, "Target.disposeBrowserContext", map[string]interface{}{"browserContextId": sess.BrowserContextID})
	return err
}

// ExecuteInTenantSession thực thi lệnh CDP trong ngữ cảnh phiên cách ly của Tenant
func (c *BrowserCDPController) ExecuteInTenantSession(ctx context.Context, tenantID, method string, params map[string]interface{}) (json.RawMessage, error) {
	sess, err := c.GetOrCreateTenantSession(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return c.executeCDPCommandWithSession(ctx, sess.SessionID, method, params)
}

// Close giải phóng toàn bộ kết nối WebSocket và dọn dẹp các phiên
func (c *BrowserCDPController) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for tenantID, sess := range c.sessions {
		if c.wsConn != nil {
			_ = c.wsConn.WriteJSON(map[string]interface{}{
				"id":     atomic.AddInt64(&c.reqID, 1),
				"method": "Target.disposeBrowserContext",
				"params": map[string]interface{}{"browserContextId": sess.BrowserContextID},
			})
		}
		delete(c.sessions, tenantID)
	}
	if c.wsConn != nil {
		_ = c.wsConn.Close()
		c.wsConn = nil
	}
}

// -------------------------------------------------------------
// Browser Tools
// -------------------------------------------------------------

// BrowserNavigateTool mở URL trên trình duyệt trong phiên cách ly của tenant
type BrowserNavigateTool struct {
	ctrl *BrowserCDPController
}

func NewBrowserNavigateTool(ctrl *BrowserCDPController) *BrowserNavigateTool {
	return &BrowserNavigateTool{ctrl: ctrl}
}

func (t *BrowserNavigateTool) Name() string { return "browser_navigate" }
func (t *BrowserNavigateTool) Description() string {
	return "Điều khiển trình duyệt Chrome mở một đường dẫn URL web thời gian thực thông qua Chrome CDP với phiên cách ly."
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

	identity, ok := domain.TenantIdentityFromContext(ctx)
	if !ok {
		identity = domain.DefaultInternalIdentity()
	}

	// Kiểm tra chính sách mạng của Tenant
	if !identity.IsURLAllowed(args.URL) {
		return "", fmt.Errorf("truy cập bị chặn bởi Network Policy: URL %q không được phép truy cập theo chính sách của tenant %q", args.URL, identity.TenantID)
	}

	_, err := t.ctrl.ExecuteInTenantSession(ctx, identity.TenantID, "Page.navigate", map[string]interface{}{"url": args.URL})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("✓ Trình duyệt đã chuyển hướng thành công tới: %s (Tenant: %s)", args.URL, identity.TenantID), nil
}

// BrowserEvaluateTool thực thi JavaScript trong ngữ cảnh trang của tenant
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

// Permission: browser_evaluate là công cụ thực thi mã lệnh trên trang web, có thể trích xuất token hoặc tương tác với DOM, do đó yêu cầu phê duyệt bảo mật
func (t *BrowserEvaluateTool) Permission() domain.PermissionLevel {
	return domain.PermissionRequiresApproval
}

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

	identity, ok := domain.TenantIdentityFromContext(ctx)
	if !ok {
		identity = domain.DefaultInternalIdentity()
	}

	raw, err := t.ctrl.ExecuteInTenantSession(ctx, identity.TenantID, "Runtime.evaluate", map[string]interface{}{
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
	return fmt.Sprintf("Kết quả đánh giá JS (Tenant: %s):\n%s", identity.TenantID, string(valBytes)), nil
}

// BrowserScreenshotTool chụp ảnh màn hình trang web trong phiên cách ly của tenant
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
	identity, ok := domain.TenantIdentityFromContext(ctx)
	if !ok {
		identity = domain.DefaultInternalIdentity()
	}

	raw, err := t.ctrl.ExecuteInTenantSession(ctx, identity.TenantID, "Page.captureScreenshot", map[string]interface{}{"format": "png"})
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

	tenantStorageDir := filepath.Join(t.storageDir, identity.TenantID)
	_ = os.MkdirAll(tenantStorageDir, 0755)
	filename := fmt.Sprintf("screenshot_%d.png", time.Now().UnixNano())
	filePath := filepath.Join(tenantStorageDir, filename)

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
