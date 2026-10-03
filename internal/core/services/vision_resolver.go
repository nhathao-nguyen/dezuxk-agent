package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/security/netguard"
)

// ResolvedImageData chứa dữ liệu nhị phân của ảnh sau khi phân giải từ Data URL hoặc HTTP URL
type ResolvedImageData struct {
	FileName   string
	MimeType   string
	Data       []byte
	Size       int64
	RawDataURL string
}

// VisionResolver xử lý tải về, xác thực dung lượng/MIME và định tuyến upload ảnh cho Vision
type VisionResolver struct {
	cfg           config.VisionConfig
	uploadService ports.GeminiUploadUseCase
	httpClient    *http.Client
	urlGuard      netguard.URLGuard
}

// NewVisionResolver khởi tạo VisionResolver với safe HTTP client và SSRF protection
func NewVisionResolver(cfg config.VisionConfig, uploadService ports.GeminiUploadUseCase, customClient ...*http.Client) *VisionResolver {
	timeout := cfg.GetHTTPDownloadTimeout()
	var client *http.Client
	guard := netguard.NewDefaultGuard(false)

	if len(customClient) > 0 && customClient[0] != nil {
		client = customClient[0]
	} else {
		client = netguard.NewSafeHTTPClient(netguard.SafeHTTPConfig{
			Timeout: timeout,
		})
	}

	return &VisionResolver{
		cfg:           cfg,
		uploadService: uploadService,
		httpClient:    client,
		urlGuard:      guard,
	}
}

// SetURLGuard cập nhật URLGuard (phục vụ unit testability kiểm thử an toàn)
func (vr *VisionResolver) SetURLGuard(guard netguard.URLGuard) {
	vr.urlGuard = guard
}

// SetHTTPClient cập nhật HTTP Client (phục vụ unit testability kiểm thử an toàn)
func (vr *VisionResolver) SetHTTPClient(client *http.Client) {
	vr.httpClient = client
}

// ResolveImage tải hoặc giải mã ảnh từ URL (Base64 Data URL hoặc HTTP URL) và xác thực theo cấu hình
func (vr *VisionResolver) ResolveImage(ctx context.Context, rawURL string, index int) (*ResolvedImageData, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "URL ảnh rỗng")
	}

	maxBytes := vr.cfg.GetMaxImageSizeBytes()
	allowedMimes := vr.cfg.GetAllowedMimeTypes()

	// 1. Xử lý Base64 Data URL: data:image/png;base64,iVBORw0...
	if strings.HasPrefix(rawURL, "data:") {
		parts := strings.SplitN(rawURL, ",", 2)
		if len(parts) != 2 {
			return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "định dạng Data URL không hợp lệ")
		}

		header := parts[0] // e.g. "data:image/png;base64"
		encoded := parts[1]

		mimeType := "image/jpeg"
		header = strings.TrimPrefix(header, "data:")
		if idx := strings.Index(header, ";"); idx != -1 {
			mimeType = strings.TrimSpace(header[:idx])
		} else if header != "" {
			mimeType = strings.TrimSpace(header)
		}

		if !isMimeAllowed(mimeType, allowedMimes) {
			return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini,
				fmt.Sprintf("định dạng ảnh %s không được hỗ trợ (chỉ chấp nhận: %s)", mimeType, strings.Join(allowedMimes, ", ")))
		}

		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			// Thử lại với URLEncoding
			decoded, err = base64.URLEncoding.DecodeString(encoded)
			if err != nil {
				return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "giải mã base64 ảnh thất bại")
			}
		}

		size := int64(len(decoded))
		if size > maxBytes {
			return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini,
				fmt.Sprintf("dung lượng ảnh (%d bytes) vượt quá giới hạn cấu hình (%d bytes)", size, maxBytes))
		}

		fileName := fileNameForMime(mimeType, index)

		return &ResolvedImageData{
			FileName:   fileName,
			MimeType:   mimeType,
			Data:       decoded,
			Size:       size,
			RawDataURL: rawURL,
		}, nil
	}

	// 2. Xử lý HTTP / HTTPS URL
	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		// Thẩm định an toàn chống SSRF trước khi khởi tạo kết nối mạng
		if vr.urlGuard != nil {
			if _, err := vr.urlGuard.Validate(ctx, rawURL); err != nil {
				return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, fmt.Sprintf("SSRF blocked: %v", err))
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, fmt.Sprintf("URL ảnh không hợp lệ: %v", err))
		}

		resp, err := vr.httpClient.Do(req)
		if err != nil {
			return nil, domain.CodecTransport(domain.OriginStreamGenerate, domain.ServiceGemini, fmt.Errorf("không thể tải ảnh từ URL: %w", err))
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, domain.UpstreamRejected(domain.OpChatCompletions, "", domain.ServiceGemini,
				fmt.Sprintf("tải ảnh thất bại với mã HTTP %d", resp.StatusCode))
		}

		// Đọc có giới hạn để chống tấn công cạn kiệt bộ nhớ (OOM / Zip Bomb)
		limitReader := io.LimitReader(resp.Body, maxBytes+1)
		data, err := io.ReadAll(limitReader)
		if err != nil {
			return nil, domain.CodecTransport(domain.OriginStreamGenerate, domain.ServiceGemini, fmt.Errorf("lỗi đọc dữ liệu ảnh: %w", err))
		}

		size := int64(len(data))
		if size > maxBytes {
			return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini,
				fmt.Sprintf("dung lượng ảnh tải về (%d bytes) vượt quá giới hạn cấu hình (%d bytes)", size, maxBytes))
		}

		headerMime := resp.Header.Get("Content-Type")
		if idx := strings.Index(headerMime, ";"); idx != -1 {
			headerMime = strings.TrimSpace(headerMime[:idx])
		}
		detectedMime := http.DetectContentType(data)
		if idx := strings.Index(detectedMime, ";"); idx != -1 {
			detectedMime = strings.TrimSpace(detectedMime[:idx])
		}

		mimeType := headerMime
		if mimeType == "" || mimeType == "application/octet-stream" {
			mimeType = detectedMime
		}

		// Security: Nếu Content-Type khai báo là image/* nhưng magic bytes phát hiện lại là HTML hoặc script nguy hiểm
		if strings.HasPrefix(mimeType, "image/") && strings.HasPrefix(detectedMime, "text/html") {
			return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini,
				fmt.Sprintf("tệp tải về bị từ chối do xung đột định dạng (khai báo: %s, nội dung thực tế: %s)", mimeType, detectedMime))
		}

		if !isMimeAllowed(mimeType, allowedMimes) {
			return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini,
				fmt.Sprintf("định dạng ảnh %s không được hỗ trợ (chỉ chấp nhận: %s)", mimeType, strings.Join(allowedMimes, ", ")))
		}

		fileName := fileNameForMime(mimeType, index)

		return &ResolvedImageData{
			FileName: fileName,
			MimeType: mimeType,
			Data:     data,
			Size:     size,
		}, nil
	}

	return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "URL ảnh phải là data:base64 hoặc http(s)://")
}

// ProcessImageForAccount đưa ảnh qua giao thức SCOTTY Push Upload hoặc Inline Blob theo cấu hình
func (vr *VisionResolver) ProcessImageForAccount(
	ctx context.Context,
	account *domain.ManagedAccount,
	img *ResolvedImageData,
) (*domain.GeminiAttachment, error) {
	method := vr.cfg.GetUploadMethod()

	// 1. Nhúng Inline Data Blob trực tiếp vào inner vector
	if method == "inline" {
		dataURL := img.RawDataURL
		if dataURL == "" {
			dataURL = fmt.Sprintf("data:%s;base64,%s", img.MimeType, base64.StdEncoding.EncodeToString(img.Data))
		}
		return &domain.GeminiAttachment{
			StorageToken: dataURL,
			MimeType:     img.MimeType,
			FileName:     img.FileName,
		}, nil
	}

	// 2. Giao thức chuẩn SCOTTY Resumable Upload (docs-2/gemini/uploads.md)
	if vr.uploadService == nil {
		return nil, domain.UpstreamUnavailable(domain.OpChatCompletions, "", domain.ServiceGemini, "dịch vụ GeminiUploadService chưa sẵn sàng")
	}

	storageToken, err := vr.uploadService.UploadFileWithAccount(
		ctx,
		account,
		img.FileName,
		img.MimeType,
		bytes.NewReader(img.Data),
		img.Size,
	)
	if err != nil {
		return nil, err
	}

	return &domain.GeminiAttachment{
		StorageToken: storageToken,
		MimeType:     img.MimeType,
		FileName:     img.FileName,
	}, nil
}

// ProcessRequestAttachments trích xuất toàn bộ ảnh từ các tin nhắn OpenAI và tạo danh sách GeminiAttachment
func (vr *VisionResolver) ProcessRequestAttachments(
	ctx context.Context,
	account *domain.ManagedAccount,
	req *domain.OpenAIChatRequest,
) ([]domain.GeminiAttachment, error) {
	var attachments []domain.GeminiAttachment
	if len(req.Attachments) > 0 {
		attachments = append(attachments, req.Attachments...)
	}

	imageURLs := req.GetAllImageURLs()
	if len(imageURLs) == 0 {
		return attachments, nil
	}

	if !vr.cfg.IsEnabled() {
		return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "tính năng Vision hiện đang tắt trong cấu hình gateway")
	}

	for i, url := range imageURLs {
		resolved, err := vr.ResolveImage(ctx, url, i)
		if err != nil {
			return nil, err
		}

		att, err := vr.ProcessImageForAccount(ctx, account, resolved)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, *att)
	}

	return attachments, nil
}

func isMimeAllowed(mime string, allowed []string) bool {
	mime = strings.ToLower(strings.TrimSpace(mime))
	for _, a := range allowed {
		if strings.ToLower(strings.TrimSpace(a)) == mime {
			return true
		}
	}
	return false
}

func fileNameForMime(mime string, index int) string {
	ext := extensionForMime(mime)
	prefix := "upload_image"
	lower := strings.ToLower(strings.TrimSpace(mime))
	if strings.HasPrefix(lower, "application/") || strings.HasPrefix(lower, "text/") {
		prefix = "upload_doc"
	} else if strings.HasPrefix(lower, "audio/") {
		prefix = "upload_audio"
	}
	return fmt.Sprintf("%s_%d.%s", prefix, index+1, ext)
}

func extensionForMime(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "application/pdf":
		return "pdf"
	case "text/plain":
		return "txt"
	case "text/csv":
		return "csv"
	case "application/json":
		return "json"
	case "text/markdown", "text/x-markdown":
		return "md"
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/wav", "audio/x-wav":
		return "wav"
	case "audio/ogg":
		return "ogg"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return "docx"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return "xlsx"
	default:
		if strings.HasPrefix(mime, "audio/") {
			return "mp3"
		}
		if strings.HasPrefix(mime, "text/") {
			return "txt"
		}
		return "bin"
	}
}
