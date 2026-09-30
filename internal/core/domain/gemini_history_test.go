package domain_test

import (
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestBuildHistoryDetailRequest(t *testing.T) {
	req, err := domain.BuildHistoryDetailRequest("c_123456789")
	if err != nil {
		t.Fatalf("BuildHistoryDetailRequest thất bại: %v", err)
	}

	if !strings.Contains(req, "cZOhpc") {
		t.Fatalf("thiếu rpc cZOhpc: %s", req)
	}
	if !strings.Contains(req, "c_123456789") {
		t.Fatalf("thiếu conversation_id trong payload: %s", req)
	}
}

func TestParseHistoryDetailResponse(t *testing.T) {
	// Giả lập payload cây hội thoại trả về từ cZOhpc
	fixture := `[
		[
			[
				"r_turn_1",
				["Hôm nay thời tiết thế nào?"],
				[
					["rc_choice_1", ["Hôm nay trời nắng đẹp, nhiệt độ khoảng 28 độ C."]],
					["rc_choice_2", ["Thời tiết hôm nay tương đối mát mẻ có mây nhẹ."]]
				]
			],
			[
				"r_turn_2",
				["Ngày mai thì sao?"],
				[
					["rc_choice_3", ["Ngày mai dự báo có mưa rào rải rác vào buổi chiều."]]
				]
			]
		]
	]`

	tree, err := domain.ParseHistoryDetailResponse("c_123456789", fixture)
	if err != nil {
		t.Fatalf("ParseHistoryDetailResponse thất bại: %v", err)
	}

	if tree.ConversationID != "c_123456789" {
		t.Errorf("mong đợi c_123456789, nhận: %s", tree.ConversationID)
	}
	if len(tree.Turns) != 2 {
		t.Fatalf("mong đợi 2 turns, nhận: %d", len(tree.Turns))
	}

	turn1 := tree.Turns[0]
	if turn1.TurnID != "r_turn_1" || turn1.UserPrompt != "Hôm nay thời tiết thế nào?" {
		t.Errorf("turn 1 không khớp: %+v", turn1)
	}
	if len(turn1.Choices) != 2 {
		t.Fatalf("mong đợi 2 choices ở turn 1, nhận: %d", len(turn1.Choices))
	}
	if turn1.Choices[0].ChoiceID != "rc_choice_1" || !strings.Contains(turn1.Choices[0].Content, "trời nắng đẹp") {
		t.Errorf("choice 1 không khớp: %+v", turn1.Choices[0])
	}
	if turn1.ActiveChoiceID != "rc_choice_1" {
		t.Errorf("active choice mong đợi rc_choice_1, nhận: %s", turn1.ActiveChoiceID)
	}

	turn2 := tree.Turns[1]
	if turn2.TurnID != "r_turn_2" || turn2.UserPrompt != "Ngày mai thì sao?" {
		t.Errorf("turn 2 không khớp: %+v", turn2)
	}
}

func TestBranchSwitchRequestAndResponse(t *testing.T) {
	req, err := domain.BuildBranchSwitchRequest("c_123", "r_456", "rc_789")
	if err != nil {
		t.Fatalf("BuildBranchSwitchRequest thất bại: %v", err)
	}

	if !strings.Contains(req, "wEb32b") {
		t.Fatalf("thiếu rpc wEb32b: %s", req)
	}
	if !strings.Contains(req, "c_123") || !strings.Contains(req, "r_456") || !strings.Contains(req, "rc_789") {
		t.Fatalf("thiếu tham số nhánh: %s", req)
	}

	// Test parse phản hồi [1]
	ok1, err := domain.ParseBranchSwitchResponse("[1]")
	if err != nil || !ok1 {
		t.Errorf("parse [1] thất bại: ok=%v, err=%v", ok1, err)
	}

	// Test parse phản hồi [true]
	ok2, err := domain.ParseBranchSwitchResponse("[true]")
	if err != nil || !ok2 {
		t.Errorf("parse [true] thất bại: ok=%v, err=%v", ok2, err)
	}
}

func TestAccountTierRequestAndResponse(t *testing.T) {
	req := domain.BuildAccountTierRequest()
	if !strings.Contains(req, "I4z33b") {
		t.Fatalf("thiếu rpc I4z33b: %s", req)
	}

	// Test parse response Free User
	infoFree, err := domain.ParseAccountTierResponse(`[["wrb.fr", "I4z33b", "[[\"FREE_USER\"]]", null, null, null, "generic"]]`)
	if err != nil {
		t.Fatalf("parse free user thất bại: %v", err)
	}
	if infoFree.TierCode != "FREE_USER" {
		t.Errorf("mong đợi FREE_USER, nhận: %s", infoFree.TierCode)
	}

	// Test parse response Premium User
	infoPrem, err := domain.ParseAccountTierResponse(`[["wrb.fr", "I4z33b", "[[\"GOOGLE_ONE_AI_PREMIUM\", 1000000]]", null, null, null, "generic"]]`)
	if err != nil {
		t.Fatalf("parse premium user thất bại: %v", err)
	}
	if infoPrem.TierCode != "GOOGLE_ONE_AI_PREMIUM" || infoPrem.ContextWindowSize != 1000000 {
		t.Errorf("mong đợi GOOGLE_ONE_AI_PREMIUM 1M context, nhận: %+v", infoPrem)
	}
}

func TestHistoryListRequestAndResponse(t *testing.T) {
	req, err := domain.BuildHistoryListRequest(20)
	if err != nil {
		t.Fatalf("BuildHistoryListRequest thất bại: %v", err)
	}
	if !strings.Contains(req, "MaZiqc") {
		t.Fatalf("thiếu rpc MaZiqc: %s", req)
	}

	fixture := `[[["c_conv_001", "Học máy và AI", [null, 1790000000]], ["c_conv_002", "Công thức toán học", [null, 1790001000]]]]`
	items, _, err := domain.ParseHistoryListResponse(fixture)
	if err != nil {
		t.Fatalf("ParseHistoryListResponse lỗi: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("mong đợi 2 cuộc trò chuyện, nhận: %d", len(items))
	}
	if items[0].ID != "c_conv_001" || items[0].Title != "Học máy và AI" {
		t.Errorf("item 0 không khớp: %+v", items[0])
	}
}

func TestRenameConversationRequestAndResponse(t *testing.T) {
	req, err := domain.BuildRenameConversationRequest("c_123", "Tiêu đề mới")
	if err != nil {
		t.Fatalf("BuildRenameConversationRequest thất bại: %v", err)
	}
	if !strings.Contains(req, "PCck7e") || !strings.Contains(req, "Tiêu đề mới") {
		t.Fatalf("payload không đúng: %s", req)
	}

	ok, err := domain.ParseRenameConversationResponse(`[1]`)
	if err != nil || !ok {
		t.Errorf("ParseRenameConversationResponse thất bại: %v", err)
	}
}

func TestDeleteConversationRequestAndResponse(t *testing.T) {
	req, err := domain.BuildDeleteConversationRequest("c_123")
	if err != nil {
		t.Fatalf("BuildDeleteConversationRequest thất bại: %v", err)
	}
	if !strings.Contains(req, "VxUbXb") || !strings.Contains(req, "c_123") {
		t.Fatalf("payload không đúng: %s", req)
	}

	ok, err := domain.ParseDeleteConversationResponse(`[1]`)
	if err != nil || !ok {
		t.Errorf("ParseDeleteConversationResponse thất bại: %v", err)
	}
}

func TestModeSwitchRequestAndResponse(t *testing.T) {
	req, err := domain.BuildModeSwitchRequest("8c46e95b1a07cecc")
	if err != nil {
		t.Fatalf("BuildModeSwitchRequest thất bại: %v", err)
	}
	if !strings.Contains(req, "L5adhe") || !strings.Contains(req, "8c46e95b1a07cecc") {
		t.Fatalf("payload không đúng: %s", req)
	}

	ok, err := domain.ParseModeSwitchResponse(`[[["wrb.fr","L5adhe","[1]",null,null,null,"generic"]]]`)
	if err != nil || !ok {
		t.Errorf("ParseModeSwitchResponse thất bại: %v", err)
	}
}
