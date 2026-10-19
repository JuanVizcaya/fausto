package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/JuanVizcaya/fausto/internal/client"
	"github.com/JuanVizcaya/fausto/internal/mcp"
)

// version is set at release (-ldflags "-X main.version=...").
var version = "dev"

// runMCP serves fausto's MCP tools over stdio for the clone it runs in, as
// Claude Code starts it (see the plugin's .mcp.json).
func runMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: fausto mcp")
		return 2
	}
	root, _ := findRoot() // the check tool says so when there is none
	c, err := client.Resolve(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := (&mcp.Server{Root: root, Client: c, Version: version}).Serve(ctx, stdin, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
