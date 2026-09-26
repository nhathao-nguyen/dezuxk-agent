package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// AlertRow represents an alert event for tabular / JSON view.
type AlertRow struct {
	AccountID string `json:"account_id"`
	Service   string `json:"service"`
	Reason    string `json:"reason"`
	Status    int    `json:"status_code,omitempty"`
	Action    string `json:"action_required"`
	CreatedAt string `json:"created_at"`
}

// NewAlertsCmd creates the "alerts" command and its subcommands.
func NewAlertsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alerts",
		Short: "Manage and monitor session drift, token expiration, and auth failure alerts",
	}

	cmd.AddCommand(newAlertsListCmd())
	cmd.AddCommand(newAlertsClearCmd())

	return cmd
}

func newAlertsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all active session alerts",
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

			var rows []AlertRow

			if execCtx.IsOnline {
				alerts, err := execCtx.Client.GetAlerts(ctx)
				if err != nil {
					return fmt.Errorf("failed to fetch alerts from server: %w", err)
				}
				for _, a := range alerts {
					accID, _ := a["account_id"].(string)
					svc, _ := a["service"].(string)
					reason, _ := a["reason"].(string)
					action, _ := a["action_required"].(string)
					created, _ := a["created_at"].(string)
					status := 0
					if s, ok := a["status_code"].(float64); ok {
						status = int(s)
					}
					rows = append(rows, AlertRow{
						AccountID: accID,
						Service:   svc,
						Reason:    reason,
						Status:    status,
						Action:    action,
						CreatedAt: created,
					})
				}
			} else {
				if execCtx.Direct.SessionRepo != nil {
					alerts := execCtx.Direct.SessionRepo.GetAlerts()
					for _, a := range alerts {
						rows = append(rows, AlertRow{
							AccountID: a.AccountID,
							Service:   string(a.Service),
							Reason:    a.Reason,
							Status:    a.StatusCode,
							Action:    a.ActionRequired,
							CreatedAt: a.CreatedAt.Format(time.RFC3339),
						})
					}
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				if rows == nil {
					rows = []AlertRow{}
				}
				return RenderJSON(cmd.OutOrStdout(), rows)
			}

			headers := []string{"ACCOUNT_ID", "SERVICE", "REASON", "STATUS", "ACTION_REQUIRED", "CREATED_AT"}
			tableRows := make([][]string, len(rows))
			for i, r := range rows {
				tableRows[i] = []string{
					r.AccountID,
					r.Service,
					r.Reason,
					fmt.Sprintf("%d", r.Status),
					r.Action,
					r.CreatedAt,
				}
			}
			RenderTable(cmd.OutOrStdout(), headers, tableRows)
			return nil
		},
	}
}

func newAlertsClearCmd() *cobra.Command {
	var account string

	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear all or account-specific alerts",
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
				if err := execCtx.Client.ClearAlerts(ctx, account); err != nil {
					return fmt.Errorf("failed to clear alerts on server: %w", err)
				}
			} else {
				if execCtx.Direct.SessionRepo != nil {
					execCtx.Direct.SessionRepo.ClearAlerts(account)
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status":  "cleared",
					"account": account,
					"message": "Alerts cleared successfully.",
				})
			}

			if account != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Alerts for account %q cleared successfully.\n", account)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "All alerts cleared successfully.")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&account, "account", "", "specific account ID to clear alerts for (optional)")
	return cmd
}
