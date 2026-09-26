package main

import (
	"flag"
	"log"

	"dezuxk-gateway/internal/app/daemon"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "Path to config YAML file")
	portOverride := flag.Int("port", 0, "Port override (optional)")
	flag.Parse()

	if err := daemon.Run(*configPath, *portOverride); err != nil {
		log.Fatalf("[FATAL] Gateway daemon stopped with error: %v", err)
	}
}
