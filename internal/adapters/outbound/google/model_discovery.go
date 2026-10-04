package google

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// GoogleModelDiscoveryProvider thu thập danh mục mô hình thực tế từ Google Gemini Web RPC
type GoogleModelDiscoveryProvider struct {
	upstream    ports.UpstreamGoogleTransport
	rpcRegistry *domain.RpcRegistry
}

var (
	ErrDiscoveryProtocolDrift = fmt.Errorf("discovery_protocol_drift")
	ErrDiscoveryUpstreamError = fmt.Errorf("discovery_upstream_error")
	ErrDiscoveryEmptyResponse = fmt.Errorf("discovery_empty_response")
)

var _ ports.ModelDiscoveryProvider = (*GoogleModelDiscoveryProvider)(nil)

func NewGoogleModelDiscoveryProvider(upstream ports.UpstreamGoogleTransport, rpcRegistry *domain.RpcRegistry) *GoogleModelDiscoveryProvider {
	if rpcRegistry == nil {
		rpcRegistry = domain.DefaultRpcRegistry()
	}
	return &GoogleModelDiscoveryProvider{
		upstream:    upstream,
		rpcRegistry: rpcRegistry,
	}
}

// DiscoverModels gửi RPC otAQ7b lên Google để khám phá danh sách mô hình và năng lực thực tế
func (p *GoogleModelDiscoveryProvider) DiscoverModels(ctx context.Context, account *domain.ManagedAccount) ([]domain.ModelDescriptor, error) {
	if account == nil {
		return nil, fmt.Errorf("tài khoản google không được để nil")
	}

	atToken := account.GetAtToken(domain.ServiceGemini)
	postBodyOtAQ7b := `f.req=` + url.QueryEscape(`[[["otAQ7b","[]",null,"generic"]]]`)
	if atToken != "" {
		postBodyOtAQ7b += "&at=" + url.QueryEscape(atToken)
	}

	reqPath := "/_/BardChatUi/data/batchexecute?rpcids=otAQ7b"
	if p.rpcRegistry != nil {
		if ep, ok := p.rpcRegistry.Get("otAQ7b"); ok {
			if ep.PathPattern != "" {
				reqPath = ep.PathPattern
			}
			if ep.TargetHost != "" && !strings.HasPrefix(reqPath, "http") {
				reqPath = ep.TargetHost + reqPath
			}
		}
	}

	resp, err := p.upstream.DoRequest(
		ctx,
		account,
		domain.ServiceGemini,
		http.MethodPost,
		reqPath,
		strings.NewReader(postBodyOtAQ7b),
		"application/x-www-form-urlencoded;charset=UTF-8",
	)
	if err != nil {
		return nil, fmt.Errorf("%w: lỗi kết nối Google discovery RPC (otAQ7b): %v", ErrDiscoveryUpstreamError, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: google discovery RPC (otAQ7b) trả về HTTP %d", ErrDiscoveryUpstreamError, resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: lỗi đọc thân phản hồi otAQ7b: %v", ErrDiscoveryUpstreamError, err)
	}

	return ParseUpstreamDiscoveryResponse(string(bodyBytes), account.Tier)
}

// ParseUpstreamDiscoveryResponse phân tích chuỗi phản hồi thô từ otAQ7b thành ModelDescriptors động
func ParseUpstreamDiscoveryResponse(raw string, accountTier int) ([]domain.ModelDescriptor, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: phản hồi discovery rỗng", ErrDiscoveryEmptyResponse)
	}

	// 1. Loại bỏ tiền tố XSSI )]}'
	for strings.HasPrefix(trimmed, ")]}'") || strings.HasPrefix(trimmed, "\n") || strings.HasPrefix(trimmed, "\r") {
		trimmed = strings.TrimPrefix(trimmed, ")]}'")
		trimmed = strings.TrimSpace(trimmed)
	}

	// 2. Tìm khối payload wrb.fr cho otAQ7b (hỗ trợ cả streaming chunked có tiền tố độ dài byte)
	var rootAny any
	lines := strings.Split(trimmed, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "[") {
			if err := json.Unmarshal([]byte(l), &rootAny); err == nil {
				break
			}
		}
	}
	if rootAny == nil {
		_ = json.Unmarshal([]byte(trimmed), &rootAny)
	}

	var innerPayload string
	if rootAny != nil {
		innerPayload = extractWrbPayload(rootAny, "otAQ7b")
	}
	if innerPayload == "" {
		innerPayload = trimmed
	}

	// 3. Phân tích danh sách mô hình từ payload đã bóc tách
	models, err := parseModelsFromPayload(innerPayload, accountTier)
	if err != nil {
		return nil, fmt.Errorf("%w: lỗi phân tích cú pháp upstream payload: %v", ErrDiscoveryProtocolDrift, err)
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("%w: payload không chứa models hợp lệ từ otAQ7b", ErrDiscoveryProtocolDrift)
	}

	return models, nil
}

func extractWrbPayload(node any, targetRpc string) string {
	var result string
	var scan func(n any)
	scan = func(n any) {
		if result != "" {
			return
		}
		arr, ok := n.([]any)
		if !ok {
			return
		}
		if len(arr) >= 3 {
			if marker, ok := arr[0].(string); ok && marker == "wrb.fr" {
				rpcID, _ := arr[1].(string)
				if rpcID == targetRpc || targetRpc == "" {
					if pStr, ok := arr[2].(string); ok && pStr != "" {
						result = pStr
						return
					}
				}
			}
		}
		for _, child := range arr {
			scan(child)
		}
	}
	scan(node)
	return result
}

func parseModelsFromPayload(payload string, accountTier int) ([]domain.ModelDescriptor, error) {
	var parsed any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return nil, err
	}

	var descriptors []domain.ModelDescriptor
	now := time.Now()

	// Quét các phần tử dạng [modeId, displayName, tierCode] hoặc object
	var scan func(n any)
	scan = func(n any) {
		arr, ok := n.([]any)
		if !ok {
			// Thử kiểm tra dạng map/object: {"id": "...", "name": "...", "tier": 1}
			if obj, isMap := n.(map[string]any); isMap {
				if idVal, hasID := obj["id"].(string); hasID && idVal != "" {
					displayName, _ := obj["display_name"].(string)
					if displayName == "" {
						displayName = idVal
					}
					tierVal := 1
					if t, ok := obj["tier"].(float64); ok {
						tierVal = int(t)
					}
					canonicalID := domain.CanonicalModelID(idVal, domain.ServiceGemini)
					caps := inferCapabilities(displayName, idVal)
					descriptors = append(descriptors, domain.ModelDescriptor{
						ID:                canonicalID,
						DisplayName:       displayName,
						TargetService:     domain.ServiceGemini,
						Capabilities:      caps,
						InternalBackendID: displayName,
						ModelTierCode:     tierVal,
						IsActive:          true,
						Source:            "upstream_discovery",
						FirstSeenAt:       now,
						LastSeenAt:        now,
						UpdatedAt:         now,
					})
				}
			}
			return
		}

		// Nhận diện mảng [modeId, name, tier]
		if len(arr) >= 2 {
			idStr, ok1 := arr[0].(string)
			nameStr, ok2 := arr[1].(string)
			if ok1 && ok2 && len(idStr) > 4 && len(nameStr) > 1 {
				tierCode := 1
				if len(arr) >= 3 {
					if tFloat, ok := arr[2].(float64); ok {
						tierCode = int(tFloat)
					}
				}
				if strings.Contains(strings.ToLower(nameStr), "pro") && tierCode < 2 {
					tierCode = 3
				}

				canonicalID := domain.CanonicalModelID(nameStr, domain.ServiceGemini)
				caps := inferCapabilities(nameStr, idStr)

				descriptors = append(descriptors, domain.ModelDescriptor{
					ID:                canonicalID,
					DisplayName:       nameStr,
					TargetService:     domain.ServiceGemini,
					Capabilities:      caps,
					InternalBackendID: nameStr,
					ModeID:            idStr,
					ModelTierCode:     tierCode,
					IsActive:          true,
					Source:            "upstream_discovery",
					FirstSeenAt:       now,
					LastSeenAt:        now,
					UpdatedAt:         now,
				})
				return
			}
		}

		for _, child := range arr {
			scan(child)
		}
	}

	scan(parsed)
	return descriptors, nil
}

func inferCapabilities(name, id string) []domain.ModelCapability {
	combined := strings.ToLower(name + " " + id)
	caps := []domain.ModelCapability{domain.CapChat}

	if strings.Contains(combined, "thinking") || strings.Contains(combined, "reasoning") {
		caps = append(caps, domain.CapThinking)
	}
	if strings.Contains(combined, "vision") || strings.Contains(combined, "image") {
		caps = append(caps, domain.CapImage)
	}
	if strings.Contains(combined, "video") {
		caps = append(caps, domain.CapVideo)
	}
	if strings.Contains(combined, "code") {
		caps = append(caps, domain.CapCode)
	}
	return caps
}
