package google

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

var (
	digitOnlyRegex            = regexp.MustCompile(`^\d+$`)
	imagePlaceholderRegex     = regexp.MustCompile(`https?://googleusercontent\.com/image_generation_content/[a-zA-Z0-9_]+`)
	cardContentRegex          = regexp.MustCompile(`https?://googleusercontent\.com/card_content/[a-zA-Z0-9_]+`)
	shoppingContentRegex      = regexp.MustCompile(`https?://googleusercontent\.com/shopping_content/[a-zA-Z0-9_\-]+`)
	markdownShoppingLinkRegex = regexp.MustCompile(`\[([^\]]+)\]\(https?://googleusercontent\.com/shopping_content/[^)]+\)`)
	youtubeContentRegex       = regexp.MustCompile(`https?://googleusercontent\.com/youtube_content/[a-zA-Z0-9_]+`)
	immersiveEntryChipRegex   = regexp.MustCompile(`https?://googleusercontent\.com/immersive_entry_chip/[a-zA-Z0-9_]+`)
	imageAgentTagRegex        = regexp.MustCompile(`https?://googleusercontent\.com/image_agent_tag_[a-zA-Z0-9_]+`)
	lmdxImageRegex            = regexp.MustCompile(`https?://googleusercontent\.com/lmdx_image/[a-zA-Z0-9_]+`)
	productComparisonRegex    = regexp.MustCompile(`(?s)<ProductComparisonTable(?:\s+title="([^"]*)")?[^>]*>(.*?)</ProductComparisonTable>`)
	xmlImageTagRegex          = regexp.MustCompile(`<Image\s+[^>]*src="([^"]+)"[^>]*\/?>`)
	entityCardRegex           = regexp.MustCompile(`(?s)<EntityCard\b(?:[^>]*?/>|[^>]*?>.*?</EntityCard>)`)
	entityCardTitleRegex      = regexp.MustCompile(`title="([^"]+)"`)
)

// FormatEntityCard chuyển đổi thẻ <EntityCard .../> sang format trích dẫn sản phẩm Markdown
func FormatEntityCard(text string) string {
	if !strings.Contains(text, "<EntityCard") {
		return text
	}
	return entityCardRegex.ReplaceAllStringFunc(text, func(m string) string {
		if sub := entityCardTitleRegex.FindStringSubmatch(m); len(sub) > 1 {
			title := strings.TrimSpace(sub[1])
			if title != "" {
				return "> 📦 **" + title + "**\n\n"
			}
		}
		return ""
	})
}

// FormatProductComparisonTable chuyển đổi thẻ <ProductComparisonTable> sang bảng Markdown GFM
func FormatProductComparisonTable(text string) string {
	if !strings.Contains(text, "<ProductComparisonTable") {
		return text
	}
	return productComparisonRegex.ReplaceAllStringFunc(text, func(m string) string {
		submatches := productComparisonRegex.FindStringSubmatch(m)
		if len(submatches) < 3 {
			return m
		}
		title := strings.TrimSpace(submatches[1])
		rawJSON := strings.TrimSpace(submatches[2])

		var products []map[string]interface{}
		if err := json.Unmarshal([]byte(rawJSON), &products); err != nil || len(products) == 0 {
			return m
		}

		attrOrder := extractJSONKeysInOrder(rawJSON)

		productNames := make([]string, len(products))
		for i, p := range products {
			name := fmt.Sprintf("Sản phẩm %d", i+1)
			if dn, ok := p["display_name"].(string); ok && dn != "" {
				name = dn
			} else if n, ok := p["name"].(string); ok && n != "" {
				name = n
			} else if t, ok := p["title"].(string); ok && t != "" {
				name = t
			}
			productNames[i] = name
		}

		var sb strings.Builder
		if title != "" {
			sb.WriteString("### ")
			sb.WriteString(title)
			sb.WriteString("\n\n")
		}

		// Header
		sb.WriteString("| Thông số |")
		for _, pn := range productNames {
			sb.WriteString(" ")
			sb.WriteString(pn)
			sb.WriteString(" |")
		}
		sb.WriteString("\n| :--- |")
		for range productNames {
			sb.WriteString(" :--- |")
		}
		sb.WriteString("\n")

		// Rows
		for _, attr := range attrOrder {
			if attr == "display_name" || attr == "product_id" || attr == "name" || attr == "title" {
				continue
			}
			sb.WriteString("| **")
			sb.WriteString(attr)
			sb.WriteString("** |")
			for _, p := range products {
				valStr := ""
				if val, exists := p[attr]; exists && val != nil {
					valStr = fmt.Sprintf("%v", val)
					valStr = strings.ReplaceAll(valStr, "\n", " ")
					valStr = strings.ReplaceAll(valStr, "|", "\\|")
				}
				sb.WriteString(" ")
				sb.WriteString(valStr)
				sb.WriteString(" |")
			}
			sb.WriteString("\n")
		}

		return strings.TrimSpace(sb.String())
	})
}

func extractJSONKeysInOrder(rawJSON string) []string {
	re := regexp.MustCompile(`"([^"\\]*(?:\\.[^"\\]*)*)"\s*:`)
	matches := re.FindAllStringSubmatch(rawJSON, -1)
	seen := make(map[string]bool)
	var keys []string
	for _, m := range matches {
		if len(m) > 1 {
			k := m[1]
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// CleanInternalPlaceholders dọn dẹp các placeholder nội bộ và chuẩn hóa bảng
func CleanInternalPlaceholders(text string) string {
	text = FormatProductComparisonTable(text)
	text = FormatEntityCard(text)
	text = markdownShoppingLinkRegex.ReplaceAllString(text, "**$1**")
	text = shoppingContentRegex.ReplaceAllString(text, "")
	text = youtubeContentRegex.ReplaceAllString(text, "")
	text = cardContentRegex.ReplaceAllString(text, "")
	text = imageAgentTagRegex.ReplaceAllString(text, "")
	text = lmdxImageRegex.ReplaceAllString(text, "")
	text = immersiveEntryChipRegex.ReplaceAllString(text, "")
	text = xmlImageTagRegex.ReplaceAllString(text, "")

	emptyLinesRegex := regexp.MustCompile(`\n{3,}`)
	text = emptyLinesRegex.ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}

// ErrStreamIdleTimeout báo hiệu luồng stream không nhận được byte mới từ Google upstream quá khoảng thời gian idle
var ErrStreamIdleTimeout = domain.ErrStreamIdleTimeout

type streamIdleTimeoutKey struct{}
type streamMetricsTrackerKey struct{}

// WithStreamIdleTimeout gắn idle timeout của stream vào context
func WithStreamIdleTimeout(ctx context.Context, d time.Duration) context.Context {
	if d <= 0 {
		return ctx
	}
	return context.WithValue(ctx, streamIdleTimeoutKey{}, d)
}

// StreamIdleTimeoutFromContext trích xuất idle timeout của stream từ context, nếu không có trả về fallback
func StreamIdleTimeoutFromContext(ctx context.Context, fallback time.Duration) time.Duration {
	if ctx == nil {
		return fallback
	}
	if d, ok := ctx.Value(streamIdleTimeoutKey{}).(time.Duration); ok && d > 0 {
		return d
	}
	return fallback
}

// StreamMetricsTracker theo dõi chi tiết hiệu năng và trạng thái của luồng stream
type StreamMetricsTracker struct {
	mu                  sync.RWMutex
	StartTime           time.Time
	TimeToFirstByte     time.Duration
	TimeToFirstToken    time.Duration
	LastByteReceivedAt  time.Time
	LastTokenReceivedAt time.Time
	BytesRead           int64
	TokensEmitted       int64
	TimeoutKind         string // "idle", "total", "client_disconnect", "parser_error", ""
	FinishReason        string
	UpstreamEOF         bool
	ClientDisconnect    bool
}

func NewStreamMetricsTracker() *StreamMetricsTracker {
	now := time.Now()
	return &StreamMetricsTracker{
		StartTime:          now,
		LastByteReceivedAt: now,
	}
}

func (s *StreamMetricsTracker) RecordByte(n int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.TimeToFirstByte == 0 {
		s.TimeToFirstByte = now.Sub(s.StartTime)
	}
	s.LastByteReceivedAt = now
	s.BytesRead += int64(n)
}

func (s *StreamMetricsTracker) RecordToken() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.TimeToFirstToken == 0 {
		s.TimeToFirstToken = now.Sub(s.StartTime)
	}
	s.LastTokenReceivedAt = now
	s.TokensEmitted++
}

func (s *StreamMetricsTracker) SetTimeoutKind(kind string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.TimeoutKind == "" {
		s.TimeoutKind = kind
	}
}

func (s *StreamMetricsTracker) SetFinishReason(reason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FinishReason = reason
}

func (s *StreamMetricsTracker) SetUpstreamEOF(eof bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.UpstreamEOF = eof
}

func (s *StreamMetricsTracker) SetClientDisconnect(disc bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ClientDisconnect = disc
	if disc && s.TimeoutKind == "" {
		s.TimeoutKind = "client_disconnect"
	}
}

// StreamMetricsSnapshot là bản sao chép chỉ đọc của StreamMetricsTracker không chứa mutex
type StreamMetricsSnapshot struct {
	StartTime           time.Time
	TimeToFirstByte     time.Duration
	TimeToFirstToken    time.Duration
	LastByteReceivedAt  time.Time
	LastTokenReceivedAt time.Time
	BytesRead           int64
	TokensEmitted       int64
	TimeoutKind         string
	FinishReason        string
	UpstreamEOF         bool
	ClientDisconnect    bool
}

func (s *StreamMetricsTracker) Snapshot() StreamMetricsSnapshot {
	if s == nil {
		return StreamMetricsSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return StreamMetricsSnapshot{
		StartTime:           s.StartTime,
		TimeToFirstByte:     s.TimeToFirstByte,
		TimeToFirstToken:    s.TimeToFirstToken,
		LastByteReceivedAt:  s.LastByteReceivedAt,
		LastTokenReceivedAt: s.LastTokenReceivedAt,
		BytesRead:           s.BytesRead,
		TokensEmitted:       s.TokensEmitted,
		TimeoutKind:         s.TimeoutKind,
		FinishReason:        s.FinishReason,
		UpstreamEOF:         s.UpstreamEOF,
		ClientDisconnect:    s.ClientDisconnect,
	}
}

// WithStreamMetricsTracker gắn StreamMetricsTracker vào context
func WithStreamMetricsTracker(ctx context.Context, tracker *StreamMetricsTracker) context.Context {
	if tracker == nil {
		return ctx
	}
	return context.WithValue(ctx, streamMetricsTrackerKey{}, tracker)
}

// StreamMetricsTrackerFromContext lấy StreamMetricsTracker từ context
func StreamMetricsTrackerFromContext(ctx context.Context) *StreamMetricsTracker {
	if ctx == nil {
		return nil
	}
	if t, ok := ctx.Value(streamMetricsTrackerKey{}).(*StreamMetricsTracker); ok {
		return t
	}
	return nil
}

// IdleTimeoutReader bọc io.Reader/io.Closer để phát hiện stream idle và ngắt kết nối an toàn
type IdleTimeoutReader struct {
	r           io.Reader
	closer      io.Closer
	idleTimeout time.Duration
	tracker     *StreamMetricsTracker
	timer       *time.Timer
	timerMu     sync.Mutex
	timedOut    atomic.Bool
	closed      atomic.Bool
}

func NewIdleTimeoutReader(r io.Reader, closer io.Closer, idleTimeout time.Duration, tracker *StreamMetricsTracker) *IdleTimeoutReader {
	if idleTimeout <= 0 {
		idleTimeout = 90 * time.Second
	}
	if tracker == nil {
		tracker = NewStreamMetricsTracker()
	} else if tracker.StartTime.IsZero() {
		tracker.StartTime = time.Now()
		tracker.LastByteReceivedAt = time.Now()
	}
	return &IdleTimeoutReader{
		r:           r,
		closer:      closer,
		idleTimeout: idleTimeout,
		tracker:     tracker,
	}
}

func (itr *IdleTimeoutReader) Read(p []byte) (int, error) {
	if itr.timedOut.Load() {
		return 0, ErrStreamIdleTimeout
	}
	if itr.closed.Load() {
		return 0, io.EOF
	}

	itr.timerMu.Lock()
	if itr.timer == nil {
		itr.timer = time.AfterFunc(itr.idleTimeout, func() {
			itr.timedOut.Store(true)
			if itr.tracker != nil {
				itr.tracker.SetTimeoutKind("idle")
			}
			if itr.closer != nil {
				_ = itr.closer.Close()
			}
		})
	} else {
		itr.timer.Reset(itr.idleTimeout)
	}
	itr.timerMu.Unlock()

	n, err := itr.r.Read(p)

	itr.timerMu.Lock()
	if itr.timer != nil {
		itr.timer.Stop()
	}
	itr.timerMu.Unlock()

	if itr.timedOut.Load() {
		return n, ErrStreamIdleTimeout
	}

	if n > 0 && itr.tracker != nil {
		itr.tracker.RecordByte(n)
	}

	return n, err
}

func (itr *IdleTimeoutReader) Close() error {
	itr.closed.Store(true)
	itr.timerMu.Lock()
	if itr.timer != nil {
		itr.timer.Stop()
	}
	itr.timerMu.Unlock()
	if itr.closer != nil {
		return itr.closer.Close()
	}
	return nil
}

type StreamHandler struct {
	bufferSize int
}

func NewStreamHandler(bufferSize int) *StreamHandler {
	if bufferSize <= 0 {
		bufferSize = 64 * 1024
	}
	return &StreamHandler{bufferSize: bufferSize}
}

// ProcessWrbFrStream giải mã luồng chunked từ Google và gọi callback onDelta khi có token mới
func (h *StreamHandler) ProcessWrbFrStream(
	ctx context.Context,
	body io.ReadCloser,
	onDelta func(delta string, convID string) error,
) error {
	defer body.Close()

	tracker := StreamMetricsTrackerFromContext(ctx)
	if tracker == nil {
		tracker = NewStreamMetricsTracker()
	}
	idleTimeout := StreamIdleTimeoutFromContext(ctx, 90*time.Second)
	idleReader := NewIdleTimeoutReader(body, body, idleTimeout, tracker)

	reader := bufio.NewReaderSize(idleReader, h.bufferSize)
	var conversationID string
	var lastFullText string

	for {
		select {
		case <-ctx.Done():
			tracker.SetClientDisconnect(true)
			return ctx.Err()
		default:
		}

		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineStr := strings.TrimSpace(string(line))

			// Bỏ qua dòng rỗng, XSSI prefix và dòng byte length
			if lineStr == "" || strings.HasPrefix(lineStr, ")]}'") || digitOnlyRegex.MatchString(lineStr) {
				continue
			}

			// Chỉ xử lý các dòng là mảng JSON hợp lệ
			if strings.HasPrefix(lineStr, "[") && strings.HasSuffix(lineStr, "]") {
				currentText, cID := parseEnvelopeChunk(lineStr)
				if cID != "" {
					conversationID = cID
				}

				if currentText != "" && currentText != lastFullText {
					effectiveText := currentText
					if strings.Contains(effectiveText, "<ProductComparisonTable") && !strings.Contains(effectiveText, "</ProductComparisonTable>") {
						effectiveText = effectiveText[:strings.Index(effectiveText, "<ProductComparisonTable")]
					}
					if effectiveText != "" && effectiveText != lastFullText {
						delta := ComputeDelta(effectiveText, lastFullText)
						lastFullText = effectiveText

						if delta != "" {
							tracker.RecordToken()
							if err := onDelta(delta, conversationID); err != nil {
								return err
							}
						}
					}
				}
			}
		}

		if err != nil {
			if err == io.EOF {
				tracker.SetUpstreamEOF(true)
				break
			}
			if errors.Is(err, ErrStreamIdleTimeout) || err == ErrStreamIdleTimeout {
				tracker.SetTimeoutKind("idle")
				return err
			}
			return err
		}
	}

	return nil
}

type StreamChunkMeta struct {
	Text            string
	ConversationID  string
	ResponseID      string
	ChoiceID        string
	ThinkingContent string
	IsThinking      bool
	ThinkingBlocks  []domain.ThoughtBlock
	Grounding       *domain.GroundingMetadata
	CodeExecutions  []domain.CodeExecution
	MediaURLs       []string
	Drafts          []string
	FinishReason    string
}

func parseEnvelopeChunk(rawLine string) (text string, convID string) {
	meta := ParseEnvelopeChunk(rawLine)
	return meta.Text, meta.ConversationID
}

// ParseEnvelopeChunk bóc tách đầy đủ siêu dữ liệu từ dòng wrb.fr theo chuẩn docs-2
func ParseEnvelopeChunk(rawLine string) StreamChunkMeta {
	var meta StreamChunkMeta
	var envelope [][]interface{}
	if err := json.Unmarshal([]byte(rawLine), &envelope); err != nil {
		return meta
	}

	for _, item := range envelope {
		if len(item) >= 3 && item[0] == "wrb.fr" {
			innerStr, ok := item[2].(string)
			if !ok || innerStr == "" {
				continue
			}

			var inner []interface{}
			if err := json.Unmarshal([]byte(innerStr), &inner); err != nil {
				continue
			}
			meta.MediaURLs = append(meta.MediaURLs, mediaURLsIn(inner)...)

			// inner[1]: metadata chứa [c_id, r_id, ...]
			if len(inner) > 1 && inner[1] != nil {
				if metaArray, ok := inner[1].([]interface{}); ok {
					if len(metaArray) > 0 {
						if id, ok := metaArray[0].(string); ok && strings.HasPrefix(id, "c_") {
							meta.ConversationID = id
						}
					}
					if len(metaArray) > 1 {
						if rID, ok := metaArray[1].(string); ok && strings.HasPrefix(rID, "r_") {
							meta.ResponseID = rID
						}
					}
				}
			}

			// inner[4]: mảng candidates [ [rc_id, [text], ..., [thinking]] ]
			if len(inner) > 4 && inner[4] != nil {
				if candidates, ok := inner[4].([]interface{}); ok && len(candidates) > 0 {
					for _, candItem := range candidates {
						if candArr, ok := candItem.([]interface{}); ok && len(candArr) > 1 && candArr[1] != nil {
							if tp, ok := candArr[1].([]interface{}); ok && len(tp) > 0 {
								var candParts []string
								for _, p := range tp {
									if s, ok := p.(string); ok && s != "" {
										candParts = append(candParts, s)
									}
								}
								if len(candParts) > 0 {
									dText := CleanInternalPlaceholders(strings.Join(candParts, "\n\n"))
									if dText != "" {
										meta.Drafts = append(meta.Drafts, dText)
									}
								}
							}
						}
					}

					if candidate, ok := candidates[0].([]interface{}); ok {
						// candidate[0]: choice id "rc_..."
						if len(candidate) > 0 {
							if rcID, ok := candidate[0].(string); ok && strings.HasPrefix(rcID, "rc_") {
								meta.ChoiceID = rcID
							}
						}

						// candidate[1]: text parts
						if len(candidate) > 1 && candidate[1] != nil {
							if textParts, ok := candidate[1].([]interface{}); ok && len(textParts) > 0 {
								var parts []string
								for _, p := range textParts {
									if s, ok := p.(string); ok && s != "" {
										parts = append(parts, s)
									}
								}
								if len(parts) > 0 {
									meta.Text = strings.Join(parts, "\n\n")
								}
							}
						}

						// Bóc tách candidate[10]: Code Interpreter sandbox
						codeExecs := extractCodeExecutions(candidate)
						if len(codeExecs) > 0 {
							meta.CodeExecutions = append(meta.CodeExecutions, codeExecs...)
						}

						// Bóc tách candidate[12]: Grounding metadata & citations
						grounding := extractGroundingMetadata(candidate)
						if grounding != nil {
							meta.Grounding = grounding
						}

						// Kiểm tra khối thinking blocks (docs-2/gemini/extended_thinking.md)
						for i := 2; i < len(candidate); i++ {
							if candidate[i] == nil {
								continue
							}
							if thoughtList, ok := candidate[i].([]interface{}); ok {
								for _, tItem := range thoughtList {
									if tMap, ok := tItem.(map[string]interface{}); ok {
										if tc, ok := tMap["thought_content"].(string); ok && tc != "" {
											meta.ThinkingContent = tc
											meta.IsThinking = true
											meta.ThinkingBlocks = append(meta.ThinkingBlocks, domain.ThoughtBlock{
												Content:    tc,
												IsThinking: true,
											})
										}
									}
								}
							}
						}

						// Bóc tách hình ảnh sinh ra từ Imagen 3 / Veo
						generatedImgs := extractGeneratedImages(candidate, inner)
						for _, img := range generatedImgs {
							if img.URL != "" {
								meta.MediaURLs = append(meta.MediaURLs, img.URL)
								if img.PlaceholderURL != "" && strings.Contains(meta.Text, img.PlaceholderURL) {
									label := img.FileName
									if label == "" {
										label = "Hình ảnh"
									}
									meta.Text = strings.ReplaceAll(meta.Text, img.PlaceholderURL, fmt.Sprintf("![%s](%s)", label, img.URL))
								}
							}
						}

						// Dọn dẹp các placeholder nội bộ của Google nếu còn sót lại
						if imagePlaceholderRegex.MatchString(meta.Text) {
							if len(generatedImgs) > 0 {
								label := generatedImgs[0].FileName
								if label == "" {
									label = "Hình ảnh"
								}
								meta.Text = imagePlaceholderRegex.ReplaceAllString(meta.Text, fmt.Sprintf("![%s](%s)", label, generatedImgs[0].URL))
							} else {
								meta.Text = imagePlaceholderRegex.ReplaceAllString(meta.Text, "")
							}
						}
						if cardContentRegex.MatchString(meta.Text) {
							meta.Text = cardContentRegex.ReplaceAllString(meta.Text, "")
						}
						// Bóc tách finish_reason từ candidate nếu upstream cung cấp
						for i := 2; i < len(candidate); i++ {
							if s, ok := candidate[i].(string); ok {
								upper := strings.ToUpper(strings.TrimSpace(s))
								if upper == "STOP" || upper == "MAX_TOKENS" || upper == "SAFETY" || upper == "RECITATION" {
									meta.FinishReason = strings.ToLower(upper)
									if meta.FinishReason == "max_tokens" {
										meta.FinishReason = "length"
									}
								}
							}
						}
						meta.Text = CleanInternalPlaceholders(meta.Text)
					}
				}
			}
			meta.MediaURLs = dedupeStrings(meta.MediaURLs)
		}
	}

	return meta
}

func extractCodeExecutions(candidate []interface{}) []domain.CodeExecution {
	if len(candidate) <= 10 || candidate[10] == nil {
		return nil
	}
	execArray, ok := candidate[10].([]interface{})
	if !ok || len(execArray) == 0 {
		return nil
	}

	var results []domain.CodeExecution
	for _, item := range execArray {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		var exec domain.CodeExecution
		if cb, ok := itemMap["code_block"].(map[string]interface{}); ok {
			if lang, ok := cb["language"].(string); ok {
				exec.Language = lang
			}
			if code, ok := cb["code"].(string); ok {
				exec.Code = code
			}
		}
		if res, ok := itemMap["execution_result"].(map[string]interface{}); ok {
			if ec, ok := res["exit_code"].(float64); ok {
				exec.ExitCode = int(ec)
			}
			if dur, ok := res["execution_duration_ms"].(float64); ok {
				exec.ExecutionDurationMs = int64(dur)
			}
			if so, ok := res["stdout"].(string); ok {
				exec.Stdout = so
			}
			if se, ok := res["stderr"].(string); ok {
				exec.Stderr = se
			}
			if imgs, ok := res["output_images"].([]interface{}); ok {
				for _, imgNode := range imgs {
					if imgMap, ok := imgNode.(map[string]interface{}); ok {
						var codeImg domain.CodeImage
						if mt, ok := imgMap["mime_type"].(string); ok {
							codeImg.MimeType = mt
						}
						if data, ok := imgMap["data"].(string); ok {
							codeImg.Data = data
						}
						if fmtStr, ok := imgMap["image_format"].(string); ok && fmtStr == "base64" {
							codeImg.IsBase64 = true
						}
						if strings.HasPrefix(codeImg.Data, "http://") || strings.HasPrefix(codeImg.Data, "https://") {
							codeImg.URL = codeImg.Data
						}
						exec.Images = append(exec.Images, codeImg)
					}
				}
			}
			if files, ok := res["output_files"].([]interface{}); ok {
				for _, fNode := range files {
					if fMap, ok := fNode.(map[string]interface{}); ok {
						var cf domain.CodeFile
						if fn, ok := fMap["file_name"].(string); ok {
							cf.FileName = fn
						}
						if du, ok := fMap["download_url"].(string); ok {
							cf.DownloadURL = du
						}
						if sb, ok := fMap["size_bytes"].(float64); ok {
							cf.SizeBytes = int64(sb)
						}
						exec.Files = append(exec.Files, cf)
					}
				}
			}
		}
		if exec.Code != "" || exec.Stdout != "" || len(exec.Images) > 0 {
			results = append(results, exec)
		}
	}
	return results
}

func extractGroundingMetadata(candidate []interface{}) *domain.GroundingMetadata {
	if len(candidate) <= 12 || candidate[12] == nil {
		return nil
	}
	metaArr, ok := candidate[12].([]interface{})
	if !ok || len(metaArr) == 0 {
		return nil
	}

	gm := &domain.GroundingMetadata{}

	// [0] queries
	if len(metaArr) > 0 && metaArr[0] != nil {
		if qArr, ok := metaArr[0].([]interface{}); ok {
			for _, q := range qArr {
				if qStr, ok := q.(string); ok && qStr != "" {
					gm.SearchQueries = append(gm.SearchQueries, qStr)
				}
			}
		}
	}

	// [2] sources: Array<[index, [uri, title, snippet, favicon_url, domain_display]]>
	if len(metaArr) > 2 && metaArr[2] != nil {
		if sArr, ok := metaArr[2].([]interface{}); ok {
			for _, sNode := range sArr {
				if sItem, ok := sNode.([]interface{}); ok && len(sItem) >= 2 {
					var src domain.GroundingSource
					if idx, ok := sItem[0].(float64); ok {
						src.Index = int(idx)
					}
					if details, ok := sItem[1].([]interface{}); ok {
						if len(details) > 0 {
							src.URL, _ = details[0].(string)
						}
						if len(details) > 1 {
							src.Title, _ = details[1].(string)
						}
						if len(details) > 2 {
							src.Snippet, _ = details[2].(string)
						}
						if len(details) > 3 {
							src.Favicon, _ = details[3].(string)
						}
						if len(details) > 4 {
							src.Domain, _ = details[4].(string)
						}
					}
					if src.URL != "" || src.Title != "" {
						gm.Sources = append(gm.Sources, src)
					}
				}
			}
		}
	}

	// [3] supports: Array<{ segment: { start_index, end_index, text }, grounding_chunk_indices, confidence_scores }>
	if len(metaArr) > 3 && metaArr[3] != nil {
		if supArr, ok := metaArr[3].([]interface{}); ok {
			for _, supNode := range supArr {
				if supMap, ok := supNode.(map[string]interface{}); ok {
					var sup domain.GroundingSupport
					if seg, ok := supMap["segment"].(map[string]interface{}); ok {
						if si, ok := seg["start_index"].(float64); ok {
							sup.StartIndex = int(si)
						}
						if ei, ok := seg["end_index"].(float64); ok {
							sup.EndIndex = int(ei)
						}
						if txt, ok := seg["text"].(string); ok {
							sup.SegmentText = txt
						}
					}
					if gci, ok := supMap["grounding_chunk_indices"].([]interface{}); ok {
						for _, idxVal := range gci {
							if f, ok := idxVal.(float64); ok {
								sup.SourceIndices = append(sup.SourceIndices, int(f))
							}
						}
					}
					if cs, ok := supMap["confidence_scores"].([]interface{}); ok {
						for _, scVal := range cs {
							if f, ok := scVal.(float64); ok {
								sup.Scores = append(sup.Scores, f)
							}
						}
					}
					gm.Supports = append(gm.Supports, sup)
				}
			}
		}
	}

	if len(gm.SearchQueries) == 0 && len(gm.Sources) == 0 && len(gm.Supports) == 0 {
		return nil
	}
	return gm
}

func mediaURLsIn(v any) []string {
	var urls []string
	walkMedia(v, &urls)
	return urls
}

func walkMedia(v any, urls *[]string) {
	if v == nil {
		return
	}
	switch val := v.(type) {
	case string:
		if isAllowedMediaURL(val) {
			*urls = append(*urls, val)
		}
	case []any:
		for _, item := range val {
			walkMedia(item, urls)
		}
	case map[string]any:
		for _, item := range val {
			walkMedia(item, urls)
		}
	}
}

type ExtractedMediaItem struct {
	URL            string
	PlaceholderURL string
	FileName       string
	MimeType       string
	Width          int
	Height         int
	SizeBytes      int64
	TaskID         string
}

var mediaHostAllowList = []string{
	"lh3.googleusercontent.com",
	"storage.googleapis.com",
	"video.google.com",
	"googleusercontent.com",
}

func isAllowedMediaURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return false
	}
	host := parsed.Hostname()
	isAllowedHost := false
	for _, allowed := range mediaHostAllowList {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			isAllowedHost = true
			break
		}
	}
	if !isAllowedHost {
		return false
	}

	// Chỉ coi là tệp truyền thông sinh ra (Generated Media) nếu là:
	// - Imagen 3: Đường dẫn chứa /gg-dl/ hoặc tên file chứa watermarked_
	// - Veo / Video: Tên miền video.google.com
	// Loại bỏ hoàn toàn các asset UI, icon, avatar (như icon đỏ <> của hộp cát Python)
	isGenMedia := strings.Contains(parsed.Path, "/gg-dl/") ||
		strings.Contains(raw, "watermarked_") ||
		strings.Contains(host, "video.google.com")

	return isGenMedia
}

func extractGeneratedImages(candidate []interface{}, inner []interface{}) []ExtractedMediaItem {
	var images []ExtractedMediaItem
	seenURLs := make(map[string]struct{})

	addImg := func(img ExtractedMediaItem) {
		if img.URL == "" {
			return
		}
		if _, exists := seenURLs[img.URL]; exists {
			return
		}
		seenURLs[img.URL] = struct{}{}
		images = append(images, img)
	}

	// 1. Kiểm tra candidate[12][7]: Vị trí chuẩn của Imagen 3 trên Google Gemini
	if len(candidate) > 12 && candidate[12] != nil {
		if arr12, ok := candidate[12].([]interface{}); ok && len(arr12) > 7 && arr12[7] != nil {
			if list7, ok := arr12[7].([]interface{}); ok && len(list7) > 0 {
				if items, ok := list7[0].([]interface{}); ok {
					for _, itemRaw := range items {
						if itemArr, ok := itemRaw.([]interface{}); ok && len(itemArr) >= 2 {
							var img ExtractedMediaItem
							extractFromNestedMetadata(&img, itemArr[0])
							if phArr, ok := itemArr[1].([]interface{}); ok && len(phArr) > 0 {
								if phStr, ok := phArr[0].(string); ok {
									img.PlaceholderURL = strings.TrimSpace(phStr)
								}
							}
							if len(itemArr) > 8 {
								if tid, ok := itemArr[8].(string); ok {
									img.TaskID = tid
								}
							}
							addImg(img)
						}
					}
				}
			}
		}
	}

	// 2. Kiểm tra candidate[4]: Định dạng Image Output ID theo docs-2/gemini/image.md
	if len(candidate) > 4 && candidate[4] != nil {
		if arr4, ok := candidate[4].([]interface{}); ok {
			for _, itemRaw := range arr4 {
				if itemArr, ok := itemRaw.([]interface{}); ok && len(itemArr) >= 2 {
					if u, ok := itemArr[1].(string); ok && isAllowedMediaURL(u) {
						var img ExtractedMediaItem
						img.URL = u
						if len(itemArr) > 3 {
							if w, ok := itemArr[3].(float64); ok {
								img.Width = int(w)
							}
						}
						if len(itemArr) > 4 {
							if h, ok := itemArr[4].(float64); ok {
								img.Height = int(h)
							}
						}
						if len(itemArr) > 5 {
							if mt, ok := itemArr[5].(string); ok {
								img.MimeType = mt
							}
						}
						addImg(img)
					}
				}
			}
		}
	}

	// 3. Fallback: Quét đệ quy nếu chưa tìm thấy
	if len(images) == 0 {
		var recursiveURLs []string
		scanMediaURLs(inner, &recursiveURLs, 0)
		for _, u := range recursiveURLs {
			addImg(ExtractedMediaItem{URL: u})
		}
	}

	return images
}

func extractFromNestedMetadata(img *ExtractedMediaItem, v interface{}) {
	switch val := v.(type) {
	case []interface{}:
		if len(val) >= 4 {
			if u, ok := val[3].(string); ok && isAllowedMediaURL(u) {
				img.URL = u
				if fn, ok := val[2].(string); ok {
					img.FileName = fn
				}
				if len(val) > 11 {
					if mt, ok := val[11].(string); ok {
						img.MimeType = mt
					}
				}
				if len(val) > 15 {
					if dims, ok := val[15].([]interface{}); ok && len(dims) >= 2 {
						if w, ok := dims[0].(float64); ok {
							img.Width = int(w)
						}
						if h, ok := dims[1].(float64); ok {
							img.Height = int(h)
						}
						if len(dims) >= 3 {
							if sb, ok := dims[2].(float64); ok {
								img.SizeBytes = int64(sb)
							}
						}
					}
				}
				return
			}
		}
		for _, item := range val {
			extractFromNestedMetadata(img, item)
			if img.URL != "" {
				return
			}
		}
	}
}

func scanMediaURLs(v interface{}, out *[]string, depth int) {
	if depth > 16 || v == nil {
		return
	}
	switch val := v.(type) {
	case string:
		if isAllowedMediaURL(val) {
			*out = append(*out, val)
		}
	case []interface{}:
		for _, item := range val {
			scanMediaURLs(item, out, depth+1)
		}
	case map[string]interface{}:
		for _, item := range val {
			scanMediaURLs(item, out, depth+1)
		}
	}
}

func dedupeStrings(input []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, s := range input {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, exists := seen[s]; !exists {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// ReadGeminiStream đọc luồng wrb.fr. Thân phản hồi không đi vào lỗi.
func ReadGeminiStream(ctx context.Context, body io.Reader, metrics *domain.ContractMetrics, onDelta func(delta, convID string) error) (domain.GeminiReply, error) {
	return ReadGeminiStreamWithThinking(ctx, body, metrics, onDelta, nil)
}

// ReadGeminiStreamWithThinking đọc luồng wrb.fr và phát cả token nội dung lẫn token suy luận (Thinking/Reasoning) thời gian thực
func ReadGeminiStreamWithThinking(
	ctx context.Context,
	body io.Reader,
	metrics *domain.ContractMetrics,
	onDelta func(delta, convID string) error,
	onReasoning func(delta, convID string) error,
) (domain.GeminiReply, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	tracker := StreamMetricsTrackerFromContext(ctx)
	if tracker == nil {
		tracker = NewStreamMetricsTracker()
	}
	idleTimeout := StreamIdleTimeoutFromContext(ctx, 60*time.Second)
	var closer io.Closer
	if c, ok := body.(io.Closer); ok {
		closer = c
	}
	idleReader := NewIdleTimeoutReader(body, closer, idleTimeout, tracker)

	reader := bufio.NewReaderSize(idleReader, 64*1024)
	var reply domain.GeminiReply
	var lastFullText string
	var lastFullThinking string
	seenMedia := map[string]struct{}{}

	emit := func(delta, convID string) error {
		if delta == "" {
			return nil
		}
		tracker.RecordToken()
		reply.Text += delta
		if onDelta == nil {
			return nil
		}
		return onDelta(delta, convID)
	}

	for {
		if ctx.Err() != nil {
			tracker.SetClientDisconnect(true)
			return reply, ctx.Err()
		}
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineStr := strings.TrimSpace(string(line))
			if lineStr == "" || strings.HasPrefix(lineStr, ")]}'") || digitOnlyRegex.MatchString(lineStr) {
				// prefix và dòng độ dài không phải payload
			} else if strings.HasPrefix(lineStr, "[") && strings.HasSuffix(lineStr, "]") {
				meta := ParseEnvelopeChunk(lineStr)
				if meta.ConversationID != "" {
					reply.ConversationID = meta.ConversationID
				}
				if meta.ResponseID != "" {
					reply.ResponseID = meta.ResponseID
				}
				if meta.ChoiceID != "" {
					reply.ChoiceID = meta.ChoiceID
				}
				if len(meta.ThinkingBlocks) > 0 {
					reply.ThinkingBlocks = meta.ThinkingBlocks
				}
				if meta.Grounding != nil {
					reply.Grounding = meta.Grounding
				}
				if len(meta.CodeExecutions) > 0 {
					reply.CodeExecutions = append(reply.CodeExecutions, meta.CodeExecutions...)
				}
				if len(meta.Drafts) > 0 {
					reply.Drafts = meta.Drafts
				}
				if meta.FinishReason != "" {
					reply.FinishReason = meta.FinishReason
				}

				mapped := meta.Text != "" || meta.ConversationID != "" || meta.ResponseID != "" || meta.ChoiceID != "" ||
					meta.ThinkingContent != "" || len(meta.ThinkingBlocks) > 0 || meta.Grounding != nil ||
					len(meta.CodeExecutions) > 0 || len(meta.MediaURLs) > 0
				if !mapped {
					reply.Unmapped++
				}

				// Xử lý luồng Thinking/Reasoning thời gian thực
				var currentThinking string
				if len(meta.ThinkingBlocks) > 0 {
					var parts []string
					for _, tb := range meta.ThinkingBlocks {
						if strings.TrimSpace(tb.Content) != "" {
							parts = append(parts, tb.Content)
						}
					}
					currentThinking = strings.Join(parts, "\n\n")
				} else if meta.ThinkingContent != "" {
					currentThinking = meta.ThinkingContent
				}

				if currentThinking != "" && currentThinking != lastFullThinking {
					deltaThinking := ComputeDelta(currentThinking, lastFullThinking)
					lastFullThinking = currentThinking
					if deltaThinking != "" && onReasoning != nil {
						tracker.RecordToken()
						if callErr := onReasoning(deltaThinking, reply.ConversationID); callErr != nil {
							return reply, callErr
						}
					}
				}

				if meta.Text != "" && meta.Text != lastFullText {
					effectiveText := meta.Text
					if strings.Contains(effectiveText, "<ProductComparisonTable") && !strings.Contains(effectiveText, "</ProductComparisonTable>") {
						effectiveText = effectiveText[:strings.Index(effectiveText, "<ProductComparisonTable")]
					}
					if strings.Contains(effectiveText, "<EntityCard") && !strings.Contains(effectiveText, "/>") && !strings.Contains(effectiveText, "</EntityCard>") {
						effectiveText = effectiveText[:strings.Index(effectiveText, "<EntityCard")]
					}
					if effectiveText != "" && effectiveText != lastFullText {
						delta := ComputeDelta(effectiveText, lastFullText)
						lastFullText = effectiveText
						if delta != "" {
							if callErr := emit(delta, reply.ConversationID); callErr != nil {
								return reply, callErr
							}
						}
					}
				}
				for _, mediaURL := range meta.MediaURLs {
					if _, ok := seenMedia[mediaURL]; ok {
						continue
					}
					seenMedia[mediaURL] = struct{}{}
					reply.MediaURLs = append(reply.MediaURLs, mediaURL)
					if !strings.Contains(reply.Text, mediaURL) {
						delta := mediaURL
						if strings.TrimSpace(reply.Text) != "" {
							delta = "\n" + mediaURL
						}
						if callErr := emit(delta, reply.ConversationID); callErr != nil {
							return reply, callErr
						}
					}
				}
			} else {
				reply.Unmapped++
			}
		}
		if err != nil {
			if err == io.EOF {
				tracker.SetUpstreamEOF(true)
				break
			}
			if errors.Is(err, ErrStreamIdleTimeout) || err == ErrStreamIdleTimeout {
				tracker.SetTimeoutKind("idle")
				if metrics != nil {
					metrics.IncrementStreamIdleTimeouts()
				}
				return reply, err
			}
			return reply, err
		}
	}
	if metrics != nil {
		metrics.AddUnmapped(reply.Unmapped)
	}
	if lastFullText != "" {
		cleaned := CleanInternalPlaceholders(lastFullText)
		for _, u := range reply.MediaURLs {
			if !strings.Contains(cleaned, u) {
				cleaned += "\n" + u
			}
		}
		reply.Text = cleaned
	}
	if tracker != nil && tracker.FinishReason != "" && reply.FinishReason == "" {
		reply.FinishReason = tracker.FinishReason
	}
	if strings.TrimSpace(reply.Text) == "" && len(reply.MediaURLs) == 0 && len(reply.ThinkingBlocks) == 0 && len(reply.CodeExecutions) == 0 {
		if metrics != nil {
			metrics.AddSchema()
		}
		return reply, domain.CodecSchema(domain.OriginStreamGenerate, domain.ServiceGemini, "phản hồi chat không đúng hợp đồng")
	}
	return reply, nil
}

// ComputeDelta tính phần chênh lệch delta an toàn giữa chuỗi mới và chuỗi trước đó
// Chống lặp toàn bộ văn bản khi có ảnh Markdown hoặc trích dẫn Grounding chèn vào giữa
func ComputeDelta(currentText, lastText string) string {
	if lastText == "" {
		return currentText
	}
	if strings.HasPrefix(currentText, lastText) {
		return currentText[len(lastText):]
	}
	// Nếu currentText chứa toàn bộ lastText (ví dụ placeholder được thay thế bằng URL)
	if idx := strings.Index(currentText, lastText); idx >= 0 {
		return currentText[idx+len(lastText):]
	}
	// Tìm tiền tố chung dài nhất (Longest Common Prefix)
	commonLen := 0
	minLen := len(currentText)
	if len(lastText) < minLen {
		minLen = len(lastText)
	}
	for commonLen < minLen && currentText[commonLen] == lastText[commonLen] {
		commonLen++
	}
	if commonLen > 0 {
		return currentText[commonLen:]
	}
	// Nếu hoàn toàn không khớp và đã từng phát text trước đó, không phát lại từ đầu
	return ""
}
