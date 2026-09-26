package services

import (
	"container/list"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
)

// CacheEntry đại diện cho một mục phản hồi được lưu trong RAM
type CacheEntry struct {
	Key         string
	Payload     []byte
	ContentType string
	Headers     map[string]string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// CacheStats thống kê hiệu năng bộ nhớ đệm
type CacheStats struct {
	Hits         uint64  `json:"hits"`
	Misses       uint64  `json:"misses"`
	TotalEntries int     `json:"total_entries"`
	MaxEntries   int     `json:"max_entries"`
	HitRatio     float64 `json:"hit_ratio"`
}

type lruItem struct {
	key   string
	entry *CacheEntry
}

// ResponseCache là tầng bộ nhớ đệm phản hồi an toàn luồng với chính sách LRU và TTL
type ResponseCache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element
	evictList  *list.List
	maxEntries int
	defaultTTL time.Duration
	methods    map[string]bool
	hits       uint64
	misses     uint64
}

// NewResponseCache khởi tạo bộ nhớ đệm với cấu hình từ YAML
func NewResponseCache(cfg config.CacheConfig) *ResponseCache {
	maxEntries := cfg.GetMaxEntries()
	if maxEntries <= 0 {
		maxEntries = 10000
	}

	ttl := cfg.GetTTL()
	if ttl <= 0 {
		ttl = 3600 * time.Second
	}

	methodsMap := make(map[string]bool)
	for _, m := range cfg.Methods {
		methodsMap[strings.ToLower(strings.TrimSpace(m))] = true
	}

	return &ResponseCache{
		entries:    make(map[string]*list.Element),
		evictList:  list.New(),
		maxEntries: maxEntries,
		defaultTTL: ttl,
		methods:    methodsMap,
	}
}

// SupportsMethod kiểm tra phương thức có được bật cache không
func (c *ResponseCache) SupportsMethod(method string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.methods[strings.ToLower(strings.TrimSpace(method))]
}

// Get truy xuất dữ liệu từ RAM. Trả về entry và cờ true nếu HIT, ngược lại MISS
func (c *ResponseCache) Get(key string) (*CacheEntry, bool) {
	if c == nil || key == "" {
		return nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	elem, exists := c.entries[key]
	if !exists {
		c.misses++
		return nil, false
	}

	item := elem.Value.(*lruItem)
	// Kiểm tra TTL hết hạn
	if time.Now().After(item.entry.ExpiresAt) {
		c.removeElement(elem)
		c.misses++
		return nil, false
	}

	// LRU: Đưa phần tử vừa dùng lên đầu danh sách
	c.evictList.MoveToFront(elem)
	c.hits++

	// Trả về bản sao để bảo vệ tính bất biến
	entryCopy := *item.entry
	return &entryCopy, true
}

// Set lưu phản hồi vào RAM. Tự động evict phần tử cũ nhất nếu vượt quá max_entries
func (c *ResponseCache) Set(key string, payload []byte, contentType string, headers map[string]string) {
	c.SetWithTTL(key, payload, contentType, headers, 0)
}

// SetWithTTL lưu phản hồi vào RAM kèm thời gian sống tùy biến (nếu ttl <= 0 dùng defaultTTL)
func (c *ResponseCache) SetWithTTL(key string, payload []byte, contentType string, headers map[string]string, ttl time.Duration) {
	if c == nil || key == "" || len(payload) == 0 {
		return
	}

	if ttl <= 0 {
		ttl = c.defaultTTL
	}

	now := time.Now()
	entry := &CacheEntry{
		Key:         key,
		Payload:     payload,
		ContentType: contentType,
		Headers:     headers,
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Nếu key đã tồn tại, cập nhật entry và chuyển lên đầu LRU
	if elem, exists := c.entries[key]; exists {
		c.evictList.MoveToFront(elem)
		elem.Value.(*lruItem).entry = entry
		return
	}

	// Nếu số lượng đạt giới hạn maxEntries, loại bỏ phần tử cũ nhất ở cuối LRU
	for c.evictList.Len() >= c.maxEntries && c.evictList.Len() > 0 {
		oldest := c.evictList.Back()
		if oldest != nil {
			c.removeElement(oldest)
		}
	}

	// Thêm mục mới vào đầu LRU
	item := &lruItem{key: key, entry: entry}
	elem := c.evictList.PushFront(item)
	c.entries[key] = elem
}

func (c *ResponseCache) removeElement(elem *list.Element) {
	c.evictList.Remove(elem)
	item := elem.Value.(*lruItem)
	delete(c.entries, item.key)
}

// Invalidate xóa các entry bắt đầu bằng prefix
func (c *ResponseCache) Invalidate(keyPrefix string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	var toRemove []*list.Element
	for elem := c.evictList.Front(); elem != nil; elem = elem.Next() {
		item := elem.Value.(*lruItem)
		if strings.HasPrefix(item.key, keyPrefix) {
			toRemove = append(toRemove, elem)
		}
	}
	for _, elem := range toRemove {
		c.removeElement(elem)
	}
}

// Clear xóa sạch toàn bộ cache
func (c *ResponseCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*list.Element)
	c.evictList.Init()
}

// Stats cung cấp chỉ số thống kê của cache
func (c *ResponseCache) Stats() CacheStats {
	if c == nil {
		return CacheStats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	total := c.hits + c.misses
	ratio := 0.0
	if total > 0 {
		ratio = (float64(c.hits) / float64(total)) * 100.0
	}

	return CacheStats{
		Hits:         c.hits,
		Misses:       c.misses,
		TotalEntries: len(c.entries),
		MaxEntries:   c.maxEntries,
		HitRatio:     ratio,
	}
}

// GenerateChatCacheKey tính toán khóa băm SHA-256 từ các tham số hội thoại
// Thuật toán: model + messages + temperature + system_prompt
func GenerateChatCacheKey(model string, messages []domain.OpenAIMessage, temperature float64, systemPrompt string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(model))
	b.WriteString("|")
	b.WriteString(strings.TrimSpace(systemPrompt))
	b.WriteString("|")
	b.WriteString(fmt.Sprintf("%.4f", temperature))
	b.WriteString("|")

	for _, m := range messages {
		b.WriteString(strings.TrimSpace(m.Role))
		b.WriteString(":")
		b.WriteString(strings.TrimSpace(m.Content))
		for _, part := range m.ContentParts {
			b.WriteString(";")
			b.WriteString(part.Type)
			b.WriteString(";")
			b.WriteString(part.Text)
			if part.ImageURL != nil {
				b.WriteString(";")
				b.WriteString(part.ImageURL.URL)
			}
		}
		b.WriteString("\n")
	}

	hash := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("chat:%x", hash)
}

// GenerateCreditsCacheKey tính toán khóa băm SHA-256 cho số dư tín dụng Flow
func GenerateCreditsCacheKey(accountID string) string {
	raw := fmt.Sprintf("credits|%s", strings.TrimSpace(accountID))
	hash := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("credits:%x", hash)
}
