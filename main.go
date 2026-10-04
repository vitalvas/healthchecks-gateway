package main

import (
	"log/slog"
	"os"

	"github.com/vitalvas/healthchecks-gateway/internal/app"
)

// version is injected at build time via -ldflags -X main.version.
var version = "dev"

func main() {
	if err := app.Run(os.Args[1:], version); err != nil {
		slog.Error("gateway exited with error", "error", err)
		os.Exit(1)
	}
}
