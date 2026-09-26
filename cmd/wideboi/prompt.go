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

func runPrompt(cfg config.Config, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("prompt", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var callerPane int
	var session, socket string

	fs.IntVar(&callerPane, "caller-pane", 0, "pane ID that invoked the prompt")
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

	file, isFile := stdin.(*os.File)
	isTTY := isFile && term.IsTerminal(file.Fd())

	if isTTY {
		state, err := term.MakeRaw(file.Fd())
		if err == nil {
			defer func() { _ = term.Restore(file.Fd(), state) }()
		}
	}

	inv := commands.Invocation{
		Cfg:          cfg,
		Socket:       cfg.Socket,
		CallerPaneID: callerPane,
		Stdout:       stdout,
		Stderr:       stderr,
	}

	if !isTTY {
		// Non-interactive mode (tests, scripts)
		reader := bufio.NewReader(stdin)
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			return nil
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "\x1b" || line == "\x03" {
			return nil
		}
		if line == "" {
			return nil
		}
		return commands.DefaultRegistry.Execute(context.Background(), inv, line)
	}

	// Interactive TTY mode
	_, _ = io.WriteString(stdout, ": ")
	var buf strings.Builder
	var history []string
	historyIdx := -1
	currentDraft := ""

	byteCh := make(chan byte, 64)
	go func() {
		b := make([]byte, 1)
		for {
			n, err := stdin.Read(b)
			if n > 0 {
				byteCh <- b[0]
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
			_, _ = io.WriteString(stdout, "\r\n")
			return nil

		case 0x1B: // Escape or arrow sequence
			select {
			case b2, ok := <-byteCh:
				if !ok {
					_, _ = io.WriteString(stdout, "\r\n")
					return nil
				}
				if b2 == '[' {
					select {
					case b3, ok := <-byteCh:
						if !ok {
							return nil
						}
						if b3 == 'A' { // Up arrow (previous history)
							if len(history) > 0 {
								if historyIdx == -1 {
									currentDraft = buf.String()
									historyIdx = len(history) - 1
								} else if historyIdx > 0 {
									historyIdx--
								}
								buf.Reset()
								buf.WriteString(history[historyIdx])
								_, _ = io.WriteString(stdout, "\r\x1b[K: "+buf.String())
							}
							continue
						} else if b3 == 'B' { // Down arrow (next history)
							if historyIdx != -1 {
								if historyIdx < len(history)-1 {
									historyIdx++
									buf.Reset()
									buf.WriteString(history[historyIdx])
									_, _ = io.WriteString(stdout, "\r\x1b[K: "+buf.String())
								} else {
									historyIdx = -1
									buf.Reset()
									buf.WriteString(currentDraft)
									_, _ = io.WriteString(stdout, "\r\x1b[K: "+buf.String())
								}
							}
							continue
						}
					case <-time.After(25 * time.Millisecond):
					}
				}
			case <-time.After(25 * time.Millisecond):
				// Plain Esc key
				_, _ = io.WriteString(stdout, "\r\n")
				return nil
			}

		case '\r', '\n': // Enter
			_, _ = io.WriteString(stdout, "\r\n")
			line := strings.TrimSpace(buf.String())
			if line == "" {
				return nil
			}

			// Capture output
			var outBuf strings.Builder
			inv.Stdout = &outBuf
			inv.Stderr = &outBuf

			execErr := commands.DefaultRegistry.Execute(context.Background(), inv, line)
			if execErr != nil {
				_, _ = fmt.Fprintf(stdout, "\x1b[31mError: %v\x1b[0m\r\nPress any key to close...", execErr)
				<-byteCh
				return nil
			}

			if outBuf.Len() > 0 {
				outText := strings.ReplaceAll(outBuf.String(), "\n", "\r\n")
				_, _ = io.WriteString(stdout, outText)
				if !strings.HasSuffix(outText, "\r\n") {
					_, _ = io.WriteString(stdout, "\r\n")
				}
				_, _ = io.WriteString(stdout, "Press any key to close...")
				<-byteCh
			}
			return nil

		case 0x7F, 0x08: // Backspace
			if buf.Len() > 0 {
				s := buf.String()
				buf.Reset()
				buf.WriteString(s[:len(s)-1])
				_, _ = io.WriteString(stdout, "\b \b")
			}

		case 0x15: // Ctrl+U: clear line
			buf.Reset()
			_, _ = io.WriteString(stdout, "\r\x1b[K: ")

		case 0x17: // Ctrl+W: delete word
			s := strings.TrimRight(buf.String(), " ")
			idx := strings.LastIndex(s, " ")
			if idx >= 0 {
				s = s[:idx+1]
			} else {
				s = ""
			}
			buf.Reset()
			buf.WriteString(s)
			_, _ = io.WriteString(stdout, "\r\x1b[K: "+s)

		case '\t': // Tab completion
			current := buf.String()
			matches := findCommandMatches(current)
			if len(matches) == 1 {
				buf.Reset()
				buf.WriteString(matches[0])
				_, _ = io.WriteString(stdout, "\r\x1b[K: "+matches[0])
			}

		default:
			if b >= 32 && b < 127 {
				buf.WriteByte(b)
				_, _ = stdout.Write([]byte{b})
			}
		}
	}
	return nil
}

func findCommandMatches(prefix string) []string {
	prefix = strings.TrimPrefix(prefix, ":")
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	var matches []string
	for _, cmd := range commands.DefaultRegistry.All() {
		if strings.HasPrefix(strings.ToLower(cmd.Name), prefix) {
			matches = append(matches, cmd.Name)
		}
	}
	return matches
}
