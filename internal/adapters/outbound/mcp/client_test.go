package mcp

import (
	"encoding/json"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestMCPToolAdapter(t *testing.T) {
	client := &MCPClient{serverName: "postgres"}
	def := MCPToolDefinition{
		Name:        "query",
		Description: "Run SQL query on PostgreSQL",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
	}

	adapter := NewMCPToolAdapter(client, def, domain.PermissionDestructive)

	if adapter.Name() != "postgres__query" {
		t.Errorf("Tên tool MCP không đúng format server__name: %s", adapter.Name())
	}
	if adapter.Permission() != domain.PermissionDestructive {
		t.Errorf("Permission không đúng: %s", adapter.Permission())
	}
	if string(adapter.Parameters()) != string(def.InputSchema) {
		t.Errorf("Parameters schema không khớp: %s", string(adapter.Parameters()))
	}
}

func TestJSONRPCSerialization(t *testing.T) {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
		Params:  map[string]interface{}{},
	}

	bytes, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Lỗi mã hóa JSON-RPC: %v", err)
	}

	var parsed jsonRPCRequest
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("Lỗi giải mã JSON-RPC: %v", err)
	}
	if parsed.Method != "tools/list" || parsed.ID != 1 {
		t.Errorf("Dữ liệu giải mã không đúng: %+v", parsed)
	}
}
