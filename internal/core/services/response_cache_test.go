package services_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

func TestResponseCache_KeyGeneration(t *testing.T) {
	messages1 := []domain.OpenAIMessage{
		{Role: "user", Content: "Xin chào Gemini!"},
	}
	messages2 := []domain.OpenAIMessage{
		{Role: "user", Content: "Xin chào Gemini!"},
	}
	messages3 := []domain.OpenAIMessage{
		{Role: "user", Content: "Khác nội dung"},
	}

	key1 := services.GenerateChatCacheKey("gemini-2.5-flash", messages1, 0.7, "You are a helpful assistant", false, false, false)
	key2 := services.GenerateChatCacheKey("gemini-2.5-flash", messages2, 0.7, "You are a helpful assistant", false, false, false)
	key3 := services.GenerateChatCacheKey("gemini-2.5-flash", messages3, 0.7, "You are a helpful assistant", false, false, false)
	key4 := services.GenerateChatCacheKey("gemini-2.5-pro", messages1, 0.7, "You are a helpful assistant", false, false, false)
	key5 := services.GenerateChatCacheKey("gemini-2.5-flash", messages1, 0.2, "You are a helpful assistant", false, false, false)
	keyThinking := services.GenerateChatCacheKey("gemini-2.5-flash", messages1, 0.7, "You are a helpful assistant", true, false, false)
	keyGrounding := services.GenerateChatCacheKey("gemini-2.5-flash", messages1, 0.7, "You are a helpful assistant", false, true, false)
	keyCode := services.GenerateChatCacheKey("gemini-2.5-flash", messages1, 0.7, "You are a helpful assistant", false, false, true)

	if key1 != key2 {
		t.Errorf("expected identical keys for identical chat params, got %s vs %s", key1, key2)
	}
	if key1 == key3 {
		t.Error("expected different keys when messages differ")
	}
	if key1 == key4 {
		t.Error("expected different keys when model differs")
	}
	if key1 == key5 {
		t.Error("expected different keys when temperature differs")
	}
	if key1 == keyThinking {
		t.Error("expected different keys when thinking differs")
	}
	if key1 == keyGrounding {
		t.Error("expected different keys when grounding differs")
	}
	if key1 == keyCode {
		t.Error("expected different keys when codeInterpreter differs")
	}

	// Verify key format includes SHA-256
	if len(key1) < 64 {
		t.Errorf("expected key to contain SHA-256 hash, length was %d", len(key1))
	}
}

func TestResponseCache_SetAndGet(t *testing.T) {
	enabled := true
	cfg := config.CacheConfig{
		Enabled:    &enabled,
		MaxEntries: 100,
		TTLSeconds: 60,
		Methods:    []string{"chat"},
	}

	cache := services.NewResponseCache(cfg)

	key := "test:chat:hash123"
	payload := []byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"Xin chào!"}}]}`)
	contentType := "application/json"
	headers := map[string]string{"Content-Type": "application/json"}

	// Ban đầu chưa có trong cache
	entry, found := cache.Get(key)
	if found || entry != nil {
		t.Fatal("expected cache miss on empty cache")
	}

	// Lưu vào cache
	start := time.Now()
	cache.Set(key, payload, contentType, headers)

	// Lấy lại từ cache (phải < 5ms)
	hitStart := time.Now()
	entry, found = cache.Get(key)
	latency := time.Since(hitStart)

	if !found || entry == nil {
		t.Fatal("expected cache hit after Set")
	}
	if string(entry.Payload) != string(payload) {
		t.Errorf("expected payload %s, got %s", payload, entry.Payload)
	}
	if latency >= 5*time.Millisecond {
		t.Errorf("expected cache hit latency < 5ms, got %v", latency)
	}

	stats := cache.Stats()
	if stats.Hits != 1 {
		t.Errorf("expected 1 hit, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("expected 1 miss, got %d", stats.Misses)
	}
	_ = start
}

func TestResponseCache_TTLExpiration(t *testing.T) {
	enabled := true
	cfg := config.CacheConfig{
		Enabled:    &enabled,
		MaxEntries: 100,
		TTLSeconds: 1, // 1 giây
		Methods:    []string{"chat"},
	}

	cache := services.NewResponseCache(cfg)
	key := "test:expire"
	cache.Set(key, []byte("val"), "text/plain", nil)

	// Lần 1: Còn hạn
	_, found := cache.Get(key)
	if !found {
		t.Fatal("expected cache hit before expiration")
	}

	// Đợi hết TTL
	time.Sleep(1100 * time.Millisecond)

	// Lần 2: Đã hết hạn
	_, found = cache.Get(key)
	if found {
		t.Fatal("expected cache miss after TTL expiration")
	}
}

func TestResponseCache_LRUEviction(t *testing.T) {
	enabled := true
	cfg := config.CacheConfig{
		Enabled:    &enabled,
		MaxEntries: 3, // Giới hạn chỉ 3 mục
		TTLSeconds: 3600,
		Methods:    []string{"chat"},
	}

	cache := services.NewResponseCache(cfg)

	cache.Set("k1", []byte("v1"), "text/plain", nil)
	cache.Set("k2", []byte("v2"), "text/plain", nil)
	cache.Set("k3", []byte("v3"), "text/plain", nil)

	// Truy cập k1 để k1 thành recently used nhất, k2 thành cũ nhất
	_, _ = cache.Get("k1")

	// Thêm k4 -> k2 phải bị evict
	cache.Set("k4", []byte("v4"), "text/plain", nil)

	if _, found := cache.Get("k2"); found {
		t.Error("expected k2 to be evicted due to LRU policy")
	}
	if _, found := cache.Get("k1"); !found {
		t.Error("expected k1 to still be present (recently accessed)")
	}
	if _, found := cache.Get("k3"); !found {
		t.Error("expected k3 to still be present")
	}
	if _, found := cache.Get("k4"); !found {
		t.Error("expected k4 to be present")
	}
}

func TestResponseCache_Concurrency(t *testing.T) {
	enabled := true
	cfg := config.CacheConfig{
		Enabled:    &enabled,
		MaxEntries: 100,
		TTLSeconds: 300,
		Methods:    []string{"chat"},
	}

	cache := services.NewResponseCache(cfg)
	var wg sync.WaitGroup

	// Chạy song song 50 goroutines ghi và đọc
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			k := fmt.Sprintf("k-%d", id%10)
			cache.Set(k, fmt.Appendf(nil, "val-%d", id), "text/plain", nil)
			_, _ = cache.Get(k)
		}(i)
	}

	wg.Wait()
	stats := cache.Stats()
	if stats.Hits+stats.Misses == 0 {
		t.Error("expected non-zero cache operations in concurrency test")
	}
}

// Kiểm tra SHA-256 helper
func BenchmarkGenerateChatCacheKey(b *testing.B) {
	messages := []domain.OpenAIMessage{
		{Role: "system", Content: "You are an AI coding assistant."},
		{Role: "user", Content: "Write an in-memory response cache in Go."},
	}
	b.ResetTimer()
	for b.Loop() {
		_ = services.GenerateChatCacheKey("gemini-2.5-flash", messages, 0.7, "system prompt", false, false, false)
	}
}
