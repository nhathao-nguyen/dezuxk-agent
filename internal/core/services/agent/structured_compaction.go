package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

var (
	constraintPrefixRegex = regexp.MustCompile(`(?i)(do not|never|must|ensure|always|strictly|không được|bắt buộc|tuyệt đối|lưu ý|yêu cầu)\s+([^\n\.]+)`)
	filePathRegex         = regexp.MustCompile(`(?i)(?:[a-zA-Z0-9_\-\./\\]+\.(?:go|py|js|ts|json|yaml|yml|md|sql|sh|html|css))`)
	evidenceRefRegex      = regexp.MustCompile(`ref=(ev_[a-zA-Z0-9]+)`)
)

// StructuredTaskSummary lưu giữ trạng thái có cấu trúc của tác vụ (Structured Context Compaction)
type StructuredTaskSummary struct {
	Goal              string   `json:"goal"`
	UserConstraints   []string `json:"user_constraints"`
	Decisions         []string `json:"decisions"`
	FilesInspected    []string `json:"files_inspected"`
	FilesModified     []string `json:"files_modified"`
	ImportantSymbols  []string `json:"important_symbols"`
	Evidence          []string `json:"evidence"`
	CommandsExecuted  []string `json:"commands_executed"`
	TestsRun          []string `json:"tests_run"`
	TestResults       []string `json:"test_results"`
	ErrorsEncountered []string `json:"errors_encountered"`
	AttemptsFailed    []string `json:"attempts_failed"`
	UnresolvedIssues  []string `json:"unresolved_issues"`
	CompletedWork     []string `json:"completed_work"`
	PendingWork       []string `json:"pending_work"`
	CurrentHypotheses []string `json:"current_hypotheses"`
	OpenQuestions     []string `json:"open_questions"`
	NextActions       []string `json:"next_actions"`
	RawEvidenceRefs   []string `json:"raw_evidence_refs"`
}

// ExtractStructuredSummary trích xuất tóm tắt có cấu trúc từ lịch sử hội thoại, tự động hợp nhất nếu đã có summary cũ
func ExtractStructuredSummary(messages []domain.OpenAIMessage) StructuredTaskSummary {
	var baseSummary StructuredTaskSummary
	hasBase := false
	var remainingMessages []domain.OpenAIMessage

	for _, msg := range messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "Auto-Compacted") {
			parsed, ok := ParseStructuredSummaryFromMarkdown(msg.Content)
			if ok {
				if !hasBase {
					baseSummary = parsed
					hasBase = true
				} else {
					baseSummary = MergeStructuredSummaries(baseSummary, parsed)
				}
				continue
			}
		}
		remainingMessages = append(remainingMessages, msg)
	}

	newSummary := extractRawSlice(remainingMessages)
	if hasBase {
		return MergeStructuredSummaries(baseSummary, newSummary)
	}
	return newSummary
}

func extractRawSlice(messages []domain.OpenAIMessage) StructuredTaskSummary {
	summary := StructuredTaskSummary{
		UserConstraints:   make([]string, 0),
		Decisions:         make([]string, 0),
		FilesInspected:    make([]string, 0),
		FilesModified:     make([]string, 0),
		ImportantSymbols:  make([]string, 0),
		Evidence:          make([]string, 0),
		CommandsExecuted:  make([]string, 0),
		TestsRun:          make([]string, 0),
		TestResults:       make([]string, 0),
		ErrorsEncountered: make([]string, 0),
		AttemptsFailed:    make([]string, 0),
		UnresolvedIssues:  make([]string, 0),
		CompletedWork:     make([]string, 0),
		PendingWork:       make([]string, 0),
		CurrentHypotheses: make([]string, 0),
		OpenQuestions:     make([]string, 0),
		NextActions:       make([]string, 0),
		RawEvidenceRefs:   make([]string, 0),
	}

	seenConstraints := make(map[string]struct{})
	seenInspected := make(map[string]struct{})
	seenModified := make(map[string]struct{})
	seenCommands := make(map[string]struct{})
	seenErrors := make(map[string]struct{})
	seenRefs := make(map[string]struct{})

	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)

		// 1. Trích xuất User Constraints từ các tin nhắn của người dùng
		if msg.Role == "user" {
			if summary.Goal == "" && !strings.HasPrefix(content, "[") {
				summary.Goal = content
			}
			matches := constraintPrefixRegex.FindAllString(content, -1)
			for _, m := range matches {
				m = strings.TrimSpace(m)
				if _, ok := seenConstraints[m]; !ok && len(m) > 10 {
					seenConstraints[m] = struct{}{}
					summary.UserConstraints = append(summary.UserConstraints, m)
				}
			}
		}

		// 2. Trích xuất từ các Tool Calls
		for _, tc := range msg.ToolCalls {
			fn := tc.Function.Name
			args := tc.Function.Arguments

			fileMatches := filePathRegex.FindAllString(args, -1)

			switch fn {
			case "read_file", "view_file":
				for _, fp := range fileMatches {
					if _, ok := seenInspected[fp]; !ok {
						seenInspected[fp] = struct{}{}
						summary.FilesInspected = append(summary.FilesInspected, fp)
					}
				}
			case "replace_file_content", "write_file", "write_to_file", "edit_file":
				for _, fp := range fileMatches {
					if _, ok := seenModified[fp]; !ok {
						seenModified[fp] = struct{}{}
						summary.FilesModified = append(summary.FilesModified, fp)
					}
				}
			case "run_command", "shell_execute", "execute_command":
				cmdStr := fmt.Sprintf("%s(%s)", fn, args)
				if len(cmdStr) > 150 {
					cmdStr = cmdStr[:150] + "..."
				}
				if _, ok := seenCommands[cmdStr]; !ok {
					seenCommands[cmdStr] = struct{}{}
					summary.CommandsExecuted = append(summary.CommandsExecuted, cmdStr)
				}
				if strings.Contains(args, "test") || strings.Contains(args, "vet") || strings.Contains(args, "build") {
					summary.TestsRun = append(summary.TestsRun, args)
				}
			}
		}

		// 3. Trích xuất từ Tool Results & Assistant reasoning
		if msg.Role == "tool" || msg.Role == "assistant" {
			// Tìm Evidence Refs
			refMatches := evidenceRefRegex.FindAllStringSubmatch(content, -1)
			for _, rm := range refMatches {
				if len(rm) > 1 {
					refID := rm[1]
					if _, ok := seenRefs[refID]; !ok {
						seenRefs[refID] = struct{}{}
						summary.RawEvidenceRefs = append(summary.RawEvidenceRefs, refID)
					}
				}
			}

			// Tìm kết quả test
			lowerContent := strings.ToLower(content)
			if strings.Contains(lowerContent, "pass:") || strings.Contains(lowerContent, "ok ") || strings.Contains(lowerContent, "exit code: 0") {
				if strings.Contains(lowerContent, "test") {
					resSnippet := content
					if len(resSnippet) > 120 {
						resSnippet = resSnippet[:120] + "..."
					}
					summary.TestResults = append(summary.TestResults, resSnippet)
				}
			}

			// Tìm lỗi
			if strings.Contains(lowerContent, "error:") ||
				strings.Contains(lowerContent, "failed:") ||
				strings.Contains(lowerContent, "fail:") ||
				strings.Contains(lowerContent, "panic:") {
				lines := strings.Split(content, "\n")
				for _, l := range lines {
					ll := strings.ToLower(l)
					if strings.Contains(ll, "error") || strings.Contains(ll, "fail") || strings.Contains(ll, "panic") {
						errStr := strings.TrimSpace(l)
						if len(errStr) > 160 {
							errStr = errStr[:160] + "..."
						}
						if _, ok := seenErrors[errStr]; !ok && len(errStr) > 5 {
							seenErrors[errStr] = struct{}{}
							summary.ErrorsEncountered = append(summary.ErrorsEncountered, errStr)
							summary.Evidence = append(summary.Evidence, errStr)
						}
					}
				}
			}
		}
	}

	return summary
}

// MergeStructuredSummaries hợp nhất 2 bản tóm tắt có cấu trúc mà không làm mất thông tin (Hierarchical State Merging)
func MergeStructuredSummaries(base, incoming StructuredTaskSummary) StructuredTaskSummary {
	res := base

	if incoming.Goal != "" {
		if res.Goal == "" {
			res.Goal = incoming.Goal
		}
	}

	dedupAppend := func(target []string, additions []string) []string {
		seen := make(map[string]struct{}, len(target))
		for _, item := range target {
			seen[strings.TrimSpace(item)] = struct{}{}
		}
		for _, item := range additions {
			clean := strings.TrimSpace(item)
			if clean == "" {
				continue
			}
			if _, exists := seen[clean]; !exists {
				seen[clean] = struct{}{}
				target = append(target, clean)
			}
		}
		return target
	}

	res.UserConstraints = dedupAppend(res.UserConstraints, incoming.UserConstraints)
	res.Decisions = dedupAppend(res.Decisions, incoming.Decisions)
	res.FilesInspected = dedupAppend(res.FilesInspected, incoming.FilesInspected)
	res.FilesModified = dedupAppend(res.FilesModified, incoming.FilesModified)
	res.ImportantSymbols = dedupAppend(res.ImportantSymbols, incoming.ImportantSymbols)
	res.Evidence = dedupAppend(res.Evidence, incoming.Evidence)
	res.CommandsExecuted = dedupAppend(res.CommandsExecuted, incoming.CommandsExecuted)
	res.TestsRun = dedupAppend(res.TestsRun, incoming.TestsRun)
	res.TestResults = dedupAppend(res.TestResults, incoming.TestResults)
	res.ErrorsEncountered = dedupAppend(res.ErrorsEncountered, incoming.ErrorsEncountered)
	res.AttemptsFailed = dedupAppend(res.AttemptsFailed, incoming.AttemptsFailed)
	res.UnresolvedIssues = dedupAppend(res.UnresolvedIssues, incoming.UnresolvedIssues)
	res.CompletedWork = dedupAppend(res.CompletedWork, incoming.CompletedWork)
	res.PendingWork = dedupAppend(res.PendingWork, incoming.PendingWork)
	res.CurrentHypotheses = dedupAppend(res.CurrentHypotheses, incoming.CurrentHypotheses)
	res.OpenQuestions = dedupAppend(res.OpenQuestions, incoming.OpenQuestions)
	res.NextActions = dedupAppend(res.NextActions, incoming.NextActions)
	res.RawEvidenceRefs = dedupAppend(res.RawEvidenceRefs, incoming.RawEvidenceRefs)

	return res
}

// ParseStructuredSummaryFromMarkdown phục hồi StructuredTaskSummary từ văn bản markdown đã nén
func ParseStructuredSummaryFromMarkdown(content string) (StructuredTaskSummary, bool) {
	if !strings.Contains(content, "Auto-Compacted") {
		return StructuredTaskSummary{}, false
	}

	var summary StructuredTaskSummary
	lines := strings.Split(content, "\n")
	var currentSection string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "**Current Task / Goal:**") {
			summary.Goal = strings.TrimSpace(strings.TrimPrefix(trimmed, "**Current Task / Goal:**"))
			continue
		}

		if strings.HasPrefix(trimmed, "### ") {
			currentSection = strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))
			continue
		}

		if strings.HasPrefix(trimmed, "- ") {
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			item = strings.Trim(item, "`")
			if item == "" {
				continue
			}

			switch {
			case strings.Contains(currentSection, "User Constraints"):
				summary.UserConstraints = append(summary.UserConstraints, item)
			case strings.Contains(currentSection, "Decisions"):
				summary.Decisions = append(summary.Decisions, item)
			case strings.Contains(currentSection, "Files Modified"):
				summary.FilesModified = append(summary.FilesModified, item)
			case strings.Contains(currentSection, "Files Inspected"):
				summary.FilesInspected = append(summary.FilesInspected, item)
			case strings.Contains(currentSection, "Critical Evidence"):
				summary.Evidence = append(summary.Evidence, item)
			case strings.Contains(currentSection, "Commands Executed"):
				summary.CommandsExecuted = append(summary.CommandsExecuted, item)
			case strings.Contains(currentSection, "Tests Run"):
				summary.TestsRun = append(summary.TestsRun, item)
			case strings.Contains(currentSection, "Raw Evidence References"):
				summary.RawEvidenceRefs = append(summary.RawEvidenceRefs, item)
			}
		}
	}

	return summary, true
}

// ToMarkdown định dạng StructuredTaskSummary thành văn bản Markdown chuẩn
func (s StructuredTaskSummary) ToMarkdown() string {
	var sb strings.Builder
	sb.WriteString("## Structured Conversation State & Context Summary (Auto-Compacted)\n\n")

	if s.Goal != "" {
		sb.WriteString(fmt.Sprintf("**Current Task / Goal:** %s\n\n", s.Goal))
	}

	if len(s.UserConstraints) > 0 {
		sb.WriteString("### ⚠️ User Constraints (MUST PRESERVE)\n")
		for _, c := range s.UserConstraints {
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
		sb.WriteString("\n")
	}

	if len(s.Decisions) > 0 {
		sb.WriteString("### Decisions Made\n")
		for _, d := range s.Decisions {
			sb.WriteString(fmt.Sprintf("- %s\n", d))
		}
		sb.WriteString("\n")
	}

	if len(s.FilesModified) > 0 {
		sb.WriteString("### Files Modified\n")
		for _, f := range s.FilesModified {
			sb.WriteString(fmt.Sprintf("- `%s`\n", f))
		}
		sb.WriteString("\n")
	}

	if len(s.FilesInspected) > 0 {
		sb.WriteString("### Files Inspected\n")
		for _, f := range s.FilesInspected {
			sb.WriteString(fmt.Sprintf("- `%s`\n", f))
		}
		sb.WriteString("\n")
	}

	if len(s.Evidence) > 0 {
		sb.WriteString("### Critical Evidence & Errors\n")
		for _, e := range s.Evidence {
			sb.WriteString(fmt.Sprintf("- %s\n", e))
		}
		sb.WriteString("\n")
	}

	if len(s.CommandsExecuted) > 0 {
		sb.WriteString("### Commands Executed\n")
		for _, cmd := range s.CommandsExecuted {
			sb.WriteString(fmt.Sprintf("- `%s`\n", cmd))
		}
		sb.WriteString("\n")
	}

	if len(s.TestsRun) > 0 {
		sb.WriteString("### Tests Run\n")
		for _, t := range s.TestsRun {
			sb.WriteString(fmt.Sprintf("- `%s`\n", t))
		}
		sb.WriteString("\n")
	}

	if len(s.RawEvidenceRefs) > 0 {
		sb.WriteString("### Raw Evidence References (Persisted in EvidenceStore)\n")
		for _, ref := range s.RawEvidenceRefs {
			sb.WriteString(fmt.Sprintf("- `%s`\n", ref))
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}

// GenerateStructuredCompaction tổng hợp và nén an toàn lịch sử hội thoại cũ
func GenerateStructuredCompaction(
	ctx context.Context,
	messages []domain.OpenAIMessage,
	chatUseCase ports.ChatUseCase,
	model string,
) domain.OpenAIMessage {
	// 1. Trích xuất và hợp nhất tất cả các sự kiện có cấu trúc (Deterministic Extraction & Merge)
	extracted := ExtractStructuredSummary(messages)

	// 2. Định dạng thành Structured Context Compaction Markdown
	summaryContent := extracted.ToMarkdown()

	if strings.TrimSpace(summaryContent) == "" {
		// Fallback tất định, không bao giờ để rỗng
		summaryContent = "## Structured Conversation State & Context Summary (Auto-Compacted)\n\n**Current Task / Goal:** Preserved execution context."
	}

	return domain.OpenAIMessage{
		Role:    "system",
		Content: summaryContent,
	}
}
