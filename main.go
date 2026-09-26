package main

import (
	"os"

	"dezuxk-gateway/internal/adapters/inbound/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
