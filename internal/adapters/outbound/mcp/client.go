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
)

// MCPClient kết nối tới MCP Server qua Stdio JSON-RPC 2.0 (Model Context Protocol chuẩn Anthropic)
type MCPClient struct {
	serverName string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Scanner
	mu         sync.Mutex
	reqID      int64
	pending    map[int64]chan jsonRPCResponse
	closed     bool
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
		cmd:        cmd,
		stdin:      stdin,
		stdout:     bufio.NewScanner(stdoutPipe),
		pending:    make(map[int64]chan jsonRPCResponse),
	}

	// Đọc stdout liên tục trong background goroutine
	go client.readLoop()

	// 1. Gửi initialize request
	initParams := map[string]interface{}{
		"protocolVersion": "2024-11-05",
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

	// 2. Gửi notifications/initialized
	_ = client.Notify("notifications/initialized", map[string]interface{}{})

	return client, nil
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

// MCPToolAdapter bọc một công cụ từ MCP Server thành domain.AgentTool
type MCPToolAdapter struct {
	client     *MCPClient
	def        MCPToolDefinition
	permission domain.PermissionLevel
}

func NewMCPToolAdapter(client *MCPClient, def MCPToolDefinition, permission domain.PermissionLevel) *MCPToolAdapter {
	if permission == "" {
		permission = domain.PermissionSafe
	}
	return &MCPToolAdapter{
		client:     client,
		def:        def,
		permission: permission,
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
