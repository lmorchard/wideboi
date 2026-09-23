package server

// White-box (package server): plants entries in s.escapees and drives
// the real poll and Close against real processes. A PID cannot be
// recycled on demand, so "recycled" is simulated honestly: a live
// process recorded under a start time that is not its own is exactly
// what the escapee set holds after the original exits and the OS hands
// its PID to something new.

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// notItsStart is a start time no process in a test run has.
const notItsStart = "Thu Jan  1 00:00:00 1970"

// startSleeper runs a long sleep, killed at cleanup, and returns its pid
// and a channel closed when it exits.
func startSleeper(t *testing.T) (int, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command("sleep", "600")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	return cmd.Process.Pid, done
}

// startOf reads pid's start time the same way the server does.
func startOf(t *testing.T, pid int) string {
	t.Helper()
	table, err := readProcTable()
	if err != nil {
		t.Fatalf("read process table: %v", err)
	}
	e, ok := table[pid]
	if !ok {
		t.Fatalf("pid %d not in the process table", pid)
	}
	return e.start
}

// A PID recorded as an escapee and since handed to another process must
// not be SIGKILLed at close: that process is not ours.
func TestCloseSparesARecycledEscapeePID(t *testing.T) {
	pid, done := startSleeper(t)
	s := newBareServer()
	s.escapees[pid] = notItsStart

	_ = s.Close()

	// Close sends its kill -9s before it returns, so the window only
	// has to cover delivery. An absence has no event to wait for.
	select {
	case <-done:
		t.Fatal("Close killed a process whose start time did not match the recorded escapee")
	case <-time.After(300 * time.Millisecond):
	}
}

// The identity check must not cost the reap: an escapee that is still
// the process that was recorded is killed at close.
func TestCloseKillsAnEscapeeThatIsStillItself(t *testing.T) {
	pid, done := startSleeper(t)
	s := newBareServer()
	s.escapees[pid] = startOf(t, pid)

	_ = s.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close left a matching escapee alive")
	}
}

// The poll forgets escapees that exited or whose PID now belongs to a
// different process, and keeps the ones still alive as themselves. It
// does so even with no panes left to walk from, which is when a stale
// set would otherwise sit until close.
func TestPollPrunesExitedAndRecycledEscapees(t *testing.T) {
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}
	exited := gone.Process.Pid
	recycled, _ := startSleeper(t)
	kept, _ := startSleeper(t)

	s := newBareServer()
	s.escapees[exited] = notItsStart
	s.escapees[recycled] = notItsStart
	s.escapees[kept] = startOf(t, kept)

	s.pollDescendants()

	if _, ok := s.escapees[exited]; ok {
		t.Errorf("poll kept exited pid %d", exited)
	}
	if _, ok := s.escapees[recycled]; ok {
		t.Errorf("poll kept recycled pid %d", recycled)
	}
	if _, ok := s.escapees[kept]; !ok {
		t.Errorf("poll dropped pid %d, still alive as itself", kept)
	}
}

// Close SIGKILLs by what parseProcTable reads, so a row it cannot read
// exactly must be dropped, never guessed at. On 2026-09-23 a ps column
// that printed blank shifted every row's fields, the tail that was
// joined into a "start time" matched almost everything, and an
// uncommitted version of the poll had a test's Close kill most of the
// machine's processes. Parse-only: nothing here runs ps or sends a
// signal.
func TestParseProcTableRejectsRowsItCannotReadExactly(t *testing.T) {
	out := strings.Join([]string{
		"  101     1 Wed Sep 23 10:37:01 2026",       // well-formed
		"  102   101 Thu Sep  3 09:05:00 2026",       // padded day: still 5 words
		"  103   101 Wed Sep 23 10:37:01",            // a column short
		"  104   101 ?? Wed Sep 23 10:37:01 2026",    // a column extra
		"  105   101 Wed Sep 23 10:37:01 2026 extra", // trailing junk
		"  abc   101 Wed Sep 23 10:37:01 2026",       // non-numeric pid
		"  106   xyz Wed Sep 23 10:37:01 2026",       // non-numeric ppid
		"",
	}, "\n")

	table := parseProcTable(out)

	want := map[int]procEntry{
		101: {ppid: 1, start: "Wed Sep 23 10:37:01 2026"},
		102: {ppid: 101, start: "Thu Sep 3 09:05:00 2026"},
	}
	if len(table) != len(want) {
		t.Errorf("parsed %d rows, want %d: %+v", len(table), len(want), table)
	}
	for pid, w := range want {
		if got, ok := table[pid]; !ok || got != w {
			t.Errorf("pid %d: got %+v (present %v), want %+v", pid, got, ok, w)
		}
	}
}
