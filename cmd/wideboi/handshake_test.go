package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// fakeServer listens on a fresh socket and greets every connection with
// greeting, as a server from another build would, then hangs up once
// the client does.
func fakeServer(t *testing.T, greeting []byte) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = c.Write(greeting)
				_, _ = io.Copy(io.Discard, c)
			}()
		}
	}()
	return sock
}

func otherVersionHello(pid uint32) []byte {
	b := make([]byte, 4+16)
	binary.BigEndian.PutUint32(b, 16)
	copy(b[4:], "WIDEBOI\x00")
	binary.BigEndian.PutUint32(b[12:], protocol.Version+1)
	binary.BigEndian.PutUint32(b[16:], pid)
	return b
}

// Every command that talks to a server must name a stale one as such,
// not as a decode failure (#174).
func TestCommandsReportAMismatchedServer(t *testing.T) {
	servers := []struct {
		name     string
		greeting []byte
		want     []string
	}{
		{"other version", otherVersionHello(4242), []string{"(pid 4242)", fmt.Sprintf("speaks protocol v%d", protocol.Version+1), fmt.Sprintf("this client speaks v%d", protocol.Version)}},
		// What a pre-protobuf server sent in #174.
		{"gob stream", []byte{0xFF, 0xA0, 0x10, 0x00, 0x01}, []string{"older than protocol versioning", fmt.Sprintf("this client speaks v%d", protocol.Version)}},
	}
	commands := []struct {
		name string
		run  func(config.Config) error
		want string
	}{
		{"attach", func(c config.Config) error { return runAttach(c, nil) }, "-L <name>"},
		{"status", func(c config.Config) error { return runStatus(c, false, io.Discard) }, "-L <name>"},
		{"kill-session", runKillSession, "Use a matching build to end it"},
	}
	for _, srv := range servers {
		for _, cmd := range commands {
			t.Run(srv.name+"/"+cmd.name, func(t *testing.T) {
				sock := fakeServer(t, srv.greeting)
				err := cmd.run(config.Config{Socket: sock})
				if err == nil {
					t.Fatal("no error from a mismatched server")
				}
				for _, w := range append(srv.want, sock, cmd.want) {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("error %q lacks %q", err, w)
					}
				}
				if strings.Contains(err.Error(), "too large") {
					t.Errorf("error %q still blames the frame size", err)
				}
			})
		}
	}
}

// An owner from another build can only happen at startup, before any
// pane exists, so the server leaves rather than run a session no one
// can reach (#174).
func TestServerExitsOnAMismatchedOwner(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	ours := os.NewFile(uintptr(fds[0]), "owner")
	defer ours.Close()
	// runServer takes over fds[1], as a spawned server takes fd 3.
	if _, err := ours.Write(otherVersionHello(4242)); err != nil {
		t.Fatal(err)
	}

	dir, err := os.MkdirTemp("", "wb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")

	done := make(chan error, 1)
	go func() { done <- runServer(config.Config{Socket: sock, Shell: "/bin/sh"}, fds[1]) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("protocol v%d", protocol.Version+1)) {
			t.Fatalf("runServer = %v, want a protocol mismatch", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server kept running for an owner it cannot talk to")
	}
	if c, err := net.Dial("unix", sock); err == nil {
		c.Close()
		t.Fatal("the socket still answers after the server gave up")
	}
}

// A server we spawned that answers in another version -- a rebuild
// between our start and its exec -- exits, as it should. Its exit must
// not turn the mismatch into a vague startup failure.
func TestOwnerReportsAMismatchedSpawnedServer(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	ours, theirs := net.Pipe()
	defer theirs.Close()
	go func() {
		_, _ = theirs.Write(otherVersionHello(4242))
		_, _ = io.Copy(io.Discard, theirs)
	}()
	exited := make(chan int, 1)
	exited <- 1

	dir, err := os.MkdirTemp("", "wb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	err = runClient(config.Config{Socket: filepath.Join(dir, "s.sock")}, nil, ours, exited)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("speaks protocol v%d", protocol.Version+1)) {
		t.Fatalf("runClient = %v, want the protocol mismatch", err)
	}
}
