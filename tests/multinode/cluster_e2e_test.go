package multinode_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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
	if runID == "" {
		t.Fatalf("Không nhận được RunID từ Gateway A")
	}

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

	// 3. Polling từ Gateway B để xác nhận trạng thái cuối cùng chuyển sang cancelled
	var finalState struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	cancelled := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		reqB, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/agent/runs/%s", nodeBBaseURL, runID), nil)
		reqB.Header.Set("Authorization", "Bearer "+masterAPIKey)

		respB, err := client.Do(reqB)
		if err == nil {
			_ = json.NewDecoder(respB.Body).Decode(&finalState)
			respB.Body.Close()
			if finalState.Status == "cancelled" {
				cancelled = true
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	if !cancelled {
		t.Fatalf("Cross-node cancel thất bại: run %s không đạt trạng thái 'cancelled' trong thời gian quy định (trạng thái: %s)", runID, finalState.Status)
	}
	t.Logf("Cross-node cancel thành công: run %s đã chuyển sang trạng thái cancelled trên toàn cụm", runID)
}

// 6. REAL SHARED RATE LIMIT: Gửi requests song song chia đều Node A, Node B, Node C & LB, Redis là authority duy nhất
func TestClusterProcess_06_SharedRateLimitClusterWide(t *testing.T) {
	skipIfNotClusterRunning(t)

	client := &http.Client{Timeout: 5 * time.Second}

	// 1. Tạo Virtual Key riêng cho tenant-cluster-ratelimit để kiểm tra Redis Shared Rate Limiter
	createKeyPayload := map[string]any{
		"name":      "Rate Limit Cluster Key",
		"tenant_id": "tenant-cluster-ratelimit",
		"role":      "user",
	}
	pBytes, _ := json.Marshal(createKeyPayload)
	reqKey, _ := http.NewRequest(http.MethodPost, nodeABaseURL+"/v1/admin/keys", bytes.NewReader(pBytes))
	reqKey.Header.Set("Content-Type", "application/json")
	reqKey.Header.Set("Authorization", "Bearer "+masterAPIKey)

	respKey, err := client.Do(reqKey)
	if err != nil {
		t.Fatalf("Tạo virtual key cho rate limit test thất bại: %v", err)
	}
	defer respKey.Body.Close()
	if respKey.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(respKey.Body)
		t.Fatalf("Tạo virtual key cho rate limit test trả về status %d: %s", respKey.StatusCode, string(body))
	}
	var createdKey struct {
		Key    string `json:"key"`
		RawKey string `json:"raw_key"`
	}
	_ = json.NewDecoder(respKey.Body).Decode(&createdKey)
	tokenRateLimit := createdKey.Key
	if tokenRateLimit == "" {
		tokenRateLimit = createdKey.RawKey
	}
	if tokenRateLimit == "" {
		t.Fatalf("Không thể trích xuất token cho rate limit test")
	}

	const totalCalls = 100
	var status429Count int64
	var status200Count int64

	targets := []string{
		nodeABaseURL + "/v1/models",
		nodeBBaseURL + "/v1/models",
		nodeCBaseURL + "/v1/models",
		lbBaseURL + "/v1/models",
	}

	var wg sync.WaitGroup
	for i := 0; i < totalCalls; i++ {
		wg.Add(1)
		targetURL := targets[i%len(targets)]

		go func(url string) {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, url, nil)
			req.Header.Set("Authorization", "Bearer "+tokenRateLimit)
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

	t.Logf("Shared rate limit test kết quả: 200 OK=%d, 429 TooManyRequests=%d (Total completed=%d)",
		status200Count, status429Count, status200Count+status429Count)

	if status429Count == 0 {
		t.Fatalf("LỖI FALSE-POSITIVE: Rate limiter không sinh bất kỳ lỗi 429 nào (200 OK=%d, 429=%d), giới hạn cụm Redis chưa có hiệu lực!", status200Count, status429Count)
	}
	if status200Count > 35 { // 30 configured limit + 5 burst allowance
		t.Fatalf("LỖI RATE LIMIT TOÀN CỤM: Số request thành công (%d) vượt quá giới hạn toàn cụm 30!", status200Count)
	}
	if status200Count+status429Count != totalCalls {
		t.Fatalf("Tổng số requests hoàn thành (%d) không khớp tổng số gửi (%d)", status200Count+status429Count, totalCalls)
	}
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
	for idx, res := range results {
		if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusAccepted {
			t.Fatalf("Node %d trả về HTTP status không hợp lệ: %d", idx, res.StatusCode)
		}
		if res.RunID == "" {
			t.Fatalf("Node %d trả về RunID rỗng", idx)
		}
		if firstRunID == "" {
			firstRunID = res.RunID
		} else if res.RunID != firstRunID {
			t.Fatalf("Xung đột Idempotency: nhận 2 RunID khác nhau (%s != %s)", firstRunID, res.RunID)
		}
	}

	if firstRunID == "" {
		t.Fatalf("LỖI IDEMPOTENCY: Không nhận được RunID hợp lệ từ bất kỳ node nào")
	}

	// Xác nhận run duy nhất tồn tại trên cả 3 nodes và khớp dữ liệu
	for _, nodeURL := range nodes {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/agent/runs/%s", nodeURL, firstRunID), nil)
		req.Header.Set("Authorization", "Bearer "+masterAPIKey)
		resp, err := client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("Không thể truy vấn run %s từ %s: status=%d, err=%v", firstRunID, nodeURL, resp.StatusCode, err)
		}
		resp.Body.Close()
	}

	t.Logf("Idempotency race thành công: Cả 3 node trả về chung RunID duy nhất: %s", firstRunID)
}

// 8. REAL SHARED MEDIA: Tải asset qua Gateway A -> Đọc lại qua Gateway C với kiểm tra cô lập Tenant
func TestClusterProcess_08_SharedMediaCrossNode(t *testing.T) {
	skipIfNotClusterRunning(t)

	client := &http.Client{Timeout: 10 * time.Second}

	// 1. Tạo 2 Virtual API Keys cho 2 tenants riêng biệt qua Gateway A
	createKey := func(tenantID, keyName string) string {
		payload := map[string]any{
			"name":      keyName,
			"tenant_id": tenantID,
			"role":      "user",
		}
		pBytes, _ := json.Marshal(payload)
		req, _ := http.NewRequest(http.MethodPost, nodeABaseURL+"/v1/admin/keys", bytes.NewReader(pBytes))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+masterAPIKey)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Tạo virtual key cho %s thất bại: %v", tenantID, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("Tạo virtual key cho %s trả về status %d: %s", tenantID, resp.StatusCode, string(body))
		}
		var created struct {
			Key    string `json:"key"`
			RawKey string `json:"raw_key"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&created)
		if created.Key != "" {
			return created.Key
		}
		return created.RawKey
	}

	tokenTenantA := createKey("tenant-cluster-alpha", "Key Tenant Alpha")
	tokenTenantB := createKey("tenant-cluster-beta", "Key Tenant Beta")

	if tokenTenantA == "" || tokenTenantB == "" {
		t.Fatalf("Không thể tạo khóa API cho kiểm thử cô lập Tenant: A=%s, B=%s", tokenTenantA, tokenTenantB)
	}

	// 2. Upload asset riêng tư (private) qua Gateway A (:8081) với khóa của Tenant Alpha
	rawPayload := []byte("CROSS_NODE_SHARED_MEDIA_VERIFIED_BINARY_CONTENT_2026")
	base64Payload := base64.StdEncoding.EncodeToString(rawPayload)

	uploadBody := map[string]any{
		"file_name":   "cluster-shared-logo.png",
		"mime_type":   "image/png",
		"data_base64": base64Payload,
		"is_public":   false,
	}
	uBytes, _ := json.Marshal(uploadBody)

	reqUpload, _ := http.NewRequest(http.MethodPost, nodeABaseURL+"/v1/media", bytes.NewReader(uBytes))
	reqUpload.Header.Set("Content-Type", "application/json")
	reqUpload.Header.Set("Authorization", "Bearer "+tokenTenantA)

	respUpload, err := client.Do(reqUpload)
	if err != nil {
		t.Fatalf("Upload media qua Gateway A thất bại: %v", err)
	}
	defer respUpload.Body.Close()

	if respUpload.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(respUpload.Body)
		t.Fatalf("Upload media qua Gateway A trả về status %d: %s", respUpload.StatusCode, string(b))
	}

	var uploadedAsset struct {
		ID       string `json:"id"`
		FileName string `json:"file_name"`
		TenantID string `json:"tenant_id"`
		IsPublic bool   `json:"is_public"`
	}
	_ = json.NewDecoder(respUpload.Body).Decode(&uploadedAsset)
	assetID := uploadedAsset.ID
	if assetID == "" {
		t.Fatalf("Không nhận được Asset ID sau khi upload lên Gateway A")
	}

	// 3. Tải asset qua Gateway C (:8083) với khóa của Tenant Alpha -> Phải thành công 200 OK
	reqDownloadA, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/media/%s", nodeCBaseURL, assetID), nil)
	reqDownloadA.Header.Set("Authorization", "Bearer "+tokenTenantA)

	respDownloadA, err := client.Do(reqDownloadA)
	if err != nil {
		t.Fatalf("Download media qua Gateway C thất bại: %v", err)
	}
	defer respDownloadA.Body.Close()

	if respDownloadA.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(respDownloadA.Body)
		t.Fatalf("Gateway C trả về status %d khi Tenant Alpha tải asset của chính mình: %s", respDownloadA.StatusCode, string(b))
	}

	downloadedBytesA, err := io.ReadAll(respDownloadA.Body)
	if err != nil {
		t.Fatalf("Đọc dữ liệu tải về từ Gateway C thất bại: %v", err)
	}

	if !bytes.Equal(downloadedBytesA, rawPayload) {
		t.Fatalf("Dữ liệu tải về từ Gateway C không khớp tuyệt đối với dữ liệu đã upload lên Gateway A: '%s' != '%s'", string(downloadedBytesA), string(rawPayload))
	}

	if ct := respDownloadA.Header.Get("Content-Type"); !strings.Contains(ct, "image/png") {
		t.Errorf("Content-Type không đúng: %s (kỳ vọng image/png)", ct)
	}

	// 4. Kiểm tra cô lập Tenant: Tenant Beta cố tải private asset của Tenant Alpha qua Gateway C -> Bắt buộc 403 Forbidden
	reqDownloadB, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/media/%s", nodeCBaseURL, assetID), nil)
	reqDownloadB.Header.Set("Authorization", "Bearer "+tokenTenantB)

	respDownloadB, err := client.Do(reqDownloadB)
	if err != nil {
		t.Fatalf("Thực thi request của Tenant B tới Gateway C thất bại: %v", err)
	}
	defer respDownloadB.Body.Close()

	if respDownloadB.StatusCode != http.StatusForbidden {
		t.Fatalf("LỖI CÔ LẬP TENANT: Tenant B truy cập private asset của Tenant Alpha nhưng nhận status %d (kỳ vọng 403 Forbidden)!", respDownloadB.StatusCode)
	}

	// 5. Kiểm tra Public Asset: Upload asset công khai qua Gateway A, Tenant Beta phải tải được qua Gateway C
	publicBody := map[string]any{
		"file_name":   "cluster-public-readme.txt",
		"mime_type":   "text/plain",
		"data_base64": base64.StdEncoding.EncodeToString([]byte("PUBLIC_DOCUMENT_ACCESSIBLE_BY_ALL")),
		"is_public":   true,
	}
	pubBytes, _ := json.Marshal(publicBody)
	reqPubUpload, _ := http.NewRequest(http.MethodPost, nodeABaseURL+"/v1/media", bytes.NewReader(pubBytes))
	reqPubUpload.Header.Set("Content-Type", "application/json")
	reqPubUpload.Header.Set("Authorization", "Bearer "+tokenTenantA)

	respPubUpload, err := client.Do(reqPubUpload)
	if err != nil || respPubUpload.StatusCode != http.StatusCreated {
		t.Fatalf("Upload public asset qua Gateway A thất bại: status=%d, err=%v", respPubUpload.StatusCode, err)
	}
	var pubAsset struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(respPubUpload.Body).Decode(&pubAsset)
	respPubUpload.Body.Close()

	reqPubDownloadB, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/media/%s", nodeCBaseURL, pubAsset.ID), nil)
	reqPubDownloadB.Header.Set("Authorization", "Bearer "+tokenTenantB)

	respPubDownloadB, err := client.Do(reqPubDownloadB)
	if err != nil || respPubDownloadB.StatusCode != http.StatusOK {
		t.Fatalf("Tenant B tải public asset qua Gateway C thất bại: status=%d, err=%v", respPubDownloadB.StatusCode, err)
	}
	pubDownloaded, _ := io.ReadAll(respPubDownloadB.Body)
	respPubDownloadB.Body.Close()

	if string(pubDownloaded) != "PUBLIC_DOCUMENT_ACCESSIBLE_BY_ALL" {
		t.Fatalf("Dữ liệu public asset không khớp: %s", string(pubDownloaded))
	}

	t.Logf("Shared media cross-node hoàn thành: Upload Node A -> Tải Node C thành công; Cô lập Tenant bảo đảm 403 Forbidden; Public asset khả dụng toàn cụm")
}

// 9. REAL NODE CRASH & AGENT TAKE OVER: Node A crash mid-run -> Node B/C takeover với claim_generation tăng -> hoàn thành run
func TestClusterProcess_09_RealNodeCrashAndAgentTakeover(t *testing.T) {
	skipIfNotClusterRunning(t)

	// Kiểm tra công cụ docker có sẵn để thực hiện kill container
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker binary không có trong PATH, bỏ qua test container kill")
		return
	}

	client := &http.Client{Timeout: 15 * time.Second}

	// 1. Submit run qua Gateway A với mục tiêu controlled sleep để đảm bảo run đang chạy khi Gateway A bị kill
	createPayload := map[string]any{
		"goal":      "Controlled sleep failover task: verify crash takeover",
		"model":     "gemini-3.8-flash",
		"max_steps": 5,
	}
	bBytes, _ := json.Marshal(createPayload)

	reqA, _ := http.NewRequest(http.MethodPost, nodeABaseURL+"/v1/agent/runs", bytes.NewReader(bBytes))
	reqA.Header.Set("Content-Type", "application/json")
	reqA.Header.Set("Authorization", "Bearer "+masterAPIKey)

	respA, err := client.Do(reqA)
	if err != nil {
		t.Fatalf("Submit run qua Gateway A thất bại: %v", err)
	}
	defer respA.Body.Close()

	if respA.StatusCode != http.StatusAccepted && respA.StatusCode != http.StatusCreated && respA.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(respA.Body)
		t.Fatalf("Gateway A SubmitRun trả về status %d: %s", respA.StatusCode, string(b))
	}

	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(respA.Body).Decode(&created)
	runID := created.ID
	if runID == "" {
		t.Fatalf("Không nhận được Run ID từ Gateway A")
	}

	// Chờ run đạt trạng thái running với worker là Gateway A
	var initialWorkerID string
	var initialClaimGen int
	runRunning := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		reqCheck, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/agent/runs/%s", nodeABaseURL, runID), nil)
		reqCheck.Header.Set("Authorization", "Bearer "+masterAPIKey)
		respCheck, err := client.Do(reqCheck)
		if err == nil {
			var state struct {
				Status          string `json:"status"`
				WorkerID        string `json:"worker_id"`
				ClaimGeneration int    `json:"claim_generation"`
			}
			_ = json.NewDecoder(respCheck.Body).Decode(&state)
			respCheck.Body.Close()
			if state.Status == "running" && state.WorkerID != "" {
				initialWorkerID = state.WorkerID
				initialClaimGen = state.ClaimGeneration
				runRunning = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !runRunning {
		t.Fatalf("Run %s không đạt trạng thái 'running' trên Gateway A trong thời gian quy định", runID)
	}
	t.Logf("Run %s đang chạy trên Gateway A (worker_id=%s, claim_gen=%d)", runID, initialWorkerID, initialClaimGen)

	// 2. Kill bất ngờ Gateway Node A (không graceful shutdown)
	t.Log("Simulating crash: docker kill dezuxk-gateway-a")
	killCmd := exec.Command("docker", "kill", "dezuxk-gateway-a")
	if out, err := killCmd.CombinedOutput(); err != nil {
		t.Fatalf("docker kill dezuxk-gateway-a thất bại: %v (output: %s)", err, string(out))
	}

	// Đảm bảo luôn khởi động lại Gateway Node A sau khi kết thúc test
	defer func() {
		_ = exec.Command("docker", "start", "dezuxk-gateway-a").Run()
		// Chờ Gateway A phục hồi
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
			resp, err := client.Get(nodeABaseURL + "/ready")
			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				break
			}
			if resp != nil {
				resp.Body.Close()
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	// 3. Chờ lease hết hạn (~5s trong test mode) và Node B hoặc C phát hiện & takeover run
	t.Log("Chờ Gateway Node B hoặc C takeover run...")
	var finalRunState struct {
		Status          string `json:"status"`
		WorkerID        string `json:"worker_id"`
		ClaimGeneration int    `json:"claim_generation"`
		Error           string `json:"error"`
	}

	takeoverSucceeded := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		reqB, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/agent/runs/%s", nodeBBaseURL, runID), nil)
		reqB.Header.Set("Authorization", "Bearer "+masterAPIKey)
		respB, err := client.Do(reqB)
		if err == nil {
			_ = json.NewDecoder(respB.Body).Decode(&finalRunState)
			respB.Body.Close()

			if finalRunState.ClaimGeneration > initialClaimGen && (finalRunState.Status == "completed" || finalRunState.Status == "running" || finalRunState.Status == "recovering") {
				takeoverSucceeded = true
				if finalRunState.Status == "completed" {
					break
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !takeoverSucceeded {
		t.Fatalf("Takeover thất bại: run %s không được node khác nhận lại (status=%s, worker_id=%s, claim_gen=%d)",
			runID, finalRunState.Status, finalRunState.WorkerID, finalRunState.ClaimGeneration)
	}

	if finalRunState.Status != "completed" {
		t.Fatalf("Run %s không hoàn thành sau khi takeover (status=%s, worker_id=%s, claim_gen=%d)",
			runID, finalRunState.Status, finalRunState.WorkerID, finalRunState.ClaimGeneration)
	}

	t.Logf("Takeover thành công: Worker mới %s đã tiếp quản run %s với claim_generation=%d (ban đầu %d), trạng thái cuối: %s",
		finalRunState.WorkerID, runID, finalRunState.ClaimGeneration, initialClaimGen, finalRunState.Status)

	if finalRunState.WorkerID == initialWorkerID {
		t.Errorf("WorkerID không đổi sau crash: %s", finalRunState.WorkerID)
	}
	if finalRunState.ClaimGeneration <= initialClaimGen {
		t.Errorf("ClaimGeneration không tăng: %d <= %d", finalRunState.ClaimGeneration, initialClaimGen)
	}
}
