package google

import (
	"bufio"
	"io"
	"strings"

	"dezuxk-gateway/internal/core/domain"
)

// ReadFlowMedia đọc thân StreamChat. Không đưa thân gốc vào lỗi.
func ReadFlowMedia(body io.Reader, metrics *domain.ContractMetrics) (domain.MediaExtract, error) {
	raw, err := io.ReadAll(io.LimitReader(body, 8<<20))
	if err != nil {
		return domain.MediaExtract{}, domain.CodecTransport(domain.OriginStreamChat, domain.ServiceFlow, err)
	}
	found := domain.MediaExtract{}
	sawLine := false
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ")]}'") || digitOnlyRegex.MatchString(line) {
			continue
		}
		sawLine = true
		extracted, extractErr := domain.ExtractMediaDocument(line)
		if extractErr != nil {
			found.Unmapped++
			continue
		}
		found.Unmapped += extracted.Unmapped
		if extracted.SawProgress {
			found.SawProgress = true
		}
		if extracted.URL != "" && found.URL == "" {
			found.URL = extracted.URL
			found.MimeType = extracted.MimeType
			continue
		}
		if extracted.URL != "" {
			found.Unmapped++
		}
	}
	if !sawLine {
		extracted, extractErr := domain.ExtractMediaDocument(strings.TrimSpace(string(raw)))
		if extractErr == nil {
			found = extracted
		}
	}
	if found.URL == "" {
		metrics.AddSchema()
		return domain.MediaExtract{}, domain.CodecSchema(domain.OriginStreamChat, domain.ServiceFlow, "phản hồi media không đúng hợp đồng")
	}
	metrics.AddUnmapped(found.Unmapped)
	return found, nil
}
