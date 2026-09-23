package main

import (
	"context"
	"log"

	"github.com/PatrickFanella/get-rich-quick/internal/cli"
)

var (
	version          = "dev"
	sourceCommit     = "unknown"
	sourceTreeSHA256 = "unknown"
)

func main() {
	if err := cli.Execute(context.Background(), cli.Dependencies{
		Version:      version,
		NewAPIServer: newAPIServer,
	}); err != nil {
		log.Fatalf("tradingagent: %v", err)
	}
}
