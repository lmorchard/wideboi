package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/config"
)

// This suite runs inside wideboi sessions: agents run it from a pane.
// A test that resolves the default session there reaches the live one,
// and a palette test that typed "quit" ended the session hosting it
// (#277). TestMain points TMPDIR at a private directory for that reason;
// this fails if it is ever missing.
func TestSuiteIsIsolatedFromRealSessions(t *testing.T) {
	dir := os.Getenv("WIDEBOI_TEST_ISOLATED")
	if dir == "" {
		t.Fatal("TestMain did not isolate this suite: WIDEBOI_TEST_ISOLATED is unset")
	}
	if got := config.DefaultSocketPath(); !strings.HasPrefix(got, dir) {
		t.Errorf("DefaultSocketPath = %q, want it under the private %q", got, dir)
	}
	for _, v := range []string{"WIDEBOI_SOCK", "WIDEBOI_SESSION"} {
		if os.Getenv(v) != "" {
			t.Errorf("%s is set; a test could reach the session it names", v)
		}
	}
}

// The palette tests type real commands -- "quit" among them -- and must
// not reach the default session even inside the isolated TMPDIR: the
// isolation is the safety net, not the design. Listens where the
// default session would be, runs the palette the way its tests do, and
// fails if anything dialled it.
func TestPaletteTestsNeverReachTheDefaultSession(t *testing.T) {
	if os.Getenv("WIDEBOI_TEST_ISOLATED") == "" {
		t.Fatal("not isolated; refusing to listen on the real default socket")
	}
	if err := os.MkdirAll(config.SessionDir(), 0700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", config.DefaultSocketPath())
	if err != nil {
		t.Fatalf("listen on the default socket: %v", err)
	}
	defer l.Close()

	// Accepted while the palette runs, and hung up on at once: a dialler
	// then fails its handshake straight away instead of waiting out the
	// handshake ceiling for a server that never speaks.
	dialled := make(chan struct{}, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return // closed below
		}
		conn.Close()
		dialled <- struct{}{}
	}()

	var in, out, errOut bytes.Buffer
	in.WriteString("quit\n")
	_ = runPalette(paletteTestConfig(t), []string{"--caller-pane=1"}, &in, &out, &errOut)

	// Dispatch is synchronous, so any dial has already happened; the
	// ceiling only covers the accept goroutine reporting it.
	select {
	case <-dialled:
		t.Fatal("the palette's quit reached the default session")
	case <-time.After(200 * time.Millisecond):
	}
}

// paletteTestConfig is what palette and prompt tests run with: a socket
// nothing listens on, so a command they dispatch fails instead of
// reaching a session. An empty config.Config{} falls back to the
// default socket -- the live session, when run from a wideboi pane
// (#277).
func paletteTestConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{Socket: filepath.Join(shortTempDir(t), "none.sock")}
}
