package google

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Marshaller hỗ trợ đóng gói mảng JSON 2 lớp cho Google Wire Protocol
type Marshaller struct{}

func NewMarshaller() *Marshaller {
	return &Marshaller{}
}

// EncodeBatchexecute đóng gói dạng f.req=[[[rpcId, JSON_STRING(inner), null, "generic"]]]
func (m *Marshaller) EncodeBatchexecute(rpcID string, innerPayload interface{}, atToken string) (string, error) {
	var innerJson string
	switch v := innerPayload.(type) {
	case string:
		innerJson = v
	default:
		bytes, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("marshal inner payload error: %w", err)
		}
		innerJson = string(bytes)
	}

	outerArray := [][][]interface{}{
		{
			{rpcID, innerJson, nil, "generic"},
		},
	}

	outerJson, err := json.Marshal(outerArray)
	if err != nil {
		return "", fmt.Errorf("marshal outer envelope error: %w", err)
	}

	vals := url.Values{}
	vals.Set("f.req", string(outerJson))
	if atToken != "" {
		vals.Set("at", atToken)
	}

	return vals.Encode(), nil
}

// EncodeStreamGenerate đóng gói dạng f.req=[null, JSON_STRING(inner)]
func (m *Marshaller) EncodeStreamGenerate(innerArray interface{}, atToken string) (string, error) {
	innerBytes, err := json.Marshal(innerArray)
	if err != nil {
		return "", fmt.Errorf("marshal inner array error: %w", err)
	}

	outerArray := []interface{}{nil, string(innerBytes)}
	outerBytes, err := json.Marshal(outerArray)
	if err != nil {
		return "", fmt.Errorf("marshal outer array error: %w", err)
	}

	vals := url.Values{}
	vals.Set("f.req", string(outerBytes))
	if atToken != "" {
		vals.Set("at", atToken)
	}

	return vals.Encode(), nil
}

// StripXSSIPrefix loại bỏ tiền tố bảo vệ )]}'
func StripXSSIPrefix(raw string) string {
	return strings.TrimPrefix(strings.TrimSpace(raw), ")]}'")
}
