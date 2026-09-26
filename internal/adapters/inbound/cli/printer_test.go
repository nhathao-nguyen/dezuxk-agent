package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/inbound/cli"
)

func TestPrinter_FormatTable(t *testing.T) {
	headers := []string{"ID", "NAME", "STATUS"}
	rows := [][]string{
		{"1", "Profile A", "active"},
		{"2", "Profile B", "inactive"},
	}

	var buf bytes.Buffer
	cli.RenderTable(&buf, headers, rows)

	out := buf.String()
	for _, h := range headers {
		if !strings.Contains(out, h) {
			t.Errorf("expected output to contain header %q, got:\n%s", h, out)
		}
	}
	for _, row := range rows {
		for _, cell := range row {
			if !strings.Contains(out, cell) {
				t.Errorf("expected output to contain cell %q, got:\n%s", cell, out)
			}
		}
	}
}

func TestPrinter_FormatJSON(t *testing.T) {
	type SampleData struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	data := SampleData{
		Name:  "test-item",
		Count: 42,
	}

	var buf bytes.Buffer
	err := cli.RenderJSON(&buf, data)
	if err != nil {
		t.Fatalf("unexpected error from RenderJSON: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "  \"name\": \"test-item\"") {
		t.Errorf("expected 2-space indented json, got:\n%s", out)
	}

	var parsed SampleData
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("failed to unmarshal rendered JSON: %v", err)
	}

	if parsed.Name != data.Name || parsed.Count != data.Count {
		t.Errorf("unmarshaled data does not match original: got %+v, want %+v", parsed, data)
	}
}
