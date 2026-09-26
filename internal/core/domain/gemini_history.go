package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HistoryChoice biểu diễn một phương án trả lời trong một lượt của cây hội thoại Gemini
type HistoryChoice struct {
	ChoiceID string   `json:"choice_id"`
	Content  string   `json:"content"`
	Images   []string `json:"images,omitempty"`
}

// HistoryTurn biểu diễn một lượt hỏi - đáp (User prompt + AI choices)
type HistoryTurn struct {
	TurnID         string          `json:"turn_id"`
	UserPrompt     string          `json:"user_prompt"`
	Choices        []HistoryChoice `json:"choices"`
	ActiveChoiceID string          `json:"active_choice_id"`
}

// ConversationTree biểu diễn toàn bộ cây hội thoại và lịch sử các lượt
type ConversationTree struct {
	ConversationID string        `json:"conversation_id"`
	Title          string        `json:"title,omitempty"`
	Turns          []HistoryTurn `json:"turns"`
	UnmappedFields int           `json:"unmapped_fields,omitempty"`
}

// AccountTierInfo biểu diễn thông tin hạn mức và cấp độ tài khoản từ RPC I4z33b
type AccountTierInfo struct {
	TierCode          string   `json:"tier_code"`          // FREE_USER, GOOGLE_ONE_AI_PREMIUM, WORKSPACE_ENTERPRISE
	ContextWindowSize int64    `json:"context_window_size"` // e.g. 1000000
	Capabilities      []string `json:"capabilities"`
	RawPayload        string   `json:"raw_payload,omitempty"`
}

// BuildHistoryDetailRequest đóng gói payload batchexecute cho RPC cZOhpc
func BuildHistoryDetailRequest(conversationID string) (string, error) {
	convID := strings.TrimSpace(conversationID)
	if convID == "" {
		return "", fmt.Errorf("conversation_id không được để trống")
	}

	innerArgs, err := json.Marshal([]string{convID})
	if err != nil {
		return "", fmt.Errorf("lỗi encode args cZOhpc: %w", err)
	}

	outerEnvelope := [][]any{
		{
			"cZOhpc",
			string(innerArgs),
			nil,
			"generic",
		},
	}
	payloadBytes, err := json.Marshal([]any{outerEnvelope})
	if err != nil {
		return "", fmt.Errorf("lỗi encode envelope cZOhpc: %w", err)
	}

	return string(payloadBytes), nil
}

// ExtractBatchexecuteEnvelopePayload bóc tách payload thực tế từ envelope batchexecute của Google
// Loại bỏ tiền tố XSSI )]}' và unwrap mảng ["wrb.fr", rpcID, innerPayloadStr, ...]
func ExtractBatchexecuteEnvelopePayload(raw string, rpcID string) (any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("payload rỗng")
	}

	// 1. Gọt bỏ tiền tố chống XSSI )]}' nếu có
	if strings.HasPrefix(raw, ")]}'") {
		raw = strings.TrimPrefix(raw, ")]}'")
		raw = strings.TrimSpace(raw)
	}

	// 2. Tìm vị trí mảng JSON đầu tiên
	idx := strings.Index(raw, "[")
	if idx < 0 {
		return nil, fmt.Errorf("không tìm thấy mảng JSON trong phản hồi")
	}
	cleanJSON := raw[idx:]

	var root any
	if err := json.Unmarshal([]byte(cleanJSON), &root); err != nil {
		// Thử đọc theo từng dòng (trường hợp chunked stream với độ dài byte)
		lines := strings.Split(cleanJSON, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				if err := json.Unmarshal([]byte(line), &root); err == nil {
					break
				}
			}
		}
		if root == nil {
			return nil, fmt.Errorf("không thể phân tích JSON envelope: %w", err)
		}
	}

	// 3. Quét đệ quy tìm ["wrb.fr", rpcID, innerPayload, ...]
	var foundPayload any
	var searchWrbFr func(node any)
	searchWrbFr = func(node any) {
		if foundPayload != nil {
			return
		}
		arr, ok := node.([]any)
		if !ok {
			return
		}
		if len(arr) >= 3 {
			if marker, ok := arr[0].(string); ok && marker == "wrb.fr" {
				if rpcID == "" || arr[1] == rpcID {
					foundPayload = arr[2]
					return
				}
			}
		}
		for _, child := range arr {
			searchWrbFr(child)
		}
	}

	searchWrbFr(root)

	// 4. Nếu tìm thấy wrb.fr payload
	if foundPayload != nil {
		if str, ok := foundPayload.(string); ok {
			str = strings.TrimSpace(str)
			if strings.HasPrefix(str, "[") || strings.HasPrefix(str, "{") {
				var inner any
				if err := json.Unmarshal([]byte(str), &inner); err == nil {
					return inner, nil
				}
			}
			return str, nil
		}
		return foundPayload, nil
	}

	// Không có wrapper wrb.fr (trường hợp fixture mock), trả về trực tiếp root
	return root, nil
}

// ParseHistoryDetailResponse phân tích payload trả về từ RPC cZOhpc thành ConversationTree
func ParseHistoryDetailResponse(conversationID string, rawJSON string) (*ConversationTree, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, fmt.Errorf("conversation_id không được để trống")
	}

	parsed, err := ExtractBatchexecuteEnvelopePayload(rawJSON, "cZOhpc")
	if err != nil {
		return nil, fmt.Errorf("cZOhpc JSON không hợp lệ: %w", err)
	}

	tree := &ConversationTree{
		ConversationID: conversationID,
		Turns:          make([]HistoryTurn, 0),
	}

	extractTurnsFromGeneric(parsed, tree)
	return tree, nil
}

// extractTurnsFromGeneric quét đệ quy hoặc theo vị trí mảng để tìm các lượt hội thoại
func extractTurnsFromGeneric(node any, tree *ConversationTree) {
	arr, ok := node.([]any)
	if !ok {
		return
	}

	// Kiểm tra nếu arr đại diện cho 1 turn: [turn_id, [user_text, ...], [ [choice_id, [content]] ]]
	if len(arr) >= 3 {
		turnID, isTurnID := arr[0].(string)
		userArr, isUserArr := arr[1].([]any)
		choicesArr, isChoicesArr := arr[2].([]any)

		if isTurnID && (strings.HasPrefix(turnID, "r_") || strings.HasPrefix(turnID, "rc_") || isUserArr) {
			turn := HistoryTurn{
				TurnID: turnID,
			}
			if isUserArr && len(userArr) > 0 {
				if userText, ok := userArr[0].(string); ok {
					turn.UserPrompt = userText
				}
			}
			if isChoicesArr {
				for _, cNode := range choicesArr {
					cArr, ok := cNode.([]any)
					if ok && len(cArr) >= 2 {
						choiceID, _ := cArr[0].(string)
						var content string
						if textArr, ok := cArr[1].([]any); ok && len(textArr) > 0 {
							content, _ = textArr[0].(string)
						} else if textStr, ok := cArr[1].(string); ok {
							content = textStr
						}
						turn.Choices = append(turn.Choices, HistoryChoice{
							ChoiceID: choiceID,
							Content:  content,
						})
					}
				}
				if len(turn.Choices) > 0 {
					turn.ActiveChoiceID = turn.Choices[0].ChoiceID
				}
			}

			if turn.UserPrompt != "" || len(turn.Choices) > 0 {
				tree.Turns = append(tree.Turns, turn)
				return
			}
		}
	}

	// Đệ quy tìm sâu hơn
	for _, child := range arr {
		extractTurnsFromGeneric(child, tree)
	}
}

// BuildBranchSwitchRequest đóng gói payload batchexecute cho RPC wEb32b (chuyển nhánh DAG)
func BuildBranchSwitchRequest(convID, respID, choiceID string) (string, error) {
	convID = strings.TrimSpace(convID)
	respID = strings.TrimSpace(respID)
	choiceID = strings.TrimSpace(choiceID)

	if convID == "" || respID == "" || choiceID == "" {
		return "", fmt.Errorf("convID, respID, choiceID không được để trống khi chuyển nhánh")
	}

	innerArgs, err := json.Marshal([]string{convID, respID, choiceID})
	if err != nil {
		return "", fmt.Errorf("lỗi encode args wEb32b: %w", err)
	}

	outerEnvelope := [][]any{
		{
			"wEb32b",
			string(innerArgs),
			nil,
			"generic",
		},
	}
	payloadBytes, err := json.Marshal([]any{outerEnvelope})
	if err != nil {
		return "", fmt.Errorf("lỗi encode envelope wEb32b: %w", err)
	}

	return string(payloadBytes), nil
}

// ParseBranchSwitchResponse kiểm tra phản hồi từ RPC wEb32b ([1] hoặc [true])
func ParseBranchSwitchResponse(rawJSON string) (bool, error) {
	root, err := ExtractBatchexecuteEnvelopePayload(rawJSON, "wEb32b")
	if err != nil {
		return false, fmt.Errorf("wEb32b JSON không hợp lệ: %w", err)
	}

	arr, ok := root.([]any)
	if !ok || len(arr) == 0 {
		return true, nil
	}

	switch val := arr[0].(type) {
	case float64:
		return val == 1, nil
	case bool:
		return val, nil
	default:
		return true, nil
	}
}

// BuildAccountTierRequest đóng gói payload batchexecute cho RPC I4z33b
func BuildAccountTierRequest() string {
	outerEnvelope := [][]any{
		{
			"I4z33b",
			"[]",
			nil,
			"generic",
		},
	}
	payloadBytes, _ := json.Marshal([]any{outerEnvelope})
	return string(payloadBytes)
}

// ParseAccountTierResponse phân tích payload trả về từ RPC I4z33b
func ParseAccountTierResponse(rawJSON string) (*AccountTierInfo, error) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return nil, fmt.Errorf("payload I4z33b rỗng")
	}

	info := &AccountTierInfo{
		TierCode:          "FREE_USER",
		ContextWindowSize: 32000,
		Capabilities:      make([]string, 0),
		RawPayload:        rawJSON,
	}

	// Nhận diện gói theo các token trong payload
	if strings.Contains(rawJSON, "GOOGLE_ONE_AI_PREMIUM") {
		info.TierCode = "GOOGLE_ONE_AI_PREMIUM"
		info.ContextWindowSize = 1000000
		info.Capabilities = append(info.Capabilities, "advanced_reasoning", "pro_tier", "expanded_context")
	} else if strings.Contains(rawJSON, "WORKSPACE_ENTERPRISE") {
		info.TierCode = "WORKSPACE_ENTERPRISE"
		info.ContextWindowSize = 2000000
		info.Capabilities = append(info.Capabilities, "enterprise_privacy", "expanded_context")
	} else {
		info.Capabilities = append(info.Capabilities, "flash_tier")
	}

	return info, nil
}

// ConversationSummary tóm tắt một cuộc trò chuyện trong danh sách lịch sử
type ConversationSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// BuildHistoryListRequest đóng gói payload batchexecute cho RPC MaZiqc
func BuildHistoryListRequest(limit int) (string, error) {
	if limit <= 0 {
		limit = 25
	}

	innerArgs, err := json.Marshal([]any{limit, nil, []any{1, nil, 1}})
	if err != nil {
		return "", fmt.Errorf("lỗi encode args MaZiqc: %w", err)
	}

	outerEnvelope := [][]any{
		{
			"MaZiqc",
			string(innerArgs),
			nil,
			"generic",
		},
	}
	payloadBytes, err := json.Marshal([]any{outerEnvelope})
	if err != nil {
		return "", fmt.Errorf("lỗi encode envelope MaZiqc: %w", err)
	}

	return string(payloadBytes), nil
}

// ParseHistoryListResponse phân tích payload trả về từ RPC MaZiqc thành danh sách ConversationSummary
func ParseHistoryListResponse(rawJSON string) ([]ConversationSummary, string, error) {
	root, err := ExtractBatchexecuteEnvelopePayload(rawJSON, "MaZiqc")
	if err != nil {
		return nil, "", fmt.Errorf("MaZiqc JSON không hợp lệ: %w", err)
	}

	var summaries []ConversationSummary
	seenIDs := make(map[string]bool)

	var scanForConversations func(node any)
	scanForConversations = func(node any) {
		arr, ok := node.([]any)
		if !ok {
			return
		}

		// Kiểm tra nếu arr khớp [c_id, title, ...]
		if len(arr) >= 2 {
			if idStr, ok := arr[0].(string); ok && strings.HasPrefix(idStr, "c_") {
				if titleStr, ok := arr[1].(string); ok {
					if !seenIDs[idStr] {
						seenIDs[idStr] = true
						summaries = append(summaries, ConversationSummary{
							ID:    idStr,
							Title: titleStr,
						})
						return
					}
				}
			}
		}

		for _, child := range arr {
			scanForConversations(child)
		}
	}

	scanForConversations(root)
	return summaries, "", nil
}

// BuildRenameConversationRequest đóng gói payload batchexecute cho RPC PCck7e
func BuildRenameConversationRequest(convID, newTitle string) (string, error) {
	convID = strings.TrimSpace(convID)
	newTitle = strings.TrimSpace(newTitle)
	if convID == "" {
		return "", fmt.Errorf("convID không được để trống khi đổi tên")
	}
	if newTitle == "" {
		newTitle = "Cuộc trò chuyện"
	}

	innerArgs, err := json.Marshal([]any{convID, newTitle})
	if err != nil {
		return "", fmt.Errorf("lỗi encode args PCck7e: %w", err)
	}

	outerEnvelope := [][]any{
		{
			"PCck7e",
			string(innerArgs),
			nil,
			"generic",
		},
	}
	payloadBytes, err := json.Marshal([]any{outerEnvelope})
	if err != nil {
		return "", fmt.Errorf("lỗi encode envelope PCck7e: %w", err)
	}

	return string(payloadBytes), nil
}

// ParseRenameConversationResponse kiểm tra phản hồi từ RPC PCck7e
func ParseRenameConversationResponse(rawJSON string) (bool, error) {
	_, err := ExtractBatchexecuteEnvelopePayload(rawJSON, "PCck7e")
	if err != nil {
		return false, fmt.Errorf("PCck7e JSON không hợp lệ: %w", err)
	}
	return true, nil
}

// BuildDeleteConversationRequest đóng gói payload batchexecute cho RPC VxUbXb
func BuildDeleteConversationRequest(convID string) (string, error) {
	convID = strings.TrimSpace(convID)
	if convID == "" {
		return "", fmt.Errorf("convID không được để trống khi xóa cuộc trò chuyện")
	}

	innerArgs, err := json.Marshal([]any{convID})
	if err != nil {
		return "", fmt.Errorf("lỗi encode args VxUbXb: %w", err)
	}

	outerEnvelope := [][]any{
		{
			"VxUbXb",
			string(innerArgs),
			nil,
			"generic",
		},
	}
	payloadBytes, err := json.Marshal([]any{outerEnvelope})
	if err != nil {
		return "", fmt.Errorf("lỗi encode envelope VxUbXb: %w", err)
	}

	return string(payloadBytes), nil
}

// ParseDeleteConversationResponse kiểm tra phản hồi từ RPC VxUbXb
func ParseDeleteConversationResponse(rawJSON string) (bool, error) {
	_, err := ExtractBatchexecuteEnvelopePayload(rawJSON, "VxUbXb")
	if err != nil {
		return false, fmt.Errorf("VxUbXb JSON không hợp lệ: %w", err)
	}
	return true, nil
}
