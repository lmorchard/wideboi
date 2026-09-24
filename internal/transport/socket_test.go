package transport_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// NewSocketListener unlinks a stale socket so a server that died
// without cleaning up does not block the next one. Once WIDEBOI_SOCK
// let a caller name any path, that unlink became a way to delete an
// arbitrary file by typo -- `WIDEBOI_SOCK=~/notes.txt wideboi server`
// would have removed it before failing to listen.
func TestNewSocketListenerRefusesToRemoveANonSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "precious.txt")
	if err := os.WriteFile(path, []byte("do not delete me"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	sl, err := transport.NewSocketListener(path)
	if err == nil {
		sl.Close()
		t.Fatal("NewSocketListener accepted a regular file")
	}

	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("the file was removed: %v", readErr)
	}
	if string(got) != "do not delete me" {
		t.Fatalf("file contents changed: %q", got)
	}
}

func TestSocketListenerAndConnRoundTrip(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "wb")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(dir)
	sockPath := filepath.Join(dir, "t.sock")

	sl, err := transport.NewSocketListener(sockPath)
	if err != nil {
		t.Fatalf("NewSocketListener failed: %v", err)
	}
	defer sl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	connErr := make(chan error, 1)
	var clientConn *transport.ClientSocketConn

	go func() {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			connErr <- err
			return
		}
		clientConn = transport.NewClientSocketConn(conn, 16)
		clientConn.RunPumps(ctx)
		connErr <- nil
	}()

	srvConnRaw, err := sl.Accept()
	if err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
	srvConn := transport.NewServerSocketConn(srvConnRaw, 16)
	srvConn.RunPumps(ctx)

	if err := <-connErr; err != nil {
		t.Fatalf("net.Dial failed: %v", err)
	}

	// Send client -> server message (MsgAttach)
	attachMsg := protocol.MsgAttach{Cols: 80, Rows: 24}
	if !clientConn.SendClient(ctx, attachMsg) {
		t.Fatal("client SendClient failed")
	}

	select {
	case msg := <-srvConn.ClientSendChan():
		got, ok := msg.(protocol.MsgAttach)
		if !ok || got.Cols != 80 || got.Rows != 24 {
			t.Fatalf("got server msg %+v, want MsgAttach {80, 24}", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for client message on server")
	}

	// Send server -> client message (MsgLayoutSnapshot)
	snapMsg := protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{{PaneID: 1, Width: 80, Height: 24}},
	}
	if !srvConn.SendServer(ctx, snapMsg) {
		t.Fatal("server SendServer failed")
	}

	select {
	case msg := <-clientConn.ServerSendChan():
		_, ok := msg.(protocol.MsgLayoutSnapshot)
		if !ok {
			t.Fatalf("client received %T, want MsgLayoutSnapshot", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for server message on client")
	}

	clientConn.Close()
	srvConn.Close()
}

// Closing a server-side connection must release a broadcast stuck on
// its full queue. Otherwise one stalled client -- a peer that stopped
// reading, so the write pump stopped draining -- parks the server's Run
// loop inside SendServer forever, and a server that has finished
// shutting down never exits.
func TestServerSendReturnsOnceClosed(t *testing.T) {
	ours, theirs := net.Pipe()
	defer theirs.Close()
	// No pumps: nothing drains ServerSend, as with a stuck write pump.
	sc := transport.NewServerSocketConn(ours, 1)
	sc.SendServer(context.Background(), protocol.MsgPaneClosed{PaneID: 1})

	done := make(chan bool, 1)
	go func() { done <- sc.SendServer(context.Background(), protocol.MsgPaneClosed{PaneID: 2}) }()
	time.Sleep(50 * time.Millisecond)
	_ = sc.Close()

	select {
	case ok := <-done:
		if ok {
			t.Error("SendServer reported queueing a message on a closed connection")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendServer stayed blocked after Close")
	}
}

// shortTempDir is a private directory short enough for a socket path:
// darwin caps sun_path at 104 bytes, and t.TempDir() under /var/folders
// can exceed it.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wb")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// A second server for a path must not probe, unlink or rebind it while
// the first is alive (#86). Two opens of one file conflict under flock
// even within one process, so this exercises the real exclusion.
func TestSecondListenerIsRefusedAndLeavesTheSocketAlone(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "s.sock")
	first, err := transport.NewSocketListener(path)
	if err != nil {
		t.Fatalf("first listener: %v", err)
	}
	defer first.Close()
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	second, err := transport.NewSocketListener(path)
	if err == nil {
		second.Close()
		t.Fatal("a second listener bound a path whose owner is alive")
	}
	if !errors.Is(err, transport.ErrSessionTaken) {
		t.Fatalf("second listener: err = %v, want ErrSessionTaken", err)
	}
	if !strings.Contains(err.Error(), "already listening") {
		t.Errorf("message %q lost 'already listening'", err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("the first listener's socket was replaced or removed (err %v)", err)
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("first listener unreachable: %v", err)
	}
	c.Close()
}

func TestListenerRebindsOnceTheOwnerCloses(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "s.sock")
	first, err := transport.NewSocketListener(path)
	if err != nil {
		t.Fatalf("first listener: %v", err)
	}
	first.Close()
	second, err := transport.NewSocketListener(path)
	if err != nil {
		t.Fatalf("rebinding after the owner closed: %v", err)
	}
	second.Close()
}

// A SIGKILLed server runs no cleanup, so it leaves its socket file --
// but the kernel drops its lock, and the next server reclaims the path.
func TestStaleSocketWithoutALockIsReclaimed(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "s.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture: the corpse socket is missing: %v", err)
	}

	sl, err := transport.NewSocketListener(path)
	if err != nil {
		t.Fatalf("reclaiming a stale socket: %v", err)
	}
	defer sl.Close()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("reclaimed socket unreachable: %v", err)
	}
	c.Close()
}

// A server from a binary older than the lock serves its socket without
// holding one. Taking the lock must not be read as "this socket is a
// corpse" while something still answers it.
func TestLiveSocketWithoutALockIsLeftAlone(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "s.sock")
	old, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer old.Close()

	sl, err := transport.NewSocketListener(path)
	if err == nil {
		sl.Close()
		t.Fatal("bound over a live, unlocked socket")
	}
	if !errors.Is(err, transport.ErrSessionTaken) {
		t.Fatalf("err = %v, want ErrSessionTaken", err)
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("the old server's socket was removed: %v", err)
	}
	c.Close()
}
