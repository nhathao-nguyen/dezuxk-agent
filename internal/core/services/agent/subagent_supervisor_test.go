package agent

import (
	"context"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/tools"
	"dezuxk-gateway/internal/core/domain"
)

func TestSubagentSupervisor_RoleRegistration(t *testing.T) {
	reg := tools.NewToolRegistry()
	mockChat := &MockChatUseCase{}
	mockApprove := &MockApprovalProvider{ApproveAll: true}

	supervisor := NewSubagentSupervisor(mockChat, reg, mockApprove, nil)

	roles := supervisor.ListRoles()
	if len(roles) < 4 {
		t.Fatalf("kỳ vọng có ít nhất 4 vai trò mặc định, nhận được %d", len(roles))
	}

	researcher, ok := supervisor.GetRoleDescriptor(domain.RoleResearcher)
	if !ok {
		t.Fatalf("không tìm thấy descriptor cho RoleResearcher")
	}
	if researcher.Role != domain.RoleResearcher {
		t.Errorf("vai trò không khớp: %v", researcher.Role)
	}

	coder, ok := supervisor.GetRoleDescriptor(domain.RoleCoder)
	if !ok {
		t.Fatalf("không tìm thấy descriptor cho RoleCoder")
	}
	if coder.Role != domain.RoleCoder {
		t.Errorf("vai trò không khớp: %v", coder.Role)
	}
}

func TestSubagentSupervisor_ToolFiltering(t *testing.T) {
	tempDir := t.TempDir()
	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	mockChat := &MockChatUseCase{}
	mockApprove := &MockApprovalProvider{ApproveAll: true}
	supervisor := NewSubagentSupervisor(mockChat, reg, mockApprove, nil)

	// Kiểm tra FilteredToolRegistry cho Researcher
	researcherDesc, _ := supervisor.GetRoleDescriptor(domain.RoleResearcher)
	filtered := tools.NewFilteredToolRegistry(reg, researcherDesc.AllowedTools)

	// read_file phải được phép
	if _, ok := filtered.GetTool("read_file"); !ok {
		t.Errorf("kỳ vọng Researcher có quyền gọi read_file")
	}

	// write_file không được phép đối với Researcher
	if _, ok := filtered.GetTool("write_file"); ok {
		t.Errorf("kỳ vọng Researcher KHÔNG có quyền gọi write_file")
	}

	// Thực thi write_file qua filtered registry phải trả về lỗi phân quyền
	_, err := filtered.Execute(context.Background(), "write_file", `{"path":"test.txt","content":"hello"}`)
	if err == nil {
		t.Fatalf("kỳ vọng lỗi khi Researcher cố thực thi write_file")
	}
}

func TestSubagentSupervisor_InvokeSubagent(t *testing.T) {
	tempDir := t.TempDir()
	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "Báo cáo nghiên cứu: Cấu trúc dự án tuân thủ Clean Architecture tuyệt đối.",
						},
					},
				},
			},
		},
	}
	mockApprove := &MockApprovalProvider{ApproveAll: true}
	supervisor := NewSubagentSupervisor(mockChat, reg, mockApprove, nil)

	result, err := supervisor.InvokeSubagent(context.Background(), domain.SubagentTask{
		Role:      domain.RoleResearcher,
		Prompt:    "Hãy khảo sát kiến trúc dự án",
		Workspace: tempDir,
	})
	if err != nil {
		t.Fatalf("InvokeSubagent thất bại: %v", err)
	}

	if !result.Success {
		t.Errorf("kỳ vọng sub-agent hoàn thành thành công")
	}
	if result.Role != domain.RoleResearcher {
		t.Errorf("kỳ vọng vai trò là researcher, nhận được: %s", result.Role)
	}
	if result.Summary == "" {
		t.Errorf("kỳ vọng có kết quả tóm tắt từ sub-agent")
	}
}
