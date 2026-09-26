package cli

import (
	"fmt"

	"dezuxk-gateway/internal/config"

	"github.com/spf13/cobra"
)

// ExecutionContext holds the operational environment (Client or Direct DB) and flags for CLI commands.
type ExecutionContext struct {
	IsOnline   bool
	Format     string // "table" or "json"
	Client     *GatewayClient
	Direct     *DirectServices
	ConfigPath string
}

// Close gracefully releases any direct database locks or opened resources.
func (e *ExecutionContext) Close() error {
	if e == nil {
		return nil
	}
	if e.Direct != nil {
		return e.Direct.Close()
	}
	return nil
}

func getStringFlag(cmd *cobra.Command, name, fallback string) string {
	if cmd != nil {
		if f := cmd.Flag(name); f != nil && f.Value.String() != "" {
			return f.Value.String()
		}
	}
	return fallback
}

func getBoolFlag(cmd *cobra.Command, name string, fallback bool) bool {
	if cmd != nil {
		if f := cmd.Flag(name); f != nil {
			b, err := cmd.Flags().GetBool(name)
			if err == nil {
				return b
			}
			return f.Value.String() == "true"
		}
	}
	return fallback
}

// GetExecutionContext resolves execution mode (Online vs Direct) based on flags and daemon status.
func GetExecutionContext(cmd *cobra.Command) (*ExecutionContext, error) {
	configPath := getStringFlag(cmd, "config", ConfigFile)
	if configPath == "" {
		configPath = "configs/config.yaml"
	}

	format := getStringFlag(cmd, "format", Format)
	if format == "" {
		format = "table"
	}

	serverURL := getStringFlag(cmd, "url", ServerURL)
	offline := getBoolFlag(cmd, "offline", Offline)
	token := getStringFlag(cmd, "token", Token)

	// Explicit offline mode
	if offline {
		direct, err := InitDirectServices(configPath)
		if err != nil {
			return nil, err
		}
		return &ExecutionContext{
			IsOnline:   false,
			Format:     format,
			Direct:     direct,
			ConfigPath: configPath,
		}, nil
	}

	// Auto-detect target server URL if not specified
	if serverURL == "" {
		cfg, err := config.LoadConfig(configPath)
		if err == nil && cfg.Server.Port > 0 {
			host := cfg.Server.Host
			if host == "" {
				host = "127.0.0.1"
			}
			serverURL = fmt.Sprintf("http://%s:%d", host, cfg.Server.Port)
		} else {
			serverURL = "http://127.0.0.1:8080"
		}
	}

	// Probe daemon
	if ProbeDaemon(serverURL, 500) {
		client := NewGatewayClient(serverURL, token)
		return &ExecutionContext{
			IsOnline:   true,
			Format:     format,
			Client:     client,
			ConfigPath: configPath,
		}, nil
	}

	// Automatic fallback to Direct SQLite/Vault mode
	direct, err := InitDirectServices(configPath)
	if err != nil {
		return nil, fmt.Errorf("daemon is offline (%s) and direct mode failed: %w", serverURL, err)
	}

	return &ExecutionContext{
		IsOnline:   false,
		Format:     format,
		Direct:     direct,
		ConfigPath: configPath,
	}, nil
}
