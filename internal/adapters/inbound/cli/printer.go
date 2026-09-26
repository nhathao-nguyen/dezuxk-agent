package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// RenderTable prints tabular data aligned neatly using text/tabwriter.
func RenderTable(w io.Writer, headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	if len(headers) > 0 {
		fmt.Fprintln(tw, strings.Join(headers, "\t"))
		separators := make([]string, len(headers))
		for i, h := range headers {
			separators[i] = strings.Repeat("-", len(h))
		}
		fmt.Fprintln(tw, strings.Join(separators, "\t"))
	}
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
}

// RenderJSON serializes data to formatted JSON indented by 2 spaces.
func RenderJSON(w io.Writer, data any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}
