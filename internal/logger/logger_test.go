package logger

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":      slog.LevelInfo,
		"trace": LevelTrace,
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		"DEBUG": slog.LevelDebug,
		" Info": slog.LevelInfo,
	}
	for in, want := range cases {
		got, err := ParseLevel(in)
		if err != nil {
			t.Errorf("ParseLevel(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

// A typo must be an error, not a silent fallback: an unmatchable value
// that behaves exactly like an absent one is how the pgdn binding
// shipped dead (docs/LESSONS.md).
func TestParseLevelRejectsUnknown(t *testing.T) {
	for _, in := range []string{"verbose", "warning", "dbg", "5"} {
		if _, err := ParseLevel(in); err == nil {
			t.Errorf("ParseLevel(%q) accepted an unknown level", in)
		}
	}
}

// slog names an unknown level relative to its neighbour, so Trace would
// print as "DEBUG-4" -- which reads as a debug line and greps as one.
func TestTraceIsNamedTrace(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(newHandler(&buf, LevelTrace))
	log.Log(context.Background(), LevelTrace, "per-message detail")
	if !strings.Contains(buf.String(), "level=TRACE") {
		t.Errorf("trace line not labelled TRACE: %q", buf.String())
	}
}

// The default verbosity must not record per-message traffic: a session
// left running for a day wrote about 500MB of it.
func TestInfoDropsTraceAndDebug(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(newHandler(&buf, slog.LevelInfo))
	log.Log(context.Background(), LevelTrace, "trace")
	log.Debug("debug")
	log.Info("info")
	if strings.Contains(buf.String(), "trace") || strings.Contains(buf.String(), "debug") {
		t.Errorf("info-level handler recorded trace or debug: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "info") {
		t.Errorf("info-level handler dropped an info line: %q", buf.String())
	}
}

// Logs sit beside the socket, so sessions do not interleave in one
// file and a harness's private socket directory gets private logs.
func TestPathSitsBesideTheSocket(t *testing.T) {
	cases := []struct{ socket, component, want string }{
		{"/t/wideboi-501/default.sock", "server", "/t/wideboi-501/default.server.log"},
		{"/x/work.sock", "client", "/x/work.client.log"},
		{"/x/plain", "server", "/x/plain.server.log"}, // WIDEBOI_SOCK needn't end in .sock
	}
	for _, c := range cases {
		if got := Path(c.socket, c.component); got != c.want {
			t.Errorf("Path(%q, %q) = %q, want %q", c.socket, c.component, got, c.want)
		}
	}
}

// exits.log is shared by every session in the directory, so it sits
// beside the sockets rather than being named after one of them.
func TestExitsPathBesideSocket(t *testing.T) {
	if got := ExitsPath("/a/b/x.sock"); got != "/a/b/exits.log" {
		t.Errorf("ExitsPath = %q, want /a/b/exits.log", got)
	}
	if got := SessionOf("/a/b/x.sock"); got != "x" {
		t.Errorf("SessionOf = %q, want x", got)
	}
}

// Each record is one line naming the session, the component and the
// writing process, followed by the event's own attributes; records
// append rather than replace.
func TestAppendExitWritesOneRecord(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	for i := 0; i < 2; i++ {
		if err := AppendExit(sock, "server", "server exit", "reason", "shutdown-request"); err != nil {
			t.Fatalf("AppendExit: %v", err)
		}
	}
	data, err := os.ReadFile(ExitsPath(sock))
	if err != nil {
		t.Fatalf("read exits log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), data)
	}
	for _, want := range []string{`msg="server exit"`, "session=s", "component=server",
		fmt.Sprintf("pid=%d", os.Getpid()), "reason=shutdown-request"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("record %q lacks %q", lines[0], want)
		}
	}
}

// Several processes append at once. One short O_APPEND write per record
// keeps every line whole.
func TestAppendExitConcurrentLinesStayWhole(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_ = AppendExit(sock, "client", "client exit", "g", g, "i", i)
			}
		}(g)
	}
	wg.Wait()
	data, err := os.ReadFile(ExitsPath(sock))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 400 {
		t.Fatalf("got %d lines, want 400", len(lines))
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "time=") || !strings.Contains(l, "component=client") {
			t.Fatalf("torn record: %q", l)
		}
	}
}
