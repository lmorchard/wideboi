package commands_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/commands"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
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

func TestSetWidthValidation(t *testing.T) {
	r := commands.DefaultRegistry
	ctx := context.Background()

	// No args
	inv := commands.Invocation{CallerPaneID: 1}
	err := r.Execute(ctx, inv, ":set-width")
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("expected usage error, got %v", err)
	}

	// No caller pane
	invNoPane := commands.Invocation{CallerPaneID: 0}
	err = r.Execute(ctx, invNoPane, ":set-width 60")
	if err == nil || !strings.Contains(err.Error(), "no focused pane") {
		t.Fatalf("expected 'no focused pane' error, got %v", err)
	}

	// Invalid int
	err = r.Execute(ctx, inv, ":set-width invalid")
	if err == nil || !strings.Contains(err.Error(), "invalid width") {
		t.Fatalf("expected 'invalid width' error, got %v", err)
	}

	// Below MinColumnWidth
	err = r.Execute(ctx, inv, ":set-width 10")
	if err == nil || !strings.Contains(err.Error(), "between") {
		t.Fatalf("expected width bounds error, got %v", err)
	}

	// Above MaxColumnWidth (layout.MaxColumnWidth is 4096)
	err = r.Execute(ctx, inv, ":set-width 5000")
	if err == nil || !strings.Contains(err.Error(), "between") {
		t.Fatalf("expected width bounds error, got %v", err)
	}
}

func TestSetWidthExecution(t *testing.T) {
	dir, err := os.MkdirTemp("", "wbtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	sl, err := transport.NewSocketListener(sock)
	if err != nil {
		t.Fatalf("NewSocketListener: %v", err)
	}
	defer sl.Close()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.ListenSocket(ctx, sl)
	go func() { _ = s.Run(ctx) }()

	// Attach a client so the server has active session geometry and startup panes
	clientConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	defer clientConn.Close()
	if _, err := transport.Handshake(clientConn); err != nil {
		t.Fatalf("handshake client: %v", err)
	}
	if err := transport.WriteClientFrame(clientConn, protocol.MsgAttach{Cols: 100, Rows: 30}); err != nil {
		t.Fatalf("write attach: %v", err)
	}

	// Read until initial layout snapshot arrives
	var paneID int
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		msg, err := transport.ReadServerFrame(clientConn)
		if err != nil {
			t.Fatalf("reading initial server frame: %v", err)
		}
		if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok && len(snap.Columns) > 0 {
			paneID = snap.Columns[0].PaneID
			break
		}
	}

	inv := commands.Invocation{
		CallerPaneID: paneID,
		Socket:       sock,
	}

	r := commands.DefaultRegistry
	if err := r.Execute(ctx, inv, ":set-width 65"); err != nil {
		t.Fatalf("Execute(:set-width 65) failed: %v", err)
	}

	// Read frames until layout snapshot with updated width arrives
	var updatedWidth int
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		msg, err := transport.ReadServerFrame(clientConn)
		if err != nil {
			t.Fatalf("reading server frame after set-width: %v", err)
		}
		if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok {
			for _, col := range snap.Columns {
				if col.PaneID == paneID && col.Width == 65 {
					updatedWidth = col.Width
					break
				}
			}
			if updatedWidth == 65 {
				break
			}
		}
	}

	if updatedWidth != 65 {
		t.Fatalf("column width = %d, want 65", updatedWidth)
	}
}

func TestDetachCommand(t *testing.T) {
	dir, err := os.MkdirTemp("", "wbtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	detachFile := filepath.Join(dir, "detach.tmp")
	inv := commands.Invocation{
		CallerPaneID: 1,
		DetachFile:   detachFile,
	}

	r := commands.DefaultRegistry
	if err := r.Execute(context.Background(), inv, ":detach"); err != nil {
		t.Fatalf("Execute(:detach) with DetachFile failed: %v", err)
	}

	data, err := os.ReadFile(detachFile)
	if err != nil {
		t.Fatalf("reading detachFile: %v", err)
	}
	if !strings.Contains(string(data), "detach") {
		t.Fatalf("detachFile content = %q, want 'detach'", string(data))
	}
}

func TestRenamePaneExecution(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "s.sock")
	sl, err := transport.NewSocketListener(sock)
	if err != nil {
		t.Fatalf("NewSocketListener: %v", err)
	}
	defer sl.Close()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.ListenSocket(ctx, sl)
	go func() { _ = s.Run(ctx) }()

	clientConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	defer clientConn.Close()
	if _, err := transport.Handshake(clientConn); err != nil {
		t.Fatalf("handshake client: %v", err)
	}
	if err := transport.WriteClientFrame(clientConn, protocol.MsgAttach{Cols: 100, Rows: 30}); err != nil {
		t.Fatalf("write attach: %v", err)
	}

	var paneID int
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		msg, err := transport.ReadServerFrame(clientConn)
		if err != nil {
			t.Fatalf("reading initial server frame: %v", err)
		}
		if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok && len(snap.Columns) > 0 {
			paneID = snap.Columns[0].PaneID
			break
		}
	}

	inv := commands.Invocation{
		CallerPaneID: paneID,
		Socket:       sock,
	}

	r := commands.DefaultRegistry

	// 1. Rename via :title
	if err := r.Execute(ctx, inv, `:title "Renamed Card"`); err != nil {
		t.Fatalf("Execute(:title) failed: %v", err)
	}

	// Read frames until title matches
	var gotTitle string
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		msg, err := transport.ReadServerFrame(clientConn)
		if err != nil {
			t.Fatalf("reading server frame after :title: %v", err)
		}
		if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok {
			if t, ok := snap.PaneTitles[paneID]; ok && t == "Renamed Card" {
				gotTitle = t
				break
			}
		}
	}
	if gotTitle != "Renamed Card" {
		t.Fatalf("gotTitle = %q, want 'Renamed Card'", gotTitle)
	}

	// 2. Clear via :title ""
	if err := r.Execute(ctx, inv, `:title ""`); err != nil {
		t.Fatalf("Execute(:title \"\") failed: %v", err)
	}
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		msg, err := transport.ReadServerFrame(clientConn)
		if err != nil {
			t.Fatalf("reading server frame after clear: %v", err)
		}
		if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok {
			if t, ok := snap.PaneTitles[paneID]; ok && t == "" {
				gotTitle = t
				break
			}
		}
	}
	if gotTitle != "" {
		t.Fatalf("gotTitle = %q after clear, want empty", gotTitle)
	}

	// 3. Rename invalid pane ID returns error
	err = r.Execute(ctx, inv, `:title 99999 "Invalid"`)
	if err == nil || !strings.Contains(err.Error(), "99999 not found") {
		t.Fatalf("expected 'pane 99999 not found' error, got %v", err)
	}
}
