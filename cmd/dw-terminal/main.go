package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	terminal "github.com/brightman-ai/deepwork-terminal"
)

// Build metadata, injected at release time via -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
// Defaults make `dw-terminal --version` meaningful for source builds too.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	addr := flag.String("addr", ":8022", "listen address")
	shell := flag.String("shell", "", "shell command (default: $SHELL)")
	authCode := flag.String("auth-code", "", "auth code (default: generated)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("dw-terminal %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	cfg := terminal.DefaultConfig()
	cfg.Addr = *addr
	cfg.Version = version
	if *authCode != "" {
		cfg.AuthCode = *authCode
	}
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
