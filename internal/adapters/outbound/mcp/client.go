package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

// MCPClient kết nối tới MCP Server qua Stdio JSON-RPC 2.0 (Model Context Protocol chuẩn Anthropic)
type MCPClient struct {
	serverName      string
	command         string
	args            []string
	protocolVersion string
	timeout         time.Duration
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	stdout          *bufio.Scanner
	mu              sync.Mutex
	reqID           int64
	pending         map[int64]chan jsonRPCResponse
	closed          bool
}

func (c *MCPClient) ProtocolVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.protocolVersion
}

func (c *MCPClient) SetTimeout(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.timeout = d
	}
}

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// StartStdioMCPClient khởi tạo và bắt tay với một MCP Server chạy bằng process cục bộ qua Stdio
func StartStdioMCPClient(ctx context.Context, serverName, command string, args ...string) (*MCPClient, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("không thể mở stdin pipe cho MCP server %s: %w", serverName, err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("không thể mở stdout pipe cho MCP server %s: %w", serverName, err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("không thể khởi chạy MCP server %s: %w", serverName, err)
	}

	client := &MCPClient{
		serverName: serverName,
		command:    command,
		args:       args,
		timeout:    30 * time.Second,
		cmd:        cmd,
		stdin:      stdin,
		stdout:     bufio.NewScanner(stdoutPipe),
		pending:    make(map[int64]chan jsonRPCResponse),
	}

	// Đọc stdout liên tục trong background goroutine
	go client.readLoop()

	// 1. Thương lượng phiên bản giao thức (Protocol Version Negotiation)
	desiredVersion := "2024-11-05"
	initParams := map[string]interface{}{
		"protocolVersion": desiredVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "dezuxk-agent",
			"version": "1.0.0",
		},
	}

	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var initResult json.RawMessage
	if err := client.Call(initCtx, "initialize", initParams, &initResult); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("bắt tay initialize thất bại với MCP server %s: %w", serverName, err)
	}

	var initResp struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(initResult, &initResp); err == nil && initResp.ProtocolVersion != "" {
		client.protocolVersion = initResp.ProtocolVersion
	} else {
		client.protocolVersion = desiredVersion
	}

	// 2. Gửi notifications/initialized
	_ = client.Notify("notifications/initialized", map[string]interface{}{})

	return client, nil
}

// Reconnect khởi động lại tiến trình MCP Server khi kết nối bị gián đoạn
func (c *MCPClient) Reconnect(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("MCP client đã bị đóng hoàn toàn")
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	c.mu.Unlock()

	cmd := exec.CommandContext(ctx, c.command, c.args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("không thể mở lại stdin pipe cho MCP server %s: %w", c.serverName, err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("không thể mở lại stdout pipe cho MCP server %s: %w", c.serverName, err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("không thể khởi chạy lại MCP server %s: %w", c.serverName, err)
	}

	c.mu.Lock()
	c.cmd = cmd
	c.stdin = stdin
	c.stdout = bufio.NewScanner(stdoutPipe)
	c.pending = make(map[int64]chan jsonRPCResponse)
	c.mu.Unlock()

	go c.readLoop()

	version := c.protocolVersion
	if version == "" {
		version = "2024-11-05"
	}

	initParams := map[string]interface{}{
		"protocolVersion": version,
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "dezuxk-agent",
			"version": "1.0.0",
		},
	}

	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var initResult json.RawMessage
	if err := c.Call(initCtx, "initialize", initParams, &initResult); err != nil {
		return fmt.Errorf("bắt tay initialize lại thất bại với MCP server %s: %w", c.serverName, err)
	}

	_ = c.Notify("notifications/initialized", map[string]interface{}{})
	return nil
}

func (c *MCPClient) readLoop() {
	buf := make([]byte, 1024*1024)
	c.stdout.Buffer(buf, 1024*1024)

	for c.stdout.Scan() {
		line := c.stdout.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}

		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()

		if ok {
			ch <- resp
		}
	}

	c.mu.Lock()
	c.closed = true
	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
	c.mu.Unlock()
}

// Call gọi phương thức JSON-RPC có trả về kết quả
func (c *MCPClient) Call(ctx context.Context, method string, params interface{}, result interface{}) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	id := atomic.AddInt64(&c.reqID, 1)

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return err
	}
	reqBytes = append(reqBytes, '\n')

	respChan := make(chan jsonRPCResponse, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("MCP client đã bị đóng")
	}
	c.pending[id] = respChan
	_, writeErr := c.stdin.Write(reqBytes)
	c.mu.Unlock()

	if writeErr != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("lỗi ghi vào MCP stdin: %w", writeErr)
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case resp, ok := <-respChan:
		if !ok {
			return fmt.Errorf("kết nối MCP server bị đóng bất ngờ")
		}
		if resp.Error != nil {
			return fmt.Errorf("MCP lỗi (code %d): %s", resp.Error.Code, resp.Error.Message)
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
}

// Notify gửi thông báo 1 chiều không chờ phản hồi
func (c *MCPClient) Notify(method string, params interface{}) error {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return err
	}
	reqBytes = append(reqBytes, '\n')

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("MCP client đã đóng")
	}
	_, err = c.stdin.Write(reqBytes)
	return err
}

func (c *MCPClient) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()

	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	return nil
}

// -------------------------------------------------------------
// MCP Tools Schema & Adapter to domain.AgentTool
// -------------------------------------------------------------

type MCPToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type mcpListToolsResult struct {
	Tools []MCPToolDefinition `json:"tools"`
}

// ListTools lấy danh sách các công cụ được cung cấp bởi MCP Server
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPToolDefinition, error) {
	var res mcpListToolsResult
	if err := c.Call(ctx, "tools/list", map[string]interface{}{}, &res); err != nil {
		return nil, err
	}
	return res.Tools, nil
}

// CallTool thực thi một công cụ cụ thể trên MCP Server
func (c *MCPClient) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (string, error) {
	params := map[string]interface{}{
		"name":      name,
		"arguments": arguments,
	}

	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content"`
		IsError bool `json:"isError,omitempty"`
	}

	if err := c.Call(ctx, "tools/call", params, &res); err != nil {
		return "", err
	}

	var sb stringsBuilder
	for _, item := range res.Content {
		if item.Text != "" {
			sb.WriteString(item.Text)
			sb.WriteString("\n")
		}
	}

	out := sb.String()
	if out == "" {
		out = "[Công cụ MCP thực thi thành công và không trả về text]"
	}
	if res.IsError {
		return out, fmt.Errorf("công cụ MCP %s báo lỗi: %s", name, out)
	}
	return out, nil
}

type stringsBuilder struct {
	buf []byte
}

func (b *stringsBuilder) WriteString(s string) {
	b.buf = append(b.buf, s...)
}
func (b *stringsBuilder) String() string {
	return string(b.buf)
}

// InferMCPToolPermission phân loại quyền hạn của công cụ MCP theo quy tắc an ninh
// Tuyệt đối không bao giờ mặc định công cụ MCP bên ngoài là Safe
func InferMCPToolPermission(toolName string, customPerm domain.PermissionLevel) domain.PermissionLevel {
	if customPerm != "" {
		return customPerm
	}
	lower := strings.ToLower(toolName)
	// Chỉ các thao tác đọc / tra cứu thuần túy mới được coi là Safe
	if strings.HasPrefix(lower, "read_") || strings.HasPrefix(lower, "get_") ||
		strings.HasPrefix(lower, "list_") || strings.HasPrefix(lower, "search_") ||
		strings.HasPrefix(lower, "fetch_") || strings.HasPrefix(lower, "query_") {
		return domain.PermissionSafe
	}
	if strings.Contains(lower, "exec") || strings.Contains(lower, "run") ||
		strings.Contains(lower, "shell") || strings.Contains(lower, "cmd") ||
		strings.Contains(lower, "bash") || strings.Contains(lower, "terminal") {
		return domain.PermissionExecute
	}
	if strings.Contains(lower, "write") || strings.Contains(lower, "update") ||
		strings.Contains(lower, "delete") || strings.Contains(lower, "remove") ||
		strings.Contains(lower, "drop") || strings.Contains(lower, "create") ||
		strings.Contains(lower, "patch") {
		return domain.PermissionDestructive
	}
	if strings.Contains(lower, "network") || strings.Contains(lower, "http") ||
		strings.Contains(lower, "request") || strings.Contains(lower, "send") {
		return domain.PermissionNetwork
	}
	// Mặc định công cụ MCP chưa rõ danh tính phải yêu cầu phê duyệt
	return domain.PermissionRequiresApproval
}

// MCPServerAllowlist quản lý danh sách server MCP được phép kích hoạt
type MCPServerAllowlist struct {
	mu           sync.RWMutex
	allowedNames map[string]bool
}

func NewMCPServerAllowlist(allowed []string) *MCPServerAllowlist {
	m := make(map[string]bool)
	for _, a := range allowed {
		if tr := strings.TrimSpace(a); tr != "" {
			m[tr] = true
		}
	}
	return &MCPServerAllowlist{allowedNames: m}
}

func (al *MCPServerAllowlist) IsAllowed(serverName string) bool {
	if al == nil {
		return true
	}
	al.mu.RLock()
	defer al.mu.RUnlock()
	if len(al.allowedNames) == 0 {
		return true
	}
	return al.allowedNames[serverName]
}

// MCPToolAdapter bọc một công cụ từ MCP Server thành domain.AgentTool
type MCPToolAdapter struct {
	client     *MCPClient
	def        MCPToolDefinition
	permission domain.PermissionLevel
}

func NewMCPToolAdapter(client *MCPClient, def MCPToolDefinition, permission domain.PermissionLevel) *MCPToolAdapter {
	effectivePerm := InferMCPToolPermission(def.Name, permission)
	return &MCPToolAdapter{
		client:     client,
		def:        def,
		permission: effectivePerm,
	}
}

func (a *MCPToolAdapter) Name() string {
	return a.client.serverName + "__" + a.def.Name
}
func (a *MCPToolAdapter) Description() string {
	return fmt.Sprintf("[MCP %s] %s", a.client.serverName, a.def.Description)
}
func (a *MCPToolAdapter) Parameters() json.RawMessage {
	return a.def.InputSchema
}
func (a *MCPToolAdapter) Permission() domain.PermissionLevel {
	return a.permission
}

func (a *MCPToolAdapter) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Kiểm tra Tool Schema Validation trước khi gửi tới server MCP
	if len(a.def.InputSchema) > 0 && string(a.def.InputSchema) != "{}" && string(a.def.InputSchema) != "null" {
		if err := services.ValidateJSONSchema(a.def.InputSchema, argsJSON); err != nil {
			return "", fmt.Errorf("tham số cho công cụ MCP %q vi phạm schema: %w", a.def.Name, err)
		}
	}

	var args map[string]interface{}
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("tham số JSON không hợp lệ cho MCP tool: %w", err)
		}
	}
	if args == nil {
		args = make(map[string]interface{})
	}
	return a.client.CallTool(ctx, a.def.Name, args)
}
