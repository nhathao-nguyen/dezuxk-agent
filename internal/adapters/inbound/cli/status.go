package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// StatusOutput represents health and operational overview metrics.
type StatusOutput struct {
	ServerStatus    string `json:"server_status"`
	Address         string `json:"address"`
	ActiveModels    int    `json:"active_models"`
	TotalAccounts   int    `json:"total_accounts"`
	HealthyAccounts int    `json:"healthy_accounts"`
	PendingAlerts   int    `json:"pending_alerts"`
}

// NewStatusCmd creates the "status" command.
func NewStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Display Dezuxk AI Gateway health and cluster status",
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

			var status StatusOutput

			if execCtx.IsOnline {
				status.ServerStatus = "Online"
				status.Address = execCtx.Client.BaseURL

				overview, err := execCtx.Client.GetOverview(ctx)
				if err != nil {
					// Fallback to public /health endpoint
					health, healthErr := execCtx.Client.GetHealth(ctx)
					if healthErr != nil {
						return fmt.Errorf("failed to get status from server: %w", err)
					}
					status.ServerStatus = "Online"
					if s, ok := health["status"].(string); ok {
						status.ServerStatus = fmt.Sprintf("Online (%s)", s)
					}
					if am, ok := health["models_active"].(float64); ok {
						status.ActiveModels = int(am)
					}
					if al, ok := health["alerts"].([]any); ok {
						status.PendingAlerts = len(al)
					}
				} else {
					if models, ok := overview["models"].([]any); ok {
					status.ActiveModels = len(models)
				} else if modelsMap, ok := overview["models"].([]map[string]any); ok {
					status.ActiveModels = len(modelsMap)
				} else if am, ok := overview["active_models"].(float64); ok {
					status.ActiveModels = int(am)
				}

				if accounts, ok := overview["accounts"].([]any); ok {
					status.TotalAccounts = len(accounts)
					for _, item := range accounts {
						if accMap, ok := item.(map[string]any); ok {
							if isHealthy, ok := accMap["is_healthy"].(bool); ok && isHealthy {
								status.HealthyAccounts++
							}
						}
					}
				} else if accountsMap, ok := overview["accounts"].([]map[string]any); ok {
					status.TotalAccounts = len(accountsMap)
					for _, item := range accountsMap {
						if isHealthy, ok := item["is_healthy"].(bool); ok && isHealthy {
							status.HealthyAccounts++
						}
					}
				}

				if alerts, ok := overview["alerts"].([]any); ok {
					status.PendingAlerts = len(alerts)
				} else if alertsMap, ok := overview["alerts"].([]map[string]any); ok {
					status.PendingAlerts = len(alertsMap)
				}
			}
		} else {
				status.ServerStatus = "Offline"
				status.Address = execCtx.ConfigPath

				if execCtx.Direct.ModelRegistry != nil {
					status.ActiveModels = len(execCtx.Direct.ModelRegistry.List())
				}

				if execCtx.Direct.SessionRepo != nil {
					accounts := execCtx.Direct.SessionRepo.ListAll(ctx)
					status.TotalAccounts = len(accounts)
					for _, acc := range accounts {
						if acc != nil && acc.IsHealthy {
							status.HealthyAccounts++
						}
					}
					alerts := execCtx.Direct.SessionRepo.GetAlerts()
					status.PendingAlerts = len(alerts)
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), status)
			}

			headers := []string{"STATUS", "ADDRESS", "MODELS", "TOTAL_ACCOUNTS", "HEALTHY_ACCOUNTS", "PENDING_ALERTS"}
			rows := [][]string{
				{
					status.ServerStatus,
					status.Address,
					fmt.Sprintf("%d", status.ActiveModels),
					fmt.Sprintf("%d", status.TotalAccounts),
					fmt.Sprintf("%d", status.HealthyAccounts),
					fmt.Sprintf("%d", status.PendingAlerts),
				},
			}
			RenderTable(cmd.OutOrStdout(), headers, rows)
			return nil
		},
	}

	return cmd
}
