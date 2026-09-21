package main

import (
	"flag"
	"fmt"
	terminal "github.com/brightman-ai/deepwork-terminal"
	"io"
	"os"
	"path/filepath"
)

func runClipboard(args []string) error {
	if len(args) == 0 || args[0] != "get" {
		return fmt.Errorf("usage: dw-terminal clipboard get --target <target> [--data-dir <dir>]")
	}
	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("clipboard get", flag.ContinueOnError)
	dataDir := fs.String("data-dir", filepath.Join(home, ".dw-terminal"), "server data directory")
	target := fs.String("target", "", "target shown in remote clipboard panel")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *target == "" {
		return fmt.Errorf("--target is required")
	}
	f, err := os.Open(terminal.ClipboardBufferPath(*dataDir, *target))
	if err != nil {
		return fmt.Errorf("no clipboard content for this target")
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}
