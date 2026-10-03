package tools

import (
	"context"
	"os"
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestFilesystemTools(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dezuxk-tools-test-*")
	if err != nil {
		t.Fatalf("Không thể tạo temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	reg := NewToolRegistry()
	RegisterDefaultTools(reg, tempDir)

	ctx := context.Background()

	// 1. Test WriteFileTool
	writeRes, err := reg.Execute(ctx, "write_file", `{"path": "hello.txt", "content": "line 1\nline 2\nline 3\n"}`)
	if err != nil {
		t.Fatalf("Lỗi write_file: %v", err)
	}
	if !strings.Contains(writeRes, "Đã ghi thành công") {
		t.Errorf("Kết quả write_file bất thường: %s", writeRes)
	}

	// 2. Test ReadFileTool
	readRes, err := reg.Execute(ctx, "read_file", `{"path": "hello.txt", "start_line": 2, "end_line": 2}`)
	if err != nil {
		t.Fatalf("Lỗi read_file: %v", err)
	}
	if !strings.Contains(readRes, "2: line 2") {
		t.Errorf("Kết quả read_file không chứa dòng 2: %s", readRes)
	}
	if strings.Contains(readRes, "1: line 1") {
		t.Errorf("read_file không lọc start_line: %s", readRes)
	}

	// 3. Test ReplaceFileContentTool
	replaceRes, err := reg.Execute(ctx, "replace_file_content", `{"path": "hello.txt", "target_content": "line 2", "replacement_content": "line TWO modified"}`)
	if err != nil {
		t.Fatalf("Lỗi replace_file_content: %v", err)
	}
	if !strings.Contains(replaceRes, "Đã thay thế thành công") {
		t.Errorf("Kết quả replace bất thường: %s", replaceRes)
	}

	// Verify modified content
	verifyRead, _ := reg.Execute(ctx, "read_file", `{"path": "hello.txt"}`)
	if !strings.Contains(verifyRead, "line TWO modified") {
		t.Errorf("File chưa được sửa đổi: %s", verifyRead)
	}

	// 4. Test ListDirectoryTool
	listRes, err := reg.Execute(ctx, "list_directory", `{"path": "."}`)
	if err != nil {
		t.Fatalf("Lỗi list_directory: %v", err)
	}
	if !strings.Contains(listRes, "hello.txt") {
		t.Errorf("list_directory không tìm thấy hello.txt: %s", listRes)
	}
}

func TestRunCommandTool(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dezuxk-shell-test-*")
	if err != nil {
		t.Fatalf("Không thể tạo temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cmdTool := NewRunCommandTool(tempDir)
	ctx := context.Background()

	// Chạy lệnh in chuỗi
	out, err := cmdTool.Execute(ctx, `{"command": "echo DEZUXK_TEST_OK"}`)
	if err != nil {
		t.Fatalf("Lỗi chạy run_command: %v", err)
	}
	if !strings.Contains(out, "DEZUXK_TEST_OK") {
		t.Errorf("run_command không in ra chuỗi mong muốn: %s", out)
	}
	if !strings.Contains(out, "Exit Code: 0") {
		t.Errorf("run_command không có Exit Code 0: %s", out)
	}
}

func TestToolRegistryToOpenAITools(t *testing.T) {
	reg := NewToolRegistry()
	RegisterDefaultTools(reg, ".")

	openAITools := reg.ToOpenAITools()
	if len(openAITools) < 5 {
		t.Fatalf("Kỳ vọng ít nhất 5 công cụ, nhận được: %d", len(openAITools))
	}

	var foundRead bool
	for _, ot := range openAITools {
		if ot.Function.Name == "read_file" {
			foundRead = true
			if len(ot.Function.Parameters) == 0 {
				t.Errorf("read_file thiếu parameters")
			}
		}
	}
	if !foundRead {
		t.Errorf("Không tìm thấy read_file trong OpenAI tools")
	}
}

type mockMemoryService struct {
	core    domain.CoreMemory
	storage map[string]string
}

func (m *mockMemoryService) GetCoreMemory() *domain.CoreMemory {
	return &m.core
}
func (m *mockMemoryService) GetCoreMemoryForContext(ctx context.Context) *domain.CoreMemory {
	return &m.core
}
func (m *mockMemoryService) UpdateCoreMemory(update func(core *domain.CoreMemory)) {
	update(&m.core)
}
func (m *mockMemoryService) UpdateCoreMemoryForContext(ctx context.Context, update func(core *domain.CoreMemory)) {
	update(&m.core)
}
func (m *mockMemoryService) StoreArchival(ctx context.Context, key, content string, tags []string) error {
	m.storage[key] = content
	return nil
}
func (m *mockMemoryService) SearchArchival(ctx context.Context, query string, topK int) ([]domain.MemorySearchResult, error) {
	var res []domain.MemorySearchResult
	for k, v := range m.storage {
		if strings.Contains(v, query) {
			res = append(res, domain.MemorySearchResult{
				Item:      domain.ArchivalMemoryItem{Key: k, Content: v},
				Score:     1.0,
				MatchType: "hybrid",
			})
		}
	}
	return res, nil
}
func (m *mockMemoryService) CompactConversation(ctx context.Context, messages []domain.OpenAIMessage, threshold int) ([]domain.OpenAIMessage, error) {
	return messages, nil
}

func TestMemoryTools(t *testing.T) {
	memSvc := &mockMemoryService{storage: make(map[string]string)}
	reg := NewToolRegistry()
	RegisterMemoryTools(reg, memSvc)

	ctx := context.Background()

	// 1. memory_store
	storeOut, err := reg.Execute(ctx, "memory_store", `{"key": "test_fact", "content": "Antigravity is fast"}`)
	if err != nil {
		t.Fatalf("Lỗi memory_store: %v", err)
	}
	if !strings.Contains(storeOut, "Đã lưu thành công") {
		t.Errorf("memory_store output bất thường: %s", storeOut)
	}

	// 2. memory_search
	searchOut, err := reg.Execute(ctx, "memory_search", `{"query": "Antigravity"}`)
	if err != nil {
		t.Fatalf("Lỗi memory_search: %v", err)
	}
	if !strings.Contains(searchOut, "test_fact") {
		t.Errorf("memory_search không tìm thấy test_fact: %s", searchOut)
	}

	// 3. memory_update_core
	updateOut, err := reg.Execute(ctx, "memory_update_core", `{"target": "scratchpad", "content": "Working on agent"}`)
	if err != nil {
		t.Fatalf("Lỗi memory_update_core: %v", err)
	}
	if !strings.Contains(updateOut, "Đã cập nhật thành công") {
		t.Errorf("memory_update_core output bất thường: %s", updateOut)
	}
	if memSvc.GetCoreMemory().Scratchpad != "Working on agent" {
		t.Errorf("Scratchpad chưa được cập nhật trong core memory")
	}
}

type mockSubagentSupervisor struct {
	invokedRole domain.SubagentRole
}

func (m *mockSubagentSupervisor) InvokeSubagent(ctx context.Context, task domain.SubagentTask) (*domain.SubagentResult, error) {
	m.invokedRole = task.Role
	return &domain.SubagentResult{
		Role:       task.Role,
		Prompt:     task.Prompt,
		Summary:    "Mock research complete: everything is healthy",
		StepsCount: 2,
		Success:    true,
	}, nil
}
func (m *mockSubagentSupervisor) RegisterRole(descriptor domain.SubagentDescriptor) {}
func (m *mockSubagentSupervisor) GetRoleDescriptor(role domain.SubagentRole) (*domain.SubagentDescriptor, bool) {
	return nil, false
}
func (m *mockSubagentSupervisor) ListRoles() []domain.SubagentDescriptor { return nil }

func TestSubagentTool(t *testing.T) {
	reg := NewToolRegistry()
	mockSup := &mockSubagentSupervisor{}
	RegisterSubagentTool(reg, mockSup)

	tool, ok := reg.GetTool("invoke_subagent")
	if !ok {
		t.Fatalf("Không tìm thấy công cụ invoke_subagent")
	}
	if tool.Permission() != domain.PermissionSafe {
		t.Errorf("PermissionLevel của invoke_subagent phải là safe")
	}

	ctx := context.Background()
	res, err := reg.Execute(ctx, "invoke_subagent", `{"role": "researcher", "prompt": "survey code"}`)
	if err != nil {
		t.Fatalf("Lỗi thực thi invoke_subagent: %v", err)
	}

	if !strings.Contains(res, "Mock research complete") {
		t.Errorf("Kết quả trả về không khớp kỳ vọng: %s", res)
	}
	if mockSup.invokedRole != domain.RoleResearcher {
		t.Errorf("Supervisor không nhận đúng vai trò researcher: %s", mockSup.invokedRole)
	}
}

func TestBrowserToolsRegistration(t *testing.T) {
	tempDir := t.TempDir()
	reg := NewToolRegistry()
	RegisterBrowserTools(reg, 9222, tempDir)

	expectedPermissions := map[string]domain.PermissionLevel{
		"browser_navigate":   domain.PermissionSafe,
		"browser_evaluate":   domain.PermissionRequiresApproval,
		"browser_screenshot": domain.PermissionSafe,
	}

	for name, expectedPerm := range expectedPermissions {
		tool, ok := reg.GetTool(name)
		if !ok {
			t.Errorf("Thiếu công cụ duyệt web: %s", name)
		}
		if tool.Permission() != expectedPerm {
			t.Errorf("PermissionLevel của %s phải là %s, nhận được: %s", name, expectedPerm, tool.Permission())
		}
	}
}

func TestPathSandboxing(t *testing.T) {
	tempDir := t.TempDir()
	reg := NewToolRegistry()
	RegisterDefaultTools(reg, tempDir)

	ctx := context.Background()

	// 1. Thử ghi file vượt ra ngoài thư mục tempDir
	_, err := reg.Execute(ctx, "write_file", `{"path": "../malicious.txt", "content": "pwned"}`)
	if err == nil {
		t.Fatalf("Kỳ vọng lỗi Path Sandboxing khi ghi file ra ngoài workspace, nhưng không có lỗi")
	}
	if !strings.Contains(err.Error(), "Path Sandboxing") {
		t.Errorf("Lỗi trả về không chứa thông điệp Path Sandboxing: %v", err)
	}

	// 2. Thử đọc file vượt ra ngoài thư mục tempDir
	_, err = reg.Execute(ctx, "read_file", `{"path": "../../etc/passwd"}`)
	if err == nil {
		t.Fatalf("Kỳ vọng lỗi Path Sandboxing khi đọc file ngoài workspace, nhưng không có lỗi")
	}
	if !strings.Contains(err.Error(), "Path Sandboxing") {
		t.Errorf("Lỗi trả về không chứa thông điệp Path Sandboxing: %v", err)
	}
}
