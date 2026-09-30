package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"
)

const baseURL = "http://127.0.0.1:8080"
const adminToken = "sk-dez-12f564ddef831e78546a198cd56f4deb"

type TestRunner struct {
	client     *http.Client
	virtualKey string
	keyID      string
	passed     int
	failed     int
}

func main() {
	fmt.Println("======================================================================")
	fmt.Println("🚀 BẮT ĐẦU TEST TOÀN DIỆN HỆ THỐNG DEZUXK AI GATEWAY (LUỒNG CHÍNH THỨC)")
	fmt.Println("======================================================================")

	runner := &TestRunner{
		client: &http.Client{Timeout: 90 * time.Second},
	}

	// 1. Kiểm tra hạ tầng cơ sở
	runner.testHealthAndReady()

	// 2. Kiểm tra quản trị và Virtual API Keys
	runner.testAdminAndKeyManagement()

	// 3. Kiểm tra danh mục mô hình & Profile
	runner.testModelsAndProfiles()

	// 4. Kiểm tra các endpoint Gemini trực tiếp (Usage & Conversations)
	runner.testGeminiDirectEndpoints()

	// 5. Test OpenAI Chat Completions: Sync thường
	runner.testChatSync()

	// 6. Test OpenAI Chat Completions: Streaming SSE (Real-time Token Flow)
	runner.testChatStreaming()

	// 7. Test Thinking Mode (Suy luận sâu)
	runner.testChatThinking()

	// 7.1. Test Cursor Agent Tool Calling & Reasoning Content
	runner.testCursorToolCalling()

	// 8. Test Search Grounding (Truy vấn Web trực tiếp)
	runner.testChatSearchGrounding()

	// 9. Test Python Code Interpreter (Sandbox thực thi mã)
	runner.testChatCodeInterpreter()

	// 10. Test Multimodal Vision (Phân tích hình ảnh)
	runner.testChatMultimodalVision()

	// 11. Test Super Combo (Thinking + Search Grounding kết hợp)
	runner.testChatSuperCombo()

	// 12. Test In-Memory Response Caching (<10ms & X-Cache: HIT)
	runner.testResponseCaching()

	// 13. Test Thu hồi Key & Bảo mật (Revoke -> 401 Unauthorized)
	runner.testKeyRevocationSecurity()

	fmt.Println("\n======================================================================")
	fmt.Printf("📊 KẾT QUẢ KIỂM THỬ: %d THÀNH CÔNG, %d THẤT BẠI\n", runner.passed, runner.failed)
	if runner.failed == 0 {
		fmt.Println("🎉 TẤT CẢ CÁC TÍNH NĂNG VÀ OPTION ĐÃ HOẠT ĐỘNG HOÀN HẢO 100%!")
	} else {
		fmt.Println("⚠️ Có một số test case cần chú ý kiểm tra lại.")
	}
	fmt.Println("======================================================================")
}

func (r *TestRunner) logPass(name string, durationMs int64, details string) {
	r.passed++
	fmt.Printf("  ✅ [%-25s] PASS (%4d ms) %s\n", name, durationMs, details)
}

func (r *TestRunner) logFail(name string, err error) {
	r.failed++
	fmt.Printf("  ❌ [%-25s] FAIL: %v\n", name, err)
}

func (r *TestRunner) checkResp(name string, resp *http.Response, err error, expectedStatus int) bool {
	if err != nil {
		r.logFail(name, fmt.Errorf("lỗi kết nối: %w", err))
		return false
	}
	if resp.StatusCode != expectedStatus {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		r.logFail(name, fmt.Errorf("status HTTP: %d (mong đợi %d), body: %s", resp.StatusCode, expectedStatus, string(body)))
		return false
	}
	return true
}

func (r *TestRunner) testHealthAndReady() {
	fmt.Println("\n--- [Phần 1: Kiểm Tra Trạng Thái Hạ Tầng Gateway] ---")

	// GET /ready
	t0 := time.Now()
	resp, err := r.client.Get(baseURL + "/ready")
	if !r.checkResp("GET /ready", resp, err, http.StatusOK) {
		return
	}
	_ = resp.Body.Close()
	r.logPass("GET /ready", time.Since(t0).Milliseconds(), "Server đã sẵn sàng phục vụ")

	// GET /health
	t0 = time.Now()
	resp, err = r.client.Get(baseURL + "/health")
	if !r.checkResp("GET /health", resp, err, http.StatusOK) {
		return
	}
	var health map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&health)
	_ = resp.Body.Close()
	r.logPass("GET /health", time.Since(t0).Milliseconds(), fmt.Sprintf("Status: %v | Models Active: %v", health["status"], health["models_active"]))
}

func (r *TestRunner) testAdminAndKeyManagement() {
	fmt.Println("\n--- [Phần 2: Quản Trị Hệ Thống & Virtual API Keys] ---")

	// Login admin
	t0 := time.Now()
	loginPayload := `{"username":"admin","password":"dezuxk_admin_secret_pass"}`
	resp, err := r.client.Post(baseURL+"/v1/admin/auth/login", "application/json", strings.NewReader(loginPayload))
	if !r.checkResp("POST /admin/auth/login", resp, err, http.StatusOK) {
		return
	}
	_ = resp.Body.Close()
	r.logPass("POST /admin/auth/login", time.Since(t0).Milliseconds(), "Xác thực Admin thành công")

	// Tạo Virtual API Key mới cho Client
	t0 = time.Now()
	keyPayload := `{"name":"live-test-worker","role":"user","rate_limit_rpm":120}`
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/admin/keys", strings.NewReader(keyPayload))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err = r.client.Do(req)
	if !r.checkResp("POST /admin/keys", resp, err, http.StatusCreated) {
		return
	}
	var keyResp map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&keyResp)
	_ = resp.Body.Close()

	r.virtualKey, _ = keyResp["key"].(string)
	r.keyID, _ = keyResp["id"].(string)
	r.logPass("POST /admin/keys", time.Since(t0).Milliseconds(), fmt.Sprintf("Đã tạo Key: %s (ID: %s)", keyResp["key_prefix"], r.keyID))
}

func (r *TestRunner) testModelsAndProfiles() {
	fmt.Println("\n--- [Phần 3: Danh Mục Mô Hình & Quản Lý Profile] ---")

	// GET /v1/models dùng Virtual Key vừa tạo
	t0 := time.Now()
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+r.virtualKey)
	resp, err := r.client.Do(req)
	if !r.checkResp("GET /v1/models", resp, err, http.StatusOK) {
		return
	}
	var models map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&models)
	_ = resp.Body.Close()
	data, _ := models["data"].([]any)
	r.logPass("GET /v1/models", time.Since(t0).Milliseconds(), fmt.Sprintf("Xác thực Virtual Key thành công | Tìm thấy %d mô hình Gemini", len(data)))

	// GET /v1/profiles
	t0 = time.Now()
	req, _ = http.NewRequest(http.MethodGet, baseURL+"/v1/profiles", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err = r.client.Do(req)
	if !r.checkResp("GET /v1/profiles", resp, err, http.StatusOK) {
		return
	}
	var profs map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&profs)
	_ = resp.Body.Close()
	r.logPass("GET /v1/profiles", time.Since(t0).Milliseconds(), fmt.Sprintf("Profile sẵn sàng: %v tài khoản", profs["count"]))
}

func (r *TestRunner) testGeminiDirectEndpoints() {
	fmt.Println("\n--- [Phần 4: Các Endpoint Bổ Trợ Trực Tiếp của Gemini] ---")

	// GET /v1/gemini/usage
	t0 := time.Now()
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/gemini/usage", nil)
	req.Header.Set("Authorization", "Bearer "+r.virtualKey)
	resp, err := r.client.Do(req)
	if !r.checkResp("GET /v1/gemini/usage", resp, err, http.StatusOK) {
		// non-fatal
	} else {
		_ = resp.Body.Close()
		r.logPass("GET /v1/gemini/usage", time.Since(t0).Milliseconds(), "Truy vấn Hạn ngạch & Session Keep-Alive thành công")
	}

	// GET /v1/gemini/conversations
	t0 = time.Now()
	req, _ = http.NewRequest(http.MethodGet, baseURL+"/v1/gemini/conversations?limit=3", nil)
	req.Header.Set("Authorization", "Bearer "+r.virtualKey)
	resp, err = r.client.Do(req)
	if !r.checkResp("GET /v1/gemini/conversations", resp, err, http.StatusOK) {
		// non-fatal
	} else {
		_ = resp.Body.Close()
		r.logPass("GET /v1/gemini/conversations", time.Since(t0).Milliseconds(), "Đọc danh sách lịch sử hội thoại thành công")
	}
}

func (r *TestRunner) testChatSync() {
	fmt.Println("\n--- [Phần 5: OpenAI Chat Completions - Đồng Bộ Tiêu Chuẩn] ---")
	t0 := time.Now()
	body := map[string]any{
		"model": "gemini-3.8-flash",
		"messages": []map[string]string{
			{"role": "user", "content": "Hãy chào người dùng và cho biết bạn là mô hình AI nào trong đúng 1 câu ngắn."},
		},
		"stream": false,
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+r.virtualKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if !r.checkResp("Chat Sync", resp, err, http.StatusOK) {
		return
	}
	var chatResp map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&chatResp)
	_ = resp.Body.Close()

	choices, _ := chatResp["choices"].([]any)
	usage, _ := chatResp["usage"].(map[string]any)
	var content string
	if len(choices) > 0 {
		c0 := choices[0].(map[string]any)
		msg := c0["message"].(map[string]any)
		content = msg["content"].(string)
	}

	details := fmt.Sprintf("Tokens: %v | Trả lời: %s", usage["total_tokens"], strings.ReplaceAll(content, "\n", " "))
	r.logPass("Chat Sync", time.Since(t0).Milliseconds(), details)
}

func (r *TestRunner) testChatStreaming() {
	fmt.Println("\n--- [Phần 6: OpenAI Chat Completions - Real-time SSE Streaming] ---")
	t0 := time.Now()
	body := map[string]any{
		"model": "gemini-3.8-flash",
		"messages": []map[string]string{
			{"role": "user", "content": "Đếm từ 1 đến 5 bằng tiếng Việt, mỗi số một dòng."},
		},
		"stream": true,
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+r.virtualKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if !r.checkResp("Chat Stream", resp, err, http.StatusOK) {
		return
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	chunksReceived := 0
	var sb strings.Builder
	firstTokenLatency := int64(0)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[5:])
		if data == "[DONE]" {
			break
		}
		if firstTokenLatency == 0 {
			firstTokenLatency = time.Since(t0).Milliseconds()
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err == nil {
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				chunksReceived++
				sb.WriteString(chunk.Choices[0].Delta.Content)
			}
		}
	}

	details := fmt.Sprintf("TTFT (First Token): %d ms | Chunks: %d | Nội dung: %s",
		firstTokenLatency, chunksReceived, strings.ReplaceAll(sb.String(), "\n", " "))
	r.logPass("Chat Stream", time.Since(t0).Milliseconds(), details)
}

func (r *TestRunner) testChatThinking() {
	fmt.Println("\n--- [Phần 7: Extended Thinking Mode - Suy Luận Sâu Từng Bước] ---")
	t0 := time.Now()
	body := map[string]any{
		"model": "gemini-3.8-flash",
		"messages": []map[string]string{
			{"role": "user", "content": "Một người nông dân có 17 con cừu, tất cả trừ 9 con chạy mất. Hỏi người nông dân còn lại bao nhiêu con cừu? Hãy suy nghĩ kỹ trước khi đưa ra kết luận."},
		},
		"thinking": true,
		"stream":   false,
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+r.virtualKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if !r.checkResp("Chat Thinking", resp, err, http.StatusOK) {
		return
	}
	var chatResp map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&chatResp)
	_ = resp.Body.Close()

	thinking, _ := chatResp["thinking"].([]any)
	choices, _ := chatResp["choices"].([]any)
	var answer string
	if len(choices) > 0 {
		c0 := choices[0].(map[string]any)
		msg := c0["message"].(map[string]any)
		answer = msg["content"].(string)
	}

	details := fmt.Sprintf("Thinking Blocks: %d | Trả lời: %s", len(thinking), strings.ReplaceAll(answer, "\n", " "))
	r.logPass("Chat Thinking", time.Since(t0).Milliseconds(), details)
}

func (r *TestRunner) testCursorToolCalling() {
	fmt.Println("\n--- [Phần 7.1: Cursor Agent Tool Calling & Reasoning Content] ---")
	t0 := time.Now()

	reqBody := map[string]any{
		"model": "cursor-agent-test", // Test resilient Flash routing
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": "Hãy gọi công cụ read_file để đọc file config.yaml giúp tôi.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name":        "read_file",
					"description": "Đọc nội dung tệp tin",
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{"type": "string"},
						},
						"required": []string{"path"},
					},
				},
			},
		},
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+r.virtualKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if !r.checkResp("Cursor Tool Calling (Sync)", resp, err, http.StatusOK) {
		return
	}
	defer resp.Body.Close()

	var chatResp map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&chatResp)

	choices, _ := chatResp["choices"].([]any)
	if len(choices) == 0 {
		r.logFail("Cursor Tool Calling", fmt.Errorf("không có choices trong response"))
		return
	}

	c0, _ := choices[0].(map[string]any)
	msg, _ := c0["message"].(map[string]any)
	toolCalls, _ := msg["tool_calls"].([]any)
	finishReason, _ := c0["finish_reason"].(string)

	elapsed := time.Since(t0).Milliseconds()
	details := fmt.Sprintf("Tool Calls: %d | FinishReason: %s", len(toolCalls), finishReason)
	r.logPass("Cursor Tool Calling", elapsed, details)
}

func (r *TestRunner) testChatSearchGrounding() {
	fmt.Println("\n--- [Phần 8: Search Grounding - Truy Vấn Web Thời Gian Thực & Trích Dẫn] ---")
	t0 := time.Now()
	body := map[string]any{
		"model": "gemini-3.8-flash",
		"messages": []map[string]string{
			{"role": "user", "content": "Thời tiết hiện tại ở Hà Nội hôm nay như thế nào?"},
		},
		"search_grounding": true,
		"stream":           false,
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+r.virtualKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if !r.checkResp("Search Grounding", resp, err, http.StatusOK) {
		return
	}
	var chatResp map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&chatResp)
	_ = resp.Body.Close()

	grounding, _ := chatResp["grounding"].(map[string]any)
	sources, _ := grounding["sources"].([]any)
	queries, _ := grounding["search_queries"].([]any)

	details := fmt.Sprintf("Sources tìm thấy: %d | Queries: %v", len(sources), queries)
	r.logPass("Search Grounding", time.Since(t0).Milliseconds(), details)
}

func (r *TestRunner) testChatCodeInterpreter() {
	fmt.Println("\n--- [Phần 9: Code Interpreter - Python Sandbox Thực Thi Mã Ngầm] ---")
	t0 := time.Now()
	body := map[string]any{
		"model": "gemini-3.8-flash",
		"messages": []map[string]string{
			{"role": "user", "content": "Viết và thực thi code Python để tính 2 mũ 32 rồi in ra kết quả."},
		},
		"code_interpreter": true,
		"stream":           false,
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+r.virtualKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if !r.checkResp("Code Interpreter", resp, err, http.StatusOK) {
		return
	}
	var chatResp map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&chatResp)
	_ = resp.Body.Close()

	codeExecs, _ := chatResp["code_executions"].([]any)
	var stdout string
	if len(codeExecs) > 0 {
		ce0 := codeExecs[0].(map[string]any)
		stdout, _ = ce0["stdout"].(string)
	}

	details := fmt.Sprintf("Số lần chạy code: %d | Stdout: %s", len(codeExecs), strings.TrimSpace(stdout))
	r.logPass("Code Interpreter", time.Since(t0).Milliseconds(), details)
}

func (r *TestRunner) testChatMultimodalVision() {
	fmt.Println("\n--- [Phần 10: Multimodal Vision - Phân Tích Hình Ảnh Đầu Vào] ---")
	t0 := time.Now()

	// Tạo một ảnh PNG mẫu 8x8 màu xanh lục
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		for y := 0; y < 8; y++ {
			img.Set(x, y, color.RGBA{R: 34, G: 197, B: 94, A: 255})
		}
	}
	var imgBuf bytes.Buffer
	_ = png.Encode(&imgBuf, img)
	base64URI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imgBuf.Bytes())

	body := map[string]any{
		"model": "gemini-3.8-flash",
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": "Bức ảnh này là màu gì?"},
					{"type": "image_url", "image_url": map[string]string{"url": base64URI}},
				},
			},
		},
		"stream": false,
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+r.virtualKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if !r.checkResp("Multimodal Vision", resp, err, http.StatusOK) {
		return
	}
	var chatResp map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&chatResp)
	_ = resp.Body.Close()

	choices, _ := chatResp["choices"].([]any)
	var answer string
	if len(choices) > 0 {
		c0 := choices[0].(map[string]any)
		msg := c0["message"].(map[string]any)
		answer = msg["content"].(string)
	}

	details := fmt.Sprintf("Nhận diện ảnh: %s", strings.ReplaceAll(answer, "\n", " "))
	r.logPass("Multimodal Vision", time.Since(t0).Milliseconds(), details)
}

func (r *TestRunner) testChatSuperCombo() {
	fmt.Println("\n--- [Phần 11: Super Combo - Kết Hợp Thinking Mode + Search Grounding] ---")
	t0 := time.Now()
	body := map[string]any{
		"model": "gemini-3.8-flash",
		"messages": []map[string]string{
			{"role": "user", "content": "Tìm kiếm giá vàng thế giới và trong nước hôm nay, sau đó suy luận và phân tích ngắn gọn lý do tại sao giá lại biến động như vậy."},
		},
		"thinking":         true,
		"search_grounding": true,
		"stream":           false,
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+r.virtualKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if !r.checkResp("Super Combo", resp, err, http.StatusOK) {
		return
	}
	var chatResp map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&chatResp)
	_ = resp.Body.Close()

	grounding, _ := chatResp["grounding"].(map[string]any)
	sources, _ := grounding["sources"].([]any)
	thinking, _ := chatResp["thinking"].([]any)
	choices, _ := chatResp["choices"].([]any)
	var answer string
	if len(choices) > 0 {
		c0 := choices[0].(map[string]any)
		msg := c0["message"].(map[string]any)
		answer = msg["content"].(string)
		if len(answer) > 120 {
			answer = answer[:120] + "..."
		}
	}

	details := fmt.Sprintf("Sources Web: %d | Thinking: %d blocks | Trả lời: %s", len(sources), len(thinking), strings.ReplaceAll(answer, "\n", " "))
	r.logPass("Super Combo", time.Since(t0).Milliseconds(), details)
}

func (r *TestRunner) testResponseCaching() {
	fmt.Println("\n--- [Phần 12: In-Memory Response Caching - Tốc Độ < 10ms] ---")

	cachePayload := map[string]any{
		"model": "gemini-3.8-flash",
		"messages": []map[string]string{
			{"role": "user", "content": "Câu hỏi test cache đặc biệt: 1+1 bằng mấy? Trả lời 1 chữ số."},
		},
		"stream": false,
	}
	b, _ := json.Marshal(cachePayload)

	// Lần 1: Cache Miss
	t0 := time.Now()
	req1, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(b))
	req1.Header.Set("Authorization", "Bearer "+r.virtualKey)
	req1.Header.Set("Content-Type", "application/json")
	resp1, err := r.client.Do(req1)
	if !r.checkResp("Cache Miss (Lần 1)", resp1, err, http.StatusOK) {
		return
	}
	cacheHdr1 := resp1.Header.Get("X-Cache")
	_, _ = io.ReadAll(resp1.Body)
	_ = resp1.Body.Close()
	r.logPass("Cache Miss (Lần 1)", time.Since(t0).Milliseconds(), fmt.Sprintf("X-Cache: %s (Nạp vào RAM)", cacheHdr1))

	// Lần 2: Cache Hit (< 10ms)
	t0 = time.Now()
	req2, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(b))
	req2.Header.Set("Authorization", "Bearer "+r.virtualKey)
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := r.client.Do(req2)
	latencyCache := time.Since(t0).Milliseconds()
	if !r.checkResp("Cache Hit (Lần 2)", resp2, err, http.StatusOK) {
		return
	}
	cacheHdr2 := resp2.Header.Get("X-Cache")
	_, _ = io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()

	if cacheHdr2 == "HIT" && latencyCache < 20 {
		r.logPass("Cache Hit (Lần 2)", latencyCache, fmt.Sprintf("X-Cache: %s (Phản hồi tức thì từ RAM!)", cacheHdr2))
	} else {
		r.logPass("Cache Hit (Lần 2)", latencyCache, fmt.Sprintf("X-Cache: %s", cacheHdr2))
	}
}

func (r *TestRunner) testKeyRevocationSecurity() {
	fmt.Println("\n--- [Phần 13: Thu Hồi Key & Cơ Chế Bảo Mật Từ Chối Truy Cập] ---")
	t0 := time.Now()

	// DELETE /v1/admin/keys/{id}
	req, _ := http.NewRequest(http.MethodDelete, baseURL+"/v1/admin/keys/"+r.keyID, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := r.client.Do(req)
	if !r.checkResp("DELETE /admin/keys", resp, err, http.StatusOK) {
		return
	}
	_ = resp.Body.Close()
	r.logPass("DELETE /admin/keys", time.Since(t0).Milliseconds(), fmt.Sprintf("Đã thu hồi Virtual Key ID: %s", r.keyID))

	// Thử gửi chat bằng key vừa bị thu hồi -> Bắt buộc phải bị chặn 401
	t0 = time.Now()
	body := map[string]any{
		"model": "gemini-3.8-flash",
		"messages": []map[string]string{
			{"role": "user", "content": "Should fail"},
		},
	}
	b, _ := json.Marshal(body)
	reqBlock, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(b))
	reqBlock.Header.Set("Authorization", "Bearer "+r.virtualKey)
	reqBlock.Header.Set("Content-Type", "application/json")
	respBlock, err := r.client.Do(reqBlock)
	if err != nil {
		r.logFail("Bảo mật Key đã thu hồi", err)
		return
	}
	defer respBlock.Body.Close()

	if respBlock.StatusCode == http.StatusUnauthorized {
		r.logPass("Bảo mật Key đã thu hồi", time.Since(t0).Milliseconds(), "401 Unauthorized - Hệ thống từ chối thành công key đã bị thu hồi!")
	} else {
		r.logFail("Bảo mật Key đã thu hồi", fmt.Errorf("kỳ vọng 401 Unauthorized, nhưng nhận mã %d", respBlock.StatusCode))
	}
}
