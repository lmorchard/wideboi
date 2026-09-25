package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

func takeResponse[T any](t *testing.T, tp *transport.InProcChannel) T {
	t.Helper()
	for {
		select {
		case msg := <-tp.ServerSend:
			if resp, ok := msg.(T); ok {
				return resp
			}
		case <-time.After(time.Second):
			var zero T
			t.Fatalf("timeout waiting for %T", zero)
			return zero
		}
	}
}

func TestServerPaneControlRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := &Server{
		panes:      make(map[int]*Pane),
		cols:       80,
		rows:       24,
		stopCh:     make(chan struct{}),
		shell:      "/bin/sh",
		cwd:        t.TempDir(),
		closeGrace: 10 * time.Millisecond,
		strip:      layout.NewStrip(),
	}

	tp := transport.NewInProcChannel(64)
	s.transports = append(s.transports, tp)

	// Add an initial pane using term.NewVT
	g1 := term.NewVT(40, 20)
	defer g1.Close()
	fmt.Fprintf(g1, "initial output\r\n")

	p1 := &Pane{
		id:     1,
		grid:   g1,
		cols:   40,
		rows:   20,
		closed: make(chan struct{}),
	}
	s.panes[1] = p1
	s.strip.AddColumn(1, 40, 20, 0)

	// 1. Test MsgCaptureRequest on existing pane
	s.handleClientMsg(ctx, tp, protocol.MsgCaptureRequest{PaneID: 1})
	resp1 := takeResponse[protocol.MsgCaptureResponse](t, tp)
	if resp1.Error != "" {
		t.Fatalf("unexpected error: %s", resp1.Error)
	}
	if resp1.PaneID != 1 {
		t.Fatalf("expected PaneID 1, got %d", resp1.PaneID)
	}
	if resp1.Text != "initial output\n" {
		t.Fatalf("unexpected text: %q", resp1.Text)
	}

	// 2. Test MsgCaptureRequest on non-existent pane
	s.handleClientMsg(ctx, tp, protocol.MsgCaptureRequest{PaneID: 999})
	resp2 := takeResponse[protocol.MsgCaptureResponse](t, tp)
	if resp2.Error == "" {
		t.Fatal("expected error for non-existent pane, got none")
	}

	// 3. Test MsgSendInputRequest on non-existent pane
	s.handleClientMsg(ctx, tp, protocol.MsgSendInputRequest{PaneID: 999, Data: []byte("test")})
	resp3 := takeResponse[protocol.MsgSendInputResponse](t, tp)
	if resp3.Error == "" {
		t.Fatal("expected error for non-existent pane, got none")
	}

	// 4. Test MsgSplitRequest with invalid after_pane_id
	s.handleClientMsg(ctx, tp, protocol.MsgSplitRequest{AfterPaneID: 999})
	resp4 := takeResponse[protocol.MsgSplitResponse](t, tp)
	if resp4.Error == "" {
		t.Fatal("expected error for invalid after_pane_id, got none")
	}

	// 5. Test MsgClosePaneRequest on non-existent pane
	s.handleClientMsg(ctx, tp, protocol.MsgClosePaneRequest{PaneID: 999})
	resp5 := takeResponse[protocol.MsgClosePaneResponse](t, tp)
	if resp5.Error == "" {
		t.Fatal("expected error for non-existent pane, got none")
	}

	// 6. Test MsgSplitRequest success
	s.handleClientMsg(ctx, tp, protocol.MsgSplitRequest{Command: "sleep 10", Cwd: s.cwd})
	resp6 := takeResponse[protocol.MsgSplitResponse](t, tp)
	if resp6.Error != "" {
		t.Fatalf("unexpected error spawning pane: %s", resp6.Error)
	}
	if resp6.PaneID <= 0 {
		t.Fatalf("expected positive PaneID, got %d", resp6.PaneID)
	}
	spawnedID := resp6.PaneID

	// Verify pane exists
	if _, ok := s.panes[spawnedID]; !ok {
		t.Fatalf("expected pane %d to exist in s.panes", spawnedID)
	}

	// 7. Test MsgSendInputRequest success
	s.handleClientMsg(ctx, tp, protocol.MsgSendInputRequest{PaneID: spawnedID, Data: []byte("hello\n")})
	resp7 := takeResponse[protocol.MsgSendInputResponse](t, tp)
	if resp7.Error != "" {
		t.Fatalf("unexpected error sending input: %s", resp7.Error)
	}
	if resp7.PaneID != spawnedID {
		t.Fatalf("expected PaneID %d, got %d", spawnedID, resp7.PaneID)
	}

	// 8. Test MsgClosePaneRequest on existing pane
	s.handleClientMsg(ctx, tp, protocol.MsgClosePaneRequest{PaneID: 1})
	resp8 := takeResponse[protocol.MsgClosePaneResponse](t, tp)
	if resp8.Error != "" {
		t.Fatalf("unexpected error: %s", resp8.Error)
	}
	if resp8.PaneID != 1 {
		t.Fatalf("expected PaneID 1, got %d", resp8.PaneID)
	}

	// Verify pane was removed from server
	if _, ok := s.panes[1]; ok {
		t.Fatal("pane 1 still exists in s.panes")
	}

	// Clean up spawned pane
	s.handleClientMsg(ctx, tp, protocol.MsgClosePaneRequest{PaneID: spawnedID})
	takeResponse[protocol.MsgClosePaneResponse](t, tp)
}

// newControlFixture is a server with no panes and no Run loop: requests
// go straight to handleClientMsg, and responses land on tp.
func newControlFixture(t *testing.T) (*Server, *transport.InProcChannel) {
	t.Helper()
	s := &Server{
		panes:      make(map[int]*Pane),
		cols:       80,
		rows:       24,
		stopCh:     make(chan struct{}),
		shell:      "/bin/sh",
		cwd:        t.TempDir(),
		closeGrace: 10 * time.Millisecond,
		strip:      layout.NewStrip(),
	}
	tp := transport.NewInProcChannel(256)
	s.transports = append(s.transports, tp)
	return s, tp
}

// splitPane asks the fixture for a pane and fails the test if it cannot.
func splitPane(t *testing.T, ctx context.Context, s *Server, tp *transport.InProcChannel, req protocol.MsgSplitRequest) int {
	t.Helper()
	s.handleClientMsg(ctx, tp, req)
	resp := takeResponse[protocol.MsgSplitResponse](t, tp)
	if resp.Error != "" {
		t.Fatalf("split %q: %s", req.Command, resp.Error)
	}
	return resp.PaneID
}

// lookupPane reads s.panes under the lock the exit paths write it under.
func lookupPane(s *Server, id int) (*Pane, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.panes[id]
	return p, ok
}

// waitFor polls cond until it holds or ceiling passes.
func waitFor(t *testing.T, ceiling time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(ceiling)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", ceiling, what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestKeptPaneSurvivesExit pins split --keep: the pane outlives its
// process with its screen and exit code, refuses input, and still closes.
func TestKeptPaneSurvivesExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, tp := newControlFixture(t)

	id := splitPane(t, ctx, s, tp, protocol.MsgSplitRequest{Command: "echo kept-output; exit 3", Keep: true})

	var p *Pane
	waitFor(t, 5*time.Second, "the kept pane's exit", func() bool {
		var ok bool
		p, ok = lookupPane(s, id)
		if !ok {
			t.Fatalf("kept pane %d was removed on exit", id)
		}
		_, exited := p.ExitStatus()
		return exited
	})

	if code, _ := p.ExitStatus(); code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	if st := p.Status(); st != protocol.StatusFailed {
		t.Errorf("status = %v, want failed", st)
	}
	s.mu.Lock()
	_, inStrip := s.strip.ColumnWidth(id)
	s.mu.Unlock()
	if !inStrip {
		t.Errorf("kept pane %d left the strip", id)
	}

	s.handleClientMsg(ctx, tp, protocol.MsgCaptureRequest{PaneID: id})
	if cap := takeResponse[protocol.MsgCaptureResponse](t, tp); !strings.Contains(cap.Text, "kept-output") {
		t.Errorf("capture after exit = %q (err %q), want it to contain kept-output", cap.Text, cap.Error)
	}

	s.handleClientMsg(ctx, tp, protocol.MsgSendInputRequest{PaneID: id, Data: []byte("x")})
	if resp := takeResponse[protocol.MsgSendInputResponse](t, tp); !strings.Contains(resp.Error, "has exited") {
		t.Errorf("send to exited pane: error = %q, want it to mention \"has exited\"", resp.Error)
	}

	var meta *protocol.MsgPaneMetadata
	s.sendPaneMetadataTo(ctx, tp)
	for meta == nil {
		m := takeResponse[protocol.MsgPaneMetadata](t, tp)
		if m.PaneID == id {
			meta = &m
		}
	}
	if !meta.Exited || meta.ExitCode != 3 {
		t.Errorf("metadata exited=%v exit_code=%d, want true/3", meta.Exited, meta.ExitCode)
	}

	s.handleClientMsg(ctx, tp, protocol.MsgClosePaneRequest{PaneID: id})
	if resp := takeResponse[protocol.MsgClosePaneResponse](t, tp); resp.Error != "" {
		t.Fatalf("close kept pane: %s", resp.Error)
	}
	if _, ok := lookupPane(s, id); ok {
		t.Error("kept pane still present after close")
	}
}

// TestUnkeptPaneStillCloses pins the default: without --keep a pane goes
// away when its process exits, as it always has.
func TestUnkeptPaneStillCloses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, tp := newControlFixture(t)

	// A long-lived neighbour, so the exit is not the last pane's and the
	// server stays up.
	anchor := splitPane(t, ctx, s, tp, protocol.MsgSplitRequest{Command: "sleep 30"})
	id := splitPane(t, ctx, s, tp, protocol.MsgSplitRequest{Command: "exit 0"})

	waitFor(t, 5*time.Second, "the unkept pane to close", func() bool {
		_, ok := lookupPane(s, id)
		return !ok
	})

	s.handleClientMsg(ctx, tp, protocol.MsgClosePaneRequest{PaneID: anchor})
	takeResponse[protocol.MsgClosePaneResponse](t, tp)
}

// TestKeptPaneExitFollowsItsOutput pins the order a waiter relies on:
// by the time a kept pane reports exited, its last output is on screen.
// The reap can beat the pty reader to the final bytes; a pane that
// marked itself exited at the reap would let `wait` then `capture` miss
// the end.
func TestKeptPaneExitFollowsItsOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, tp := newControlFixture(t)
	// Closing each kept pane must not be closing the last pane, or the
	// server shuts down under the next iteration.
	anchor := splitPane(t, ctx, s, tp, protocol.MsgSplitRequest{Command: "sleep 60"})
	defer func() {
		s.handleClientMsg(ctx, tp, protocol.MsgClosePaneRequest{PaneID: anchor})
		takeResponse[protocol.MsgClosePaneResponse](t, tp)
	}()

	for i := 0; i < 5; i++ {
		id := splitPane(t, ctx, s, tp, protocol.MsgSplitRequest{
			Command: "i=0; while [ $i -lt 3000 ]; do echo line-$i; i=$((i+1)); done; echo TAIL-MARK",
			Keep:    true,
		})
		var p *Pane
		waitFor(t, 10*time.Second, "the kept pane's exit", func() bool {
			var ok bool
			if p, ok = lookupPane(s, id); !ok {
				t.Fatalf("run %d: kept pane %d vanished", i, id)
			}
			_, exited := p.ExitStatus()
			return exited
		})
		if text := p.CaptureText(false, 0); !strings.Contains(text, "TAIL-MARK") {
			t.Fatalf("run %d: pane reported exited before its last output landed; screen tail:\n%s", i, lastLines(text, 3))
		}
		s.handleClientMsg(ctx, tp, protocol.MsgClosePaneRequest{PaneID: id})
		takeResponse[protocol.MsgClosePaneResponse](t, tp)
	}
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// TestKeptPaneExitDoesNotWaitOnBackgroundJobs pins keptDrainCeiling: a
// background job holding the pty keeps EOF away, but the exit is the
// shell's, and it is reported anyway.
func TestKeptPaneExitDoesNotWaitOnBackgroundJobs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, tp := newControlFixture(t)

	id := splitPane(t, ctx, s, tp, protocol.MsgSplitRequest{Command: "sleep 5 & exit 4", Keep: true})
	var p *Pane
	waitFor(t, keptDrainCeiling+3*time.Second, "the exit despite a background job", func() bool {
		p, _ = lookupPane(s, id)
		_, exited := p.ExitStatus()
		return exited
	})
	if code, _ := p.ExitStatus(); code != 4 {
		t.Errorf("exit code = %d, want 4", code)
	}
	s.handleClientMsg(ctx, tp, protocol.MsgClosePaneRequest{PaneID: id})
	takeResponse[protocol.MsgClosePaneResponse](t, tp)
}

// TestWatchKeptPaneHonoursDrainCeiling drives the ceiling directly. On
// macOS the session leader's exit hangs up the pty, so EOF arrives even
// with a background job holding it, and the behavioural test above
// never reaches the ceiling; Linux can keep the pty open. Here the drain
// never comes, whatever the platform.
func TestWatchKeptPaneHonoursDrainCeiling(t *testing.T) {
	s, _ := newControlFixture(t)
	p, err := NewPane(1, []string{"/bin/sh", "-c", "exit 4"}, 40, 10, s.cwd)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	p.closeGrace = 10 * time.Millisecond
	defer p.Close()
	s.mu.Lock()
	s.panes[1] = p
	s.mu.Unlock()
	p.Start(func() {})

	start := time.Now()
	s.watchKeptPane(1, p, make(chan struct{})) // never drained
	if d := time.Since(start); d < keptDrainCeiling {
		t.Errorf("watchKeptPane returned after %v, before the %v drain ceiling", d, keptDrainCeiling)
	}
	if code, exited := p.ExitStatus(); !exited || code != 4 {
		t.Errorf("ExitStatus() = (%d, %v), want (4, true)", code, exited)
	}
}

// expectNoWaitResponse checks that nothing answers a waiter yet. A
// negative check: the short window is the whole assertion.
func expectNoWaitResponse(t *testing.T, tp *transport.InProcChannel, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case msg := <-tp.ServerSend:
			if resp, ok := msg.(protocol.MsgWaitResponse); ok {
				t.Fatalf("waiter answered early: %+v", resp)
			}
		case <-deadline:
			return
		}
	}
}

// TestWaitRequest pins `wideboi wait`'s server side: a waiter is answered
// with the exit code when the process exits, at once if a kept pane has
// already exited, with an error for an unknown pane, and one way or the
// other when the pane is closed under it.
func TestWaitRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, tp := newControlFixture(t)
	anchor := splitPane(t, ctx, s, tp, protocol.MsgSplitRequest{Command: "sleep 60"})
	defer func() {
		s.handleClientMsg(ctx, tp, protocol.MsgClosePaneRequest{PaneID: anchor})
		takeResponse[protocol.MsgClosePaneResponse](t, tp)
	}()

	// A kept pane, blocked until we let it go.
	kept := splitPane(t, ctx, s, tp, protocol.MsgSplitRequest{Command: "read x; exit 7", Keep: true})
	s.handleClientMsg(ctx, tp, protocol.MsgWaitRequest{PaneID: kept})
	expectNoWaitResponse(t, tp, 100*time.Millisecond)
	s.handleClientMsg(ctx, tp, protocol.MsgSendInputRequest{PaneID: kept, Data: []byte("go\r")})
	if resp := takeResponseWithin[protocol.MsgWaitResponse](t, tp, 5*time.Second); resp.PaneID != kept || resp.ExitCode != 7 || resp.Error != "" {
		t.Errorf("wait on kept pane = %+v, want pane %d exit 7", resp, kept)
	}

	// Already exited and kept: answered at once.
	s.handleClientMsg(ctx, tp, protocol.MsgWaitRequest{PaneID: kept})
	if resp := takeResponse[protocol.MsgWaitResponse](t, tp); resp.ExitCode != 7 {
		t.Errorf("second wait on exited kept pane = %+v, want exit 7", resp)
	}

	// Unkept: the waiter registered before the exit still hears it.
	unkept := splitPane(t, ctx, s, tp, protocol.MsgSplitRequest{Command: "read x; exit 5"})
	s.handleClientMsg(ctx, tp, protocol.MsgWaitRequest{PaneID: unkept})
	s.handleClientMsg(ctx, tp, protocol.MsgSendInputRequest{PaneID: unkept, Data: []byte("go\r")})
	if resp := takeResponseWithin[protocol.MsgWaitResponse](t, tp, 5*time.Second); resp.PaneID != unkept || resp.ExitCode != 5 || resp.Error != "" {
		t.Errorf("wait on unkept pane = %+v, want pane %d exit 5", resp, unkept)
	}

	// Unknown pane.
	s.handleClientMsg(ctx, tp, protocol.MsgWaitRequest{PaneID: 999})
	if resp := takeResponse[protocol.MsgWaitResponse](t, tp); !strings.Contains(resp.Error, "not found") {
		t.Errorf("wait on unknown pane = %+v, want a not-found error", resp)
	}

	// Closed under the waiter: the hangup's exit code, or an error if
	// the child outlived the grace -- either way, an answer.
	doomed := splitPane(t, ctx, s, tp, protocol.MsgSplitRequest{Command: "sleep 30", Keep: true})
	s.handleClientMsg(ctx, tp, protocol.MsgWaitRequest{PaneID: doomed})
	s.handleClientMsg(ctx, tp, protocol.MsgClosePaneRequest{PaneID: doomed})
	resp := takeResponseWithin[protocol.MsgWaitResponse](t, tp, s.closeGrace+2*time.Second)
	if resp.PaneID != doomed || (resp.Error == "" && resp.ExitCode != 128+1) {
		t.Errorf("wait on closed pane = %+v, want SIGHUP's 129 or an error", resp)
	}
}

// takeResponseWithin is takeResponse with a caller-chosen ceiling, for
// answers that wait on a child process.
func takeResponseWithin[T any](t *testing.T, tp *transport.InProcChannel, ceiling time.Duration) T {
	t.Helper()
	deadline := time.After(ceiling)
	for {
		select {
		case msg := <-tp.ServerSend:
			if resp, ok := msg.(T); ok {
				return resp
			}
		case <-deadline:
			var zero T
			t.Fatalf("timeout after %s waiting for %T", ceiling, zero)
			return zero
		}
	}
}
