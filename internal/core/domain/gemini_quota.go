package domain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// QuotaInfo biểu diễn thông tin hạn mức điện toán của tài khoản Gemini
type QuotaInfo struct {
	Quota5h     float64 `json:"quota_5h"`      // Tỷ lệ % đã dùng trong 5 giờ (0 - 100)
	QuotaWeekly float64 `json:"quota_weekly"`  // Tỷ lệ % đã dùng trong tuần (0 - 100)
	RPMLimit    int     `json:"rpm_limit"`     // Giới hạn request mỗi phút
	ResetTime5h string  `json:"reset_time_5h"` // Thời gian hoàn trả dung lượng ISO-8601
	RawResponse string  `json:"raw_response,omitempty"`
}

var (
	quota5hRegex     = regexp.MustCompile(`["']?quota5h["']?\s*[:=]\s*["']?([\d.]+)%?["']?`)
	quotaWeeklyRegex = regexp.MustCompile(`["']?quotaWeekly["']?\s*[:=]\s*["']?([\d.]+)%?["']?`)
	rpmLimitRegex    = regexp.MustCompile(`["']?rpmLimit["']?\s*[:=]\s*["']?(\d+)["']?`)
	resetTimeRegex   = regexp.MustCompile(`["']?resetTime5h["']?\s*[:=]\s*["']?([^"',}\s]+)["']?`)
)

// ParseQuotaResponse phân tích phản hồi từ endpoint /usage (hỗ trợ cả JSON thuần lẫn HTML nhúng script)
func ParseQuotaResponse(body string) (*QuotaInfo, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("nội dung phản hồi /usage rỗng")
	}

	info := &QuotaInfo{
		Quota5h:     0,
		QuotaWeekly: 0,
		RPMLimit:    60,
		ResetTime5h: time.Now().Add(5 * time.Hour).Format(time.RFC3339),
		RawResponse: body,
	}

	// 1. Thử parse dạng JSON trước
	var jsonMap map[string]any
	if err := json.Unmarshal([]byte(body), &jsonMap); err == nil {
		if val, ok := jsonMap["quota5h"].(float64); ok {
			info.Quota5h = val
		}
		if val, ok := jsonMap["quotaWeekly"].(float64); ok {
			info.QuotaWeekly = val
		}
		if val, ok := jsonMap["rpmLimit"].(float64); ok {
			info.RPMLimit = int(val)
		}
		if val, ok := jsonMap["resetTime5h"].(string); ok && val != "" {
			info.ResetTime5h = val
		}
		return info, nil
	}

	// 2. Nếu trả về dạng HTML nhúng, bóc tách bằng Regex
	if match := quota5hRegex.FindStringSubmatch(body); len(match) > 1 {
		if f, err := strconv.ParseFloat(match[1], 64); err == nil {
			info.Quota5h = f
		}
	}
	if match := quotaWeeklyRegex.FindStringSubmatch(body); len(match) > 1 {
		if f, err := strconv.ParseFloat(match[1], 64); err == nil {
			info.QuotaWeekly = f
		}
	}
	if match := rpmLimitRegex.FindStringSubmatch(body); len(match) > 1 {
		if i, err := strconv.Atoi(match[1]); err == nil {
			info.RPMLimit = i
		}
	}
	if match := resetTimeRegex.FindStringSubmatch(body); len(match) > 1 {
		info.ResetTime5h = match[1]
	}

	return info, nil
}
