package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/server"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/cloud"
)

func main() {
	help := flag.Bool("help", false, "print tool list and exit 0")
	shortHelp := flag.Bool("h", false, "alias for --help")
	flag.Parse()
	if *help || *shortHelp {
		fmt.Print(cloud.HelpText())
		return
	}
	if err := server.ServeStdio(cloud.NewServer(cloud.DefaultRuntime())); err != nil {
		fmt.Fprintf(os.Stderr, "cloud-skills-mcp: serve: %v\n", err)
		os.Exit(1)
	}
}
