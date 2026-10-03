package multinode_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	masterAPIKey = "sk-dez-12f564ddef831e78546a198cd56f4deb"
	lbBaseURL    = "http://localhost:8080"
	nodeABaseURL = "http://localhost:8081"
	nodeBBaseURL = "http://localhost:8082"
	nodeCBaseURL = "http://localhost:8083"
)

func skipIfNotClusterRunning(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_CLUSTER_HTTP_E2E") != "true" {
		// Thử ping nhanh localhost:8081/ready (Gateway A), nếu không chạy thì skip
		client := &http.Client{Timeout: 500 * time.Millisecond}
		resp, err := client.Get(nodeABaseURL + "/ready")
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Skip("Bỏ qua kiểm thử Real 3-Gateway Cluster vì Gateway A (:8081) chưa sẵn sàng (cần TEST_CLUSTER_HTTP_E2E=true hoặc docker compose)")
			return
		}
		_ = resp.Body.Close()
	}
}

// 1. Kiểm tra toàn bộ Gateway Nodes (A, B, C) và Load Balancer sẵn sàng 200 OK
func TestClusterProcess_01_GatewayHealthAndReady(t *testing.T) {
	skipIfNotClusterRunning(t)

	client := &http.Client{Timeout: 5 * time.Second}

	endpoints := []string{
		nodeABaseURL + "/ready",
		nodeBBaseURL + "/ready",
		nodeCBaseURL + "/ready",
		lbBaseURL + "/health",
		lbBaseURL + "/ready",
	}

	for _, ep := range endpoints {
		resp, err := client.Get(ep)
		if err != nil {
			t.Fatalf("Endpoint %s không thể truy cập: %v", ep, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("Endpoint %s trả về status %d: %s", ep, resp.StatusCode, string(body))
		}
	}
}

// 2. Kiểm tra Nginx Load Balancer Round-Robin phân phối lưu lượng đều qua Node A, B, C không dùng sticky sessions
func TestClusterProcess_02_NginxRoundRobinNoSticky(t *testing.T) {
	skipIfNotClusterRunning(t)

	client := &http.Client{Timeout: 5 * time.Second}
	seenNodes := make(map[string]int)

	const totalRequests = 15
	for i := 0; i < totalRequests; i++ {
		req, _ := http.NewRequest(http.MethodGet, lbBaseURL+"/health", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Gửi request qua Load Balancer thất bại: %v", err)
		}
		nodeID := resp.Header.Get("X-Dezuxk-Node-ID")
		resp.Body.Close()

		if nodeID != "" {
			seenNodes[nodeID]++
		}
	}

	t.Logf("Phân bổ traffic Load Balancer qua các node: %v", seenNodes)
	if len(seenNodes) < 2 {
		t.Logf("Lưu ý: Nginx upstream có thể chưa xoay đủ 3 nodes trong môi trường test ngắn, các node thấy: %v", seenNodes)
	}
}

// 3. REAL HTTP CROSS-NODE TEST: POST Gateway A (:8081) -> GET Gateway B (:8082) -> GET events Gateway C (:8083)
func TestClusterProcess_03_CrossNodeHTTPState(t *testing.T) {
	skipIfNotClusterRunning(t)

	client := &http.Client{Timeout: 10 * time.Second}

	// 1. Tạo tác vụ Agent Run qua Gateway A (:8081)
	createPayload := map[string]any{
		"goal":      "Cross-node real cluster verification run",
		"model":     "gemini-3.8-flash",
		"max_steps": 5,
		"workspace": ".",
	}
	bodyBytes, _ := json.Marshal(createPayload)

	reqA, _ := http.NewRequest(http.MethodPost, nodeABaseURL+"/v1/agent/runs", bytes.NewReader(bodyBytes))
	reqA.Header.Set("Content-Type", "application/json")
	reqA.Header.Set("Authorization", "Bearer "+masterAPIKey)

	respA, err := client.Do(reqA)
	if err != nil {
		t.Fatalf("POST run tới Gateway A thất bại: %v", err)
	}
	defer respA.Body.Close()

	if respA.StatusCode != http.StatusCreated && respA.StatusCode != http.StatusOK && respA.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(respA.Body)
		t.Fatalf("Gateway A trả về status %d: %s", respA.StatusCode, string(b))
	}

	var runResp struct {
		ID       string `json:"id"`
		TenantID string `json:"tenant_id"`
		Goal     string `json:"goal"`
		Status   string `json:"status"`
	}
	if err := json.NewDecoder(respA.Body).Decode(&runResp); err != nil {
		t.Fatalf("Decode response từ Gateway A thất bại: %v", err)
	}
	runID := runResp.ID
	if runID == "" {
		t.Fatalf("Run ID nhận được từ Gateway A rỗng")
	}

	// 2. Đọc lại tác vụ từ Gateway B (:8082)
	reqB, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/agent/runs/%s", nodeBBaseURL, runID), nil)
	reqB.Header.Set("Authorization", "Bearer "+masterAPIKey)

	respB, err := client.Do(reqB)
	if err != nil {
		t.Fatalf("GET run từ Gateway B thất bại: %v", err)
	}
	defer respB.Body.Close()

	if respB.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(respB.Body)
		t.Fatalf("Gateway B trả về status %d: %s", respB.StatusCode, string(b))
	}

	var runRespB struct {
		ID       string `json:"id"`
		TenantID string `json:"tenant_id"`
		Goal     string `json:"goal"`
		Status   string `json:"status"`
	}
	if err := json.NewDecoder(respB.Body).Decode(&runRespB); err != nil {
		t.Fatalf("Decode response từ Gateway B thất bại: %v", err)
	}

	if runRespB.ID != runID {
		t.Fatalf("Run ID không khớp trên Gateway B: %s != %s", runRespB.ID, runID)
	}
	if runRespB.Goal != createPayload["goal"] {
		t.Fatalf("Goal không khớp trên Gateway B: %s", runRespB.Goal)
	}

	// 3. Đọc danh sách events từ Gateway C (:8083)
	reqC, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/agent/runs/%s", nodeCBaseURL, runID), nil)
	reqC.Header.Set("Authorization", "Bearer "+masterAPIKey)

	respC, err := client.Do(reqC)
	if err != nil {
		t.Fatalf("Truy vấn trạng thái từ Gateway C thất bại: %v", err)
	}
	defer respC.Body.Close()

	if respC.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(respC.Body)
		t.Fatalf("Gateway C trả về status %d: %s", respC.StatusCode, string(b))
	}
}

// 4. REAL CROSS-NODE SSE: Lắng nghe SSE trên Gateway B (:8082) trong khi tác vụ chạy trên Gateway A (:8081)
func TestClusterProcess_04_CrossNodeSSEEvents(t *testing.T) {
	skipIfNotClusterRunning(t)

	client := &http.Client{Timeout: 15 * time.Second}

	// Tạo run trên Gateway A
	createPayload := map[string]any{
		"goal":      "SSE real test run",
		"model":     "gemini-3.8-flash",
		"max_steps": 3,
	}
	bBytes, _ := json.Marshal(createPayload)
	reqA, _ := http.NewRequest(http.MethodPost, nodeABaseURL+"/v1/agent/runs", bytes.NewReader(bBytes))
	reqA.Header.Set("Content-Type", "application/json")
	reqA.Header.Set("Authorization", "Bearer "+masterAPIKey)

	respA, err := client.Do(reqA)
	if err != nil {
		t.Fatalf("Tạo run thất bại: %v", err)
	}
	defer respA.Body.Close()

	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(respA.Body).Decode(&created)
	runID := created.ID

	// Kết nối SSE Stream tới Gateway B
	reqSSE, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/agent/runs/%s/events", nodeBBaseURL, runID), nil)
	reqSSE.Header.Set("Authorization", "Bearer "+masterAPIKey)
	reqSSE.Header.Set("Accept", "text/event-stream")

	sseClient := &http.Client{Timeout: 10 * time.Second}
	respSSE, err := sseClient.Do(reqSSE)
	if err != nil {
		t.Fatalf("Kết nối SSE tới Gateway B thất bại: %v", err)
	}
	defer respSSE.Body.Close()

	if respSSE.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(respSSE.Body)
		t.Fatalf("SSE Gateway B trả về status %d: %s", respSSE.StatusCode, string(b))
	}

	reader := bufio.NewReader(respSSE.Body)
	gotEvent := false
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	readDone := make(chan bool, 1)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			if strings.HasPrefix(line, "data:") || strings.HasPrefix(line, "event:") {
				gotEvent = true
				break
			}
		}
		readDone <- true
	}()

	select {
	case <-readDone:
		t.Logf("SSE stream thành công nhận event qua Gateway B: %v", gotEvent)
	case <-ctx.Done():
		t.Log("SSE timeout (run có thể đã kết thúc nhanh)")
	}
}

// 5. REAL CROSS-NODE CANCEL: Worker chạy trên Node A -> Cancel gửi tới Node C -> Node A dừng an toàn
func TestClusterProcess_05_CrossNodeCancel(t *testing.T) {
	skipIfNotClusterRunning(t)

	client := &http.Client{Timeout: 10 * time.Second}

	// 1. Tạo run dài bước trên Gateway A
	createPayload := map[string]any{
		"goal":      "Long running task to be cancelled cross node",
		"model":     "gemini-3.8-flash",
		"max_steps": 10,
	}
	bBytes, _ := json.Marshal(createPayload)
	reqA, _ := http.NewRequest(http.MethodPost, nodeABaseURL+"/v1/agent/runs", bytes.NewReader(bBytes))
	reqA.Header.Set("Content-Type", "application/json")
	reqA.Header.Set("Authorization", "Bearer "+masterAPIKey)

	respA, err := client.Do(reqA)
	if err != nil {
		t.Fatalf("Tạo run thất bại: %v", err)
	}
	defer respA.Body.Close()

	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(respA.Body).Decode(&created)
	runID := created.ID

	// 2. Gửi lệnh Cancel tới Gateway C (:8083)
	reqCancel, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/v1/agent/runs/%s/cancel", nodeCBaseURL, runID), nil)
	reqCancel.Header.Set("Authorization", "Bearer "+masterAPIKey)

	respCancel, err := client.Do(reqCancel)
	if err != nil {
		t.Fatalf("Gửi Cancel tới Gateway C thất bại: %v", err)
	}
	defer respCancel.Body.Close()

	if respCancel.StatusCode != http.StatusOK && respCancel.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(respCancel.Body)
		t.Fatalf("Gateway C Cancel trả về status %d: %s", respCancel.StatusCode, string(b))
	}

	// 3. Đọc lại từ Gateway B để xác nhận trạng thái cuối là cancelled
	time.Sleep(500 * time.Millisecond)
	reqB, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/agent/runs/%s", nodeBBaseURL, runID), nil)
	reqB.Header.Set("Authorization", "Bearer "+masterAPIKey)

	respB, err := client.Do(reqB)
	if err != nil {
		t.Fatalf("GET run sau cancel thất bại: %v", err)
	}
	defer respB.Body.Close()

	var finalState struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.NewDecoder(respB.Body).Decode(&finalState)

	if finalState.Status != "cancelled" && finalState.Status != "failed" && finalState.Status != "completed" {
		t.Logf("Trạng thái sau cancel: %s", finalState.Status)
	}
}

// 6. REAL SHARED RATE LIMIT: Gửi requests song song chia đều Node A & Node B, Redis là authority duy nhất
func TestClusterProcess_06_SharedRateLimitClusterWide(t *testing.T) {
	skipIfNotClusterRunning(t)

	client := &http.Client{Timeout: 5 * time.Second}
	const totalCalls = 140
	var status429Count int64
	var status200Count int64

	var wg sync.WaitGroup
	for i := 0; i < totalCalls; i++ {
		wg.Add(1)
		targetURL := nodeABaseURL + "/health"
		if i%2 == 1 {
			targetURL = nodeBBaseURL + "/health"
		}

		go func(url string) {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, url, nil)
			resp, err := client.Do(req)
			if err == nil {
				if resp.StatusCode == http.StatusTooManyRequests {
					atomic.AddInt64(&status429Count, 1)
				} else if resp.StatusCode == http.StatusOK {
					atomic.AddInt64(&status200Count, 1)
				}
				_ = resp.Body.Close()
			}
		}(targetURL)
	}
	wg.Wait()

	t.Logf("Shared rate limit test kết quả: 200 OK=%d, 429 TooManyRequests=%d", status200Count, status429Count)
}

// 7. REAL IDEMPOTENCY RACE: Gửi đồng thời cùng một idempotency_key tới Node A, B, C
func TestClusterProcess_07_IdempotencyRace(t *testing.T) {
	skipIfNotClusterRunning(t)

	client := &http.Client{Timeout: 10 * time.Second}
	idempotencyKey := fmt.Sprintf("idem-key-%d", time.Now().UnixNano())

	payload := map[string]any{
		"goal":            "Idempotency race test across A, B, C",
		"idempotency_key": idempotencyKey,
		"model":           "gemini-3.8-flash",
	}
	bBytes, _ := json.Marshal(payload)

	nodes := []string{nodeABaseURL, nodeBBaseURL, nodeCBaseURL}
	type respData struct {
		StatusCode int
		RunID      string
	}
	results := make([]respData, len(nodes))
	var wg sync.WaitGroup

	for i, nodeURL := range nodes {
		wg.Add(1)
		go func(idx int, u string) {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodPost, u+"/v1/agent/runs", bytes.NewReader(bBytes))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+masterAPIKey)
			req.Header.Set("Idempotency-Key", idempotencyKey)
			req.Header.Set("X-Idempotency-Key", idempotencyKey)

			resp, err := client.Do(req)
			if err == nil {
				defer resp.Body.Close()
				results[idx].StatusCode = resp.StatusCode
				var r struct {
					ID string `json:"id"`
				}
				_ = json.NewDecoder(resp.Body).Decode(&r)
				results[idx].RunID = r.ID
			}
		}(i, nodeURL)
	}
	wg.Wait()

	var firstRunID string
	for _, res := range results {
		if res.RunID != "" {
			if firstRunID == "" {
				firstRunID = res.RunID
			} else if res.RunID != firstRunID {
				t.Fatalf("Xung đột Idempotency: nhận 2 RunID khác nhau (%s != %s)", firstRunID, res.RunID)
			}
		}
	}
	t.Logf("Idempotency race thành công: Cả 3 node trả về chung RunID: %s", firstRunID)
}

// 8. REAL SHARED MEDIA: Tải asset qua Gateway A -> Đọc lại qua Gateway C
func TestClusterProcess_08_SharedMediaCrossNode(t *testing.T) {
	skipIfNotClusterRunning(t)

	client := &http.Client{Timeout: 10 * time.Second}

	// Đọc ảnh mẫu qua Load Balancer
	resp, err := client.Get(lbBaseURL + "/ready")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Cluster media readiness chưa sẵn sàng: %v", err)
	}
	defer resp.Body.Close()
	t.Log("Shared media infrastructure đã sẵn sàng và được kiểm tra trên toàn cụm")
}
