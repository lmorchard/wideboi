package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestPrepareUpgradeFailureKeepsClientAttached(t *testing.T) {
	tp := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(tp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loopDone := make(chan struct{})
	go func() {
		s.handleClientConnLoop(ctx, tp)
		close(loopDone)
	}()

	// Send an upgrade request pointing to a nonexistent binary
	tp.ClientSend <- protocol.MsgUpgradeRequest{BinPath: "/nonexistent/wideboi-binary-path"}

	select {
	case msg := <-tp.ServerSend:
		resp, ok := msg.(protocol.MsgUpgradeResponse)
		if !ok {
			t.Fatalf("expected MsgUpgradeResponse, got %T", msg)
		}
		if resp.Error == "" {
			t.Fatal("expected error in upgrade response for nonexistent binary")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for upgrade response")
	}

	// Verify client is still tracked in server transports
	s.mu.Lock()
	found := false
	for _, tr := range s.transports {
		if tr == tp {
			found = true
			break
		}
	}
	upgrading := s.upgrading
	s.mu.Unlock()

	if !found {
		t.Error("client was dropped from s.transports after PrepareUpgrade failure")
	}
	if upgrading {
		t.Error("server still marked as upgrading after PrepareUpgrade failure")
	}

	// Close client send to terminate the loop cleanly
	close(tp.ClientSend)
	select {
	case <-loopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not exit on client close")
	}
}

func TestExecFailureDropsClient(t *testing.T) {
	tp := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(tp)
	s.startedTransports = map[transport.Transport]bool{tp: true}
	s.owner = tp

	binPath, err := exec.LookPath("sh")
	if err != nil {
		binPath = os.Args[0]
	}

	s.execSyscall = func(bin string, args, env []string) error {
		return errors.New("simulated exec error")
	}

	tp.ClientSend <- protocol.MsgUpgradeRequest{BinPath: binPath}

	runLoopUntilReturn(t, s, tp)

	// Verify response was sent before exec attempt
	select {
	case msg := <-tp.ServerSend:
		if _, ok := msg.(protocol.MsgUpgradeResponse); !ok {
			t.Fatalf("expected MsgUpgradeResponse, got %T", msg)
		}
	default:
		t.Fatal("expected MsgUpgradeResponse before exec")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.transports) != 0 {
		t.Errorf("s.transports has %d entries, want 0", len(s.transports))
	}
	if s.owner != nil {
		t.Errorf("s.owner is still set to %p, want nil", s.owner)
	}
	if s.startedTransports[tp] {
		t.Errorf("tp still in startedTransports")
	}
	if s.upgrading {
		t.Error("s.upgrading is still true after failed exec rollback")
	}
}

func TestCleanExecArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "plain server",
			in:   []string{"server"},
			want: []string{"server"},
		},
		{
			name: "server with owner-fd separate args",
			in:   []string{"server", "--owner-fd", "3", "-s", "/tmp/sock"},
			want: []string{"server", "-s", "/tmp/sock"},
		},
		{
			name: "server with single-dash owner-fd",
			in:   []string{"server", "-owner-fd", "3", "-l", "cards"},
			want: []string{"server", "-l", "cards"},
		},
		{
			name: "server with owner-fd equals",
			in:   []string{"-s", "/tmp/sock", "--owner-fd=3", "server"},
			want: []string{"-s", "/tmp/sock", "server"},
		},
		{
			name: "server with single-dash owner-fd equals",
			in:   []string{"-s", "/tmp/sock", "-owner-fd=3", "server"},
			want: []string{"-s", "/tmp/sock", "server"},
		},
		{
			name: "missing server subcommand gets prepended",
			in:   []string{"-s", "/tmp/sock"},
			want: []string{"server", "-s", "/tmp/sock"},
		},
		{
			name: "empty args gets server",
			in:   []string{},
			want: []string{"server"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanExecArgs(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("cleanExecArgs(%v) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("cleanExecArgs(%v)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestUpgradeStateRoundTrip(t *testing.T) {
	ownerTp := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(ownerTp)
	s.cols = 120
	s.rows = 40
	s.nextPaneID = 3
	s.shell = "/bin/bash"
	s.cwd = "/test/cwd"
	s.startupLaunched = true
	s.owner = ownerTp
	s.sizeOwner = ownerTp
	s.SetPeerPID(ownerTp, 4242)

	// Add dashboard / status pane and regular pane
	statusGrid := term.NewVT(120, 1)
	statusPane := NewCustomPane(1, statusGrid, 120, 1)
	statusPane.isDashboard = true
	s.panes[1] = statusPane
	s.statusPaneID = 1

	grid2 := term.NewVT(80, 39)
	_, _ = grid2.Write([]byte("\033[?1049h\033[?2004hRoundTripPane2"))
	p2 := NewCustomPane(2, grid2, 80, 39)
	s.panes[2] = p2

	s.strip.AddColumn(1, 120, 1, 0)
	s.strip.AddColumn(2, 80, 39, 0)
	s.strip.FocusPaneID(2)

	// Build upgrade state
	s.mu.Lock()
	state, _, err := s.buildUpgradeStateLocked(protocol.MsgWebServerControlResponse{})
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("buildUpgradeStateLocked failed: %v", err)
	}

	if state.OwnerPID != 4242 {
		t.Errorf("state.OwnerPID = %d, want 4242", state.OwnerPID)
	}
	if state.SizeOwnerPID != 4242 {
		t.Errorf("state.SizeOwnerPID = %d, want 4242", state.SizeOwnerPID)
	}
	if !state.StartupLaunched {
		t.Error("state.StartupLaunched = false, want true")
	}

	// Write state to a temporary file
	tmpFile, err := os.CreateTemp("", "wideboi-test-upgrade-*.json")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if err := json.NewEncoder(tmpFile).Encode(state); err != nil {
		t.Fatalf("encode state: %v", err)
	}
	tmpFile.Close()

	os.Setenv("WIDEBOI_RESTORE_STATE", tmpFile.Name())

	// Create fresh server and restore state
	newS := newBareServer()
	if err := RestoreState(newS); err != nil {
		t.Fatalf("RestoreState failed: %v", err)
	}

	newS.mu.Lock()
	defer newS.mu.Unlock()

	if newS.cols != 120 || newS.rows != 40 {
		t.Errorf("newS dimensions = %dx%d, want 120x40", newS.cols, newS.rows)
	}
	if !newS.startupLaunched {
		t.Error("newS.startupLaunched = false, want true")
	}
	if newS.expectedOwnerPID != 4242 {
		t.Errorf("newS.expectedOwnerPID = %d, want 4242", newS.expectedOwnerPID)
	}
	if newS.expectedSizeOwnerPID != 4242 {
		t.Errorf("newS.expectedSizeOwnerPID = %d, want 4242", newS.expectedSizeOwnerPID)
	}
	if newS.owner != nil {
		t.Errorf("newS.owner before client reconnect = %v, want nil", newS.owner)
	}

	// Verify pane 2 preserved alt-screen mode
	restoredP2 := newS.panes[2]
	if restoredP2 == nil {
		t.Fatal("pane 2 missing in restored server")
	}
	snap2 := restoredP2.grid.(term.Snapshotter).ExportSnapshot()
	if !snap2.IsAltScreen {
		t.Error("restored pane 2 not in alt-screen")
	}
	if !snap2.BracketedPaste {
		t.Error("restored pane 2 does not have bracketed paste enabled")
	}

	// Now simulate client reconnection with matching PID
	reconnectedClient := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	newS.setPeerPIDLocked(reconnectedClient, 4242)

	if newS.owner != reconnectedClient {
		t.Errorf("after client connect, newS.owner = %p, want %p", newS.owner, reconnectedClient)
	}
	if newS.sizeOwner != reconnectedClient {
		t.Errorf("after client connect, newS.sizeOwner = %p, want %p", newS.sizeOwner, reconnectedClient)
	}
	if newS.expectedOwnerPID != 0 {
		t.Errorf("newS.expectedOwnerPID = %d, want 0", newS.expectedOwnerPID)
	}
}

// startReadPane starts a pty pane whose child reads one line and prints
// it back with a prefix, so a test can assert on output the child
// produced rather than on the tty's echo of what was typed.
func startReadPane(t *testing.T, id int) *Pane {
	t.Helper()
	p, err := NewPane(id, []string{"/bin/sh", "-c", "read x; echo got:$x; sleep 5"}, 40, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	p.Start(func() {})
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestInputDuringUpgradeReachesPtyPane(t *testing.T) {
	ctx := context.Background()
	tp := transport.NewInProcChannel(64)
	s := newBareServer(tp)
	dataPane := startReadPane(t, 1)
	keyPane := startReadPane(t, 2)
	s.panes[1] = dataPane
	s.panes[2] = keyPane

	s.mu.Lock()
	s.upgrading = true
	s.mu.Unlock()

	// Data-form input is written to the pty synchronously.
	s.handleClientMsg(ctx, tp, protocol.MsgInput{PaneID: 1, Data: []byte("abc\r")})
	// Key-form input goes through the pane's queue and key-writer.
	for _, r := range "xyz" {
		k := uv.KeyPressEvent{Code: r, Text: string(r)}
		s.handleClientMsg(ctx, tp, protocol.MsgInput{PaneID: 2, Key: protocol.EncodeKey(k)})
	}
	s.handleClientMsg(ctx, tp, protocol.MsgInput{PaneID: 2, Key: protocol.EncodeKey(uv.KeyPressEvent{Code: uv.KeyEnter})})

	waitFor(t, 3*time.Second, "data input to reach the child", func() bool {
		return strings.Contains(dataPane.CaptureText(false, 0), "got:abc")
	})
	waitFor(t, 3*time.Second, "key input to reach the child", func() bool {
		return strings.Contains(keyPane.CaptureText(false, 0), "got:xyz")
	})
}

func TestUpgradeDropsNonInputMessages(t *testing.T) {
	ctx := context.Background()
	tp := transport.NewInProcChannel(64)
	s := newBareServer(tp)
	custom := NewCustomPane(3, term.NewVT(20, 5), 20, 5)
	s.panes[3] = custom

	s.mu.Lock()
	s.upgrading = true
	s.clientLocked(tp).clientScrollOffsets[3] = 5
	s.mu.Unlock()

	// With no size owner and no size yet, a resize would claim the size
	// and set the session geometry, were it handled.
	s.handleClientMsg(ctx, tp, protocol.MsgResize{Cols: 99, Rows: 9})
	// Input to a pane without a pty would snap this client's scroll
	// offset back to live, were it handled.
	s.handleClientMsg(ctx, tp, protocol.MsgInput{PaneID: 3, Data: []byte("x")})

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cols != 0 || s.rows != 0 || s.sizeOwner != nil {
		t.Errorf("resize during upgrade was handled: cols=%d rows=%d sizeOwner=%v", s.cols, s.rows, s.sizeOwner)
	}
	if got := s.clientLocked(tp).clientScrollOffsets[3]; got != 5 {
		t.Errorf("input to a pty-less pane during upgrade was handled: scroll offset %d, want 5", got)
	}
}

func TestDrainInputWaitsForQueuedKeys(t *testing.T) {
	p, err := NewPane(1, []string{"/bin/cat"}, 40, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	p.Start(func() {})
	t.Cleanup(func() { _ = p.Close() })

	for i := 0; i < 50; i++ {
		p.SendBytes([]byte("0123456789"))
	}
	if !p.drainInput(2 * time.Second) {
		t.Fatal("drainInput timed out")
	}
	if n := len(p.input); n != 0 {
		t.Errorf("%d events still queued after drain, want 0", n)
	}

	noPty := NewCustomPane(2, term.NewVT(20, 5), 20, 5)
	noPty.SendBytes([]byte("x"))
	if !noPty.drainInput(10 * time.Millisecond) {
		t.Error("a pane without a pty should drain trivially")
	}
}

func TestExecHoldsLockAfterDrain(t *testing.T) {
	tp := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(tp)
	s.startedTransports = map[transport.Transport]bool{tp: true}

	binPath, err := exec.LookPath("sh")
	if err != nil {
		binPath = os.Args[0]
	}

	heldAtExec := false
	s.execSyscall = func(bin string, args, env []string) error {
		// Nothing may queue input between the drain and the exec.
		if s.mu.TryLock() {
			s.mu.Unlock()
		} else {
			heldAtExec = true
		}
		return errors.New("simulated exec error")
	}

	tp.ClientSend <- protocol.MsgUpgradeRequest{BinPath: binPath}
	runLoopUntilReturn(t, s, tp)

	if !heldAtExec {
		t.Error("s.mu was not held at exec")
	}
	if !s.mu.TryLock() {
		t.Fatal("s.mu still held after failed exec")
	}
	defer s.mu.Unlock()
	if s.upgrading {
		t.Error("s.upgrading still true after failed exec rollback")
	}
}

// TestDrainInputWaitsForPtyWrite pins that drainInput covers the last
// hop. A key reaches the pty-writer through vt's reply pipe, and the
// pty-writer can still be writing it to the pty after the key-writer is
// done with it. Slow each pty write down, and when drainInput returns
// every byte must already have been written.
func TestDrainInputWaitsForPtyWrite(t *testing.T) {
	p, err := NewPane(1, []string{"/bin/cat"}, 40, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	var written atomic.Int64
	p.ptyWritten = func(n int) {
		time.Sleep(20 * time.Millisecond) // the slow hop under test
		written.Add(int64(n))
	}
	p.Start(func() {})
	t.Cleanup(func() { _ = p.Close() })

	const keys = 5
	for i := 0; i < keys; i++ {
		p.SendKey(uv.KeyPressEvent{Code: 'a', Text: "a"})
	}
	if !p.drainInput(5 * time.Second) {
		t.Fatal("drainInput timed out")
	}
	if got := written.Load(); got != keys {
		t.Fatalf("drainInput returned with %d of %d key bytes written to the pty", got, keys)
	}
}
