package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/server"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/tencent"
)

func main() {
	help := flag.Bool("help", false, "print tool list and exit 0")
	h := flag.Bool("h", false, "alias for --help")
	flag.Parse()

	if *help || *h {
		fmt.Print(tencent.HelpText())
		return
	}

	if err := server.ServeStdio(tencent.NewServer(tencent.DefaultRuntime())); err != nil {
		fmt.Fprintf(os.Stderr, "tencent-cloud-mcp: serve: %v\n", err)
		os.Exit(1)
	}
}
