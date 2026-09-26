package commands_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/commands"
)

func TestParseLine(t *testing.T) {
	tests := []struct {
		input    string
		wantName string
		wantArgs []string
		wantErr  bool
	}{
		{input: "", wantName: "", wantArgs: nil},
		{input: "   ", wantName: "", wantArgs: nil},
		{input: ":", wantName: "", wantArgs: nil},
		{input: ":quit", wantName: "quit", wantArgs: nil},
		{input: "quit", wantName: "quit", wantArgs: nil},
		{input: ":q", wantName: "q", wantArgs: nil},
		{input: ":new-column --cwd /tmp", wantName: "new-column", wantArgs: []string{"--cwd", "/tmp"}},
		{input: `split --cwd "/path with spaces" -keep`, wantName: "split", wantArgs: []string{"--cwd", "/path with spaces", "-keep"}},
		{input: `:echo 'hello "world"'`, wantName: "echo", wantArgs: []string{`hello "world"`}},
		{input: `:split --cwd ""`, wantName: "split", wantArgs: []string{"--cwd", ""}},
		{input: `:split --cmd "unclosed quote`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			name, args, err := commands.ParseLine(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseLine(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if len(args) != len(tt.wantArgs) {
				t.Fatalf("args len = %d, want %d (%v vs %v)", len(args), len(tt.wantArgs), args, tt.wantArgs)
			}
			for i := range args {
				if args[i] != tt.wantArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, args[i], tt.wantArgs[i])
				}
			}
		})
	}
}

func TestRegistryLookupAndExecute(t *testing.T) {
	r := commands.NewRegistry()
	var executed bool
	var gotArgs []string

	cmd := commands.Command{
		Name:        "test-cmd",
		Aliases:     []string{"tc", "t"},
		Description: "A test command",
		Category:    "Test",
		Run: func(ctx context.Context, inv commands.Invocation) error {
			executed = true
			gotArgs = inv.Args
			_, _ = inv.Stdout.Write([]byte("ok"))
			return nil
		},
	}
	r.Register(cmd)

	// Lookup by name
	found, ok := r.Lookup("test-cmd")
	if !ok || found.Name != "test-cmd" {
		t.Fatalf("Lookup(test-cmd) failed: found=%v, ok=%v", found, ok)
	}

	// Lookup by alias
	found, ok = r.Lookup("tc")
	if !ok || found.Name != "test-cmd" {
		t.Fatalf("Lookup(tc) failed: found=%v, ok=%v", found, ok)
	}

	// Lookup case-insensitive
	found, ok = r.Lookup("TEST-CMD")
	if !ok || found.Name != "test-cmd" {
		t.Fatalf("Lookup(TEST-CMD) failed")
	}

	// Execute
	var stdout bytes.Buffer
	inv := commands.Invocation{
		CallerPaneID: 42,
		Stdout:       &stdout,
	}
	err := r.Execute(context.Background(), inv, ":tc --flag arg1")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !executed {
		t.Errorf("command Run was not called")
	}
	if len(gotArgs) != 2 || gotArgs[0] != "--flag" || gotArgs[1] != "arg1" {
		t.Errorf("gotArgs = %v, want [--flag arg1]", gotArgs)
	}
	if stdout.String() != "ok" {
		t.Errorf("stdout = %q, want 'ok'", stdout.String())
	}

	// Unknown command
	err = r.Execute(context.Background(), inv, ":nonexistent")
	if !errors.Is(err, commands.ErrUnknownCommand) {
		t.Errorf("expected ErrUnknownCommand, got %v", err)
	}
}

func TestDefaultRegistryBuiltins(t *testing.T) {
	r := commands.DefaultRegistry
	expected := []string{
		"new-column", "split", "run",
		"set-width", "move-left", "move-right",
		"kill-pane", "close",
		"toggle-status",
		"detach", "quit", "help",
	}

	for _, name := range expected {
		cmd, ok := r.Lookup(name)
		if !ok {
			t.Errorf("builtin command %q not found in DefaultRegistry", name)
			continue
		}
		if cmd.Description == "" {
			t.Errorf("command %q has empty Description", name)
		}
	}
}

func TestHelpCommand(t *testing.T) {
	r := commands.DefaultRegistry
	var stdout bytes.Buffer
	inv := commands.Invocation{
		Stdout: &stdout,
	}

	// Run help
	err := r.Execute(context.Background(), inv, ":help")
	if err != nil {
		t.Fatalf("Execute(:help) failed: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "new-column") || !strings.Contains(out, "quit") {
		t.Errorf("help output missing expected commands:\n%s", out)
	}

	// Run help for specific command
	stdout.Reset()
	err = r.Execute(context.Background(), inv, ":help new-column")
	if err != nil {
		t.Fatalf("Execute(:help new-column) failed: %v", err)
	}
	out = stdout.String()
	if !strings.Contains(out, "new-column") {
		t.Errorf("help new-column output missing name:\n%s", out)
	}
}

func TestRunCommandRegistered(t *testing.T) {
	r := commands.DefaultRegistry
	for _, alias := range []string{"run", "sh", "!", "exec"} {
		cmd, ok := r.Lookup(alias)
		if !ok || cmd.Name != "run" {
			t.Errorf("alias %q failed to resolve to run command", alias)
		}
	}
}

func TestQuotedArgsPreservedInRun(t *testing.T) {
	name, args, err := commands.ParseLine(`:run printf '%s' "hello world"`)
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if name != "run" {
		t.Fatalf("name = %q, want 'run'", name)
	}
	if len(args) != 3 || args[0] != "printf" || args[1] != "%s" || args[2] != "hello world" {
		t.Fatalf("args = %v, want ['printf', '%%s', 'hello world']", args)
	}
}
