package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/commands"
	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
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
		{[]string{"grep", "a b", "f"}, `grep 'a b' f`},
		{[]string{"echo", "it's"}, `echo 'it'\''s'`},
		{[]string{"printf", ""}, `printf ''`},
	}
	for _, c := range cases {
		if got := commands.ShellJoin(c.in); got != c.want {
			t.Errorf("commands.ShellJoin(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestShellJoinRoundTrips runs the joined command through a real shell:
// every operand must arrive as exactly one argument, byte for byte.
func TestShellJoinRoundTrips(t *testing.T) {
	args := []string{"printf", "%s|", "a b", "c'd", `$HOME`, "*", ""}
	out, err := exec.Command("/bin/sh", "-c", commands.ShellJoin(args)).Output()
	if err != nil {
		t.Fatalf("sh -c %q: %v", commands.ShellJoin(args), err)
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

func TestRenamePaneSubcommand(t *testing.T) {
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

	clientConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	defer clientConn.Close()
	if _, err := transport.Handshake(clientConn); err != nil {
		t.Fatalf("handshake client: %v", err)
	}
	if err := transport.WriteClientFrame(clientConn, protocol.MsgAttach{Cols: 80, Rows: 24}); err != nil {
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
	paneIDStr := fmt.Sprintf("%d", paneID)

	var stderr bytes.Buffer

	// 1. Rename with explicit pane ID
	stderr.Reset()
	if err := runRenamePane(cfg, []string{paneIDStr, "Explicit Title"}, &stderr); err != nil {
		t.Fatalf("runRenamePane explicit failed: %v, stderr: %s", err, stderr.String())
	}

	// Read frame to verify title
	var gotTitle string
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		msg, err := transport.ReadServerFrame(clientConn)
		if err != nil {
			t.Fatalf("reading server frame: %v", err)
		}
		if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok {
			if t, ok := snap.PaneTitles[paneID]; ok && t == "Explicit Title" {
				gotTitle = t
				break
			}
		}
	}
	if gotTitle != "Explicit Title" {
		t.Fatalf("gotTitle = %q, want 'Explicit Title'", gotTitle)
	}

	// 2. Rename with WIDEBOI_PANE_ID
	t.Setenv("WIDEBOI_PANE_ID", paneIDStr)
	stderr.Reset()
	if err := runRenamePane(cfg, []string{"Env Title"}, &stderr); err != nil {
		t.Fatalf("runRenamePane env failed: %v, stderr: %s", err, stderr.String())
	}
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		msg, err := transport.ReadServerFrame(clientConn)
		if err != nil {
			t.Fatalf("reading server frame: %v", err)
		}
		if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok {
			if t, ok := snap.PaneTitles[paneID]; ok && t == "Env Title" {
				gotTitle = t
				break
			}
		}
	}
	if gotTitle != "Env Title" {
		t.Fatalf("gotTitle = %q, want 'Env Title'", gotTitle)
	}

	// 3. Clear title with empty string
	stderr.Reset()
	if err := runRenamePane(cfg, []string{paneIDStr, ""}, &stderr); err != nil {
		t.Fatalf("runRenamePane clear failed: %v, stderr: %s", err, stderr.String())
	}
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		msg, err := transport.ReadServerFrame(clientConn)
		if err != nil {
			t.Fatalf("reading server frame: %v", err)
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

	// 4. Error outside session without pane ID
	t.Setenv("WIDEBOI_PANE_ID", "")
	stderr.Reset()
	err = runRenamePane(cfg, []string{"No Pane"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "pane-id required") {
		t.Fatalf("expected pane-id required error, got %v", err)
	}

	// 5. Error with non-existent pane ID
	stderr.Reset()
	err = runRenamePane(cfg, []string{"99999", "Fake"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "99999 not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestSetPaneStatusCommand(t *testing.T) {
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

	clientConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	defer clientConn.Close()
	if _, err := transport.Handshake(clientConn); err != nil {
		t.Fatalf("handshake client: %v", err)
	}

	if err := transport.WriteClientFrame(clientConn, protocol.MsgAttach{Cols: 80, Rows: 24}); err != nil {
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

	cfg := config.Config{Socket: sockPath}
	var stderr bytes.Buffer
	paneIDStr := fmt.Sprintf("%d", paneID)

	// 1. Explicit pane ID and status "working"
	if err := runSetPaneStatus(cfg, []string{paneIDStr, "working"}, &stderr); err != nil {
		t.Fatalf("runSetPaneStatus working failed: %v, stderr: %s", err, stderr.String())
	}

	// 2. Environment $WIDEBOI_PANE_ID and status "input"
	t.Setenv("WIDEBOI_PANE_ID", paneIDStr)
	stderr.Reset()
	if err := runSetPaneStatus(cfg, []string{"input"}, &stderr); err != nil {
		t.Fatalf("runSetPaneStatus input failed: %v, stderr: %s", err, stderr.String())
	}

	// 3. Clear status
	stderr.Reset()
	if err := runSetPaneStatus(cfg, []string{paneIDStr, "clear"}, &stderr); err != nil {
		t.Fatalf("runSetPaneStatus clear failed: %v, stderr: %s", err, stderr.String())
	}

	// 4. Error outside session without pane ID
	t.Setenv("WIDEBOI_PANE_ID", "")
	stderr.Reset()
	err = runSetPaneStatus(cfg, []string{"working"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "pane-id required") {
		t.Fatalf("expected pane-id required error, got %v", err)
	}

	// 5. Error with non-existent pane ID
	stderr.Reset()
	err = runSetPaneStatus(cfg, []string{"99999", "working"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "99999 not found") {
		t.Fatalf("expected not found error, got %v", err)
	}

	// 6. Error with unknown status
	stderr.Reset()
	err = runSetPaneStatus(cfg, []string{paneIDStr, "dancing"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown status") {
		t.Fatalf("expected unknown status error, got %v", err)
	}
}

func TestDumpPaneCLI(t *testing.T) {
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

	var splitOut, splitErr bytes.Buffer
	err = runSplit(cfg, nil, []string{"echo dump1 && echo dump2 && sleep 5"}, &splitOut, &splitErr)
	if err != nil {
		t.Fatalf("runSplit failed: %v, stderr: %s", err, splitErr.String())
	}

	paneIDStr := strings.TrimSpace(splitOut.String())
	if paneIDStr == "" {
		t.Fatal("expected pane ID from runSplit, got empty")
	}

	// Wait for output
	var stdout, stderr bytes.Buffer
	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		stdout.Reset()
		stderr.Reset()
		if err := runDumpPane(cfg, []string{paneIDStr}, &stdout, &stderr); err == nil {
			if strings.Contains(stdout.String(), "dump1") && strings.Contains(stdout.String(), "dump2") {
				found = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Fatalf("expected dumped text to contain dump1 and dump2, got %q", stdout.String())
	}

	// 1. Basic dump
	stdout.Reset()
	stderr.Reset()
	if err := runDumpPane(cfg, []string{paneIDStr}, &stdout, &stderr); err != nil {
		t.Fatalf("runDumpPane failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "dump1\ndump2") {
		t.Fatalf("dump text = %q, want dump1\\ndump2", stdout.String())
	}

	// 2. Count-only (-c)
	stdout.Reset()
	stderr.Reset()
	if err := runDumpPane(cfg, []string{paneIDStr, "-c"}, &stdout, &stderr); err != nil {
		t.Fatalf("runDumpPane -c failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "2" {
		t.Fatalf("dump count = %q, want 2", stdout.String())
	}

	// 3. Offset and limit (--offset 0 --limit 1)
	stdout.Reset()
	stderr.Reset()
	if err := runDumpPane(cfg, []string{paneIDStr, "--offset", "0", "--limit", "1"}, &stdout, &stderr); err != nil {
		t.Fatalf("runDumpPane offset/limit failed: %v", err)
	}
	if stdout.String() != "dump1\n" {
		t.Fatalf("dump page 0 = %q, want 'dump1\\n'", stdout.String())
	}

	// 4. Scrollback with line count (--scrollback 100)
	stdout.Reset()
	stderr.Reset()
	if err := runDumpPane(cfg, []string{paneIDStr, "--scrollback", "100"}, &stdout, &stderr); err != nil {
		t.Fatalf("runDumpPane --scrollback 100 failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "dump1\ndump2") {
		t.Fatalf("dump scrollback 100 = %q, want dump1\\ndump2", stdout.String())
	}

	// 5. Output file (-o)
	outFile := filepath.Join(dir, "dump.txt")
	stdout.Reset()
	stderr.Reset()
	if err := runDumpPane(cfg, []string{paneIDStr, "-o", outFile}, &stdout, &stderr); err != nil {
		t.Fatalf("runDumpPane -o failed: %v", err)
	}
	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if !strings.Contains(string(content), "dump1\ndump2") {
		t.Fatalf("file content = %q, want dump1\\ndump2", string(content))
	}

	// 6. In-session fallback (WIDEBOI_PANE_ID)
	t.Setenv("WIDEBOI_PANE_ID", paneIDStr)
	stdout.Reset()
	stderr.Reset()
	if err := runDumpPane(cfg, []string{"--offset", "1", "--limit", "1"}, &stdout, &stderr); err != nil {
		t.Fatalf("runDumpPane with WIDEBOI_PANE_ID failed: %v", err)
	}
	if stdout.String() != "dump2\n" {
		t.Fatalf("dump page 1 = %q, want 'dump2\\n'", stdout.String())
	}

	// 7. Error outside session without pane ID
	t.Setenv("WIDEBOI_PANE_ID", "")
	stdout.Reset()
	stderr.Reset()
	err = runDumpPane(cfg, []string{}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "pane-id required") {
		t.Fatalf("expected pane-id required error, got %v", err)
	}

	// 8. Error with invalid pane ID
	stderr.Reset()
	err = runDumpPane(cfg, []string{"abc"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "invalid pane id") {
		t.Fatalf("expected invalid pane id error, got %v", err)
	}

	// 9. Error with non-existent pane ID
	stderr.Reset()
	err = runDumpPane(cfg, []string{"99999"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "99999 not found") {
		t.Fatalf("expected not found error, got %v", err)
	}

	// 10. Verify capture alias calls runDumpPane
	stdout.Reset()
	stderr.Reset()
	if err := runCapture(cfg, []string{paneIDStr}, &stdout, &stderr); err != nil {
		t.Fatalf("runCapture failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "dump1\ndump2") {
		t.Fatalf("runCapture output = %q, want dump1\\ndump2", stdout.String())
	}

	// 11. Verify legacy flag-first syntax `capture -S <pane-id>` preserves pane ID
	t.Setenv("WIDEBOI_PANE_ID", "")
	stdout.Reset()
	stderr.Reset()
	if err := runCapture(cfg, []string{"-S", paneIDStr}, &stdout, &stderr); err != nil {
		t.Fatalf("runCapture -S <pane-id> failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "dump1\ndump2") {
		t.Fatalf("runCapture -S output = %q, want dump1\\ndump2", stdout.String())
	}

	// 12. Verify flag-first `dump-pane --scrollback <pane-id>` outside session
	stdout.Reset()
	stderr.Reset()
	if err := runDumpPane(cfg, []string{"--scrollback", paneIDStr}, &stdout, &stderr); err != nil {
		t.Fatalf("runDumpPane --scrollback <pane-id> failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "dump1\ndump2") {
		t.Fatalf("runDumpPane --scrollback output = %q, want dump1\\ndump2", stdout.String())
	}

	// 13. Verify --scrollback=false is honored as a boolean
	stdout.Reset()
	stderr.Reset()
	if err := runDumpPane(cfg, []string{"--scrollback=false", paneIDStr}, &stdout, &stderr); err != nil {
		t.Fatalf("runDumpPane --scrollback=false failed: %v", err)
	}

	// 14. Verify malformed boolean returns error
	stderr.Reset()
	err = runDumpPane(cfg, []string{"--scrollback=notabool", paneIDStr}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for --scrollback=notabool, got nil")
	}

	_ = runClose(cfg, []string{paneIDStr}, &stderr)
}

func TestPipePaneCLI(t *testing.T) {
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

	// Keep a persistent dummy pane alive so the server stays up across splits
	var dummyOut, dummyErr bytes.Buffer
	err = runSplit(cfg, nil, []string{"--keep", "sleep 10"}, &dummyOut, &dummyErr)
	if err != nil {
		t.Fatalf("runSplit dummy failed: %v", err)
	}

	// 1. Split a command that sleeps briefly, prints output, and exits
	var splitOut, splitErr bytes.Buffer
	err = runSplit(cfg, nil, []string{"sleep 0.1 && echo stream1 && echo stream2"}, &splitOut, &splitErr)
	if err != nil {
		t.Fatalf("runSplit failed: %v, stderr: %s", err, splitErr.String())
	}

	paneIDStr := strings.TrimSpace(splitOut.String())
	if paneIDStr == "" {
		t.Fatal("expected pane ID from runSplit, got empty")
	}

	var stdout, stderr bytes.Buffer
	if err := runPipePane(cfg, []string{paneIDStr}, &stdout, &stderr); err != nil {
		t.Fatalf("runPipePane failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "stream1") || !strings.Contains(stdout.String(), "stream2") {
		t.Fatalf("runPipePane output = %q, want stream1 and stream2", stdout.String())
	}

	// 2. Test output file with -o and -a
	splitOut.Reset()
	splitErr.Reset()
	err = runSplit(cfg, nil, []string{"sleep 0.1 && echo file1"}, &splitOut, &splitErr)
	if err != nil {
		t.Fatalf("runSplit 2 failed: %v", err)
	}
	p2Str := strings.TrimSpace(splitOut.String())

	outFile := filepath.Join(dir, "pipe.raw")
	stdout.Reset()
	stderr.Reset()
	if err := runPipePane(cfg, []string{p2Str, "-o", outFile}, &stdout, &stderr); err != nil {
		t.Fatalf("runPipePane -o failed: %v", err)
	}
	data, err := os.ReadFile(outFile)
	if err != nil || !strings.Contains(string(data), "file1") {
		t.Fatalf("reading pipe output file: %v, data=%q", err, string(data))
	}

	// Append to file
	splitOut.Reset()
	splitErr.Reset()
	err = runSplit(cfg, nil, []string{"sleep 0.1 && echo file2"}, &splitOut, &splitErr)
	if err != nil {
		t.Fatalf("runSplit 3 failed: %v", err)
	}
	p3Str := strings.TrimSpace(splitOut.String())

	stdout.Reset()
	stderr.Reset()
	if err := runPipePane(cfg, []string{p3Str, "-o", outFile, "-a"}, &stdout, &stderr); err != nil {
		t.Fatalf("runPipePane -o -a failed: %v", err)
	}
	data, err = os.ReadFile(outFile)
	if err != nil || !strings.Contains(string(data), "file1") || !strings.Contains(string(data), "file2") {
		t.Fatalf("reading appended pipe output file: %v, data=%q", err, string(data))
	}

	// 3. Error outside session without pane ID
	t.Setenv("WIDEBOI_PANE_ID", "")
	stdout.Reset()
	stderr.Reset()
	err = runPipePane(cfg, []string{}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "pane-id required") {
		t.Fatalf("expected pane-id required error, got %v", err)
	}

	// 4. Error with non-existent pane ID
	stderr.Reset()
	err = runPipePane(cfg, []string{"99999"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "99999 not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}
