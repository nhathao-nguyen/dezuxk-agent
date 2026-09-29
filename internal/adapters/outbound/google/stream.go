package google

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"

	"dezuxk-gateway/internal/core/domain"
)

var (
	digitOnlyRegex        = regexp.MustCompile(`^\d+$`)
	imagePlaceholderRegex = regexp.MustCompile(`https?://googleusercontent\.com/image_generation_content/[a-zA-Z0-9_]+`)
	cardContentRegex      = regexp.MustCompile(`https?://googleusercontent\.com/card_content/[a-zA-Z0-9_]+`)
)

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

	reader := bufio.NewReaderSize(body, h.bufferSize)
	var conversationID string
	var lastFullText string

	for {
		select {
		case <-ctx.Done():
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
					var delta string
					if strings.HasPrefix(currentText, lastFullText) {
						delta = currentText[len(lastFullText):]
					} else {
						delta = currentText
					}
					lastFullText = currentText

					if delta != "" {
						if err := onDelta(delta, conversationID); err != nil {
							return err
						}
					}
				}
			}
		}

		if err != nil {
			if err == io.EOF {
				break
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
					if candidate, ok := candidates[0].([]interface{}); ok {
						// candidate[0]: choice id "rc_..."
						if len(candidate) > 0 {
							if rcID, ok := candidate[0].(string); ok && strings.HasPrefix(rcID, "rc_") {
								meta.ChoiceID = rcID
							}
						}

						// candidate[1][0]: text parts
						if len(candidate) > 1 && candidate[1] != nil {
							if textParts, ok := candidate[1].([]interface{}); ok && len(textParts) > 0 {
								if partStr, ok := textParts[0].(string); ok {
									meta.Text = partStr
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
						meta.Text = strings.TrimSpace(meta.Text)
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
		if strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://") {
			lower := strings.ToLower(val)
			if strings.Contains(lower, "googleusercontent.com") || strings.Contains(lower, "storage.googleapis.com") {
				*urls = append(*urls, val)
			}
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

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
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
	reader := bufio.NewReaderSize(body, 64*1024)
	var reply domain.GeminiReply
	var lastFullText string
	seenMedia := map[string]struct{}{}

	emit := func(delta, convID string) error {
		if delta == "" {
			return nil
		}
		reply.Text += delta
		if onDelta == nil {
			return nil
		}
		return onDelta(delta, convID)
	}

	for {
		if ctx.Err() != nil {
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
					reply.ThinkingBlocks = append(reply.ThinkingBlocks, meta.ThinkingBlocks...)
				}
				if meta.Grounding != nil {
					reply.Grounding = meta.Grounding
				}
				if len(meta.CodeExecutions) > 0 {
					reply.CodeExecutions = append(reply.CodeExecutions, meta.CodeExecutions...)
				}

				mapped := meta.Text != "" || meta.ConversationID != "" || meta.ResponseID != "" || meta.ChoiceID != "" ||
					meta.ThinkingContent != "" || len(meta.ThinkingBlocks) > 0 || meta.Grounding != nil ||
					len(meta.CodeExecutions) > 0 || len(meta.MediaURLs) > 0
				if !mapped {
					reply.Unmapped++
				}
				if meta.Text != "" && meta.Text != lastFullText {
					var delta string
					if strings.HasPrefix(meta.Text, lastFullText) {
						delta = meta.Text[len(lastFullText):]
					} else {
						delta = meta.Text
					}
					lastFullText = meta.Text
					if delta != "" {
						if callErr := emit(delta, reply.ConversationID); callErr != nil {
							return reply, callErr
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
				break
			}
			return reply, err
		}
	}
	metrics.AddUnmapped(reply.Unmapped)
	if strings.TrimSpace(reply.Text) == "" && len(reply.MediaURLs) == 0 {
		metrics.AddSchema()
		return reply, domain.CodecSchema(domain.OriginStreamGenerate, domain.ServiceGemini, "phản hồi chat không đúng hợp đồng")
	}
	return reply, nil
}
