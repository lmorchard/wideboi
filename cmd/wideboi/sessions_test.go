package main

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"

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
