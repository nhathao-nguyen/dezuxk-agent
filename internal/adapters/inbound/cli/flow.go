package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dezuxk-gateway/internal/core/domain"

	"github.com/spf13/cobra"
)

// FlowCreditsRow represents credit balance for tabular / JSON view.
type FlowCreditsRow struct {
	AccountID string `json:"account_id"`
	Credits   int    `json:"credits_balance"`
	Tier      string `json:"tier"`
}

// FlowProjectRow represents project info for tabular / JSON view.
type FlowProjectRow struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
	IsActive  bool   `json:"is_active"`
}

// FlowVoiceRow represents voice persona info for tabular / JSON view.
type FlowVoiceRow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Gender      string `json:"gender"`
	Description string `json:"description"`
}

// NewFlowCmd creates the "flow" command and its subcommands.
func NewFlowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "flow",
		Short: "Manage Google Flow Studio projects, credits, voices, and audio generation",
	}

	cmd.AddCommand(newFlowCreditsCmd())
	cmd.AddCommand(newFlowProjectsCmd())
	cmd.AddCommand(newFlowVoicesCmd())
	cmd.AddCommand(newFlowAudioCmd())

	return cmd
}

func newFlowCreditsCmd() *cobra.Command {
	var account string

	cmd := &cobra.Command{
		Use:   "credits",
		Short: "Check Flow credit balance via RPC nzlxg",
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

			var row FlowCreditsRow

			if execCtx.IsOnline {
				res, err := execCtx.Client.GetFlowCredits(ctx, account)
				if err != nil {
					return fmt.Errorf("failed to fetch credits: %w", err)
				}
				data, _ := res["data"].(map[string]any)
				if data == nil {
					data = res
				}
				row.AccountID, _ = data["account_id"].(string)
				if c, ok := data["credits_balance"].(float64); ok {
					row.Credits = int(c)
				} else if c, ok := data["credits_balance"].(int); ok {
					row.Credits = c
				}
				row.Tier, _ = data["tier"].(string)
				if row.Tier == "" {
					row.Tier = "Free"
				}
			} else {
				if execCtx.Direct.FlowCreditService != nil {
					accID, bal, err := execCtx.Direct.FlowCreditService.GetCredits(ctx)
					if err == nil {
						row.AccountID = accID
						row.Credits = bal.Amount
						row.Tier = "Standard"
					}
				}
				if row.AccountID == "" && execCtx.Direct.SessionRepo != nil {
					accs := execCtx.Direct.SessionRepo.ListAll(ctx)
					for _, acc := range accs {
						if acc != nil && (account == "" || acc.ID == account) {
							row.AccountID = acc.ID
							row.Credits = acc.CreditsBalance
							if acc.Tier == 2 {
								row.Tier = "Pro"
							} else if acc.Tier == 3 {
								row.Tier = "Ultra"
							} else {
								row.Tier = "Free"
							}
							break
						}
					}
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), row)
			}

			headers := []string{"ACCOUNT_ID", "CREDITS_BALANCE", "TIER"}
			rows := [][]string{
				{row.AccountID, fmt.Sprintf("%d", row.Credits), row.Tier},
			}
			RenderTable(cmd.OutOrStdout(), headers, rows)
			return nil
		},
	}

	cmd.Flags().StringVar(&account, "account", "", "target account ID to inspect credits")
	return cmd
}

func newFlowProjectsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "Manage Flow Studio projects",
	}

	cmd.AddCommand(newFlowProjectsListCmd())
	cmd.AddCommand(newFlowProjectsCreateCmd())
	cmd.AddCommand(newFlowProjectsTrashCmd())
	cmd.AddCommand(newFlowProjectsRestoreCmd())
	cmd.AddCommand(newFlowProjectsDeleteCmd())

	return cmd
}

func newFlowProjectsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List active Flow Studio projects",
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

			var rows []FlowProjectRow

			if execCtx.IsOnline {
				projects, err := execCtx.Client.ListFlowProjects(ctx)
				if err != nil {
					return fmt.Errorf("failed to list flow projects: %w", err)
				}
				for _, p := range projects {
					id, _ := p["id"].(string)
					title, _ := p["title"].(string)
					updatedAt, _ := p["updated_at"].(string)
					isActive := true
					if act, ok := p["is_active"].(bool); ok {
						isActive = act
					}
					rows = append(rows, FlowProjectRow{
						ID:        id,
						Title:     title,
						UpdatedAt: updatedAt,
						IsActive:  isActive,
					})
				}
			} else {
				if execCtx.Direct.FlowService != nil {
					projects, err := execCtx.Direct.FlowService.ListProjects(ctx)
					if err != nil {
						return fmt.Errorf("failed to list projects offline: %w", err)
					}
					for _, p := range projects {
						rows = append(rows, FlowProjectRow{
							ID:        p.ID,
							Title:     p.Title,
							UpdatedAt: p.UpdatedAt.Format(time.RFC3339),
							IsActive:  p.IsActive,
						})
					}
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				if rows == nil {
					rows = []FlowProjectRow{}
				}
				return RenderJSON(cmd.OutOrStdout(), rows)
			}

			headers := []string{"ID", "TITLE", "UPDATED_AT", "IS_ACTIVE"}
			tableRows := make([][]string, len(rows))
			for i, r := range rows {
				tableRows[i] = []string{
					r.ID,
					r.Title,
					r.UpdatedAt,
					fmt.Sprintf("%t", r.IsActive),
				}
			}
			RenderTable(cmd.OutOrStdout(), headers, tableRows)
			return nil
		},
	}
}

func newFlowProjectsCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <title>",
		Short: "Create a new Flow Studio project",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := strings.Join(args, " ")
			execCtx, err := GetExecutionContext(cmd)
			if err != nil {
				return err
			}
			defer execCtx.Close()

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			var projectID string
			if execCtx.IsOnline {
				res, err := execCtx.Client.CreateFlowProject(ctx, title)
				if err != nil {
					return fmt.Errorf("failed to create project: %w", err)
				}
				projectID, _ = res["id"].(string)
				if t, ok := res["title"].(string); ok && t != "" {
					title = t
				}
			} else {
				if execCtx.Direct.FlowService == nil {
					return fmt.Errorf("flow service unavailable offline")
				}
				id, err := execCtx.Direct.FlowService.CreateProject(ctx, title)
				if err != nil {
					return fmt.Errorf("failed to create project offline: %w", err)
				}
				projectID = id
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status": "created",
					"id":     projectID,
					"title":  title,
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Flow project created successfully:\n  ID:    %s\n  Title: %s\n", projectID, title)
			return nil
		},
	}
}

func newFlowProjectsTrashCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "trash <id>",
		Short: "Move a Flow Studio project to trash",
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
				if err := execCtx.Client.TrashFlowProject(ctx, id); err != nil {
					return fmt.Errorf("failed to trash project: %w", err)
				}
			} else {
				if execCtx.Direct.FlowService == nil {
					return fmt.Errorf("flow service unavailable offline")
				}
				if err := execCtx.Direct.FlowService.MoveProjectToTrash(ctx, id); err != nil {
					return fmt.Errorf("failed to trash project offline: %w", err)
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status":  "trashed",
					"id":      id,
					"message": "Project moved to trash successfully.",
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Project %q moved to trash successfully.\n", id)
			return nil
		},
	}
}

func newFlowProjectsRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <id>",
		Short: "Restore a project from trash",
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
				if err := execCtx.Client.RestoreFlowProject(ctx, id); err != nil {
					return fmt.Errorf("failed to restore project: %w", err)
				}
			} else {
				if execCtx.Direct.FlowService == nil {
					return fmt.Errorf("flow service unavailable offline")
				}
				if err := execCtx.Direct.FlowService.RestoreProject(ctx, id); err != nil {
					return fmt.Errorf("failed to restore project offline: %w", err)
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status":  "restored",
					"id":      id,
					"message": "Project restored successfully.",
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Project %q restored successfully.\n", id)
			return nil
		},
	}
}

func newFlowProjectsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Permanently delete a project from trash",
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
				if err := execCtx.Client.DeleteFlowProject(ctx, id); err != nil {
					return fmt.Errorf("failed to delete project: %w", err)
				}
			} else {
				if execCtx.Direct.FlowService == nil {
					return fmt.Errorf("flow service unavailable offline")
				}
				if err := execCtx.Direct.FlowService.DeleteProjectPermanently(ctx, id); err != nil {
					return fmt.Errorf("failed to delete project offline: %w", err)
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status":  "deleted",
					"id":      id,
					"message": "Project deleted permanently.",
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Project %q deleted permanently.\n", id)
			return nil
		},
	}
}

func newFlowVoicesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "voices",
		Short: "List standard 30 AI voice personas from Google Flow",
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

			var rows []FlowVoiceRow

			if execCtx.IsOnline {
				voices, err := execCtx.Client.ListFlowVoices(ctx)
				if err != nil {
					return fmt.Errorf("failed to list voices: %w", err)
				}
				for _, v := range voices {
					id, _ := v["id"].(string)
					name, _ := v["name"].(string)
					gender, _ := v["gender"].(string)
					desc, _ := v["description"].(string)
					rows = append(rows, FlowVoiceRow{
						ID:          id,
						Name:        name,
						Gender:      gender,
						Description: desc,
					})
				}
			} else {
				var personas []domain.VoicePersona
				if execCtx.Direct.FlowService != nil {
					personas, _ = execCtx.Direct.FlowService.ListVoicePersonas(ctx)
				}
				if len(personas) == 0 {
					personas = domain.DefaultVoicePersonas()
				}
				for _, p := range personas {
					rows = append(rows, FlowVoiceRow{
						ID:          p.ID,
						Name:        p.Name,
						Gender:      p.Gender,
						Description: p.Description,
					})
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				if rows == nil {
					rows = []FlowVoiceRow{}
				}
				return RenderJSON(cmd.OutOrStdout(), rows)
			}

			headers := []string{"ID", "NAME", "GENDER", "DESCRIPTION"}
			tableRows := make([][]string, len(rows))
			for i, r := range rows {
				tableRows[i] = []string{r.ID, r.Name, r.Gender, r.Description}
			}
			RenderTable(cmd.OutOrStdout(), headers, tableRows)
			return nil
		},
	}
}

func newFlowAudioCmd() *cobra.Command {
	var (
		prompt   string
		duration int
	)

	cmd := &cobra.Command{
		Use:   "audio",
		Short: "Generate soundtrack and sound effects via MusicFX",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(prompt) == "" {
				return fmt.Errorf("--prompt is required")
			}
			if duration <= 0 {
				duration = 8
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

			var res map[string]any

			if execCtx.IsOnline {
				data, err := execCtx.Client.GenerateFlowAudio(ctx, prompt, duration)
				if err != nil {
					return fmt.Errorf("failed to generate audio: %w", err)
				}
				res = data
			} else {
				if execCtx.Direct.FlowService == nil {
					return fmt.Errorf("flow service unavailable offline")
				}
				resp, err := execCtx.Direct.FlowService.GenerateAudio(ctx, domain.FlowMusicRequest{
					Prompt:                prompt,
					TargetDurationSeconds: duration,
				})
				if err != nil {
					return fmt.Errorf("failed to generate audio offline: %w", err)
				}
				res = map[string]any{
					"asset_id":          resp.AssetID,
					"url":               resp.URL,
					"mime_type":         resp.MimeType,
					"duration_seconds":  resp.DurationSeconds,
					"credits_deducted":  resp.CreditsDeducted,
					"remaining_credits": resp.RemainingCredits,
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), res)
			}

			headers := []string{"ASSET_ID", "URL", "DURATION", "CREDITS"}
			rows := [][]string{
				{
					fmt.Sprintf("%v", res["asset_id"]),
					fmt.Sprintf("%v", res["url"]),
					fmt.Sprintf("%v", res["duration_seconds"]),
					fmt.Sprintf("%v", res["credits_deducted"]),
				},
			}
			RenderTable(cmd.OutOrStdout(), headers, rows)
			return nil
		},
	}

	cmd.Flags().StringVar(&prompt, "prompt", "", "music and audio prompt description (required)")
	cmd.Flags().IntVar(&duration, "duration", 8, "duration in seconds (4-30)")
	_ = cmd.MarkFlagRequired("prompt")

	return cmd
}
