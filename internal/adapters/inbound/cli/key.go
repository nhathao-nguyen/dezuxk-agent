package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dezuxk-gateway/internal/core/domain"

	"github.com/spf13/cobra"
)

// KeyRow represents a Virtual API Key for tabular and JSON views.
type KeyRow struct {
	KeyID     string `json:"key_id"`
	Name      string `json:"name"`
	MaskedKey string `json:"masked_key"`
	RPMLimit  int    `json:"rpm_limit"`
	IsAdmin   bool   `json:"is_admin"`
	CreatedAt string `json:"created_at"`
}

// NewKeyCmd creates the "key" command and its subcommands.
func NewKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Manage Virtual API Keys and rate limits",
	}

	cmd.AddCommand(newKeyListCmd())
	cmd.AddCommand(newKeyCreateCmd())
	cmd.AddCommand(newKeyRevokeCmd())

	return cmd
}

func newKeyListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all active Virtual API Keys",
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

			var rows []KeyRow

			if execCtx.IsOnline {
				keys, err := execCtx.Client.ListKeys(ctx)
				if err != nil {
					return fmt.Errorf("failed to list keys from server: %w", err)
				}

				for _, k := range keys {
					keyID, _ := k["id"].(string)
					name, _ := k["name"].(string)
					masked, _ := k["masked_key"].(string)
					if masked == "" {
						if prefix, ok := k["key_prefix"].(string); ok && prefix != "" {
							masked = prefix + "..."
						} else {
							masked = keyID
						}
					}

					rpm := 0
					if r, ok := k["rate_limit_rpm"].(float64); ok {
						rpm = int(r)
					} else if r, ok := k["rate_limit_rpm"].(int); ok {
						rpm = r
					}

					isAdmin := false
					if adm, ok := k["is_admin"].(bool); ok {
						isAdmin = adm
					} else if role, ok := k["role"].(string); ok {
						isAdmin = strings.EqualFold(role, "admin")
					}

					createdAt, _ := k["created_at"].(string)
					if createdAt == "" {
						createdAt = "-"
					}

					rows = append(rows, KeyRow{
						KeyID:     keyID,
						Name:      name,
						MaskedKey: masked,
						RPMLimit:  rpm,
						IsAdmin:   isAdmin,
						CreatedAt: createdAt,
					})
				}
			} else {
				if execCtx.Direct.KeyService != nil {
					keys, err := execCtx.Direct.KeyService.ListActiveKeys(ctx)
					if err != nil {
						return fmt.Errorf("failed to list keys offline: %w", err)
					}

					for _, k := range keys {
						if k == nil {
							continue
						}
						masked := k.KeyPrefix + "..."
						if k.KeyPrefix == "" {
							masked = k.ID
						}
						rows = append(rows, KeyRow{
							KeyID:     k.ID,
							Name:      k.Name,
							MaskedKey: masked,
							RPMLimit:  k.RateLimitRPM,
							IsAdmin:   strings.EqualFold(k.Role, "admin"),
							CreatedAt: k.CreatedAt.Format(time.RFC3339),
						})
					}
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				if rows == nil {
					rows = []KeyRow{}
				}
				return RenderJSON(cmd.OutOrStdout(), rows)
			}

			headers := []string{"KEY_ID", "NAME", "MASKED_KEY", "RPM_LIMIT", "IS_ADMIN", "CREATED_AT"}
			tableRows := make([][]string, len(rows))
			for i, r := range rows {
				tableRows[i] = []string{
					r.KeyID,
					r.Name,
					r.MaskedKey,
					fmt.Sprintf("%d", r.RPMLimit),
					fmt.Sprintf("%t", r.IsAdmin),
					r.CreatedAt,
				}
			}
			RenderTable(cmd.OutOrStdout(), headers, tableRows)
			return nil
		},
	}
}

func newKeyCreateCmd() *cobra.Command {
	var name string
	var rpm int
	var isAdmin bool

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new Virtual API Key",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("--name is required")
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

			var keyID, keySecret, role string
			var rateLimitRPM int

			if execCtx.IsOnline {
				res, err := execCtx.Client.CreateKey(ctx, name, rpm, isAdmin)
				if err != nil {
					return fmt.Errorf("failed to create key: %w", err)
				}
				keyID, _ = res["id"].(string)
				keySecret, _ = res["key"].(string)
				role, _ = res["role"].(string)
				if r, ok := res["rate_limit_rpm"].(float64); ok {
					rateLimitRPM = int(r)
				} else if r, ok := res["rate_limit_rpm"].(int); ok {
					rateLimitRPM = r
				}
			} else {
				roleStr := "user"
				if isAdmin {
					roleStr = "admin"
				}
				created, err := execCtx.Direct.KeyService.CreateKey(ctx, domain.CreateKeyRequest{
					Name:         name,
					Role:         roleStr,
					RateLimitRPM: rpm,
				})
				if err != nil {
					return fmt.Errorf("failed to create key offline: %w", err)
				}
				keyID = created.ID
				keySecret = created.Key
				role = created.Role
				rateLimitRPM = created.RateLimitRPM
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status":         "created",
					"id":             keyID,
					"name":           name,
					"key":            keySecret,
					"role":           role,
					"rate_limit_rpm": rateLimitRPM,
				})
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Virtual API Key created successfully:")
			fmt.Fprintf(cmd.OutOrStdout(), "  Key ID:        %s\n", keyID)
			fmt.Fprintf(cmd.OutOrStdout(), "  Name:          %s\n", name)
			fmt.Fprintf(cmd.OutOrStdout(), "  Role:          %s\n", role)
			fmt.Fprintf(cmd.OutOrStdout(), "  RPM Limit:     %d\n", rateLimitRPM)
			fmt.Fprintf(cmd.OutOrStdout(), "  Generated Key: %s\n\n", keySecret)
			fmt.Fprintln(cmd.OutOrStdout(), "IMPORTANT: Please copy your secret key now. It cannot be retrieved again.")
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "name of the virtual key (required)")
	cmd.Flags().IntVar(&rpm, "rpm", 60, "rate limit in requests per minute")
	cmd.Flags().BoolVar(&isAdmin, "admin", false, "grant administrative privileges")
	_ = cmd.MarkFlagRequired("name")

	return cmd
}

func newKeyRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an active Virtual API Key",
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
				if err := execCtx.Client.RevokeKey(ctx, id); err != nil {
					return fmt.Errorf("failed to revoke key: %w", err)
				}
			} else {
				if err := execCtx.Direct.KeyService.RevokeKey(ctx, id); err != nil {
					return fmt.Errorf("failed to revoke key offline: %w", err)
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status":  "revoked",
					"id":      id,
					"message": "Key revoked successfully.",
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Virtual key %q has been revoked successfully.\n", id)
			return nil
		},
	}
}
