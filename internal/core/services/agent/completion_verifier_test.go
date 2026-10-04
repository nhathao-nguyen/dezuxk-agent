package agent

import (
	"context"
	"testing"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

func TestCompletionVerifier_CompleteWhenSatisfied(t *testing.T) {
	verifier := NewCompletionVerifier()

	state := &domain.AgentState{
		TaskID: "task_1",
		Goal:   "Tra cứu tài liệu về Go interfaces",
		Steps: []domain.AgentStep{
			{
				StepIndex: 1,
				ToolCalls: []domain.OpenAIToolCall{
					{
						Function: domain.OpenAIFunctionCallData{Name: "read_file"},
					},
				},
				ToolResults: []string{"interface content"},
			},
		},
	}

	assistantMsg := domain.OpenAIMessage{
		Role:    "assistant",
		Content: "Dưới đây là thông tin chi tiết về Go interfaces theo tài liệu đã tra cứu. Chúc bạn lập trình vui vẻ!",
	}

	res := verifier.Verify(context.Background(), state.Goal, state, assistantMsg, domain.AgentRunOptions{})
	if res.Status != StatusComplete {
		t.Fatalf("kỳ vọng StatusComplete, nhận được %s: %s", res.Status, res.Reason)
	}
}

func TestCompletionVerifier_CutOffMarkdown(t *testing.T) {
	verifier := NewCompletionVerifier()

	state := &domain.AgentState{
		TaskID: "task_2",
		Goal:   "Viết hàm helper",
	}

	assistantMsg := domain.OpenAIMessage{
		Role:    "assistant",
		Content: "Dưới đây là mã nguồn của hàm:\n```go\nfunc Helper() {", // unclosed code block
	}

	res := verifier.Verify(context.Background(), state.Goal, state, assistantMsg, domain.AgentRunOptions{})
	if res.Status != StatusIncomplete {
		t.Fatalf("kỳ vọng StatusIncomplete cho cut-off markdown, nhận được %s", res.Status)
	}
}

func TestCompletionVerifier_TrailingIntent(t *testing.T) {
	verifier := NewCompletionVerifier()

	state := &domain.AgentState{
		TaskID: "task_3",
		Goal:   "Refactor code và test",
	}

	assistantMsg := domain.OpenAIMessage{
		Role:    "assistant",
		Content: "Tôi đã xem xét code và bây giờ tôi sẽ chạy lệnh kiểm thử để xác minh.",
	}

	res := verifier.Verify(context.Background(), state.Goal, state, assistantMsg, domain.AgentRunOptions{})
	if res.Status != StatusNeedsMoreTools {
		t.Fatalf("kỳ vọng StatusNeedsMoreTools cho trailing intent, nhận được %s: %s", res.Status, res.Reason)
	}
}

func TestCompletionVerifier_RequireActionMissing(t *testing.T) {
	verifier := NewCompletionVerifier()

	state := &domain.AgentState{
		TaskID: "task_4",
		Goal:   "Fix bug in auth handler",
		Steps: []domain.AgentStep{
			{
				StepIndex: 1,
				ToolCalls: []domain.OpenAIToolCall{
					{
						Function: domain.OpenAIFunctionCallData{Name: "read_file"},
					},
				},
				ToolResults: []string{"code here"},
			},
		},
	}

	assistantMsg := domain.OpenAIMessage{
		Role:    "assistant",
		Content: "Tôi đã tìm ra nguyên nhân: bạn nên sửa dòng 42 thành if err != nil.",
	}

	opts := domain.AgentRunOptions{RequireAction: true}
	res := verifier.Verify(context.Background(), state.Goal, state, assistantMsg, opts)
	if res.Status != StatusIncomplete || !res.UnappliedFix {
		t.Fatalf("kỳ vọng StatusIncomplete với UnappliedFix=true, nhận được %s", res.Status)
	}
}

func TestCompletionVerifier_UnverifiedChanges(t *testing.T) {
	verifier := NewCompletionVerifier()

	state := &domain.AgentState{
		TaskID: "task_5",
		Goal:   "Fix bug in service and verify",
		Steps: []domain.AgentStep{
			{
				StepIndex: 1,
				ToolCalls: []domain.OpenAIToolCall{
					{
						Function: domain.OpenAIFunctionCallData{Name: "replace_file_content"},
					},
				},
				ToolResults: []string{"Replaced successfully"},
			},
		},
	}

	assistantMsg := domain.OpenAIMessage{
		Role:    "assistant",
		Content: "Tôi đã sửa xong lỗi trong service.",
	}

	res := verifier.Verify(context.Background(), state.Goal, state, assistantMsg, domain.AgentRunOptions{})
	if res.Status != StatusNeedsVerification || !res.Unverified {
		t.Fatalf("kỳ vọng StatusNeedsVerification với Unverified=true, nhận được %s", res.Status)
	}
}

func TestCompletionVerifier_TestFailed(t *testing.T) {
	verifier := NewCompletionVerifier()

	state := &domain.AgentState{
		TaskID: "task_6",
		Goal:   "Fix bug in service and verify",
		Steps: []domain.AgentStep{
			{
				StepIndex: 1,
				ToolCalls: []domain.OpenAIToolCall{
					{
						Function: domain.OpenAIFunctionCallData{Name: "replace_file_content"},
					},
				},
				ToolResults: []string{"Replaced successfully"},
			},
			{
				StepIndex: 2,
				ToolCalls: []domain.OpenAIToolCall{
					{
						Function: domain.OpenAIFunctionCallData{Name: "run_command"},
					},
				},
				ToolResults: []string{"--- FAIL: TestAuth (0.01s)\nexit status 1"},
				Timestamp:   time.Now(),
			},
		},
	}

	assistantMsg := domain.OpenAIMessage{
		Role:    "assistant",
		Content: "Đã sửa xong lỗi.",
	}

	res := verifier.Verify(context.Background(), state.Goal, state, assistantMsg, domain.AgentRunOptions{})
	if res.Status != StatusNeedsVerification {
		t.Fatalf("kỳ vọng StatusNeedsVerification khi test fail, nhận được %s", res.Status)
	}
}

func TestCompletionVerifier_GoalMissingRequirement_ReturnsIncompleteWithRepair(t *testing.T) {
	verifier := NewCompletionVerifier()

	goal := "fix long context + continuation + tests"
	plan := &domain.TaskPlan{
		Goal: goal,
		Steps: []domain.PlanStep{
			{
				ID:                  1,
				Title:               "Sửa long context budget",
				Description:         "Implement context budget manager",
				VerificationCommand: "go test -v ./...",
				Status:              domain.StepStatusPassed,
			},
			{
				ID:                  2,
				Title:               "Chạy tests",
				Description:         "Run all package tests",
				VerificationCommand: "go test -v ./...",
				Status:              domain.StepStatusPassed,
			},
		},
	}

	state := &domain.AgentState{
		TaskID: "task_plan_missing",
		Steps: []domain.AgentStep{
			{
				StepIndex: 1,
				ToolCalls: []domain.OpenAIToolCall{
					{
						Function: domain.OpenAIFunctionCallData{
							Name:      "replace_file_content",
							Arguments: "context_budget.go",
						},
					},
					{
						Function: domain.OpenAIFunctionCallData{
							Name:      "run_command",
							Arguments: "go test -v ./...",
						},
					},
				},
				ToolResults: []string{"success", "PASS\nExit Code: 0"},
			},
		},
	}

	res := verifier.VerifyGoalAndPlan(context.Background(), goal, plan, state, domain.AgentRunOptions{}, 0)

	if res.Status != StatusIncomplete {
		t.Fatalf("kỳ vọng StatusIncomplete khi thiếu requirement 'continuation', nhận được %s", res.Status)
	}

	foundContinuation := false
	for _, req := range res.MissingRequirements {
		if req == "continuation" {
			foundContinuation = true
			break
		}
	}
	if !foundContinuation {
		t.Fatalf("kỳ vọng missing requirements chứa 'continuation', nhận được: %v", res.MissingRequirements)
	}

	if len(res.RepairPlanSteps) == 0 {
		t.Fatalf("kỳ vọng phát sinh RepairPlanSteps cho continuation")
	}
}

func TestCompletionVerifier_TestCommandFailed_ReturnsIncomplete(t *testing.T) {
	verifier := NewCompletionVerifier()

	goal := "fix bug in auth handler and run tests"
	plan := &domain.TaskPlan{
		Goal: goal,
		Steps: []domain.PlanStep{
			{
				ID:                  1,
				Title:               "Fix bug in auth handler",
				Description:         "Edit auth handler",
				VerificationCommand: "go test ./auth",
				Status:              domain.StepStatusPassed,
			},
			{
				ID:                  2,
				Title:               "Run tests",
				Description:         "Verify tests pass",
				VerificationCommand: "go test ./auth",
				Status:              domain.StepStatusPassed,
			},
		},
	}

	state := &domain.AgentState{
		TaskID: "task_test_fail",
		Steps: []domain.AgentStep{
			{
				StepIndex: 1,
				ToolCalls: []domain.OpenAIToolCall{
					{
						Function: domain.OpenAIFunctionCallData{
							Name:      "replace_file_content",
							Arguments: "auth.go",
						},
					},
					{
						Function: domain.OpenAIFunctionCallData{
							Name:      "run_command",
							Arguments: "go test ./auth",
						},
					},
				},
				ToolResults: []string{"updated", "--- FAIL: TestAuth\nexit status 1"},
			},
		},
	}

	res := verifier.VerifyGoalAndPlan(context.Background(), goal, plan, state, domain.AgentRunOptions{}, 0)

	if res.Status != StatusNeedsVerification || !res.TestFailed {
		t.Fatalf("kỳ vọng StatusNeedsVerification với TestFailed=true, nhận được %s, reason: %s", res.Status, res.Reason)
	}
}

func TestCompletionVerifier_AllSatisfied_ReturnsComplete(t *testing.T) {
	verifier := NewCompletionVerifier()

	goal := "fix bug in auth handler and run tests"
	plan := &domain.TaskPlan{
		Goal: goal,
		Steps: []domain.PlanStep{
			{
				ID:                  1,
				Title:               "Fix bug in auth handler",
				Description:         "Edit auth handler",
				VerificationCommand: "go test ./auth",
				Status:              domain.StepStatusPassed,
			},
			{
				ID:                  2,
				Title:               "Run tests",
				Description:         "Verify tests pass",
				VerificationCommand: "go test ./auth",
				Status:              domain.StepStatusPassed,
			},
		},
	}

	state := &domain.AgentState{
		TaskID: "task_satisfied",
		Steps: []domain.AgentStep{
			{
				StepIndex: 1,
				ToolCalls: []domain.OpenAIToolCall{
					{
						Function: domain.OpenAIFunctionCallData{
							Name:      "replace_file_content",
							Arguments: "auth.go",
						},
					},
					{
						Function: domain.OpenAIFunctionCallData{
							Name:      "run_command",
							Arguments: "go test ./auth",
						},
					},
				},
				ToolResults: []string{"updated", "PASS\nok  auth  0.05s\nExit Code: 0"},
			},
		},
	}

	res := verifier.VerifyGoalAndPlan(context.Background(), goal, plan, state, domain.AgentRunOptions{}, 0)

	if res.Status != StatusComplete {
		t.Fatalf("kỳ vọng StatusComplete khi mọi điều kiện đều pass, nhận được %s, reason: %s", res.Status, res.Reason)
	}
}

func TestCompletionVerifier_AntiLoop_HaltsAtMaxRounds(t *testing.T) {
	verifier := NewCompletionVerifier()
	verifier.SetMaxRepairRounds(3)

	goal := "fix bug and verify"
	plan := &domain.TaskPlan{
		Goal:  goal,
		Steps: []domain.PlanStep{},
	}

	// Khi đã thử 3 vòng (repairRound = 3)
	res := verifier.VerifyGoalAndPlan(context.Background(), goal, plan, nil, domain.AgentRunOptions{}, 3)

	if res.Status != StatusBlocked {
		t.Fatalf("kỳ vọng StatusBlocked khi chạm giới hạn anti-loop, nhận được %s", res.Status)
	}
}
