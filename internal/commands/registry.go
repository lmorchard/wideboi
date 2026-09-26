package commands

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
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

	c := cmd
	r.commands = append(r.commands, c)
	ref := &r.commands[len(r.commands)-1]

	r.byName[strings.ToLower(c.Name)] = ref
	for _, alias := range c.Aliases {
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

// shellQuote quotes a string for safe execution in a POSIX shell.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\r\"'\\$`!*?~#&;()|<>{}^[]") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellJoin joins shell arguments, quoting any that contain special characters.
func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

func registerBuiltins(r *Registry) {
	r.Register(Command{
		Name:        "new-column",
		Aliases:     []string{"new", "n"},
		Description: "Create a new pane in a new column",
		Category:    "Layout",
		ArgsUsage:   "[command...]",
		Run: func(ctx context.Context, inv Invocation) error {
			cmd := shellJoin(inv.Args)
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
			cmd := shellJoin(fs.Args())
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
			cmd := shellJoin(args)
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
		Name:        "set-width",
		Aliases:     []string{"width"},
		Description: "Set the width of the pane in columns",
		Category:    "Layout",
		ArgsUsage:   "<columns>",
		Run: func(ctx context.Context, inv Invocation) error {
			if len(inv.Args) < 1 {
				return fmt.Errorf("usage: set-width <columns>")
			}
			width, err := strconv.Atoi(inv.Args[0])
			if err != nil {
				return fmt.Errorf("invalid width: %w", err)
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
