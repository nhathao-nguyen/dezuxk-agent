package cli

import (
	"dezuxk-gateway/internal/app/daemon"

	"github.com/spf13/cobra"
)

// DaemonRunner is the function that launches the daemon server, overridable in tests.
var DaemonRunner = daemon.Run

// NewStartCmd creates the "start" command (alias: "run").
func NewStartCmd() *cobra.Command {
	var portOverride int

	cmd := &cobra.Command{
		Use:     "start",
		Aliases: []string{"run"},
		Short:   "Start Dezuxk AI Gateway daemon server",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath := getStringFlag(cmd, "config", ConfigFile)
			return DaemonRunner(configPath, portOverride)
		},
	}

	cmd.Flags().IntVarP(&portOverride, "port", "p", 0, "port override for the server")

	return cmd
}
