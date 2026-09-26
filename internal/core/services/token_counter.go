package services

import (
	"math"
	"regexp"
	"unicode/utf8"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
)

// cl100kPattern phỏng theo biểu thức chính quy tách token BPE chuẩn OpenAI (tiktoken cl100k_base / p50k)
var bpeTokenRegex = regexp.MustCompile(`(?i)'s|'t|'re|'ve|'m|'ll|'d|[^\r\n\p{L}\p{N}]?\p{L}+|\p{N}{1,3}| ?[^\s\p{L}\p{N}]+[\r\n]*|\s*[\r\n]+|\s+`)

// TokenCounter cung cấp bộ đếm token chuẩn OpenAI (Usage Accounting)
type TokenCounter struct {
	cfg config.TokensConfig
}

// NewTokenCounter khởi tạo TokenCounter từ cấu hình hạ tầng
func NewTokenCounter(cfg config.TokensConfig) *TokenCounter {
	return &TokenCounter{cfg: cfg}
}

// CountTextTokens đếm số token của một chuỗi văn bản theo encoding đã cấu hình
func (tc *TokenCounter) CountTextTokens(text string, isCompletion bool) int {
	if text == "" {
		return 0
	}

	encoding := tc.cfg.GetEncoding()
	ratio := tc.cfg.GetPromptRatio()
	if isCompletion {
		ratio = tc.cfg.GetCompletionRatio()
	}

	if encoding == "estimation" {
		chars := utf8.RuneCountInString(text)
		tokens := int(math.Ceil(float64(chars) * ratio))
		if tokens < 1 {
			tokens = 1
		}
		return tokens
	}

	// Chuẩn BPE (cl100k_base hoặc p50k_base)
	matches := bpeTokenRegex.FindAllString(text, -1)
	if len(matches) == 0 {
		return 1
	}

	totalTokens := 0
	for _, m := range matches {
		runeCount := utf8.RuneCountInString(m)
		byteCount := len(m)

		// Xử lý từ hoặc ký tự đa byte (tiếng Việt, CJK, emoji...)
		if byteCount > runeCount {
			// Ký tự UTF-8 đa byte: mỗi rune thường chiếm từ 1 đến 2 BPE tokens
			nonAsciiRunes := 0
			for _, r := range m {
				if r > 127 {
					nonAsciiRunes++
				}
			}
			tokensForMatch := (runeCount - nonAsciiRunes) / 4
			tokensForMatch += int(math.Ceil(float64(nonAsciiRunes) * 1.2))
			if tokensForMatch < 1 {
				tokensForMatch = 1
			}
			totalTokens += tokensForMatch
		} else {
			// Ký tự ASCII thuần: từ dài được chia nhỏ subword (trung bình 3-4 ký tự/token)
			tokens := int(math.Ceil(float64(runeCount) / 3.8))
			if tokens < 1 {
				tokens = 1
			}
			totalTokens += tokens
		}
	}

	if totalTokens < 1 {
		totalTokens = 1
	}
	return totalTokens
}

// CountMessageTokens tính toán số token cho một message OpenAI (bao gồm overhead format và ảnh nếu có)
func (tc *TokenCounter) CountMessageTokens(msg domain.OpenAIMessage) int {
	// OpenAI ChatML message framing overhead: 3 tokens per message (<|im_start|>role\n...<|im_end|>)
	tokens := 3

	// Role tokens
	if msg.Role != "" {
		tokens += tc.CountTextTokens(msg.Role, false)
	}

	// Content text tokens
	if msg.Content != "" {
		tokens += tc.CountTextTokens(msg.Content, false)
	}

	// Multimodal image tokens
	imageCount := len(msg.GetImageURLs())
	if imageCount > 0 {
		tileCost := tc.cfg.GetImageTokensPerTile()
		tokens += imageCount * tileCost
	}

	return tokens
}

// CountPromptTokens tính toán tổng prompt tokens cho danh sách tin nhắn
func (tc *TokenCounter) CountPromptTokens(messages []domain.OpenAIMessage) int {
	if len(messages) == 0 {
		return 0
	}

	tokens := 0
	for _, msg := range messages {
		tokens += tc.CountMessageTokens(msg)
	}

	// 3 tokens cho phần mồi câu trả lời của assistant (<|im_start|>assistant\n)
	tokens += 3

	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// CountCompletionTokens tính toán completion tokens từ phản hồi Gemini
func (tc *TokenCounter) CountCompletionTokens(reply domain.GeminiReply) int {
	tokens := 0
	if reply.Text != "" {
		tokens += tc.CountTextTokens(reply.Text, true)
	}

	// Cộng dồn token của các khối suy luận tư duy mở rộng (ThinkingBlocks)
	for _, tb := range reply.ThinkingBlocks {
		if tb.Content != "" {
			tokens += tc.CountTextTokens(tb.Content, true)
		}
	}

	if tokens < 1 && (reply.Text != "" || len(reply.ThinkingBlocks) > 0) {
		tokens = 1
	}
	return tokens
}

// CalculateUsage tạo đối tượng domain.OpenAIUsage hoàn chỉnh
func (tc *TokenCounter) CalculateUsage(messages []domain.OpenAIMessage, reply domain.GeminiReply) *domain.OpenAIUsage {
	promptTokens := tc.CountPromptTokens(messages)
	completionTokens := tc.CountCompletionTokens(reply)
	return &domain.OpenAIUsage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
}
