package commands

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// Registry stores commands and resolves lookups by name or alias.
type Registry struct {
	mu       sync.RWMutex
	commands []Command
	byName   map[string]*Command
	byAlias  map[string]*Command
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		byName:  make(map[string]*Command),
		byAlias: make(map[string]*Command),
	}
}

// Register adds a command to the registry.
func (r *Registry) Register(cmd Command) {
	r.mu.Lock()
	defer r.mu.Unlock()

	nameKey := strings.ToLower(cmd.Name)
	var ref *Command

	if old, exists := r.byName[nameKey]; exists {
		for _, alias := range old.Aliases {
			key := strings.ToLower(alias)
			if r.byAlias[key] == old {
				delete(r.byAlias, key)
			}
		}
		*old = cmd
		ref = old
		for i := range r.commands {
			if strings.EqualFold(r.commands[i].Name, cmd.Name) {
				r.commands[i] = cmd
				break
			}
		}
	} else {
		ref = new(Command)
		*ref = cmd
		r.commands = append(r.commands, cmd)
	}

	r.byName[nameKey] = ref
	for _, alias := range cmd.Aliases {
		r.byAlias[strings.ToLower(alias)] = ref
	}
}

// Lookup finds a command by name or alias (case-insensitive).
func (r *Registry) Lookup(nameOrAlias string) (*Command, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := strings.ToLower(strings.TrimSpace(nameOrAlias))
	if cmd, ok := r.byName[key]; ok {
		return cmd, true
	}
	if cmd, ok := r.byAlias[key]; ok {
		return cmd, true
	}
	return nil, false
}

// All returns a copy of all registered commands sorted by name.
func (r *Registry) All() []Command {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]Command, len(r.commands))
	copy(res, r.commands)
	sort.Slice(res, func(i, j int) bool {
		return res[i].Name < res[j].Name
	})
	return res
}

// Execute parses a command line and runs the matching command.
func (r *Registry) Execute(ctx context.Context, inv Invocation, line string) error {
	name, args, err := ParseLine(line)
	if err != nil {
		return err
	}
	if name == "" {
		return nil
	}

	cmd, ok := r.Lookup(name)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownCommand, name)
	}

	inv.Args = args
	return cmd.Run(ctx, inv)
}

// DefaultRegistry is the global default registry containing built-in wideboi commands.
var DefaultRegistry = NewRegistry()

func init() {
	registerBuiltins(DefaultRegistry)
}

// ShellQuote quotes a string for safe execution in a POSIX shell.
// Inside single quotes nothing is special except the quote itself, which is
// closed, escaped, and reopened. Strings without shell metacharacters are
// returned unchanged; empty strings are quoted as ”.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\r\"'\\$`!*?~#&;()|<>{}^[]") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ShellJoin turns command operands into the string handed to $SHELL -c.
// One operand is already a shell command and passes through verbatim;
// several are argv, so each is quoted and the words survive intact.
func ShellJoin(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = ShellQuote(a)
	}
	return strings.Join(quoted, " ")
}

type pipeTapEntry struct {
	id     uint64
	cancel context.CancelFunc
}

var (
	activePipeTapsMu sync.Mutex
	activePipeTaps   = make(map[int]pipeTapEntry)
	nextPipeTapID    uint64
)

func reorderCommandFlags(args []string) []string {
	var flags []string
	var operands []string
	afterDashDash := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if afterDashDash {
			operands = append(operands, arg)
			continue
		}
		if arg == "--" {
			afterDashDash = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if (arg == "-n" || arg == "-lines" || arg == "--lines" ||
				arg == "-limit" || arg == "--limit" ||
				arg == "-offset" || arg == "--offset" ||
				arg == "-o" || arg == "-output" || arg == "--output" ||
				arg == "-cwd" || arg == "--cwd" ||
				arg == "-after" || arg == "--after") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				flags = append(flags, args[i])
			}
		} else {
			operands = append(operands, arg)
		}
	}
	return append(flags, operands...)
}

func registerBuiltins(r *Registry) {
	r.Register(Command{
		Name:        "new-column",
		Aliases:     []string{"new", "n"},
		Description: "Create a new pane in a new column",
		Category:    "Layout",
		ArgsUsage:   "[command...]",
		Run: func(ctx context.Context, inv Invocation) error {
			cmd := ShellJoin(inv.Args)
			req := protocol.MsgSplitRequest{
				Command:     cmd,
				AfterPaneID: inv.CallerPaneID,
			}
			resp, err := RPCQuery[protocol.MsgSplitResponse](ctx, inv, req, 5*time.Second)
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			return nil
		},
	})

	r.Register(Command{
		Name:        "split",
		Description: "Split and create a new pane with options",
		Category:    "Layout",
		ArgsUsage:   "[-keep] [-cwd <dir>] [-after <pane-id>] [command...]",
		Run: func(ctx context.Context, inv Invocation) error {
			fs := flag.NewFlagSet("split", flag.ContinueOnError)
			if inv.Stderr != nil {
				fs.SetOutput(inv.Stderr)
			}
			var cwd string
			var keep bool
			var after int
			fs.StringVar(&cwd, "cwd", "", "working directory")
			fs.BoolVar(&keep, "keep", false, "keep pane after exit")
			fs.IntVar(&after, "after", inv.CallerPaneID, "insert after pane ID")

			if err := fs.Parse(inv.Args); err != nil {
				return err
			}
			cmd := ShellJoin(fs.Args())
			req := protocol.MsgSplitRequest{
				Command:     cmd,
				Cwd:         cwd,
				Keep:        keep,
				AfterPaneID: after,
			}
			resp, err := RPCQuery[protocol.MsgSplitResponse](ctx, inv, req, 5*time.Second)
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			return nil
		},
	})

	r.Register(Command{
		Name:        "run",
		Aliases:     []string{"sh", "!", "exec"},
		Description: "Run a shell command in a new pane (keeps output pane by default)",
		Category:    "Execution",
		ArgsUsage:   "[-keep] <command...>",
		Run: func(ctx context.Context, inv Invocation) error {
			if len(inv.Args) == 0 {
				return fmt.Errorf("usage: :run [-keep] <command...>")
			}
			keep := true
			args := inv.Args
			if args[0] == "-nokeep" || args[0] == "--no-keep" {
				keep = false
				args = args[1:]
			} else if args[0] == "-keep" || args[0] == "--keep" {
				keep = true
				args = args[1:]
			}
			if len(args) == 0 {
				return fmt.Errorf("usage: :run [-keep] <command...>")
			}
			cmd := ShellJoin(args)
			req := protocol.MsgSplitRequest{
				Command:     cmd,
				Keep:        keep,
				AfterPaneID: inv.CallerPaneID,
			}
			resp, err := RPCQuery[protocol.MsgSplitResponse](ctx, inv, req, 5*time.Second)
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			return nil
		},
	})

	r.Register(Command{
		Name:        "kill-pane",
		Aliases:     []string{"kill", "k", "close", "x"},
		Description: "Close the specified pane or caller pane",
		Category:    "Panes",
		ArgsUsage:   "[pane-id]",
		Run: func(ctx context.Context, inv Invocation) error {
			targetID := inv.CallerPaneID
			if len(inv.Args) > 0 {
				id, err := strconv.Atoi(inv.Args[0])
				if err != nil {
					return fmt.Errorf("invalid pane ID: %w", err)
				}
				targetID = id
			}
			req := protocol.MsgClosePaneRequest{PaneID: targetID}
			resp, err := RPCQuery[protocol.MsgClosePaneResponse](ctx, inv, req, 5*time.Second)
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			return nil
		},
	})

	r.Register(Command{
		Name:        "rename-pane",
		Aliases:     []string{"title", "label"},
		Description: "Set or clear the title of a pane",
		Category:    "Panes",
		ArgsUsage:   "[pane-id] [title]",
		Run: func(ctx context.Context, inv Invocation) error {
			targetID := inv.CallerPaneID
			title := ""
			clear := false

			switch len(inv.Args) {
			case 0:
				if targetID <= 0 {
					return fmt.Errorf("usage: rename-pane [pane-id] [title] (pane-id required outside wideboi pane)")
				}
				clear = true
			case 1:
				if targetID > 0 {
					title = inv.Args[0]
					if title == "" {
						clear = true
					}
				} else {
					id, err := strconv.Atoi(inv.Args[0])
					if err != nil {
						return fmt.Errorf("usage: rename-pane [pane-id] [title] (pane-id required outside wideboi pane)")
					}
					targetID = id
					clear = true
				}
			default:
				id, err := strconv.Atoi(inv.Args[0])
				if err != nil {
					if targetID > 0 {
						title = strings.Join(inv.Args, " ")
					} else {
						return fmt.Errorf("invalid pane ID %q: %w", inv.Args[0], err)
					}
				} else {
					targetID = id
					title = strings.Join(inv.Args[1:], " ")
					if title == "" {
						clear = true
					}
				}
			}

			req := protocol.MsgRenamePaneRequest{
				PaneID: targetID,
				Title:  title,
				Clear:  clear,
			}
			resp, err := RPCQuery[protocol.MsgRenamePaneResponse](ctx, inv, req, 5*time.Second)
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			return nil
		},
	})

	r.Register(Command{
		Name:        "dump-pane",
		Aliases:     []string{"dump", "capture"},
		Description: "Dump the screen or scrollback of a pane",
		Category:    "Panes",
		ArgsUsage:   "[pane-id] [-s|--scrollback [lines]] [--offset <N>] [--limit|-n <N>] [-c|--count] [--ansi|--plain] [-o|--output <file>]",
		Run: func(ctx context.Context, inv Invocation) error {
			fs := flag.NewFlagSet("dump-pane", flag.ContinueOnError)
			if inv.Stderr != nil {
				fs.SetOutput(inv.Stderr)
			}
			var scrollback bool
			var offset int
			var limit int
			var countOnly bool
			var ansi, plain bool
			var output string

			fs.BoolVar(&scrollback, "scrollback", false, "include scrollback history")
			fs.BoolVar(&scrollback, "s", false, "include scrollback history")
			fs.BoolVar(&scrollback, "S", false, "include scrollback history")
			fs.IntVar(&offset, "offset", -1, "0-indexed starting line from top of buffer")
			fs.IntVar(&limit, "limit", 0, "maximum lines to return")
			fs.IntVar(&limit, "lines", 0, "maximum lines to return")
			fs.IntVar(&limit, "n", 0, "maximum lines to return")
			fs.BoolVar(&countOnly, "count", false, "query and print total line count only")
			fs.BoolVar(&countOnly, "c", false, "query and print total line count only")
			fs.BoolVar(&ansi, "ansi", false, "preserve ANSI color and style escapes")
			fs.BoolVar(&plain, "plain", false, "strip ANSI formatting (default)")
			fs.StringVar(&output, "output", "", "write output to file instead of stdout")
			fs.StringVar(&output, "o", "", "write output to file instead of stdout")

			var nonFlagInts []int
			for i := 0; i < len(inv.Args); i++ {
				arg := inv.Args[i]
				if strings.HasPrefix(arg, "-") {
					if (arg == "-n" || arg == "-lines" || arg == "--lines" ||
						arg == "-limit" || arg == "--limit" ||
						arg == "-offset" || arg == "--offset" ||
						arg == "-o" || arg == "-output" || arg == "--output") && i+1 < len(inv.Args) {
						i++
					}
					continue
				}
				if _, err := strconv.Atoi(arg); err == nil {
					nonFlagInts = append(nonFlagInts, i)
				}
			}

			var preprocessed []string
			for i := 0; i < len(inv.Args); i++ {
				arg := inv.Args[i]
				// -S is strictly boolean
				if arg == "-scrollback" || arg == "--scrollback" || arg == "-s" {
					preprocessed = append(preprocessed, arg)
					if i+1 < len(inv.Args) {
						if _, err := strconv.Atoi(inv.Args[i+1]); err == nil {
							if inv.CallerPaneID > 0 || len(nonFlagInts) > 1 {
								i++
								preprocessed = append(preprocessed, "-limit", inv.Args[i])
							}
						}
					}
					continue
				}
				if strings.HasPrefix(arg, "--scrollback=") || strings.HasPrefix(arg, "-scrollback=") || strings.HasPrefix(arg, "-s=") {
					parts := strings.SplitN(arg, "=", 2)
					if _, err := strconv.ParseBool(parts[1]); err == nil {
						preprocessed = append(preprocessed, arg)
					} else if n, err := strconv.Atoi(parts[1]); err == nil && n > 0 {
						preprocessed = append(preprocessed, parts[0], "-limit", parts[1])
					} else {
						preprocessed = append(preprocessed, arg)
					}
					continue
				}
				preprocessed = append(preprocessed, arg)
			}

			if err := fs.Parse(preprocessed); err != nil {
				return err
			}

			targetID := inv.CallerPaneID
			rest := fs.Args()
			if len(rest) > 0 {
				id, err := strconv.Atoi(rest[0])
				if err != nil {
					return fmt.Errorf("invalid pane id %q: %w", rest[0], err)
				}
				targetID = id
			} else if targetID <= 0 {
				return fmt.Errorf("usage: dump-pane [pane-id] [flags] (pane-id required outside wideboi pane)")
			}

			req := protocol.MsgDumpPaneRequest{
				PaneID:     targetID,
				Scrollback: scrollback,
				ANSI:       ansi && !plain,
				CountOnly:  countOnly,
			}

			if offset < 0 {
				if limit > 0 {
					req.TailLines = limit
				}
			} else {
				req.Offset = offset
				req.Limit = limit
			}

			resp, err := RPCQuery[protocol.MsgDumpPaneResponse](ctx, inv, req, 5*time.Second)
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}

			var outContent string
			if countOnly {
				outContent = fmt.Sprintf("%d\n", resp.TotalLines)
			} else {
				outContent = resp.Text
			}

			if output != "" {
				if err := os.WriteFile(output, []byte(outContent), 0666); err != nil {
					return fmt.Errorf("writing output file %q: %w", output, err)
				}
				return nil
			}

			if inv.Stdout == nil {
				return fmt.Errorf("output file required (-o <file>) when run from prompt")
			}

			fmt.Fprint(inv.Stdout, outContent)
			return nil
		},
	})

	r.Register(Command{
		Name:        "pipe-pane",
		Aliases:     []string{"pipe"},
		Description: "Pipe raw PTY output of a pane to a file or stream",
		Category:    "Panes",
		ArgsUsage:   "[pane-id] [-o <file>] [-a|--append] [--stop]",
		Run: func(ctx context.Context, inv Invocation) error {
			fs := flag.NewFlagSet("pipe-pane", flag.ContinueOnError)
			if inv.Stderr != nil {
				fs.SetOutput(inv.Stderr)
			}
			var output string
			var appendMode bool
			var stop bool

			fs.StringVar(&output, "output", "", "write raw stream to file")
			fs.StringVar(&output, "o", "", "write raw stream to file")
			fs.BoolVar(&appendMode, "append", false, "append to output file")
			fs.BoolVar(&appendMode, "a", false, "append to output file")
			fs.BoolVar(&stop, "stop", false, "stop active background pipe for the pane")

			if err := fs.Parse(reorderCommandFlags(inv.Args)); err != nil {
				return err
			}

			targetID := inv.CallerPaneID
			rest := fs.Args()
			if len(rest) > 1 {
				return fmt.Errorf("usage: pipe-pane [pane-id] [flags]")
			}
			if len(rest) == 1 {
				id, err := strconv.Atoi(rest[0])
				if err != nil {
					return fmt.Errorf("invalid pane id %q: %w", rest[0], err)
				}
				targetID = id
			} else if targetID <= 0 {
				return fmt.Errorf("usage: pipe-pane [pane-id] [flags] (pane-id required outside wideboi pane)")
			}

			if stop {
				activePipeTapsMu.Lock()
				entry, ok := activePipeTaps[targetID]
				if ok {
					entry.cancel()
					delete(activePipeTaps, targetID)
				}
				activePipeTapsMu.Unlock()
				return nil
			}

			socket, err := ResolveSocket(inv)
			if err != nil {
				return err
			}

			if output != "" {
				flagVal := os.O_CREATE | os.O_WRONLY
				if appendMode {
					flagVal |= os.O_APPEND
				} else {
					flagVal |= os.O_TRUNC
				}
				f, err := os.OpenFile(output, flagVal, 0666)
				if err != nil {
					return fmt.Errorf("opening output file %q: %w", output, err)
				}

				tapCtx, cancel := context.WithCancel(context.Background())
				activePipeTapsMu.Lock()
				nextPipeTapID++
				tapGen := nextPipeTapID
				if prev, ok := activePipeTaps[targetID]; ok {
					prev.cancel()
				}
				activePipeTaps[targetID] = pipeTapEntry{id: tapGen, cancel: cancel}
				activePipeTapsMu.Unlock()

				go func() {
					defer f.Close()
					defer func() {
						activePipeTapsMu.Lock()
						if cur, ok := activePipeTaps[targetID]; ok && cur.id == tapGen {
							delete(activePipeTaps, targetID)
						}
						activePipeTapsMu.Unlock()
					}()

					conn, err := DialServer(tapCtx, socket)
					if err != nil {
						return
					}
					defer conn.Close()

					done := make(chan struct{})
					defer close(done)
					go func() {
						select {
						case <-tapCtx.Done():
							_ = conn.Close()
						case <-done:
						}
					}()

					req := protocol.MsgPipePaneRequest{PaneID: targetID}
					if err := transport.WriteClientFrame(conn, req); err != nil {
						return
					}

					for {
						msg, err := transport.ReadServerFrame(conn)
						if err != nil || tapCtx.Err() != nil {
							return
						}
						resp, ok := msg.(protocol.MsgPipePaneResponse)
						if !ok {
							continue
						}
						if resp.Error != "" || resp.Closed {
							return
						}
						if len(resp.Data) > 0 {
							if _, err := f.Write(resp.Data); err != nil {
								return
							}
						}
					}
				}()
				return nil
			}

			if inv.Stdout == nil {
				return fmt.Errorf("output file required (-o <file>) when run from prompt")
			}

			conn, err := DialServer(ctx, socket)
			if err != nil {
				return err
			}
			defer conn.Close()

			done := make(chan struct{})
			defer close(done)
			go func() {
				select {
				case <-ctx.Done():
					_ = conn.Close()
				case <-done:
				}
			}()

			req := protocol.MsgPipePaneRequest{PaneID: targetID}
			if err := transport.WriteClientFrame(conn, req); err != nil {
				return fmt.Errorf("sending pipe-pane request: %w", err)
			}

			for {
				msg, err := transport.ReadServerFrame(conn)
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return fmt.Errorf("reading pipe stream: %w", err)
				}
				resp, ok := msg.(protocol.MsgPipePaneResponse)
				if !ok {
					continue
				}
				if resp.Error != "" {
					return fmt.Errorf("%s", resp.Error)
				}
				if len(resp.Data) > 0 {
					if _, err := inv.Stdout.Write(resp.Data); err != nil {
						return err
					}
				}
				if resp.Closed {
					return nil
				}
			}
		},
	})

	r.Register(Command{
		Name:        "set-width",
		Aliases:     []string{"width"},
		Description: "Set the width of the pane in columns",
		Category:    "Layout",
		ArgsUsage:   "<columns>",
		Run: func(ctx context.Context, inv Invocation) error {
			if len(inv.Args) < 1 {
				return fmt.Errorf("usage: set-width <columns>")
			}
			if inv.CallerPaneID <= 0 {
				return fmt.Errorf("no focused pane")
			}
			width, err := strconv.Atoi(inv.Args[0])
			if err != nil {
				return fmt.Errorf("invalid width: %w", err)
			}
			if width < layout.MinColumnWidth || width > layout.MaxColumnWidth {
				return fmt.Errorf("width must be between %d and %d", layout.MinColumnWidth, layout.MaxColumnWidth)
			}
			return SendClientMsg(ctx, inv, protocol.MsgSetPaneWidth{
				PaneID: inv.CallerPaneID,
				Width:  width,
			})
		},
	})

	r.Register(Command{
		Name:        "move-left",
		Aliases:     []string{"ml"},
		Description: "Move the focused pane one column to the left",
		Category:    "Layout",
		Run: func(ctx context.Context, inv Invocation) error {
			return SendVerb(ctx, inv, protocol.VerbMoveLeft)
		},
	})

	r.Register(Command{
		Name:        "move-right",
		Aliases:     []string{"mr"},
		Description: "Move the focused pane one column to the right",
		Category:    "Layout",
		Run: func(ctx context.Context, inv Invocation) error {
			return SendVerb(ctx, inv, protocol.VerbMoveRight)
		},
	})

	r.Register(Command{
		Name:        "pin-pane",
		Aliases:     []string{"pin"},
		Description: "Pin a column to stay anchored to the left",
		Category:    "Layout",
		ArgsUsage:   "[pane-id]",
		Run: func(ctx context.Context, inv Invocation) error {
			targetID := inv.CallerPaneID
			if len(inv.Args) > 0 {
				id, err := strconv.Atoi(inv.Args[0])
				if err != nil {
					return fmt.Errorf("invalid pane ID: %w", err)
				}
				targetID = id
			}
			return SendVerbTarget(ctx, inv, protocol.VerbPinPane, targetID)
		},
	})

	r.Register(Command{
		Name:        "unpin-pane",
		Aliases:     []string{"unpin"},
		Description: "Unpin a column from the left edge",
		Category:    "Layout",
		ArgsUsage:   "[pane-id]",
		Run: func(ctx context.Context, inv Invocation) error {
			targetID := inv.CallerPaneID
			if len(inv.Args) > 0 {
				id, err := strconv.Atoi(inv.Args[0])
				if err != nil {
					return fmt.Errorf("invalid pane ID: %w", err)
				}
				targetID = id
			}
			return SendVerbTarget(ctx, inv, protocol.VerbUnpinPane, targetID)
		},
	})

	r.Register(Command{
		Name:        "toggle-pin",
		Aliases:     []string{"pin-toggle"},
		Description: "Toggle pinned status of a column to stay anchored to the left",
		Category:    "Layout",
		ArgsUsage:   "[pane-id]",
		Run: func(ctx context.Context, inv Invocation) error {
			targetID := inv.CallerPaneID
			if len(inv.Args) > 0 {
				id, err := strconv.Atoi(inv.Args[0])
				if err != nil {
					return fmt.Errorf("invalid pane ID: %w", err)
				}
				targetID = id
			}
			return SendVerbTarget(ctx, inv, protocol.VerbTogglePin, targetID)
		},
	})

	r.Register(Command{
		Name:        "toggle-collapse",
		Aliases:     []string{"collapse-toggle", "collapse"},
		Description: "Toggle collapsed status of a column to narrow right margin strip",
		Category:    "Layout",
		ArgsUsage:   "[pane-id]",
		Run: func(ctx context.Context, inv Invocation) error {
			targetID := inv.CallerPaneID
			if len(inv.Args) > 0 {
				id, err := strconv.Atoi(inv.Args[0])
				if err != nil {
					return fmt.Errorf("invalid pane ID: %w", err)
				}
				targetID = id
			}
			return SendVerbTarget(ctx, inv, protocol.VerbToggleCollapse, targetID)
		},
	})

	r.Register(Command{
		Name:        "uncollapse",
		Aliases:     []string{"expand"},
		Description: "Expand a collapsed column to resume normal layout flow",
		Category:    "Layout",
		ArgsUsage:   "[pane-id]",
		Run: func(ctx context.Context, inv Invocation) error {
			targetID := inv.CallerPaneID
			if len(inv.Args) > 0 {
				id, err := strconv.Atoi(inv.Args[0])
				if err != nil {
					return fmt.Errorf("invalid pane ID: %w", err)
				}
				targetID = id
			}
			return SendVerbTarget(ctx, inv, protocol.VerbUncollapsePane, targetID)
		},
	})

	r.Register(Command{
		Name:        "set-pane-status",
		Aliases:     []string{"status-set"},
		Description: "Set or clear an explicit status override on a pane",
		Category:    "Panes",
		ArgsUsage:   "<working|input|done|failed|idle|clear> [pane-id]",
		Run: func(ctx context.Context, inv Invocation) error {
			if len(inv.Args) == 0 {
				return fmt.Errorf("usage: :set-pane-status <working|input|done|failed|idle|clear> [pane-id]")
			}
			targetID := inv.CallerPaneID
			statusWord := ""
			if id, err := strconv.Atoi(inv.Args[0]); err == nil && id > 0 {
				targetID = id
				if len(inv.Args) > 1 {
					statusWord = inv.Args[1]
				}
			} else {
				statusWord = inv.Args[0]
				if len(inv.Args) > 1 {
					if id, err := strconv.Atoi(inv.Args[1]); err == nil && id > 0 {
						targetID = id
					} else {
						return fmt.Errorf("invalid pane id %q: %w", inv.Args[1], err)
					}
				}
			}
			if targetID <= 0 {
				return fmt.Errorf("pane-id required outside wideboi pane")
			}
			clear := false
			var status protocol.PaneStatus
			switch strings.ToLower(statusWord) {
			case "clear", "reset", "none":
				clear = true
			case "working", "busy":
				status = protocol.StatusWorking
			case "input", "needs_input", "waiting":
				status = protocol.StatusNeedsInput
			case "done", "finished", "success":
				status = protocol.StatusDone
			case "failed", "error":
				status = protocol.StatusFailed
			case "idle":
				status = protocol.StatusIdle
			default:
				return fmt.Errorf("unknown status %q (must be working, input, done, failed, idle, or clear)", statusWord)
			}
			req := protocol.MsgSetPaneStatusRequest{
				PaneID: targetID,
				Status: status,
				Clear:  clear,
			}
			resp, err := RPCQuery[protocol.MsgSetPaneStatusResponse](ctx, inv, req, 5*time.Second)
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			return nil
		},
	})

	r.Register(Command{
		Name:        "wait-output",
		Description: "Block until matching text appears in a pane's output",
		Category:    "Panes",
		ArgsUsage:   "<pane-id> <pattern>",
		Run: func(ctx context.Context, inv Invocation) error {
			if len(inv.Args) == 0 {
				return fmt.Errorf("usage: :wait-output <pane-id> <pattern>")
			}
			targetID := inv.CallerPaneID
			pattern := ""
			if id, err := strconv.Atoi(inv.Args[0]); err == nil && id > 0 {
				targetID = id
				if len(inv.Args) > 1 {
					pattern = strings.Join(inv.Args[1:], " ")
				}
			} else {
				pattern = strings.Join(inv.Args, " ")
			}
			if targetID <= 0 {
				return fmt.Errorf("pane-id required outside wideboi pane")
			}
			if pattern == "" {
				return fmt.Errorf("must specify pattern")
			}
			req := protocol.MsgWaitOutputRequest{
				PaneID: targetID,
				Match:  pattern,
				Lines:  50,
			}
			resp, err := RPCQuery[protocol.MsgWaitOutputResponse](ctx, inv, req, 30*time.Second)
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			return nil
		},
	})

	r.Register(Command{
		Name:        "wait-status",
		Description: "Block until a pane transitions to one of the target statuses",
		Category:    "Panes",
		ArgsUsage:   "<pane-id> <status1,status2...>",
		Run: func(ctx context.Context, inv Invocation) error {
			if len(inv.Args) == 0 {
				return fmt.Errorf("usage: :wait-status <pane-id> <status>")
			}
			targetID := inv.CallerPaneID
			statusWord := ""
			if id, err := strconv.Atoi(inv.Args[0]); err == nil && id > 0 {
				targetID = id
				if len(inv.Args) > 1 {
					statusWord = inv.Args[1]
				}
			} else {
				statusWord = inv.Args[0]
			}
			if targetID <= 0 {
				return fmt.Errorf("pane-id required outside wideboi pane")
			}
			if statusWord == "" {
				return fmt.Errorf("must specify status")
			}
			var targetStatuses []protocol.PaneStatus
			for _, part := range strings.Split(statusWord, ",") {
				switch strings.ToLower(strings.TrimSpace(part)) {
				case "working", "busy":
					targetStatuses = append(targetStatuses, protocol.StatusWorking)
				case "input", "needs_input", "waiting":
					targetStatuses = append(targetStatuses, protocol.StatusNeedsInput)
				case "done", "finished", "success":
					targetStatuses = append(targetStatuses, protocol.StatusDone)
				case "failed", "error":
					targetStatuses = append(targetStatuses, protocol.StatusFailed)
				case "idle":
					targetStatuses = append(targetStatuses, protocol.StatusIdle)
				case "interrupted":
					targetStatuses = append(targetStatuses, protocol.StatusInterrupted)
				default:
					return fmt.Errorf("unknown status %q", part)
				}
			}
			req := protocol.MsgWaitStatusRequest{
				PaneID: targetID,
				Until:  targetStatuses,
			}
			resp, err := RPCQuery[protocol.MsgWaitStatusResponse](ctx, inv, req, 30*time.Second)
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			return nil
		},
	})

	r.Register(Command{
		Name:        "toggle-status",
		Aliases:     []string{"status-bar"},
		Description: "Toggle the session status bar/dashboard pane",
		Category:    "Layout",
		Run: func(ctx context.Context, inv Invocation) error {
			return SendVerb(ctx, inv, protocol.VerbToggleStatus)
		},
	})

	r.Register(Command{
		Name:        "detach",
		Aliases:     []string{"d"},
		Description: "Detach the current client from the session",
		Category:    "Session",
		Run: func(ctx context.Context, inv Invocation) error {
			if inv.DetachFile != "" {
				f, err := os.OpenFile(inv.DetachFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
				if err != nil {
					return err
				}
				_, err = f.Write([]byte("detach\n"))
				_ = f.Close()
				return err
			}
			return SendClientMsg(ctx, inv, protocol.MsgDetach{})
		},
	})

	r.Register(Command{
		Name:        "quit",
		Aliases:     []string{"q"},
		Description: "Shut down the wideboi session and all panes",
		Category:    "Session",
		Run: func(ctx context.Context, inv Invocation) error {
			return SendClientMsg(ctx, inv, protocol.MsgShutdown{})
		},
	})

	r.Register(Command{
		Name:        "help",
		Aliases:     []string{"?"},
		Description: "Show available commands or command details",
		Category:    "General",
		ArgsUsage:   "[command]",
		Run: func(ctx context.Context, inv Invocation) error {
			out := inv.Stdout
			if out == nil {
				return nil
			}
			if len(inv.Args) > 0 {
				target := inv.Args[0]
				cmd, ok := r.Lookup(target)
				if !ok {
					return fmt.Errorf("unknown command %q", target)
				}
				fmt.Fprintf(out, "Command: %s\n", cmd.Name)
				if len(cmd.Aliases) > 0 {
					fmt.Fprintf(out, "Aliases: %s\n", strings.Join(cmd.Aliases, ", "))
				}
				fmt.Fprintf(out, "Description: %s\n", cmd.Description)
				if cmd.ArgsUsage != "" {
					fmt.Fprintf(out, "Usage: :%s %s\n", cmd.Name, cmd.ArgsUsage)
				}
				return nil
			}

			fmt.Fprintf(out, "Available Commands:\n")
			for _, cmd := range r.All() {
				aliases := ""
				if len(cmd.Aliases) > 0 {
					aliases = fmt.Sprintf(" (%s)", strings.Join(cmd.Aliases, ", "))
				}
				fmt.Fprintf(out, "  %-16s %s\n", cmd.Name+aliases, cmd.Description)
			}
			return nil
		},
	})
}
