package cli

import (
	"github.com/spf13/cobra"
)

var (
	ConfigFile string
	Format     string
	ServerURL  string
	Offline    bool
	Token      string

	RootCmd = NewRootCmd()
)

// NewRootCmd initializes and returns the root cobra command.
func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dezuxk",
		Short: "Dezuxk AI Gateway CLI Controller",
	}

	cmd.PersistentFlags().StringVarP(&ConfigFile, "config", "c", "configs/config.yaml", "path to config file")
	cmd.PersistentFlags().StringVarP(&Format, "format", "f", "table", "output format (table, json)")
	cmd.PersistentFlags().StringVar(&ServerURL, "url", "", "Dezuxk server URL")
	cmd.PersistentFlags().BoolVarP(&Offline, "offline", "d", false, "run in offline/direct database mode")
	cmd.PersistentFlags().StringVar(&Token, "token", "", "admin token for remote server")

	cmd.AddCommand(NewStartCmd())
	cmd.AddCommand(NewStatusCmd())
	cmd.AddCommand(NewProfileCmd())
	cmd.AddCommand(NewKeyCmd())

	return cmd
}

// Execute executes the root command.
func Execute() error {
	return RootCmd.Execute()
}
