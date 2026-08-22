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

// resolveVersion returns the version to surface (CLI + UI badge). The rule itself lives in
// terminal.BuildVersion — deepwork-pro's dw-host embeds this same package and needs the exact
// same answer for its own badge, so "what a build's identity looks like" is defined once there
// rather than once per binary.
func resolveVersion() string {
	return terminal.BuildVersion(version)
}

func main() {
	// Subcommand dispatch. `dw-terminal muxd` runs the session daemon in the foreground;
	// the server spawns it automatically (connect-or-spawn, exactly like a tmux client),
	// so this exists for supervision, debugging, and the isolated fixtures tests use.
	//
	// It is the SAME binary rather than a second one on purpose: the daemon and the
	// server then always speak the same protocol version, and there is no way to install
	// or upgrade one without the other.
	// `attach` and `ls` are deliberately TOP-LEVEL rather than `muxd --attach`: attaching to
	// your own terminal is an everyday user action, while `muxd` is where you go to operate
	// the daemon. Filing them under the daemon would make the common case read like
	// administration.
	if len(os.Args) > 1 {
		var run func([]string) error
		switch os.Args[1] {
		case "muxd":
			run = runMuxd
		case "attach":
			run = runAttach
		case "ls":
			run = runList
		}
		if run != nil {
			if err := run(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", os.Args[1], err)
				os.Exit(1)
			}
			return
		}
	}

	// A self-explaining --help: an agent (or a human) running `dw-terminal --help` should be
	// able to drive the tool from this text alone — what each flag means, and copy-pasteable
	// examples for the common intents (LAN-only, public access, pinned code).
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprint(out, "dw-terminal — a mobile-first web terminal to watch and steer your AI coding\n"+
			"agents (Claude Code / Codex) from your phone or another machine.\n\n"+
			"Usage:\n  dw-terminal [flags]\n\nFlags:\n")
		flag.PrintDefaults()
		fmt.Fprint(out, "\nExamples:\n"+
			"  dw-terminal --addr :8222              serve on port 8222 (reachable on your LAN)\n"+
			"  dw-terminal --addr :8222 --tunnel     also expose it on the public internet\n"+
			"  dw-terminal --auth-code 1234-5678     pin a known login code instead of a random one\n\n"+
			"On startup the reachable URLs and the login code are printed to stdout.\n")
	}

	addr := flag.String("addr", ":8022", "listen address; \":8222\" = port 8222 on all interfaces")
	tunnel := flag.Bool("tunnel", false, "expose over the public internet via a Cloudflare quick tunnel (no account needed)")
	shell := flag.String("shell", "", "shell to launch for each session (default: $SHELL)")
	authCode := flag.String("auth-code", "", "login code (default: a random code, printed at startup)")
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
	// 本二进制就是 deepwork-terminal —— 由它显式声明自己的上游（Config.ReleaseRepo 默认为空，
	// 嵌入方不会被动继承这个身份）。
	cfg.ReleaseRepo = "brightman-ai/deepwork-terminal"
	cfg.Tunnel = *tunnel
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

	// The server owns the startup banner (addresses + auth code, and the tunnel URL when
	// --tunnel is set) — a single output owner, so main.go prints nothing here.
	if err := srv.ListenAndServe(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
	}
}
