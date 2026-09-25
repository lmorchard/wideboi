package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestControlSubcommands(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "control.sock")

	sl, err := transport.NewSocketListener(sockPath)
	if err != nil {
		t.Fatalf("NewSocketListener failed: %v", err)
	}
	defer sl.Close()

	srv := server.NewServer(nil, "/bin/sh", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv.ListenSocket(ctx, sl)
	go func() {
		_ = srv.Run(ctx)
	}()

	cfg := config.Config{
		Socket: sockPath,
	}

	// 1. Split a new pane with a command that prints a known message and waits
	var splitOut, splitErr bytes.Buffer
	err = runSplit(cfg, nil, []string{"echo hello from split && sleep 5"}, &splitOut, &splitErr)
	if err != nil {
		t.Fatalf("runSplit failed: %v, stderr: %s", err, splitErr.String())
	}

	paneIDStr := strings.TrimSpace(splitOut.String())
	if paneIDStr == "" {
		t.Fatal("expected pane ID from runSplit, got empty output")
	}

	// Wait briefly for the child process to write output to the PTY
	var captureOut, captureErr bytes.Buffer
	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		captureOut.Reset()
		captureErr.Reset()
		if err := runCapture(cfg, []string{paneIDStr}, &captureOut, &captureErr); err == nil {
			if strings.Contains(captureOut.String(), "hello from split") {
				found = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Fatalf("expected captured text to contain 'hello from split', got %q", captureOut.String())
	}

	// 2. Test send with trailing -e flag
	var sendErr bytes.Buffer
	err = runSend(cfg, []string{paneIDStr, "echo extra", "-e"}, &sendErr)
	if err != nil {
		t.Fatalf("runSend failed: %v, stderr: %s", err, sendErr.String())
	}

	// 3. Test send to unknown pane
	sendErr.Reset()
	err = runSend(cfg, []string{"999", "echo extra"}, &sendErr)
	if err == nil {
		t.Fatal("expected error sending to unknown pane 999, got nil")
	}

	// 4. Test capture from unknown pane
	captureOut.Reset()
	captureErr.Reset()
	err = runCapture(cfg, []string{"999"}, &captureOut, &captureErr)
	if err == nil {
		t.Fatal("expected error capturing from unknown pane 999, got nil")
	}

	// 5. Test close
	var closeErr bytes.Buffer
	err = runClose(cfg, []string{paneIDStr}, &closeErr)
	if err != nil {
		t.Fatalf("runClose failed: %v, stderr: %s", err, closeErr.String())
	}

	// 6. Test close on already closed pane
	closeErr.Reset()
	err = runClose(cfg, []string{paneIDStr}, &closeErr)
	if err == nil {
		t.Fatal("expected error closing already closed pane, got nil")
	}
}

func TestReorderFlags(t *testing.T) {
	tests := []struct {
		in   []string
		want []string
	}{
		{
			in:   []string{"1", "hello", "-e"},
			want: []string{"-e", "1", "hello"},
		},
		{
			in:   []string{"1", "-S", "-n", "10"},
			want: []string{"-S", "-n", "10", "1"},
		},
		{
			in:   []string{"1", "--", "-literal"},
			want: []string{"1", "-literal"},
		},
		{
			in:   []string{"--cwd", "/tmp", "-L", "test", "echo", "hi"},
			want: []string{"--cwd", "/tmp", "-L", "test", "echo", "hi"},
		},
	}
	for _, tt := range tests {
		got := reorderFlags(tt.in)
		if strings.Join(got, " ") != strings.Join(tt.want, " ") {
			t.Errorf("reorderFlags(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestShellJoin(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"make test && echo ok"}, "make test && echo ok"},
		{[]string{"grep", "a b", "f"}, `'grep' 'a b' 'f'`},
		{[]string{"echo", "it's"}, `'echo' 'it'\''s'`},
		{[]string{"printf", ""}, `'printf' ''`},
	}
	for _, c := range cases {
		if got := shellJoin(c.in); got != c.want {
			t.Errorf("shellJoin(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestShellJoinRoundTrips runs the joined command through a real shell:
// every operand must arrive as exactly one argument, byte for byte.
func TestShellJoinRoundTrips(t *testing.T) {
	args := []string{"printf", "%s|", "a b", "c'd", `$HOME`, "*", ""}
	out, err := exec.Command("/bin/sh", "-c", shellJoin(args)).Output()
	if err != nil {
		t.Fatalf("sh -c %q: %v", shellJoin(args), err)
	}
	if want := `a b|c'd|$HOME|*||`; string(out) != want {
		t.Errorf("round trip = %q, want %q", out, want)
	}
}

// TestSendRejectsExtraOperands pins that unquoted text is an error, not a
// silent truncation to its first word. The check precedes any dial, so
// no server is needed.
func TestSendRejectsExtraOperands(t *testing.T) {
	cfg := config.Config{Socket: filepath.Join(t.TempDir(), "none.sock")}
	var stderr bytes.Buffer
	err := runSend(cfg, []string{"1", "echo", "one", "two"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "quote") {
		t.Fatalf("runSend with extra operands: err = %v, want a usage error mentioning quoting", err)
	}
}

// TestSplitLeavesCommandFlagsAlone pins that split's own flags end where
// the command begins: `split --keep sh -c 'exit 5'` runs sh with its -c,
// rather than split rejecting -c as its own unknown flag.
func TestSplitLeavesCommandFlagsAlone(t *testing.T) {
	// Not t.TempDir: this test's name makes that path too long for a
	// macOS socket (104 bytes).
	dir, err := os.MkdirTemp("", "wbflags")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sockPath := filepath.Join(dir, "s.sock")
	sl, err := transport.NewSocketListener(sockPath)
	if err != nil {
		t.Fatalf("NewSocketListener failed: %v", err)
	}
	defer sl.Close()
	srv := server.NewServer(nil, "/bin/sh", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.ListenSocket(ctx, sl)
	go func() { _ = srv.Run(ctx) }()
	cfg := config.Config{Socket: sockPath}

	var out, errOut bytes.Buffer
	if err := runSplit(cfg, nil, []string{"--keep", "sh", "-c", "exit 5"}, &out, &errOut); err != nil {
		t.Fatalf("runSplit(--keep sh -c 'exit 5'): %v (stderr %q)", err, errOut.String())
	}
	id := strings.TrimSpace(out.String())
	code, err := runWait(cfg, []string{id}, &errOut)
	if err != nil || code != 5 {
		t.Fatalf("wait = %d, %v; want 5 (the command's -c must reach sh)", code, err)
	}
	_ = runClose(cfg, []string{id}, &errOut)
}
