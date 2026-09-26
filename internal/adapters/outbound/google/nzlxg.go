package google

import (
	"encoding/json"
	"math"
	"strings"

	"dezuxk-gateway/internal/core/domain"
)

// ParseNzlxgBalance đọc số dư từ payload trong của nzlxg.
// Phần tử đầu phải là số nguyên không âm. Mọi phần tử sau được đếm là unmapped và không gán nghĩa.
func ParseNzlxgBalance(inner string, metrics *domain.ContractMetrics) (domain.FlowCreditBalance, error) {
	inner = strings.TrimSpace(inner)
	var data []any
	if inner == "" || json.Unmarshal([]byte(inner), &data) != nil || len(data) == 0 {
		metrics.AddSchema()
		return domain.FlowCreditBalance{}, domain.CodecSchema(domain.OriginNzlxg, domain.ServiceFlow, "phản hồi số dư không đúng hợp đồng")
	}
	amount, ok := integerBalance(data[0])
	if !ok {
		metrics.AddSchema()
		return domain.FlowCreditBalance{}, domain.CodecSchema(domain.OriginNzlxg, domain.ServiceFlow, "phản hồi số dư không đúng hợp đồng")
	}
	unmapped := len(data) - 1
	metrics.AddUnmapped(unmapped)
	return domain.FlowCreditBalance{
		Amount:         amount,
		UnmappedFields: unmapped,
		SpecVersion:    domain.FlowCreditSpecVersion,
	}, nil
}

func integerBalance(value any) (int, bool) {
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number != math.Trunc(number) || number > math.MaxInt {
		return 0, false
	}
	return int(number), true
}
