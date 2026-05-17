package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	terminal "github.com/brightman-ai/deepwork-terminal"
)

func main() {
	addr := flag.String("addr", ":8022", "listen address")
	shell := flag.String("shell", "", "shell command (default: $SHELL)")
	flag.Parse()

	cfg := terminal.DefaultConfig()
	cfg.Addr = *addr
	if *shell != "" {
		cfg.DefaultShell = *shell
	}

	srv, err := terminal.NewServer(terminal.WithConfig(cfg))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	fmt.Printf("dw-terminal listening on http://localhost%s\n", cfg.Addr)
	if err := srv.ListenAndServe(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
	}
}
