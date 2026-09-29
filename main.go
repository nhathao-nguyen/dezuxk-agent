package main

import (
	"flag"
	"fmt"
	"os"

	"dezuxk-gateway/internal/app/daemon"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "Đường dẫn file cấu hình YAML")
	port := flag.Int("port", 0, "Cổng lắng nghe của server (0 để dùng port trong file cấu hình)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Dezuxk AI Gateway Server (Google Gemini)\n\n")
		fmt.Fprintf(os.Stderr, "Cách dùng: %s [flags]\n\nCác cờ cấu hình:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if err := daemon.Run(*configPath, *port); err != nil {
		fmt.Fprintf(os.Stderr, "Lỗi: %v\n", err)
		os.Exit(1)
	}
}
