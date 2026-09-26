package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// VoicePersona đại diện cho 1 nhân vật giọng đọc AI
type VoicePersona struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Gender      string `json:"gender"` // Female / Male
	Description string `json:"description"`
	SampleURL   string `json:"sample_url"`
}

// BuildGetCharactersPayload đóng gói mảng JSON cho RPC Zzl0ze
// LƯU Ý QUAN TRỌNG: Không dùng wildcard "projects/*", bắt buộc phải truyền "projects/<PROJECT_UUID>"
func BuildGetCharactersPayload(projectUUID string) []any {
	return []any{
		"projects/" + projectUUID,
		nil,
		nil,
		nil,
		[]any{1},
	}
}

// DefaultVoicePersonas danh mục 30 nhân vật giọng đọc chính thức trích xuất từ Google Flow
func DefaultVoicePersonas() []VoicePersona {
	list := []struct {
		id, name, gender, desc string
	}{
		{"achernar", "Achernar", "Nữ", "Nhẹ nhàng, thanh mảnh, truyền cảm (Female, soft, high pitch)"},
		{"achird", "Achird", "Nam", "Thân thiện, ấm áp, cân bằng (Male, friendly, mid pitch)"},
		{"algenib", "Algenib", "Nam", "Trầm khàn, sâu lắng (Male, gravelly, low pitch)"},
		{"algieba", "Algieba", "Nam", "Thoải mái, tự nhiên, trung-trầm (Male, easy-going, mid-low pitch)"},
		{"alnilam", "Alnilam", "Nam", "Đanh thép, dứt khoát, chuyên nghiệp (Male, firm, mid-low pitch)"},
		{"aoede", "Aoede", "Nữ", "Trong trẻo, tươi mới, thanh thoát (Female, breezy, mid pitch)"},
		{"autonoe", "Autonoe", "Nữ", "Tươi sáng, giàu năng lượng (Female, bright, mid pitch)"},
		{"callirrhoe", "Callirrhoe", "Nữ", "Trầm ấm, gần gũi, đời thường (Female, easy-going, mid pitch)"},
		{"charon", "Charon", "Nam", "Truyền cảm, chuẩn giọng đọc tin tức (Male, informative, lower pitch)"},
		{"despina", "Despina", "Nữ", "Mượt mà, cuốn hút, điện ảnh (Female, smooth, mid pitch)"},
		{"enceladus", "Enceladus", "Nam", "Hùng hồn, mạnh mẽ, kể chuyện kịch tính (Male, dramatic, low pitch)"},
		{"erriapus", "Erriapus", "Nam", "Ấm áp, sâu lắng, tin cậy (Male, warm, calm)"},
		{"fenrir", "Fenrir", "Nam", "Gai góc, sắc bén, cá tính (Male, gritty, intense)"},
		{"fornjot", "Fornjot", "Nam", "Đĩnh đạc, trung niên, uy nghiêm (Male, mature, authoritative)"},
		{"halimede", "Halimede", "Nữ", "Thanh lịch, chuẩn mực, thuyết trình (Female, elegant, professional)"},
		{"hegemone", "Hegemone", "Nữ", "Ngọt ngào, đồng cảm, tâm sự (Female, sweet, empathetic)"},
		{"helike", "Helike", "Nữ", "Tự tin, rành rọt, dẫn dắt (Female, confident, articulate)"},
		{"iapetus", "Iapetus", "Nam", "Uy quyền, vững vàng (Male, commanding, solid)"},
		{"isonoe", "Isonoe", "Nữ", "Dịu dàng, êm ả, thiền định (Female, gentle, serene)"},
		{"kari", "Kari", "Nam", "Trẻ trung, linh hoạt, sôi động (Male, youthful, dynamic)"},
		{"kiviuq", "Kiviuq", "Nam", "Phiêu lưu, lôi cuốn, du ký (Male, adventurous, engaging)"},
		{"lysithea", "Lysithea", "Nữ", "Sôi nổi, nhí nhảnh, vui tươi (Female, animated, cheerful)"},
		{"megaclite", "Megaclite", "Nữ", "Chính kịch, nội lực, biểu cảm cao (Female, dramatic, powerful)"},
		{"narvi", "Narvi", "Nam", "Hài hước, dí dỏm, giải trí (Male, humorous, witty)"},
		{"orthosie", "Orthosie", "Nữ", "Kiêu kỳ, sắc sảo, hiện đại (Female, sophisticated, sharp)"},
		{"pasiphae", "Pasiphae", "Nữ", "Thông thái, điềm tĩnh, tri thức (Female, wise, poised)"},
		{"skoll", "Skoll", "Nam", "Bí ẩn, ma mị, điện ảnh giật gân (Male, mysterious, cinematic)"},
		{"sponde", "Sponde", "Nữ", "Sâu sắc, lắng đọng, thơ ca (Female, contemplative, poetic)"},
		{"tarvos", "Tarvos", "Nam", "Dồn dập, nghẹt thở, phim hành động (Male, urgent, action)"},
		{"weywot", "Weywot", "Nam", "Phóng khoáng, tự do, phong trần (Male, rugged, casual)"},
	}

	result := make([]VoicePersona, 0, len(list))
	for _, p := range list {
		sample := fmt.Sprintf("https://gstatic.com/aitestkitchen/voices/samples/%s.wav", p.name)
		result = append(result, VoicePersona{
			ID:          p.id,
			Name:        p.name,
			Gender:      p.gender,
			Description: p.desc,
			SampleURL:   sample,
		})
	}
	return result
}

// ParseVoicePersonasResponse phân tích phản hồi RPC Zzl0ze
func ParseVoicePersonasResponse(inner string, metrics *ContractMetrics) ([]VoicePersona, error) {
	inner = strings.TrimSpace(inner)
	if inner == "" || inner == "[]" {
		// Trả về danh sách mặc định nếu server chưa trả về
		return DefaultVoicePersonas(), nil
	}

	var data []any
	if err := json.Unmarshal([]byte(inner), &data); err != nil {
		if metrics != nil {
			metrics.AddSchema()
		}
		return DefaultVoicePersonas(), nil
	}

	defaults := DefaultVoicePersonas()
	nameMap := make(map[string]VoicePersona)
	for _, d := range defaults {
		nameMap[strings.ToLower(d.Name)] = d
	}

	var result []VoicePersona
	walkStrings(data, func(s string) {
		sClean := strings.TrimSpace(s)
		if p, ok := nameMap[strings.ToLower(sClean)]; ok {
			result = append(result, p)
		}
	})

	if len(result) == 0 {
		return defaults, nil
	}
	return result, nil
}

func walkStrings(v any, fn func(string)) {
	switch t := v.(type) {
	case string:
		fn(t)
	case []any:
		for _, item := range t {
			walkStrings(item, fn)
		}
	}
}
