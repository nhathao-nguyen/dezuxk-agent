package mcp

import (
	"context"
	"encoding/json"
	"strings"
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

func TestInferMCPToolPermission(t *testing.T) {
	tests := []struct {
		name       string
		customPerm domain.PermissionLevel
		expected   domain.PermissionLevel
	}{
		{"read_table", "", domain.PermissionSafe},
		{"get_user", "", domain.PermissionSafe},
		{"list_buckets", "", domain.PermissionSafe},
		{"exec_bash", "", domain.PermissionExecute},
		{"run_script", "", domain.PermissionExecute},
		{"delete_record", "", domain.PermissionDestructive},
		{"update_config", "", domain.PermissionDestructive},
		{"send_email", "", domain.PermissionNetwork},
		{"http_post", "", domain.PermissionNetwork},
		{"unknown_mystery_tool", "", domain.PermissionRequiresApproval},
		{"custom_override", domain.PermissionSafe, domain.PermissionSafe},
	}

	for _, tt := range tests {
		got := InferMCPToolPermission(tt.name, tt.customPerm)
		if got != tt.expected {
			t.Errorf("InferMCPToolPermission(%q, %q) = %q, expected %q", tt.name, tt.customPerm, got, tt.expected)
		}
	}
}

func TestMCPServerAllowlist(t *testing.T) {
	al := NewMCPServerAllowlist([]string{"postgres", "filesystem"})

	if !al.IsAllowed("postgres") {
		t.Errorf("postgres should be allowed")
	}
	if !al.IsAllowed("filesystem") {
		t.Errorf("filesystem should be allowed")
	}
	if al.IsAllowed("untrusted_server") {
		t.Errorf("untrusted_server should be blocked")
	}
}

func TestMCPToolSchemaValidation(t *testing.T) {
	client := &MCPClient{serverName: "test"}
	def := MCPToolDefinition{
		Name:        "get_weather",
		Description: "Get weather for location",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["city"],
			"properties": {
				"city": {"type": "string"}
			}
		}`),
	}

	adapter := NewMCPToolAdapter(client, def, "")

	// Invalid arguments missing required field "city"
	_, err := adapter.Execute(context.Background(), `{"days": 5}`)
	if err == nil || !strings.Contains(err.Error(), "vi phạm schema") {
		t.Fatalf("expected schema validation error, got: %v", err)
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
