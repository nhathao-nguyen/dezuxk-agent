package cli_test

import (
	"testing"

	"dezuxk-gateway/internal/adapters/inbound/cli"
)

func TestRootCmd_Flags(t *testing.T) {
	cmd := cli.NewRootCmd()

	if cmd.Use != "dezuxk" {
		t.Errorf("expected Use to be 'dezuxk', got %q", cmd.Use)
	}

	tests := []struct {
		name         string
		shorthand    string
		defaultValue string
	}{
		{"config", "c", "configs/config.yaml"},
		{"format", "f", "table"},
		{"url", "", ""},
		{"offline", "d", "false"},
		{"token", "", ""},
	}

	for _, tt := range tests {
		flag := cmd.PersistentFlags().Lookup(tt.name)
		if flag == nil {
			t.Fatalf("expected persistent flag --%s to exist", tt.name)
		}
		if flag.Shorthand != tt.shorthand {
			t.Errorf("flag --%s shorthand mismatch: got %q, want %q", tt.name, flag.Shorthand, tt.shorthand)
		}
		if flag.DefValue != tt.defaultValue {
			t.Errorf("flag --%s default value mismatch: got %q, want %q", tt.name, flag.DefValue, tt.defaultValue)
		}
	}
}

func TestRootCmd_Execute(t *testing.T) {
	// Root command without subcommands should execute without error
	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error executing root command with --help: %v", err)
	}
}
