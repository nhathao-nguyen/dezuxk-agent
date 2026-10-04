package agent_test

import (
	"context"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services/agent"
)

func TestStructuredCompaction_PreservesCriticalFacts(t *testing.T) {
	messages := []domain.OpenAIMessage{
		{
			Role:    "system",
			Content: "You are an autonomous engineering agent.",
		},
		{
			Role:    "user",
			Content: "Fix race condition in distributed lock. Must ensure Redis lease renewal is strictly monotonic. Do not delete existing migrations.",
		},
		{
			Role:    "assistant",
			Content: "Inspecting lock implementation in distributed/redis/lock.go",
			ToolCalls: []domain.OpenAIToolCall{
				{
					ID: "call_1",
					Function: domain.OpenAIFunctionCallData{
						Name:      "read_file",
						Arguments: `{"path": "internal/adapters/outbound/distributed/redis/lock.go"}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call_1",
			Content:    "package redis\n\ntype Lock struct {}\nfunc (l *Lock) Acquire() {}",
		},
		{
			Role:    "assistant",
			Content: "Now modifying lock.go to add monotonic lease renewal",
			ToolCalls: []domain.OpenAIToolCall{
				{
					ID: "call_2",
					Function: domain.OpenAIFunctionCallData{
						Name:      "replace_file_content",
						Arguments: `{"path": "internal/adapters/outbound/distributed/redis/lock.go"}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call_2",
			Content:    "Content replaced successfully.",
		},
		{
			Role:    "assistant",
			Content: "Running tests to verify race condition fix",
			ToolCalls: []domain.OpenAIToolCall{
				{
					ID: "call_3",
					Function: domain.OpenAIFunctionCallData{
						Name:      "run_command",
						Arguments: `{"command": "go test -race ./internal/adapters/outbound/distributed/redis/..."}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call_3",
			Content:    "--- FAIL: TestLock_ConcurrentLeaseRenewal (0.12s)\nerror: lease renewal order violated",
		},
	}

	summaryMsg := agent.GenerateStructuredCompaction(context.Background(), messages, nil, "gemini-3.8-flash")
	content := summaryMsg.Content

	// 1. Kiểm tra Goal
	if !strings.Contains(content, "race condition in distributed lock") {
		t.Errorf("Compacted summary lost Goal: %s", content)
	}

	// 2. Kiểm tra Constraints
	if !strings.Contains(content, "User Constraints") {
		t.Errorf("Compacted summary lost User Constraints section: %s", content)
	}
	if !strings.Contains(content, "monotonic") {
		t.Errorf("Compacted summary lost constraint 'monotonic': %s", content)
	}
	if !strings.Contains(content, "existing migrations") {
		t.Errorf("Compacted summary lost constraint 'existing migrations': %s", content)
	}

	// 3. Kiểm tra Files Touched (Inspected & Modified)
	if !strings.Contains(content, "lock.go") {
		t.Errorf("Compacted summary lost file lock.go: %s", content)
	}

	// 4. Kiểm tra Errors & Evidence
	if !strings.Contains(content, "lease renewal order violated") {
		t.Errorf("Compacted summary lost error message: %s", content)
	}

	// 5. Kiểm tra Test Command
	if !strings.Contains(content, "go test -race") {
		t.Errorf("Compacted summary lost test command: %s", content)
	}
}

func TestStructuredCompaction_MultiRoundHierarchicalMerge(t *testing.T) {
	// Round 1: Lượt đầu tiên
	round1Messages := []domain.OpenAIMessage{
		{
			Role:    "system",
			Content: "System prompt",
		},
		{
			Role:    "user",
			Content: "Optimize PostgreSQL query. Never drop table indexes. Must ensure zero downtime.",
		},
		{
			Role:    "assistant",
			Content: "Looking at postgres query plans in storage/postgres/query.go",
			ToolCalls: []domain.OpenAIToolCall{
				{
					ID: "c1",
					Function: domain.OpenAIFunctionCallData{
						Name:      "read_file",
						Arguments: `{"path": "storage/postgres/query.go"}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "c1",
			Content:    "query content",
		},
	}

	summary1 := agent.GenerateStructuredCompaction(context.Background(), round1Messages, nil, "gemini-3.8-flash")

	// Round 2: Lịch sử mới xuất hiện tiếp nối summary1
	round2Messages := []domain.OpenAIMessage{
		summary1,
		{
			Role:    "user",
			Content: "Also strictly maintain backward compatibility with v1 API.",
		},
		{
			Role:    "assistant",
			Content: "Modifying query.go",
			ToolCalls: []domain.OpenAIToolCall{
				{
					ID: "c2",
					Function: domain.OpenAIFunctionCallData{
						Name:      "replace_file_content",
						Arguments: `{"path": "storage/postgres/query.go"}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "c2",
			Content:    "Modified query.go successfully",
		},
		{
			Role:    "assistant",
			Content: "Testing query",
			ToolCalls: []domain.OpenAIToolCall{
				{
					ID: "c3",
					Function: domain.OpenAIFunctionCallData{
						Name:      "run_command",
						Arguments: `{"command": "go test ./storage/postgres/..."}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "c3",
			Content:    "ok storage/postgres 0.05s",
		},
	}

	summary2 := agent.GenerateStructuredCompaction(context.Background(), round2Messages, nil, "gemini-3.8-flash")
	content2 := summary2.Content

	// Assert: Cả constraints từ Round 1 VÀ Round 2 đều phải còn nguyên vẹn trong summary2!
	if !strings.Contains(content2, "table indexes") {
		t.Errorf("Round 2 lost initial constraint 'table indexes':\n%s", content2)
	}
	if !strings.Contains(content2, "zero downtime") {
		t.Errorf("Round 2 lost initial constraint 'zero downtime':\n%s", content2)
	}
	if !strings.Contains(content2, "backward compatibility") {
		t.Errorf("Round 2 lost new constraint 'backward compatibility':\n%s", content2)
	}
	if !strings.Contains(content2, "query.go") {
		t.Errorf("Round 2 lost files inspected/modified:\n%s", content2)
	}
	if !strings.Contains(content2, "go test") {
		t.Errorf("Round 2 lost test command:\n%s", content2)
	}
}

func TestStructuredCompaction_ToolPairingNeverBroken(t *testing.T) {
	memRepo := session.NewMemoryMemoryRepository()
	memMgr := agent.NewMemoryManager(memRepo, nil, "gemini-3.8-flash", domain.CoreMemory{})

	// Chuỗi gồm 12 tin nhắn, trong đó ở giữa có cụm assistant tool_calls + 2 tool results
	messages := []domain.OpenAIMessage{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Task initiation"},
		{Role: "assistant", Content: "Plan"},
		{Role: "user", Content: "Proceed"},
		{Role: "assistant", Content: "Step 1"},
		{Role: "user", Content: "Step 2"},
		{Role: "assistant", Content: "Calling two tools", ToolCalls: []domain.OpenAIToolCall{
			{ID: "tc_1", Function: domain.OpenAIFunctionCallData{Name: "tool1"}},
			{ID: "tc_2", Function: domain.OpenAIFunctionCallData{Name: "tool2"}},
		}},
		{Role: "tool", ToolCallID: "tc_1", Content: "output 1"},
		{Role: "tool", ToolCallID: "tc_2", Content: "output 2"},
		{Role: "assistant", Content: "All tools finished"},
		{Role: "user", Content: "What is next?"},
		{Role: "assistant", Content: "Next step"},
	}

	// Yêu cầu compact với keepRecent = 4 (trúng tc_2)
	budget := domain.ContextBudget{
		CompactionRequired:    true,
		RecommendedKeepRecent: 4,
	}

	compacted, err := memMgr.CompactWithBudget(context.Background(), messages, budget)
	if err != nil {
		t.Fatalf("CompactWithBudget error: %v", err)
	}

	// Kiểm tra xem assistant có tool_calls và các tool results có bị tách rời không
	for i, msg := range compacted {
		if msg.Role == "tool" {
			if i == 0 {
				t.Fatalf("compacted messages cannot start with tool role at index 0")
			}
			// Tin nhắn trước đó nếu là tool hoặc assistant có tool_calls
			prev := compacted[i-1]
			if prev.Role != "tool" && prev.Role != "assistant" {
				t.Fatalf("tool message at index %d has invalid predecessor role %s", i, prev.Role)
			}
		}
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			// Tin nhắn sau đó phải là tool
			if i+1 >= len(compacted) {
				t.Fatalf("assistant with tool_calls at index %d has no following tool response", i)
			}
			next := compacted[i+1]
			if next.Role != "tool" {
				t.Fatalf("assistant with tool_calls at index %d is followed by role %s, expected 'tool'", i, next.Role)
			}
		}
	}
}
