package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// CacheStatusOutput represents cache statistics.
type CacheStatusOutput struct {
	TotalEntries int     `json:"total_entries"`
	MaxEntries   int     `json:"max_entries"`
	Hits         uint64  `json:"hits"`
	Misses       uint64  `json:"misses"`
	HitRatio     float64 `json:"hit_ratio"`
}

// NewCacheCmd creates the "cache" command and its subcommands.
func NewCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage and monitor in-memory RAM response caching",
	}

	cmd.AddCommand(newCacheStatusCmd())
	cmd.AddCommand(newCachePurgeCmd())

	return cmd
}

func newCacheStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Display RAM cache utilization, hits, misses, and hit ratio",
		RunE: func(cmd *cobra.Command, args []string) error {
			execCtx, err := GetExecutionContext(cmd)
			if err != nil {
				return err
			}
			defer execCtx.Close()

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			var stats CacheStatusOutput

			if execCtx.IsOnline {
				rawStats, err := execCtx.Client.GetCacheStats(ctx)
				if err != nil {
					return fmt.Errorf("failed to get cache stats from server: %w", err)
				}
				if te, ok := rawStats["total_entries"].(float64); ok {
					stats.TotalEntries = int(te)
				}
				if me, ok := rawStats["max_entries"].(float64); ok {
					stats.MaxEntries = int(me)
				}
				if h, ok := rawStats["hits"].(float64); ok {
					stats.Hits = uint64(h)
				}
				if m, ok := rawStats["misses"].(float64); ok {
					stats.Misses = uint64(m)
				}
				if hr, ok := rawStats["hit_ratio"].(float64); ok {
					stats.HitRatio = hr
				}
			} else {
				if execCtx.Direct.ResponseCache != nil {
					s := execCtx.Direct.ResponseCache.Stats()
					stats.TotalEntries = s.TotalEntries
					stats.MaxEntries = s.MaxEntries
					stats.Hits = s.Hits
					stats.Misses = s.Misses
					stats.HitRatio = s.HitRatio
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), stats)
			}

			headers := []string{"ENTRIES", "MAX_ENTRIES", "HITS", "MISSES", "HIT_RATIO"}
			rows := [][]string{
				{
					fmt.Sprintf("%d", stats.TotalEntries),
					fmt.Sprintf("%d", stats.MaxEntries),
					fmt.Sprintf("%d", stats.Hits),
					fmt.Sprintf("%d", stats.Misses),
					fmt.Sprintf("%.2f%%", stats.HitRatio),
				},
			}
			RenderTable(cmd.OutOrStdout(), headers, rows)
			return nil
		},
	}
}

func newCachePurgeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "purge",
		Short: "Purge all cached responses in RAM",
		RunE: func(cmd *cobra.Command, args []string) error {
			execCtx, err := GetExecutionContext(cmd)
			if err != nil {
				return err
			}
			defer execCtx.Close()

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			if execCtx.IsOnline {
				if err := execCtx.Client.PurgeCache(ctx); err != nil {
					return fmt.Errorf("failed to purge cache on server: %w", err)
				}
			} else {
				if execCtx.Direct.ResponseCache != nil {
					execCtx.Direct.ResponseCache.Clear()
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status":  "purged",
					"message": "RAM response cache purged successfully.",
				})
			}

			fmt.Fprintln(cmd.OutOrStdout(), "RAM response cache purged successfully.")
			return nil
		},
	}
}
