package domain

import (
	"encoding/json"
	"net/url"
	"strings"
)

type MediaExtract struct {
	URL            string
	MimeType       string
	Unmapped       int
	SawProgress    bool
	CreditsBalance *int
}

func ExtractMediaDocument(raw string) (MediaExtract, error) {
	raw = strings.TrimSpace(raw)
	var doc any
	if raw == "" || json.Unmarshal([]byte(raw), &doc) != nil {
		return MediaExtract{}, CodecSchema(OriginStreamChat, ServiceFlow, "phản hồi media không đúng hợp đồng")
	}
	acc := &mediaWalk{}
	acc.walk(doc, 0)
	if acc.mime != "" {
		acc.out.MimeType = acc.mime
	}
	if len(acc.urls) > 0 {
		acc.out.URL = acc.urls[0]
		if len(acc.urls) > 1 {
			acc.out.Unmapped += len(acc.urls) - 1
		}
		return acc.out, nil
	}
	if acc.out.SawProgress {
		return acc.out, nil
	}
	return MediaExtract{}, CodecSchema(OriginStreamChat, ServiceFlow, "phản hồi media không đúng hợp đồng")
}

type mediaWalk struct {
	out  MediaExtract
	urls []string
	mime string
	seen map[string]struct{}
}

var mediaHostAllow = []string{
	"lh3.googleusercontent.com",
	"storage.googleapis.com",
	"video.google.com",
	"googleusercontent.com",
}

var knownMediaKeys = map[string]struct{}{
	"status": {}, "task_status": {}, "progress": {}, "video_asset": {}, "output_assets": {},
	"asset_id": {}, "url": {}, "video_url": {}, "image_url": {}, "mime_type": {},
	"width": {}, "height": {}, "seed": {}, "resolution": {}, "duration": {},
	"duration_seconds": {}, "credits_deducted": {}, "model_used": {}, "fps": {},
}

func (w *mediaWalk) walk(v any, depth int) {
	if depth > 16 || v == nil {
		return
	}
	switch t := v.(type) {
	case string:
		if u := allowMediaURL(t); u != "" {
			w.addURL(u)
			return
		}
		if depth < 6 && (strings.HasPrefix(t, "[") || strings.HasPrefix(t, "{")) {
			var inner any
			if json.Unmarshal([]byte(t), &inner) == nil {
				w.walk(inner, depth+1)
			}
		}
	case []any:
		for _, item := range t {
			w.walk(item, depth+1)
		}
	case map[string]any:
		for key, item := range t {
			if _, ok := knownMediaKeys[key]; !ok {
				w.out.Unmapped++
			}
			if key == "progress" {
				w.out.SawProgress = true
			}
			if key == "mime_type" {
				if mime, ok := item.(string); ok && mime != "" && w.mime == "" {
					w.mime = mime
				}
			}
			w.walk(item, depth+1)
		}
	}
}

func (w *mediaWalk) addURL(raw string) {
	if w.seen == nil {
		w.seen = map[string]struct{}{}
	}
	if _, ok := w.seen[raw]; ok {
		return
	}
	w.seen[raw] = struct{}{}
	w.urls = append(w.urls, raw)
}

func allowMediaURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	host := parsed.Hostname()
	for _, allowed := range mediaHostAllow {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return raw
		}
	}
	return ""
}
