package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"

	terminal "github.com/brightman-ai/deepwork-terminal"
)

// Build metadata, injected at release time via -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
// Defaults make `dw-terminal --version` meaningful for source builds too.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// resolveVersion returns the version to surface (CLI + UI badge). A release injects a clean
// tag via ldflags. A plain `go build` leaves it "dev" — opaque, you can't tell which commit
// it's from — so we enrich it from the VCS stamp Go embeds automatically (debug.ReadBuildInfo):
// "dev-<shorthash>" (+"-dirty" when the tree had uncommitted changes). build.sh injects a
// `git describe` instead (e.g. "v0.5.0" or "v0.5.0-3-g<hash>"), which takes precedence here.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return version
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	v := "dev-" + rev
	if dirty {
		v += "-dirty"
	}
	return v
}

func main() {
	addr := flag.String("addr", ":8022", "listen address")
	shell := flag.String("shell", "", "shell command (default: $SHELL)")
	authCode := flag.String("auth-code", "", "auth code (default: generated)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	resolvedVersion := resolveVersion()

	if *showVersion {
		fmt.Printf("dw-terminal %s (commit %s, built %s)\n", resolvedVersion, commit, date)
		return
	}

	cfg := terminal.DefaultConfig()
	cfg.Addr = *addr
	cfg.Version = resolvedVersion
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
