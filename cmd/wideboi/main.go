// Command wideboi is a scrolling tiling terminal multiplexer for CLI agents.
package main

import (
	"fmt"
	"log"
	"os"
	"syscall"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/hostterm"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	t := uv.DefaultTerminal()
	scr := t.Screen()
	scr.EnterAltScreen()

	if err := t.Start(); err != nil {
		return fmt.Errorf("start terminal: %w", err)
	}

	guard := hostterm.NewGuard(func() error {
		scr.ExitAltScreen()
		_ = scr.Flush()
		return t.Stop()
	})
	guard.Arm(syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT)
	defer guard.Stop()

	defer func() {
		if r := recover(); r != nil {
			_ = guard.Stop()
			panic(r)
		}
	}()

	for ev := range t.Events() {
		switch ev := ev.(type) {
		case uv.WindowSizeEvent:
			scr.Resize(ev.Width, ev.Height)
		case uv.KeyPressEvent:
			if ev.MatchString("ctrl+q") {
				return nil
			}
			fmt.Fprintf(os.Stderr, "key: %s\r\n", ev.String())
		}
	}
	return nil
}
