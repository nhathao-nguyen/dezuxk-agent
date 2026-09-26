package domain_test

import (
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestParseQuotaResponse_JSON(t *testing.T) {
	jsonFixture := `{
		"quota5h": 24.5,
		"quotaWeekly": 48.0,
		"rpmLimit": 60,
		"resetTime5h": "2026-09-24T20:00:00Z"
	}`

	info, err := domain.ParseQuotaResponse(jsonFixture)
	if err != nil {
		t.Fatalf("ParseQuotaResponse thất bại: %v", err)
	}

	if info.Quota5h != 24.5 {
		t.Errorf("quota5h không đúng: %v", info.Quota5h)
	}
	if info.QuotaWeekly != 48.0 {
		t.Errorf("quotaWeekly không đúng: %v", info.QuotaWeekly)
	}
	if info.RPMLimit != 60 {
		t.Errorf("rpmLimit không đúng: %v", info.RPMLimit)
	}
	if info.ResetTime5h != "2026-09-24T20:00:00Z" {
		t.Errorf("resetTime5h không đúng: %v", info.ResetTime5h)
	}
}

func TestParseQuotaResponse_HTMLScript(t *testing.T) {
	htmlFixture := `<html><body><script>
		window.WIZ_global_data = {
			"quota5h": "15.0%",
			"quotaWeekly": "32.5%",
			"rpmLimit": 120,
			"resetTime5h": "2026-09-24T21:30:00Z"
		};
	</script></body></html>`

	info, err := domain.ParseQuotaResponse(htmlFixture)
	if err != nil {
		t.Fatalf("ParseQuotaResponse HTML thất bại: %v", err)
	}

	if info.Quota5h != 15.0 {
		t.Errorf("quota5h không đúng: %v", info.Quota5h)
	}
	if info.QuotaWeekly != 32.5 {
		t.Errorf("quotaWeekly không đúng: %v", info.QuotaWeekly)
	}
	if info.RPMLimit != 120 {
		t.Errorf("rpmLimit không đúng: %v", info.RPMLimit)
	}
	if info.ResetTime5h != "2026-09-24T21:30:00Z" {
		t.Errorf("resetTime5h không đúng: %v", info.ResetTime5h)
	}
}
