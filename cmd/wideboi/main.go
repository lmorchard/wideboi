// Command wideboi is a scrolling tiling terminal multiplexer for CLI agents.
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"syscall"
	"time"

	"github.com/lmorchard/wideboi/internal/hostterm"
	"github.com/lmorchard/wideboi/internal/server/ptyx"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}

	cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		cols, rows = 80, 24
	}

	pane, err := ptyx.Spawn([]string{shell}, cols, rows, cwd)
	if err != nil {
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
		return fmt.Errorf("spawn: %w", err)
	}

	guard := hostterm.NewGuard(func() error {
		_ = pane.Kill(2 * time.Second)
		return term.Restore(int(os.Stdin.Fd()), oldState)
	})
	guard.Arm(syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer guard.Stop()

	go func() { _, _ = io.Copy(pane.Master, os.Stdin) }()
	_, _ = io.Copy(os.Stdout, pane.Master) // returns when the child exits
	return nil
}
