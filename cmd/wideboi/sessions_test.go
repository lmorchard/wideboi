package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/client"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestListSessionsNamesOnlyLiveSessions(t *testing.T) {
	// /tmp, not t.TempDir(): darwin caps sun_path at 104 bytes.
	dir, err := os.MkdirTemp("/tmp", "wb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	live, err := transport.NewSocketListener(filepath.Join(dir, "alive.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	go func() {
		for {
			c, err := live.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	// A corpse: a socket file nobody answers.
	l, err := net.Listen("unix", filepath.Join(dir, "dead.sock"))
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := listSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"alive"}; !slices.Equal(got, want) {
		t.Fatalf("listSessions = %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "dead.sock")); err != nil {
		t.Errorf("listing removed the stale socket; cleanup belongs to the next bind: %v", err)
	}
}

func TestListSessionsOfAMissingDirIsEmpty(t *testing.T) {
	got, err := listSessions("/nonexistent/wideboi-sessions")
	if err != nil || len(got) != 0 {
		t.Fatalf("listSessions(missing) = %v, %v; want none, nil", got, err)
	}
}

func TestDescribeClients(t *testing.T) {
	for n, want := range map[int]string{0: "detached", 1: "1 client", 3: "3 clients"} {
		if got := describeClients(n); got != want {
			t.Errorf("describeClients(%d) = %q, want %q", n, got, want)
		}
	}
}

// The web status response's URL carries the auth token, and ls output
// lands in scrollback, so the address is built from Addr alone.
func TestDescribeWebNeverShowsTheToken(t *testing.T) {
	cases := []struct {
		web  *protocol.MsgWebServerControlResponse
		want string
	}{
		{nil, "-"},
		{&protocol.MsgWebServerControlResponse{Running: false, Addr: "127.0.0.1:8080"}, "-"},
		{&protocol.MsgWebServerControlResponse{
			Running: true, Addr: "127.0.0.1:8080", TLSEnabled: true,
			URL: "https://127.0.0.1:8080/#token=SECRET", Token: "SECRET",
		}, "https://127.0.0.1:8080"},
		{&protocol.MsgWebServerControlResponse{Running: true, Addr: "0.0.0.0:9000"}, "http://0.0.0.0:9000"},
	}
	for _, c := range cases {
		got := describeWeb(c.web)
		if got != c.want {
			t.Errorf("describeWeb(%+v) = %q, want %q", c.web, got, c.want)
		}
		if strings.Contains(got, "SECRET") {
			t.Errorf("describeWeb leaked the token: %q", got)
		}
	}
}

// startListedServer runs a real server on dir/name.sock for as long as
// the test does.
func startListedServer(t *testing.T, dir, name string) string {
	t.Helper()
	sock := filepath.Join(dir, name+".sock")
	sl, err := transport.NewSocketListener(sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := server.NewServer(nil, "/bin/sh", "")
	ctx, cancel := context.WithCancel(context.Background())
	srv.ListenSocket(ctx, sl)
	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
		<-runDone
		sl.Close()
	})
	return sock
}

func TestWriteSessionListShowsAttachmentState(t *testing.T) {
	// /tmp, not t.TempDir(): darwin caps sun_path at 104 bytes.
	dir, err := os.MkdirTemp("/tmp", "wb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	busy := startListedServer(t, dir, "busy")
	startListedServer(t, dir, "idle")

	// One client attached to busy.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := net.Dial("unix", busy)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := transport.Handshake(conn); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)
	go func() {
		for range cc.ServerSendChan() {
		}
	}()
	client.NewClient(cc, 80, 24, "C-b").Attach(ctx)

	// A socket that answers a dial but speaks no protocol.
	mute, err := transport.NewSocketListener(filepath.Join(dir, "mute.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer mute.Close()
	go func() {
		for {
			c, err := mute.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	want := [][]string{
		{"busy", "1", "client", "-"},
		{"idle", "detached", "-"},
		{"mute", "?", "?"},
	}
	// The attach is processed asynchronously; poll up to a ceiling.
	var got [][]string
	deadline := time.Now().Add(5 * time.Second)
	for {
		var buf bytes.Buffer
		if err := writeSessionList(&buf, dir); err != nil {
			t.Fatalf("writeSessionList: %v", err)
		}
		got = got[:0]
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			got = append(got, strings.Fields(line))
		}
		if slices.EqualFunc(got, want, slices.Equal) || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("writeSessionList fields = %q, want %q", got, want)
	}
}
