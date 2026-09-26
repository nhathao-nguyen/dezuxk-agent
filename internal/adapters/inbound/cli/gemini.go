package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// GeminiUsageRow represents quota info for tabular / JSON view.
type GeminiUsageRow struct {
	Quota5h     float64 `json:"quota_5h"`
	QuotaWeekly float64 `json:"quota_weekly"`
	RPMLimit    int     `json:"rpm_limit"`
	ResetTime5h string  `json:"reset_time_5h"`
}

// GeminiConversationRow represents conversation metadata.
type GeminiConversationRow struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
}

// NewGeminiCmd creates the "gemini" command and its subcommands.
func NewGeminiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gemini",
		Short: "Manage Gemini account quota, rate limits, and conversation history",
	}

	cmd.AddCommand(newGeminiUsageCmd())
	cmd.AddCommand(newGeminiConversationsCmd())

	return cmd
}

func newGeminiUsageCmd() *cobra.Command {
	var account string

	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Inspect 5h quota limits and account tier",
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

			var row GeminiUsageRow

			if execCtx.IsOnline {
				res, err := execCtx.Client.GetGeminiUsage(ctx, account)
				if err != nil {
					return fmt.Errorf("failed to fetch gemini usage: %w", err)
				}
				data, _ := res["data"].(map[string]any)
				if data == nil {
					data = res
				}
				if q, ok := data["quota_5h"].(float64); ok {
					row.Quota5h = q
				}
				if q, ok := data["quota_weekly"].(float64); ok {
					row.QuotaWeekly = q
				}
				if r, ok := data["rpm_limit"].(float64); ok {
					row.RPMLimit = int(r)
				} else if r, ok := data["rpm_limit"].(int); ok {
					row.RPMLimit = r
				}
				row.ResetTime5h, _ = data["reset_time_5h"].(string)
			} else {
				if execCtx.Direct.GeminiQuotaService != nil {
					quota, err := execCtx.Direct.GeminiQuotaService.GetQuota(ctx)
					if err == nil && quota != nil {
						row.Quota5h = quota.Quota5h
						row.QuotaWeekly = quota.QuotaWeekly
						row.RPMLimit = quota.RPMLimit
						row.ResetTime5h = quota.ResetTime5h
					}
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), row)
			}

			headers := []string{"QUOTA_5H", "QUOTA_WEEKLY", "RPM_LIMIT", "RESET_TIME_5H"}
			rows := [][]string{
				{
					fmt.Sprintf("%.1f%%", row.Quota5h),
					fmt.Sprintf("%.1f%%", row.QuotaWeekly),
					fmt.Sprintf("%d", row.RPMLimit),
					row.ResetTime5h,
				},
			}
			RenderTable(cmd.OutOrStdout(), headers, rows)
			return nil
		},
	}

	cmd.Flags().StringVar(&account, "account", "", "target account ID to inspect usage")
	return cmd
}

func newGeminiConversationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conversations",
		Short: "Manage Gemini conversation history",
	}

	cmd.AddCommand(newGeminiConversationsListCmd())
	cmd.AddCommand(newGeminiConversationsGetCmd())
	cmd.AddCommand(newGeminiConversationsRenameCmd())
	cmd.AddCommand(newGeminiConversationsDeleteCmd())

	return cmd
}

func newGeminiConversationsListCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List conversation history",
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit <= 0 {
				limit = 25
			}

			execCtx, err := GetExecutionContext(cmd)
			if err != nil {
				return err
			}
			defer execCtx.Close()

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			var rows []GeminiConversationRow

			if execCtx.IsOnline {
				convs, err := execCtx.Client.ListGeminiConversations(ctx, limit)
				if err != nil {
					return fmt.Errorf("failed to list conversations: %w", err)
				}
				for _, c := range convs {
					id, _ := c["id"].(string)
					title, _ := c["title"].(string)
					updatedAt, _ := c["updated_at"].(string)
					rows = append(rows, GeminiConversationRow{
						ID:        id,
						Title:     title,
						UpdatedAt: updatedAt,
					})
				}
			} else {
				if execCtx.Direct.GeminiHistoryService != nil {
					convs, _, err := execCtx.Direct.GeminiHistoryService.ListConversations(ctx, limit)
					if err != nil {
						return fmt.Errorf("failed to list conversations offline: %w", err)
					}
					for _, c := range convs {
						rows = append(rows, GeminiConversationRow{
							ID:        c.ID,
							Title:     c.Title,
							UpdatedAt: c.UpdatedAt,
						})
					}
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				if rows == nil {
					rows = []GeminiConversationRow{}
				}
				return RenderJSON(cmd.OutOrStdout(), rows)
			}

			headers := []string{"ID", "TITLE", "UPDATED_AT"}
			tableRows := make([][]string, len(rows))
			for i, r := range rows {
				tableRows[i] = []string{r.ID, r.Title, r.UpdatedAt}
			}
			RenderTable(cmd.OutOrStdout(), headers, tableRows)
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 25, "maximum number of conversations to retrieve")
	return cmd
}

func newGeminiConversationsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get conversation details and turn history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			execCtx, err := GetExecutionContext(cmd)
			if err != nil {
				return err
			}
			defer execCtx.Close()

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			var detail any

			if execCtx.IsOnline {
				data, err := execCtx.Client.GetGeminiConversation(ctx, id)
				if err != nil {
					return fmt.Errorf("failed to get conversation: %w", err)
				}
				detail = data
			} else {
				if execCtx.Direct.GeminiHistoryService == nil {
					return fmt.Errorf("gemini history service unavailable offline")
				}
				tree, err := execCtx.Direct.GeminiHistoryService.GetConversationDetail(ctx, id)
				if err != nil {
					return fmt.Errorf("failed to get conversation offline: %w", err)
				}
				detail = tree
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), detail)
			}

			if m, ok := detail.(map[string]any); ok {
				fmt.Fprintf(cmd.OutOrStdout(), "Conversation ID: %v\n", m["conversation_id"])
				fmt.Fprintf(cmd.OutOrStdout(), "Title:           %v\n\n", m["title"])
				if turns, ok := m["turns"].([]any); ok {
					for idx, t := range turns {
						if turnMap, ok := t.(map[string]any); ok {
							fmt.Fprintf(cmd.OutOrStdout(), "[Turn %d] User: %v\n", idx+1, turnMap["user_prompt"])
							if choices, ok := turnMap["choices"].([]any); ok {
								for cIdx, c := range choices {
									if choiceMap, ok := c.(map[string]any); ok {
										fmt.Fprintf(cmd.OutOrStdout(), "  Choice %d: %v\n", cIdx+1, choiceMap["content"])
									}
								}
							}
						}
					}
				}
				return nil
			}

			return RenderJSON(cmd.OutOrStdout(), detail)
		},
	}
}

func newGeminiConversationsRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <id> <name>",
		Short: "Rename an existing conversation",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			name := strings.Join(args[1:], " ")

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
				if err := execCtx.Client.RenameGeminiConversation(ctx, id, name); err != nil {
					return fmt.Errorf("failed to rename conversation: %w", err)
				}
			} else {
				if execCtx.Direct.GeminiHistoryService == nil {
					return fmt.Errorf("gemini history service unavailable offline")
				}
				if err := execCtx.Direct.GeminiHistoryService.RenameConversation(ctx, id, name); err != nil {
					return fmt.Errorf("failed to rename conversation offline: %w", err)
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status": "renamed",
					"id":     id,
					"title":  name,
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Conversation %q renamed to %q successfully.\n", id, name)
			return nil
		},
	}
}

func newGeminiConversationsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a conversation from history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

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
				if err := execCtx.Client.DeleteGeminiConversation(ctx, id); err != nil {
					return fmt.Errorf("failed to delete conversation: %w", err)
				}
			} else {
				if execCtx.Direct.GeminiHistoryService == nil {
					return fmt.Errorf("gemini history service unavailable offline")
				}
				if err := execCtx.Direct.GeminiHistoryService.DeleteConversation(ctx, id); err != nil {
					return fmt.Errorf("failed to delete conversation offline: %w", err)
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status":  "deleted",
					"id":      id,
					"message": "Conversation deleted successfully.",
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Conversation %q deleted successfully.\n", id)
			return nil
		},
	}
}
