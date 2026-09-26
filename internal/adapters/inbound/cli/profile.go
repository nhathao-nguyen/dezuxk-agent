package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// ProfileRow represents tabular and json output for profile accounts.
type ProfileRow struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Tier        string `json:"tier"`
	Health      string `json:"health"`
	Proxy       string `json:"proxy"`
	FlowCredits int    `json:"flow_credits"`
	Services    string `json:"services"`
}

// NewProfileCmd creates the "profile" command and its subcommands.
func NewProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage Chrome profiles, cookies, and account authentication",
	}

	cmd.AddCommand(newProfileListCmd())
	cmd.AddCommand(newProfileCreateCmd())
	cmd.AddCommand(newProfileLaunchCmd())
	cmd.AddCommand(newProfileSyncCmd())
	cmd.AddCommand(newProfileIngestCmd())
	cmd.AddCommand(newProfileProxyCmd())

	return cmd
}

func newProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all managed accounts and profiles",
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

			var rows []ProfileRow

			if execCtx.IsOnline {
				profiles, err := execCtx.Client.ListProfiles(ctx)
				if err != nil {
					return fmt.Errorf("failed to fetch profiles from server: %w", err)
				}

				for _, p := range profiles {
					id, _ := p["id"].(string)
					email, _ := p["email"].(string)
					tier, _ := p["tier"].(string)
					if tier == "" {
						tier = "Free"
					}

					health := "Degraded"
					if isH, ok := p["is_healthy"].(bool); ok && isH {
						health = "Healthy"
					} else if isL, ok := p["is_logged_in"].(bool); ok && isL {
						health = "Healthy"
					} else if st, ok := p["status"].(string); ok && (st == "active" || st == "healthy") {
						health = "Healthy"
					}

					proxy, _ := p["proxy"].(string)

					credits := 0
					if c, ok := p["flow_credits"].(float64); ok {
						credits = int(c)
					} else if c, ok := p["credits"].(float64); ok {
						credits = int(c)
					} else if c, ok := p["flow_credits"].(int); ok {
						credits = c
					}

					var svcs []string
					if hasF, ok := p["has_flow"].(bool); ok && hasF {
						svcs = append(svcs, "flow")
					}
					if hasG, ok := p["has_gemini"].(bool); ok && hasG {
						svcs = append(svcs, "gemini")
					}
					servicesStr := strings.Join(svcs, ", ")
					if servicesStr == "" {
						servicesStr = "-"
					}

					rows = append(rows, ProfileRow{
						ID:          id,
						Email:       email,
						Tier:        tier,
						Health:      health,
						Proxy:       proxy,
						FlowCredits: credits,
						Services:    servicesStr,
					})
				}
			} else {
				seen := make(map[string]bool)

				if execCtx.Direct.SessionRepo != nil {
					accounts := execCtx.Direct.SessionRepo.ListAll(ctx)
					for _, acc := range accounts {
						if acc == nil {
							continue
						}
						seen[acc.ID] = true
						tierStr := "Free"
						if acc.Tier == 2 {
							tierStr = "Pro"
						} else if acc.Tier == 3 {
							tierStr = "Ultra"
						}

						health := "Degraded"
						if acc.IsHealthy {
							health = "Healthy"
						}

						var svcs []string
						if acc.Jar != nil {
							if acc.Jar.HasKey("OSID") {
								svcs = append(svcs, "flow")
							}
							if acc.Jar.HasKey("__Secure-1PSID") {
								svcs = append(svcs, "gemini")
							}
						}
						servicesStr := strings.Join(svcs, ", ")
						if servicesStr == "" {
							servicesStr = "-"
						}

						rows = append(rows, ProfileRow{
							ID:          acc.ID,
							Email:       acc.Email,
							Tier:        tierStr,
							Health:      health,
							Proxy:       acc.GetProxy(),
							FlowCredits: acc.CreditsBalance,
							Services:    servicesStr,
						})
					}
				}

				if execCtx.Direct.ProfileManager != nil {
					profs := execCtx.Direct.ProfileManager.ListActiveProfiles()
					for _, prof := range profs {
						if prof == nil || seen[prof.ID] {
							continue
						}
						health := "Degraded"
						if prof.IsLoggedIn {
							health = "Healthy"
						}
						var svcs []string
						if prof.HasFlow {
							svcs = append(svcs, "flow")
						}
						if prof.HasGemini {
							svcs = append(svcs, "gemini")
						}
						servicesStr := strings.Join(svcs, ", ")
						if servicesStr == "" {
							servicesStr = "-"
						}

						rows = append(rows, ProfileRow{
							ID:          prof.ID,
							Email:       prof.Email,
							Tier:        "Free",
							Health:      health,
							Proxy:       prof.Proxy,
							FlowCredits: prof.FlowCredits,
							Services:    servicesStr,
						})
					}
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				if rows == nil {
					rows = []ProfileRow{}
				}
				return RenderJSON(cmd.OutOrStdout(), rows)
			}

			headers := []string{"ID", "EMAIL", "TIER", "HEALTH", "PROXY", "FLOW_CREDITS", "SERVICES"}
			tableRows := make([][]string, len(rows))
			for i, r := range rows {
				tableRows[i] = []string{
					r.ID,
					r.Email,
					r.Tier,
					r.Health,
					r.Proxy,
					fmt.Sprintf("%d", r.FlowCredits),
					r.Services,
				}
			}
			RenderTable(cmd.OutOrStdout(), headers, tableRows)
			return nil
		},
	}
}

func newProfileCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <id>",
		Short: "Create a new dedicated Chrome profile",
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
				if err := execCtx.Client.CreateProfile(ctx, id); err != nil {
					return fmt.Errorf("failed to create profile: %w", err)
				}
			} else {
				if _, err := execCtx.Direct.ProfileManager.CreateProfile(id); err != nil {
					return fmt.Errorf("failed to create profile offline: %w", err)
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status":  "created",
					"profile": id,
					"message": "Profile created successfully.",
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Profile %q created successfully.\n", id)
			return nil
		},
	}
}

func newProfileLaunchCmd() *cobra.Command {
	var cdpPort int
	var headless bool

	cmd := &cobra.Command{
		Use:   "launch <id>",
		Short: "Launch Chrome browser with isolated profile",
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
				if err := execCtx.Client.LaunchChrome(ctx, id, cdpPort, headless); err != nil {
					return fmt.Errorf("failed to launch Chrome: %w", err)
				}
			} else {
				if err := execCtx.Direct.ProfileManager.LaunchChromeForProfile(id); err != nil {
					return fmt.Errorf("failed to launch Chrome offline: %w", err)
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status":  "launched",
					"profile": id,
					"message": "Chrome launched successfully.",
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Chrome launched for profile %q.\n", id)
			return nil
		},
	}

	cmd.Flags().IntVar(&cdpPort, "cdp-port", 0, "CDP remote debugging port")
	cmd.Flags().BoolVar(&headless, "headless", false, "run Chrome in headless mode")

	return cmd
}

func newProfileSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync <id>",
		Short: "Synchronize cookies from Chrome via CDP WebSocket",
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
				if err := execCtx.Client.SyncCDP(ctx, id); err != nil {
					return fmt.Errorf("failed to sync cookies from CDP: %w", err)
				}
			} else {
				if _, err := execCtx.Direct.ProfileManager.SyncCookiesFromCDP(ctx, id); err != nil {
					return fmt.Errorf("failed to sync cookies offline: %w", err)
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status":  "synced",
					"profile": id,
					"message": "Cookies synced successfully from CDP.",
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Cookies synced successfully for profile %q.\n", id)
			return nil
		},
	}
}

func newProfileIngestCmd() *cobra.Command {
	var filePath string
	var jsonContent string

	cmd := &cobra.Command{
		Use:   "ingest <id>",
		Short: "Import cookie JSON or string into profile session",
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

			var raw []byte
			if filePath != "" {
				b, err := os.ReadFile(filePath)
				if err != nil {
					return fmt.Errorf("failed to read cookie file: %w", err)
				}
				raw = b
			} else if jsonContent != "" {
				raw = []byte(jsonContent)
			} else {
				return fmt.Errorf("either --file or --json must be specified")
			}

			if execCtx.IsOnline {
				if err := execCtx.Client.IngestCookies(ctx, id, raw); err != nil {
					return fmt.Errorf("failed to ingest cookies: %w", err)
				}
			} else {
				cookieMap, err := parseCookiesPayload(raw)
				if err != nil {
					return fmt.Errorf("invalid cookie format: %w", err)
				}
				if _, err := execCtx.Direct.ProfileManager.IngestLiveCookies(ctx, id, "", cookieMap, ""); err != nil {
					return fmt.Errorf("failed to ingest cookies offline: %w", err)
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status":  "ingested",
					"profile": id,
					"message": "Cookies ingested successfully.",
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Cookies ingested successfully for profile %q.\n", id)
			return nil
		},
	}

	cmd.Flags().StringVar(&filePath, "file", "", "path to cookie file (JSON or raw cookie string)")
	cmd.Flags().StringVar(&jsonContent, "json", "", "cookie JSON string or raw cookie string")

	return cmd
}

func newProfileProxyCmd() *cobra.Command {
	var proxyURL string

	cmd := &cobra.Command{
		Use:   "proxy <id>",
		Short: "Configure proxy for profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if proxyURL == "" {
				return fmt.Errorf("--url flag is required")
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

			if execCtx.IsOnline {
				if err := execCtx.Client.SetProxy(ctx, id, proxyURL); err != nil {
					return fmt.Errorf("failed to set proxy: %w", err)
				}
			} else {
				if err := execCtx.Direct.ProfileManager.SetProfileProxy(id, proxyURL); err != nil {
					return fmt.Errorf("failed to set proxy offline: %w", err)
				}
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), map[string]any{
					"status":  "updated",
					"profile": id,
					"proxy":   proxyURL,
					"message": "Proxy updated successfully.",
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Proxy updated for profile %q: %s\n", id, proxyURL)
			return nil
		},
	}

	cmd.Flags().StringVar(&proxyURL, "url", "", "proxy URL (e.g. http://127.0.0.1:8080 or socks5://127.0.0.1:1080)")
	_ = cmd.MarkFlagRequired("url")

	return cmd
}

func parseCookiesPayload(raw []byte) (map[string]string, error) {
	cookieMap := make(map[string]string)

	// 1. Try Map: {"OSID":"...", "__Secure-1PSID":"..."}
	var asMap map[string]string
	if err := json.Unmarshal(raw, &asMap); err == nil && len(asMap) > 0 {
		return asMap, nil
	}

	// 2. Try CDP Array: [{"name":"OSID", "value":"...", "domain":"..."}, ...]
	var asArray []struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Domain string `json:"domain"`
	}
	if err := json.Unmarshal(raw, &asArray); err == nil && len(asArray) > 0 {
		for _, c := range asArray {
			d := strings.TrimPrefix(c.Domain, ".")
			if d == "google.com" || d == "gemini.google.com" || d == "flow.google.com" {
				cookieMap[c.Name] = c.Value
			}
		}
		for _, c := range asArray {
			if _, exists := cookieMap[c.Name]; !exists && strings.Contains(c.Domain, "google.com") && !strings.Contains(c.Domain, ".vn") {
				cookieMap[c.Name] = c.Value
			}
		}
		if len(cookieMap) > 0 {
			return cookieMap, nil
		}
	}

	// 3. Try Raw cookie string: "OSID=...; __Secure-1PSID=..."
	str := string(raw)
	parts := strings.Split(str, ";")
	for _, p := range parts {
		kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
		if len(kv) == 2 {
			cookieMap[kv[0]] = kv[1]
		}
	}
	if len(cookieMap) > 0 {
		return cookieMap, nil
	}

	return nil, fmt.Errorf("no valid cookies found in payload")
}
