package server

// White-box (package server): drives handleClientConnLoop with the
// session-lifecycle messages and inspects the server's state directly.
// Same justification as transport_close_test.go, whose closableTransport
// this reuses.

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func newBareServer(tps ...transport.Transport) *Server {
	return &Server{
		strip:      layout.NewStrip(),
		panes:      make(map[int]*Pane),
		stopCh:     make(chan struct{}),
		transports: tps,
	}
}

// runLoopUntilReturn serves tp until its loop returns, failing the test
// if that takes longer than a lifecycle message should.
func runLoopUntilReturn(t *testing.T, s *Server, tp transport.Transport) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		s.handleClientConnLoop(context.Background(), tp)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleClientConnLoop did not return")
	}
}

// A shutdown hangs up on every client, not only the one that asked:
// the other attached clients must see their connection end too.
func TestShutdownClosesServerAndHangsUpEveryClient(t *testing.T) {
	asker := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	other := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(asker, other)

	asker.ClientSend <- protocol.MsgShutdown{}
	runLoopUntilReturn(t, s, asker)

	select {
	case <-s.stopCh:
	default:
		t.Fatal("MsgShutdown did not close the server")
	}
	if !asker.closed.Load() || !other.closed.Load() {
		t.Errorf("shutdown left a client connected: asker closed=%v, other closed=%v",
			asker.closed.Load(), other.closed.Load())
	}
}

// A detach ends one connection and nothing else: the server keeps
// running and the other client stays attached.
func TestDetachHangsUpOnlyThatClient(t *testing.T) {
	leaver := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	stayer := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(leaver, stayer)

	leaver.ClientSend <- protocol.MsgDetach{}
	runLoopUntilReturn(t, s, leaver)

	if !leaver.closed.Load() {
		t.Error("detaching client was not hung up on")
	}
	if stayer.closed.Load() {
		t.Error("a detach hung up on a different client")
	}
	select {
	case <-s.stopCh:
		t.Fatal("a detach stopped the server")
	default:
	}
}

// An owner that vanishes without a word -- SIGKILL runs no code at all
// -- takes the session with it.
func TestOwnerEOFWithoutDetachEndsTheSession(t *testing.T) {
	owner := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	other := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(owner, other)
	s.SetOwner(owner)

	close(owner.ClientSend)
	runLoopUntilReturn(t, s, owner)

	select {
	case <-s.stopCh:
	default:
		t.Fatal("owner EOF without detach left the session running")
	}
	if !other.closed.Load() {
		t.Error("ending the session did not hang up on the other client")
	}
}

// Once the owner detaches, its EOF is ordinary, and the session is
// ownerless for good.
func TestOwnerDetachGivesUpOwnership(t *testing.T) {
	owner := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(owner)
	s.SetOwner(owner)

	owner.ClientSend <- protocol.MsgDetach{}
	close(owner.ClientSend)
	runLoopUntilReturn(t, s, owner) // consumes the detach
	// In production nothing reads the connection after a detach -- the
	// loop returns. This second pass forces the EOF through the !ok
	// path anyway, to prove that even if it were read, ownership is
	// already gone and it would not end the session.
	runLoopUntilReturn(t, s, owner)

	select {
	case <-s.stopCh:
		t.Fatal("an owner's detach ended the session")
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner != nil {
		t.Error("detached owner still recorded as owner")
	}
}

// A non-owner's EOF is a detach, as it always was.
func TestNonOwnerEOFLeavesTheSession(t *testing.T) {
	owner := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	guest := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(owner, guest)
	s.SetOwner(owner)

	close(guest.ClientSend)
	runLoopUntilReturn(t, s, guest)

	select {
	case <-s.stopCh:
		t.Fatal("a non-owner leaving ended the session")
	default:
	}
	if owner.closed.Load() {
		t.Error("a non-owner leaving hung up on the owner")
	}
}

// A server that is shutting down must not grow new panes. Close reaps
// the panes it snapshotted; one spawned afterwards -- by a client that
// attaches, or asks for a column, during the reap -- would be nobody's
// to reap.
func TestClosedServerSpawnsNoPanes(t *testing.T) {
	s := NewServer(nil, "/bin/sh", "")
	_ = s.Close()

	s.handleClientMsg(context.Background(), protocol.MsgAttach{Cols: 80, Rows: 24})
	s.handleClientMsg(context.Background(), protocol.MsgVerb{Verb: protocol.VerbNewColumn})

	s.mu.Lock()
	leaked := make([]*Pane, 0, len(s.panes))
	for _, p := range s.panes {
		leaked = append(leaked, p)
	}
	s.mu.Unlock()
	for _, p := range leaked {
		_ = p.Close()
	}
	if len(leaked) != 0 {
		t.Errorf("a closed server spawned %d pane(s)", len(leaked))
	}
}

// Close stops accepting connections before it starts reaping, which
// takes seconds. A `wideboi` run in that window must fail to dial --
// and so start a fresh session -- rather than attach to one that is
// about to hang up on it.
func TestCloseStopsListeningBeforeReaping(t *testing.T) {
	// Not t.TempDir: this test's name makes that path longer than the
	// 104 bytes darwin allows a unix socket.
	dir, err := os.MkdirTemp("", "wb")
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

	// Real panes whose root outlives the hangup, so Close waits out its
	// whole grace: the root ignores SIGHUP and never reads its terminal.
	// An interactive shell would exit at once and leave no window to
	// observe. exec makes the root sleep itself, our direct child, so
	// cleanup signals exactly the pids this test spawned.
	root := filepath.Join(dir, "root.sh")
	if err := os.WriteFile(root, []byte("#!/bin/sh\ntrap '' HUP\n: > \"$0.ready.$$\"\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewServer(nil, root, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.ListenSocket(ctx, sl)
	s.handleClientMsg(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})

	s.mu.Lock()
	var roots []*os.Process
	for _, p := range s.panes {
		roots = append(roots, p.pty.Cmd.Process)
	}
	s.mu.Unlock()
	if len(roots) == 0 {
		t.Fatal("attach spawned no panes; the ordering check would be vacuous")
	}
	defer func() {
		for _, r := range roots {
			_ = r.Kill()
		}
	}()

	// The hangup must not beat the trap: wait until every root has set
	// it and marked itself ready.
	deadline := time.Now().Add(5 * time.Second)
	for {
		ready, _ := filepath.Glob(root + ".ready.*")
		if len(ready) >= len(roots) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d pane roots got ready", len(ready), len(roots))
		}
		time.Sleep(10 * time.Millisecond)
	}

	closed := make(chan struct{})
	go func() {
		_ = s.Close()
		close(closed)
	}()

	deadline = time.Now().Add(CloseGrace / 2)
	for {
		c, err := net.Dial("unix", sock)
		if err != nil {
			break
		}
		c.Close()
		if time.Now().After(deadline) {
			t.Fatal("still accepting connections well into Close's reap")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-closed:
		t.Fatal("Close finished before the check; the reap was too quick to prove an ordering")
	default:
	}
	<-closed
}
