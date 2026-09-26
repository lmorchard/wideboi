package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/lmorchard/wideboi/internal/commands"
	"github.com/lmorchard/wideboi/internal/config"
)

func runPalette(cfg config.Config, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("palette", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var callerPane int
	var session, socket string

	fs.IntVar(&callerPane, "caller-pane", 0, "pane ID that invoked the palette")
	fs.StringVar(&session, "L", "", "session name")
	fs.StringVar(&session, "session", "", "session name")
	fs.StringVar(&socket, "s", "", "unix domain socket path")
	fs.StringVar(&socket, "socket", "", "unix domain socket path")

	if err := fs.Parse(reorderFlags(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	applySessionFlags(&cfg, session, socket)

	allCmds := commands.DefaultRegistry.All()
	inv := commands.Invocation{
		Cfg:          cfg,
		Socket:       cfg.Socket,
		CallerPaneID: callerPane,
		Stdout:       stdout,
		Stderr:       stderr,
	}

	file, isFile := stdin.(*os.File)
	isTTY := isFile && term.IsTerminal(file.Fd())

	if isTTY {
		state, err := term.MakeRaw(file.Fd())
		if err == nil {
			defer func() { _ = term.Restore(file.Fd(), state) }()
		}
	}

	if !isTTY {
		// Non-interactive mode
		reader := bufio.NewReader(stdin)
		query, _ := reader.ReadString('\n')
		query = strings.TrimRight(query, "\r\n")
		if query == "\x1b" || query == "\x03" {
			return nil
		}
		matches := filterCommands(allCmds, query)
		for _, m := range matches {
			_, _ = fmt.Fprintf(stdout, "%s - %s\n", m.Name, m.Description)
		}
		if len(matches) > 0 && query != "" {
			_ = commands.DefaultRegistry.Execute(context.Background(), inv, matches[0].Name)
		}
		return nil
	}

	// Interactive TTY mode
	var query strings.Builder
	selected := 0

	render := func() {
		matches := filterCommands(allCmds, query.String())
		if selected >= len(matches) {
			selected = max(len(matches)-1, 0)
		}

		// Move cursor to top-left and clear screen
		var b strings.Builder
		b.WriteString("\x1b[H\x1b[2J")
		b.WriteString(fmt.Sprintf("\x1b[1m> %s\x1b[0m\x1b[7m \x1b[0m\r\n", query.String()))
		b.WriteString("\x1b[2m--- Commands (Enter to run, Esc to cancel) ---\x1b[0m\r\n")

		maxLines := 15
		for i, cmd := range matches {
			if i >= maxLines {
				b.WriteString("  ...\r\n")
				break
			}
			aliases := ""
			if len(cmd.Aliases) > 0 {
				aliases = fmt.Sprintf(" (%s)", strings.Join(cmd.Aliases, ", "))
			}
			line := fmt.Sprintf("%-16s %s", cmd.Name+aliases, cmd.Description)
			if i == selected {
				b.WriteString(fmt.Sprintf("\x1b[7m> %s\x1b[0m\r\n", line))
			} else {
				b.WriteString(fmt.Sprintf("  %s\r\n", line))
			}
		}
		_, _ = io.WriteString(stdout, b.String())
	}

	render()

	byteCh := make(chan byte, 64)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := stdin.Read(buf)
			if n > 0 {
				byteCh <- buf[0]
			}
			if err != nil {
				close(byteCh)
				return
			}
		}
	}()

	for b := range byteCh {
		switch b {
		case 0x03: // Ctrl+C
			_, _ = io.WriteString(stdout, "\x1b[H\x1b[2J")
			return nil

		case 0x1B: // Escape or arrow sequence
			select {
			case b2, ok := <-byteCh:
				if !ok {
					_, _ = io.WriteString(stdout, "\x1b[H\x1b[2J")
					return nil
				}
				if b2 == '[' {
					select {
					case b3, ok := <-byteCh:
						if !ok {
							return nil
						}
						matches := filterCommands(allCmds, query.String())
						if b3 == 'A' { // Up arrow
							if selected > 0 {
								selected--
								render()
							}
							continue
						} else if b3 == 'B' { // Down arrow
							if selected < len(matches)-1 {
								selected++
								render()
							}
							continue
						}
					case <-time.After(25 * time.Millisecond):
					}
				}
			case <-time.After(25 * time.Millisecond):
				// Plain Esc key
				_, _ = io.WriteString(stdout, "\x1b[H\x1b[2J")
				return nil
			}

		case 0x0E: // Ctrl+N (Down)
			matches := filterCommands(allCmds, query.String())
			if selected < len(matches)-1 {
				selected++
				render()
			}

		case 0x10: // Ctrl+P (Up)
			if selected > 0 {
				selected--
				render()
			}

		case '\r', '\n': // Enter
			matches := filterCommands(allCmds, query.String())
			_, _ = io.WriteString(stdout, "\x1b[H\x1b[2J")
			if len(matches) > 0 && selected < len(matches) {
				_ = commands.DefaultRegistry.Execute(context.Background(), inv, matches[selected].Name)
			}
			return nil

		case 0x7F, 0x08: // Backspace
			if query.Len() > 0 {
				s := query.String()
				query.Reset()
				query.WriteString(s[:len(s)-1])
				selected = 0
				render()
			}

		case 0x15: // Ctrl+U: clear query
			query.Reset()
			selected = 0
			render()

		default:
			if b >= 32 && b < 127 {
				query.WriteByte(b)
				selected = 0
				render()
			}
		}
	}
	return nil
}

func fuzzyMatch(target, pattern string) bool {
	target = strings.ToLower(target)
	pattern = strings.ToLower(pattern)
	if pattern == "" {
		return true
	}
	if strings.Contains(target, pattern) {
		return true
	}
	pRunes := []rune(pattern)
	tRunes := []rune(target)
	pIdx := 0
	for tIdx := 0; tIdx < len(tRunes) && pIdx < len(pRunes); tIdx++ {
		if tRunes[tIdx] == pRunes[pIdx] {
			pIdx++
		}
	}
	return pIdx == len(pRunes)
}

func filterCommands(all []commands.Command, q string) []commands.Command {
	q = strings.TrimSpace(q)
	if q == "" {
		return all
	}
	var matches []commands.Command
	for _, cmd := range all {
		if fuzzyMatch(cmd.Name, q) || fuzzyMatch(cmd.Description, q) {
			matches = append(matches, cmd)
			continue
		}
		for _, a := range cmd.Aliases {
			if fuzzyMatch(a, q) {
				matches = append(matches, cmd)
				break
			}
		}
	}
	return matches
}
