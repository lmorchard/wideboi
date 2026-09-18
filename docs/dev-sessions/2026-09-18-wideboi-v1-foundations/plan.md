# wideboi Plan 1 — Foundations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a working two-pane terminal multiplexer with a hardcoded layout, and resolve the emulator-reflow unknown that the rest of the project depends on.

**Architecture:** Each pane is a PTY-backed child process whose bytes feed a `charmbracelet/x/vt` emulator. Each emulator draws itself into an off-screen `uv.ScreenBuffer`; those surfaces composite into one screen buffer that Ultraviolet diffs and flushes. Process teardown funnels through a single reaper so no exit path leaks children.

**Tech Stack:** Go 1.27.1 · `charmbracelet/x/vt` (emulation) · `charmbracelet/ultraviolet` (cell buffers, diffing renderer, input decode) · `creack/pty` (PTYs)

**Spec:** `docs/dev-sessions/2026-09-18-wideboi-v1-foundations/spec.md`

**Covers:** Spec milestones 1–4. Plans 2–4 (layout + seam, animation, agent features) are written after this one lands, because `x/vt` and `ultraviolet` are pre-1.0 and their real shape should inform those plans.

## Global Constraints

- Module path: `github.com/lmorchard/wideboi`. Go 1.27.1.
- Platforms: macOS and Linux only. No Windows, no build tags for it.
- `github.com/charmbracelet/x/vt` is pinned to `v0.0.0-20260913004009-c615ff2f7805`. It has no tagged release; do not use `@latest`.
- Geometry uses stdlib `image.Rectangle` / `image.Point`. `uv.Rectangle` and `uv.Position` are type aliases for these, so they interchange with no conversion. Never import `ultraviolet` solely for geometry.
- `internal/layout` (added in Plan 2) must depend only on the standard library. Do not create it in this plan.
- Every exit path must restore the host terminal and reap child processes. "Every" includes panic, signal, and early return.
- All work is committed. Each task ends with a commit.

### Verified API facts

These were established by probing the real packages. Trust them over intuition:

- `*uv.Buffer` does **not** satisfy `uv.Screen` — it lacks `WidthMethod()`. Use `uv.NewScreenBuffer(w, h)`, which returns a `uv.ScreenBuffer` that does satisfy it.
- `uv.ScreenBuffer` embeds `*uv.RenderBuffer`, not `*uv.Buffer`.
- `vt.SafeEmulator` satisfies `io.Writer` — feed it PTY bytes directly.
- `emulator.Draw(dst uv.Screen, area uv.Rectangle)` and `surface.Draw(dst uv.Screen, area uv.Rectangle)` both clip to `area`.
- `uv.DefaultTerminal()` handles raw mode, alt screen, and input decoding. `t.Start()`, `t.Stop()`, `t.Events() <-chan Event`, `t.Screen() *TerminalScreen`.
- `uv.WidthMethod` is an interface with one method, `StringWidth(string) int`.
- `uv.KeyPressEvent.String()` returns a **human-readable name** (`"ctrl+q"`), not bytes. Never forward it to a PTY. `Key.Text` holds printable characters only, so it cannot encode control keys or arrows either.
- The key encode path is `emulator.SendKey(uv.KeyEvent)`, with the encoded bytes then read back off `emulator.Read()`. Verified encodings: `a`→`"a"`, `ctrl+c`→`\x03`, `enter`→`\r`, `up`→`\x1b[A`, `tab`→`\t`, `backspace`→`\x7f`, `alt+l`→`\x1bl`.
- **`SendKey` writes to an `io.Pipe` and blocks until something reads.** Every pane therefore needs *two* goroutines: one copying PTY output into the emulator, and one copying the emulator's output (encoded keys plus terminal replies) into the PTY. Calling `SendKey` without a live drain deadlocks the process.

---

### Task 1: Module skeleton, Makefile, and an idempotent shutdown guard

The host terminal must be restored exactly once no matter how the program exits. Getting this wrong strands the user in a black alt-screen rectangle, and it is much harder to retrofit than to start with.

**Files:**
- Create: `go.mod`, `Makefile`, `internal/hostterm/guard.go`
- Test: `internal/hostterm/guard_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `hostterm.NewGuard(stop func() error) *Guard`; `(*Guard).Stop() error` (idempotent, safe for concurrent use); `(*Guard).Arm(sigs ...os.Signal)`.

- [ ] **Step 1: Initialise the module and pin dependencies**

```bash
cd /Users/lorchard/devel/wideboi
go mod init github.com/lmorchard/wideboi
go get github.com/charmbracelet/x/vt@v0.0.0-20260913004009-c615ff2f7805
go get github.com/charmbracelet/ultraviolet@latest
go get github.com/creack/pty@latest
go mod tidy
```

- [ ] **Step 2: Write the Makefile**

```makefile
.PHONY: check test lint fmt build run tidy

check: fmt lint test

test:
	go test ./...

lint:
	go vet ./...

fmt:
	gofmt -l -w .

build:
	go build -o bin/wideboi ./cmd/wideboi

run: build
	./bin/wideboi

tidy:
	go mod tidy
```

- [ ] **Step 3: Write the failing test**

Create `internal/hostterm/guard_test.go`:

```go
package hostterm

import (
	"errors"
	"sync"
	"testing"
)

var errSentinel = errors.New("sentinel")

func TestGuardStopsExactlyOnce(t *testing.T) {
	var calls int
	g := NewGuard(func() error { calls++; return nil })

	for i := 0; i < 3; i++ {
		if err := g.Stop(); err != nil {
			t.Fatalf("Stop() returned %v", err)
		}
	}

	if calls != 1 {
		t.Fatalf("stop func called %d times, want 1", calls)
	}
}

func TestGuardStopIsConcurrencySafe(t *testing.T) {
	var mu sync.Mutex
	var calls int
	g := NewGuard(func() error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = g.Stop() }()
	}
	wg.Wait()

	if calls != 1 {
		t.Fatalf("stop func called %d times, want 1", calls)
	}
}

func TestGuardReturnsUnderlyingError(t *testing.T) {
	want := errSentinel
	g := NewGuard(func() error { return want })
	if got := g.Stop(); got != want {
		t.Fatalf("Stop() = %v, want %v", got, want)
	}
	if got := g.Stop(); got != nil {
		t.Fatalf("second Stop() = %v, want nil", got)
	}
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./internal/hostterm/ -run TestGuard -v`
Expected: FAIL — `undefined: NewGuard`

- [ ] **Step 5: Write the minimal implementation**

Create `internal/hostterm/guard.go`:

```go
// Package hostterm owns the lifecycle of the terminal wideboi is running
// inside. Its one job is that the terminal is restored exactly once, on
// every exit path.
package hostterm

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// Guard runs a shutdown function at most once, from whichever exit path
// reaches it first: an ordinary return, a panic, or a signal.
type Guard struct {
	once sync.Once
	stop func() error
	err  error
}

// NewGuard returns a Guard that will call stop at most once.
func NewGuard(stop func() error) *Guard {
	return &Guard{stop: stop}
}

// Stop runs the shutdown function if it has not run already. The first
// caller receives the shutdown function's error; later callers get nil.
func (g *Guard) Stop() error {
	var ran bool
	g.once.Do(func() {
		ran = true
		g.err = g.stop()
	})
	if ran {
		return g.err
	}
	return nil
}
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./internal/hostterm/ -run TestGuard -v`
Expected: PASS, all three tests

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum Makefile internal/hostterm/
git commit -m "feat(hostterm): idempotent shutdown guard

The host terminal must be restored exactly once regardless of which exit
path runs first. Guard serialises that behind sync.Once."
```

---

### Task 2: Signal path restores, then re-raises

A multiplexer that swallows SIGTERM reports the wrong exit status to its parent. The handler must restore, then re-raise with the default disposition so the caller observes `128+signo`.

Unlike gwae, whose signal handler may not allocate or lock, Go delivers signals on an ordinary goroutine — so this handler can do real work.

**Files:**
- Modify: `internal/hostterm/guard.go`
- Test: `internal/hostterm/signal_test.go`

**Interfaces:**
- Consumes: `hostterm.NewGuard` from Task 1.
- Produces: `(*Guard).Arm(sigs ...os.Signal)` — starts a goroutine that calls `Stop()` on any listed signal, then re-raises it with the default handler.

- [ ] **Step 1: Write the failing test**

This re-executes the test binary as a child so a real signal can be delivered. Create `internal/hostterm/signal_test.go`:

```go
package hostterm_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/hostterm"
)

// When run with WIDEBOI_SIGNAL_CHILD=1 this test body becomes the child
// process: it arms a guard, writes a marker on shutdown, and waits to be
// signalled.
func TestGuardArmRestoresAndReRaises(t *testing.T) {
	if os.Getenv("WIDEBOI_SIGNAL_CHILD") == "1" {
		marker := os.Getenv("WIDEBOI_MARKER")
		g := hostterm.NewGuard(func() error {
			return os.WriteFile(marker, []byte("restored"), 0o644)
		})
		g.Arm(syscall.SIGTERM)
		// Signal the parent that the handler is installed.
		if err := os.WriteFile(os.Getenv("WIDEBOI_READY"), []byte("ok"), 0o644); err != nil {
			t.Fatalf("write ready file: %v", err)
		}
		time.Sleep(10 * time.Second) // killed well before this
		return
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	ready := filepath.Join(dir, "ready")

	cmd := exec.Command(os.Args[0], "-test.run=TestGuardArmRestoresAndReRaises")
	cmd.Env = append(os.Environ(),
		"WIDEBOI_SIGNAL_CHILD=1",
		"WIDEBOI_MARKER="+marker,
		"WIDEBOI_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}

	waitForFile(t, ready, 5*time.Second)

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal child: %v", err)
	}
	err := cmd.Wait()

	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("shutdown func did not run: %v", statErr)
	}

	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("child exited with %v, want ExitError", err)
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("no WaitStatus available")
	}
	if !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Fatalf("child status = %v, want death by SIGTERM", ws)
	}
}

func waitForFile(t *testing.T, path string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/hostterm/ -run TestGuardArm -v`
Expected: FAIL — `g.Arm undefined (type *hostterm.Guard has no field or method Arm)`

- [ ] **Step 3: Write the minimal implementation**

Append to `internal/hostterm/guard.go`:

```go
// Arm installs a handler for the given signals. On receipt it runs the
// shutdown function, then re-raises the signal with the default handler
// so the parent process observes the conventional 128+signo status.
//
// Go delivers signals on an ordinary goroutine, so this handler may
// allocate, take locks, and run arbitrary code.
func (g *Guard) Arm(sigs ...os.Signal) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)

	go func() {
		s := <-ch
		_ = g.Stop()

		signal.Stop(ch)
		signal.Reset(s)
		if sig, ok := s.(syscall.Signal); ok {
			_ = syscall.Kill(os.Getpid(), sig)
		}
	}()
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/hostterm/ -run TestGuardArm -v`
Expected: PASS

- [ ] **Step 5: Run the whole package and commit**

```bash
go test ./internal/hostterm/ -v
git add internal/hostterm/
git commit -m "feat(hostterm): restore then re-raise on signal

Swallowing SIGTERM would report the wrong exit status to the parent.
Restore first, then re-raise with the default disposition."
```

---

### Task 3: First running program — raw mode key echo

Milestone 1 complete: a program that takes over the terminal, decodes keys, and always gives the terminal back.

**Files:**
- Create: `cmd/wideboi/main.go`
- Test: manual (documented below) — this task's value is the exit paths, which Tasks 1 and 2 already cover automatically.

**Interfaces:**
- Consumes: `hostterm.NewGuard`, `(*Guard).Arm`, `(*Guard).Stop`.
- Produces: a `wideboi` binary. No exported Go API.

- [ ] **Step 1: Write the program**

Create `cmd/wideboi/main.go`:

```go
// Command wideboi is a scrolling tiling terminal multiplexer for CLI agents.
package main

import (
	"fmt"
	"log"
	"os"
	"syscall"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/hostterm"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	t := uv.DefaultTerminal()
	scr := t.Screen()
	scr.EnterAltScreen()

	if err := t.Start(); err != nil {
		return fmt.Errorf("start terminal: %w", err)
	}

	guard := hostterm.NewGuard(func() error {
		scr.ExitAltScreen()
		_ = scr.Flush()
		return t.Stop()
	})
	guard.Arm(syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT)
	defer guard.Stop()

	defer func() {
		if r := recover(); r != nil {
			_ = guard.Stop()
			panic(r)
		}
	}()

	for ev := range t.Events() {
		switch ev := ev.(type) {
		case uv.WindowSizeEvent:
			scr.Resize(ev.Width, ev.Height)
		case uv.KeyPressEvent:
			if ev.MatchString("ctrl+q") {
				return nil
			}
			fmt.Fprintf(os.Stderr, "key: %s\r\n", ev.String())
		}
	}
	return nil
}
```

- [ ] **Step 2: Build and verify it compiles**

Run: `make build`
Expected: `bin/wideboi` produced, no errors

- [ ] **Step 3: Verify the happy path by hand**

Run: `./bin/wideboi`
Expected: alt screen entered. Press a few keys. Press `ctrl+q`.
Expected on exit: your shell prompt is back, the terminal echoes normally, and `stty -a` shows a sane cooked mode.

- [ ] **Step 4: Verify the signal path by hand**

In one terminal run `./bin/wideboi`. In another:

```bash
kill -TERM $(pgrep -n wideboi)
```

Expected: the first terminal returns to a working prompt, not a black rectangle. Then confirm the status:

```bash
./bin/wideboi & sleep 1; kill -TERM %1; wait %1; echo "status=$?"
```

Expected: `status=143` (128 + 15).

- [ ] **Step 5: Commit**

```bash
git add cmd/
git commit -m "feat(cmd): raw-mode key echo with guaranteed terminal restore

Milestone 1. uv.DefaultTerminal handles raw mode and input decoding;
hostterm.Guard handles giving the terminal back."
```

---

### Task 4: Spawn a child on a PTY

**Files:**
- Create: `internal/server/ptyx/pane.go`
- Test: `internal/server/ptyx/pane_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Pane struct { Master *os.File; Cmd *exec.Cmd; PGID int }`
  - `ptyx.Spawn(argv []string, cols, rows int, dir string) (*Pane, error)`
  - `(*Pane).Resize(cols, rows int) error`
  - `(*Pane).Close() error` — closes the master only; process teardown arrives in Task 5.

- [ ] **Step 1: Write the failing test**

Create `internal/server/ptyx/pane_test.go`:

```go
package ptyx_test

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/server/ptyx"
)

func TestSpawnRunsCommandAndEchoesOutput(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	if _, err := io.WriteString(p.Master, "echo wideboi-ok\n"); err != nil {
		t.Fatalf("write to pty: %v", err)
	}

	if !readUntil(t, p.Master, "wideboi-ok", 5*time.Second) {
		t.Fatal("never saw command output on the pty")
	}
}

func TestSpawnReportsWindowSizeToChild(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 73, 11, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	if _, err := io.WriteString(p.Master, "stty size\n"); err != nil {
		t.Fatalf("write to pty: %v", err)
	}

	// stty size prints "rows cols".
	if !readUntil(t, p.Master, "11 73", 5*time.Second) {
		t.Fatal("child did not see the requested window size")
	}
}

func TestSpawnPutsChildInItsOwnProcessGroup(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	if p.PGID == 0 {
		t.Fatal("PGID is zero")
	}
	if p.PGID != p.Cmd.Process.Pid {
		t.Fatalf("PGID = %d, want it to equal child pid %d", p.PGID, p.Cmd.Process.Pid)
	}
}

func readUntil(t *testing.T, r io.Reader, want string, within time.Duration) bool {
	t.Helper()
	found := make(chan bool, 1)

	go func() {
		var acc bytes.Buffer
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				if bytes.Contains(acc.Bytes(), []byte(want)) {
					found <- true
					return
				}
			}
			if err != nil {
				found <- false
				return
			}
		}
	}()

	select {
	case ok := <-found:
		return ok
	case <-time.After(within):
		return false
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/server/ptyx/ -v`
Expected: FAIL — package `ptyx` does not exist

- [ ] **Step 3: Write the minimal implementation**

Create `internal/server/ptyx/pane.go`:

```go
// Package ptyx spawns and tears down PTY-backed child processes.
//
// A pane's child is given its own session and process group. That is what
// makes signal delivery and teardown addressable per pane rather than
// per multiplexer.
package ptyx

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// Pane is one PTY and the process tree rooted at its child.
type Pane struct {
	Master *os.File
	Cmd    *exec.Cmd
	PGID   int

	// done closes when the child has been reaped. Exactly one goroutine
	// ever calls Wait, because a second call fails.
	done chan struct{}
}

// Done returns a channel closed when the pane's child process exits.
func (p *Pane) Done() <-chan struct{} { return p.done }

// Spawn starts argv on a new PTY sized cols x rows, with dir as its
// working directory.
func Spawn(argv []string, cols, rows int, dir string) (*Pane, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("ptyx: empty argv")
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
	}

	master, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
	if err != nil {
		return nil, fmt.Errorf("ptyx: start %s: %w", argv[0], err)
	}

	// Setsid makes the child a session and group leader, so its pgid is
	// its own pid.
	p := &Pane{
		Master: master,
		Cmd:    cmd,
		PGID:   cmd.Process.Pid,
		done:   make(chan struct{}),
	}

	// Reap in exactly one place. Calling Wait twice returns an error, so
	// Kill must observe exit through this channel rather than waiting
	// again itself.
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()

	return p, nil
}

// Resize reports a new logical size to the child.
func (p *Pane) Resize(cols, rows int) error {
	return pty.Setsize(p.Master, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
}

// Close releases the PTY master. It does not stop the child; see Kill.
func (p *Pane) Close() error {
	return p.Master.Close()
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/server/ptyx/ -v`
Expected: PASS, all three tests

- [ ] **Step 5: Commit**

```bash
git add internal/server/ptyx/
git commit -m "feat(ptyx): spawn a child on a PTY in its own session

Setsid gives each pane an addressable process group, which is what
makes per-pane teardown possible."
```

---

### Task 5: Kill the whole process tree

Three kills per pane, because each catches processes the others miss. A multiplexer that leaks background processes is worse than useless: the work keeps burning CPU with no window left to find it in.

**Files:**
- Modify: `internal/server/ptyx/pane.go`
- Create: `internal/server/ptyx/reap.go`
- Test: `internal/server/ptyx/reap_test.go`

**Interfaces:**
- Consumes: `ptyx.Pane` from Task 4.
- Produces: `(*Pane).Kill(grace time.Duration) error`; `ptyx.Descendants(pid int) ([]int, error)`.

- [ ] **Step 1: Write the failing test**

The interesting case is a grandchild that deliberately left the process group. A group kill alone misses it.

Create `internal/server/ptyx/reap_test.go`:

```go
package ptyx_test

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/server/ptyx"
)

func TestKillReapsEscapedGrandchild(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// A sleeper that escapes the pane's process group entirely. A plain
	// killpg would not reach it.
	tag := fmt.Sprintf("wideboi-escapee-%d", time.Now().UnixNano())
	cmd := fmt.Sprintf("sh -c 'exec -a %s sleep 300' &\n", tag)
	if _, err := io.WriteString(p.Master, cmd); err != nil {
		t.Fatalf("write to pty: %v", err)
	}

	if !waitForProcess(t, tag, true, 5*time.Second) {
		t.Fatal("escapee never started")
	}

	if err := p.Kill(2 * time.Second); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	if !waitForProcess(t, tag, false, 5*time.Second) {
		t.Fatal("escapee survived Kill — the pane leaked a process")
	}
}

func TestKillIsIdempotent(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := p.Kill(2 * time.Second); err != nil {
		t.Fatalf("first Kill: %v", err)
	}
	if err := p.Kill(2 * time.Second); err != nil {
		t.Fatalf("second Kill: %v", err)
	}
}

// waitForProcess polls the real process table until a process matching
// tag is present (want=true) or absent (want=false).
func waitForProcess(t *testing.T, tag string, want bool, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if processExists(tag) == want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func processExists(tag string) bool {
	out, err := exec.Command("ps", "-axo", "command").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, tag) && !strings.Contains(line, "ps -axo") {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/server/ptyx/ -run TestKill -v`
Expected: FAIL — `p.Kill undefined`

- [ ] **Step 3: Write the minimal implementation**

Create `internal/server/ptyx/reap.go`:

```go
package ptyx

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Descendants returns every process descended from pid, deepest first.
//
// This exists because neither a process-group kill nor a kill of the root
// pid reaches a process that deliberately left the group, via nohup,
// setsid, or an interactive shell's job control.
func Descendants(pid int) ([]int, error) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		return nil, err
	}

	children := map[int][]int{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		p, err1 := strconv.Atoi(fields[0])
		pp, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		children[pp] = append(children[pp], p)
	}

	var walk func(int) []int
	walk = func(root int) []int {
		var out []int
		for _, c := range children[root] {
			out = append(out, walk(c)...)
			out = append(out, c)
		}
		return out
	}
	return walk(pid), nil // deepest first
}

// Kill stops the pane's entire process tree. It sends SIGTERM by the
// three routes below, waits up to grace for the root to exit, then
// escalates to SIGKILL on anything left.
//
//  1. killpg on the pane's group  — the shell and its foreground job
//  2. kill on the root pid        — the shell itself
//  3. a ps tree walk, deepest first — anything that left the group
//
// Kill is safe to call more than once.
func (p *Pane) Kill(grace time.Duration) error {
	p.signalTree(syscall.SIGTERM)

	if p.waitForExit(grace) {
		_ = p.Master.Close()
		return nil
	}

	p.signalTree(syscall.SIGKILL)
	p.waitForExit(time.Second)
	_ = p.Master.Close()
	return nil
}

func (p *Pane) signalTree(sig syscall.Signal) {
	// Deepest-first, so a parent cannot respawn a child we already killed.
	if kids, err := Descendants(p.Cmd.Process.Pid); err == nil {
		for _, pid := range kids {
			_ = syscall.Kill(pid, sig)
		}
	}
	if p.PGID > 0 {
		_ = syscall.Kill(-p.PGID, sig)
	}
	_ = syscall.Kill(p.Cmd.Process.Pid, sig)
}

// waitForExit observes the single reaper goroutine started in Spawn. It
// never calls Wait itself, so it is safe to call repeatedly.
func (p *Pane) waitForExit(within time.Duration) bool {
	select {
	case <-p.done:
		return true
	case <-time.After(within):
		return false
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/server/ptyx/ -v`
Expected: PASS, all five tests. `TestKillReapsEscapedGrandchild` is the one that matters.

- [ ] **Step 5: Commit**

```bash
git add internal/server/ptyx/
git commit -m "feat(ptyx): kill the whole pane process tree

Three kills per pane. A killpg alone misses anything that left the group
via setsid or job control, which is exactly what leaks."
```

---

### Task 6: Second running program — single-pane passthrough

Milestone 2. A terminal inside a terminal, with no emulation yet: bytes in, bytes out.

**Files:**
- Modify: `cmd/wideboi/main.go`

**Interfaces:**
- Consumes: `ptyx.Spawn`, `(*Pane).Kill`, `hostterm.NewGuard`.
- Produces: nothing new.

- [ ] **Step 1: Rewrite main to pipe a shell through**

Replace the body of `run()` in `cmd/wideboi/main.go`:

```go
func run() error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}

	cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		cols, rows = 80, 24
	}

	pane, err := ptyx.Spawn([]string{shell}, cols, rows, cwd)
	if err != nil {
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
		return fmt.Errorf("spawn: %w", err)
	}

	guard := hostterm.NewGuard(func() error {
		_ = pane.Kill(2 * time.Second)
		return term.Restore(int(os.Stdin.Fd()), oldState)
	})
	guard.Arm(syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer guard.Stop()

	go func() { _, _ = io.Copy(pane.Master, os.Stdin) }()
	_, _ = io.Copy(os.Stdout, pane.Master) // returns when the child exits
	return nil
}
```

Update the imports:

```go
import (
	"fmt"
	"io"
	"log"
	"os"
	"syscall"
	"time"

	"github.com/lmorchard/wideboi/internal/hostterm"
	"github.com/lmorchard/wideboi/internal/server/ptyx"
	"golang.org/x/term"
)
```

Then: `go get golang.org/x/term && go mod tidy`

- [ ] **Step 2: Build**

Run: `make build`
Expected: no errors

- [ ] **Step 3: Verify by hand**

Run: `./bin/wideboi`
Expected: a working shell prompt. Run `ls`, `vim`, `top` — full-screen programs should work, since this is a straight byte pipe. Type `exit`.
Expected on exit: your original shell is back, terminal is sane.

- [ ] **Step 4: Verify nothing leaks**

```bash
./bin/wideboi
# inside it:  sh -c 'exec -a wideboi-leak-probe sleep 300' &
# then:       exit
ps -axo command | grep -c wideboi-leak-probe
```

Expected: `0` — the background job died with the pane.

- [ ] **Step 5: Commit**

```bash
git add cmd/ go.mod go.sum
git commit -m "feat(cmd): single-pane PTY passthrough

Milestone 2. A terminal inside a terminal, no emulation yet."
```

---

### Task 7: Compositing surfaces

The rendering core. A pane draws into an off-screen surface; surfaces composite into one screen buffer.

**Files:**
- Create: `internal/client/compose/surface.go`
- Test: `internal/client/compose/surface_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Surface = uv.ScreenBuffer`
  - `compose.NewSurface(cols, rows int) Surface`
  - `compose.Blit(dst uv.Screen, src Surface, dest image.Rectangle)` — draws src into dst at dest, clipping to dest.
  - `compose.Text(s uv.Screen, area image.Rectangle) []string` — test helper, renders a screen region to plain strings.

- [ ] **Step 1: Write the failing test**

Create `internal/client/compose/surface_test.go`:

```go
package compose_test

import (
	"image"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/client/compose"
)

func TestBlitPlacesSurfaceAtOffset(t *testing.T) {
	src := compose.NewSurface(5, 1)
	compose.WriteString(src, 0, 0, "hello")

	dst := compose.NewSurface(12, 3)
	compose.Blit(dst, src, image.Rect(3, 1, 8, 2))

	got := compose.Text(dst, dst.Bounds())
	want := []string{
		"            ",
		"   hello    ",
		"            ",
	}
	assertLines(t, got, want)
}

func TestBlitClipsToDestination(t *testing.T) {
	src := compose.NewSurface(10, 1)
	compose.WriteString(src, 0, 0, "abcdefghij")

	dst := compose.NewSurface(10, 1)
	// Only four columns of room.
	compose.Blit(dst, src, image.Rect(0, 0, 4, 1))

	got := compose.Text(dst, dst.Bounds())
	want := []string{"abcd      "}
	assertLines(t, got, want)
}

func TestBlitLaterDrawsCoverEarlierOnes(t *testing.T) {
	under := compose.NewSurface(6, 1)
	compose.WriteString(under, 0, 0, "UUUUUU")
	over := compose.NewSurface(3, 1)
	compose.WriteString(over, 0, 0, "OOO")

	dst := compose.NewSurface(8, 1)
	compose.Blit(dst, under, image.Rect(0, 0, 6, 1))
	compose.Blit(dst, over, image.Rect(2, 0, 5, 1))

	got := compose.Text(dst, dst.Bounds())
	want := []string{"UUOOO   "}
	assertLines(t, got, want)
}

func assertLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d\ngot:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n  got  %q\n  want %q", i, got[i], want[i])
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/client/compose/ -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: Write the minimal implementation**

Create `internal/client/compose/surface.go`:

```go
// Package compose turns per-pane cell surfaces into one screen buffer.
//
// Compositing is painter's algorithm: callers Blit back to front, and
// later draws cover earlier ones. That is what will let overlapping
// card layouts work later without changing this package.
package compose

import (
	"image"

	uv "github.com/charmbracelet/ultraviolet"
)

// Surface is an off-screen cell buffer that satisfies uv.Screen.
//
// Note: *uv.Buffer does NOT satisfy uv.Screen — it lacks WidthMethod().
// uv.ScreenBuffer is the type that does.
type Surface = uv.ScreenBuffer

// NewSurface returns a blank surface of the given size.
func NewSurface(cols, rows int) Surface {
	return uv.NewScreenBuffer(cols, rows)
}

// Blit draws src into dst at dest, clipping anything outside dest.
func Blit(dst uv.Screen, src Surface, dest image.Rectangle) {
	src.Draw(dst, dest)
}

// WriteString writes plain unstyled text into s starting at (x, y).
func WriteString(s uv.Screen, x, y int, text string) {
	for i, r := range []rune(text) {
		s.SetCell(x+i, y, uv.NewCell(s.WidthMethod(), string(r)))
	}
}

// Text renders a screen region to plain strings, for tests and snapshots.
func Text(s uv.Screen, area image.Rectangle) []string {
	lines := make([]string, 0, area.Dy())
	for y := area.Min.Y; y < area.Max.Y; y++ {
		row := make([]rune, 0, area.Dx())
		for x := area.Min.X; x < area.Max.X; x++ {
			c := s.CellAt(x, y)
			if c == nil || c.Content == "" {
				row = append(row, ' ')
				continue
			}
			row = append(row, []rune(c.Content)[0])
		}
		lines = append(lines, string(row))
	}
	return lines
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/client/compose/ -v`
Expected: PASS, all three tests

- [ ] **Step 5: Commit**

```bash
git add internal/client/compose/
git commit -m "feat(compose): off-screen surfaces and painter's-algorithm blit

uv.ScreenBuffer rather than uv.Buffer: only the former satisfies
uv.Screen, since Buffer lacks WidthMethod()."
```

---

### Task 8: The emulator behind an interface

`x/vt` has no tagged release. The interface is the insulation, and `x/vt` already exports a `Terminal` interface of its own — but ours is deliberately narrower, covering only what wideboi uses.

**Files:**
- Create: `internal/server/term/grid.go`
- Test: `internal/server/term/grid_test.go`

**Interfaces:**
- Consumes: `compose.Surface`, `compose.Text` (test only).
- Produces:
  - `type Grid interface { io.Writer; io.Reader; SendKey(k uv.KeyEvent); Resize(cols, rows int); Draw(dst uv.Screen, area image.Rectangle); Size() (cols, rows int) }`
  - `term.NewVT(cols, rows int) Grid`

`Write` takes PTY output in. `Read` gives encoded keystrokes and terminal replies out, destined for the PTY. Both directions go through the emulator so it stays the single authority on pane state.

- [ ] **Step 1: Write the failing test**

Create `internal/server/term/grid_test.go`:

```go
package term_test

import (
	"io"
	"testing"

	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/server/term"
)

func TestGridRendersWrittenBytes(t *testing.T) {
	g := term.NewVT(10, 2)
	if _, err := io.WriteString(g, "hello\r\nworld"); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := compose.NewSurface(10, 2)
	g.Draw(s, s.Bounds())

	got := compose.Text(s, s.Bounds())
	want := []string{"hello     ", "world     "}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n  got  %q\n  want %q", i, got[i], want[i])
		}
	}
}

func TestGridReportsItsSize(t *testing.T) {
	g := term.NewVT(37, 9)
	cols, rows := g.Size()
	if cols != 37 || rows != 9 {
		t.Fatalf("Size() = %d,%d want 37,9", cols, rows)
	}
}

func TestGridHonoursResize(t *testing.T) {
	g := term.NewVT(10, 2)
	g.Resize(20, 4)
	cols, rows := g.Size()
	if cols != 20 || rows != 4 {
		t.Fatalf("Size() after resize = %d,%d want 20,4", cols, rows)
	}
}

func TestGridAppliesSGRWithoutPrintingIt(t *testing.T) {
	g := term.NewVT(10, 1)
	// Bold "hi" — the escape sequence must not appear as text.
	io.WriteString(g, "\x1b[1mhi\x1b[0m")

	s := compose.NewSurface(10, 1)
	g.Draw(s, s.Bounds())

	if got := compose.Text(s, s.Bounds())[0]; got != "hi        " {
		t.Fatalf("got %q, want %q", got, "hi        ")
	}
}

// SendKey encodes a decoded key event back into the bytes a child
// process expects. Forwarding KeyPressEvent.String() would send the
// literal text "ctrl+c" instead of \x03.
//
// SendKey writes to an io.Pipe and blocks until something reads, so the
// drain goroutine below is mandatory, not incidental.
func TestGridEncodesKeysForTheChild(t *testing.T) {
	cases := []struct {
		name string
		key  uv.KeyPressEvent
		want string
	}{
		{"printable", uv.KeyPressEvent{Code: 'a', Text: "a"}, "a"},
		{"ctrl+c", uv.KeyPressEvent{Code: 'c', Mod: uv.ModCtrl}, "\x03"},
		{"enter", uv.KeyPressEvent{Code: uv.KeyEnter}, "\r"},
		{"up arrow", uv.KeyPressEvent{Code: uv.KeyUp}, "\x1b[A"},
		{"tab", uv.KeyPressEvent{Code: uv.KeyTab}, "\t"},
		{"backspace", uv.KeyPressEvent{Code: uv.KeyBackspace}, "\x7f"},
		{"alt+l", uv.KeyPressEvent{Code: 'l', Text: "l", Mod: uv.ModAlt}, "\x1bl"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := term.NewVT(20, 3)

			out := make(chan string, 1)
			go func() {
				buf := make([]byte, 64)
				n, err := g.Read(buf)
				if n > 0 {
					out <- string(buf[:n])
					return
				}
				if err != nil {
					out <- ""
				}
			}()

			g.SendKey(uv.KeyEvent(tc.key))

			select {
			case got := <-out:
				if got != tc.want {
					t.Errorf("SendKey(%s) = %q, want %q", tc.name, got, tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("SendKey(%s) produced no bytes — is the drain running?", tc.name)
			}
		})
	}
}
```

Add `uv "github.com/charmbracelet/ultraviolet"` and `"time"` to the test file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/server/term/ -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: Write the minimal implementation**

Create `internal/server/term/grid.go`:

```go
// Package term wraps the VT emulator behind an interface wideboi owns.
//
// charmbracelet/x/vt has no tagged release. This interface is the seam
// that keeps an upstream break confined to one file, and the reason a
// different emulator could be substituted without touching callers.
package term

import (
	"image"
	"io"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// Grid is one pane's terminal state.
//
// Write takes PTY output in. Read gives out encoded keystrokes and
// terminal replies, destined for the PTY. Routing input through the
// emulator keeps it the single authority on pane state, and is the only
// way to turn a decoded uv.KeyEvent back into the bytes a child expects.
type Grid interface {
	io.Writer
	io.Reader
	SendKey(k uv.KeyEvent)
	Resize(cols, rows int)
	Draw(dst uv.Screen, area image.Rectangle)
	Size() (cols, rows int)
}

// vtGrid adapts x/vt's SafeEmulator to Grid. SafeEmulator rather than
// Emulator because a pane's PTY reader goroutine writes to it while the
// compositor reads from it.
type vtGrid struct {
	em *vt.SafeEmulator
}

// NewVT returns a Grid backed by charmbracelet/x/vt.
func NewVT(cols, rows int) Grid {
	return &vtGrid{em: vt.NewSafeEmulator(cols, rows)}
}

func (g *vtGrid) Write(p []byte) (int, error) { return g.em.Write(p) }
func (g *vtGrid) Read(p []byte) (int, error)  { return g.em.Read(p) }
func (g *vtGrid) SendKey(k uv.KeyEvent)       { g.em.SendKey(k) }
func (g *vtGrid) Resize(cols, rows int)       { g.em.Resize(cols, rows) }
func (g *vtGrid) Size() (int, int)            { return g.em.Width(), g.em.Height() }

func (g *vtGrid) Draw(dst uv.Screen, area image.Rectangle) {
	g.em.Draw(dst, area)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/server/term/ -v`
Expected: PASS — four behaviour tests plus seven `TestGridEncodesKeysForTheChild` subtests

- [ ] **Step 5: Commit**

```bash
git add internal/server/term/
git commit -m "feat(term): VT emulator behind a narrow owned interface

x/vt has no tagged release, so confine it to one file."
```

---

### Task 9: Resolve the reflow unknown

The spec's one load-bearing open question. gwae replaced its first emulator over exactly this: narrowing truncated cell tails, so widening could not recover the text. Find out now, while the answer is still cheap to act on.

**Files:**
- Test: `internal/server/term/reflow_test.go`
- Modify: `docs/dev-sessions/2026-09-18-wideboi-v1-foundations/spec.md` (record the answer)

**Interfaces:**
- Consumes: `term.NewVT`, `compose.NewSurface`, `compose.Text`.
- Produces: no code. Produces an answer.

- [ ] **Step 1: Write the probe test**

Create `internal/server/term/reflow_test.go`:

```go
package term_test

import (
	"io"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/server/term"
)

// A line written at width 20, narrowed to 10, then widened back to 20
// should be recoverable. If the emulator truncates instead of reflowing,
// the tail is gone forever and column-width cycling silently eats text.
func TestReflowRecoversTextAfterNarrowAndWiden(t *testing.T) {
	const line = "abcdefghijklmnopqrst" // exactly 20 columns

	g := term.NewVT(20, 4)
	io.WriteString(g, line)

	g.Resize(10, 4)
	g.Resize(20, 4)

	s := compose.NewSurface(20, 4)
	g.Draw(s, s.Bounds())
	got := strings.Join(compose.Text(s, s.Bounds()), "")

	if !strings.Contains(got, line) {
		t.Errorf("text not recovered after narrow+widen.\n  want to find: %q\n  got screen:   %q", line, got)
	}
}

// Narrowing should wrap the tail onto the next row rather than dropping it.
func TestNarrowWrapsRatherThanTruncates(t *testing.T) {
	const line = "abcdefghijklmnopqrst"

	g := term.NewVT(20, 4)
	io.WriteString(g, line)
	g.Resize(10, 4)

	s := compose.NewSurface(10, 4)
	g.Draw(s, s.Bounds())
	got := strings.Join(compose.Text(s, s.Bounds()), "")

	if !strings.Contains(got, "klmnopqrst") {
		t.Errorf("tail lost on narrow.\n  want to find: %q\n  got screen:   %q", "klmnopqrst", got)
	}
}
```

- [ ] **Step 2: Run the probe and record what actually happens**

Run: `go test ./internal/server/term/ -run 'TestReflow|TestNarrow' -v`

Do **not** assume the outcome. Record it:

- **Both pass** — `x/vt` reflows. Note it in the spec and continue. No further work.
- **Either fails** — `x/vt` truncates. Do not try to fix `x/vt` in this task. Mark both tests `t.Skip("x/vt does not reflow; see spec Open Questions")` with a comment linking the failing behaviour, record the finding in the spec, and raise it before starting Plan 2. This changes the emulator decision and is worth a conversation, not a workaround.

- [ ] **Step 3: Update the spec with the answer**

In `spec.md`, replace the bullet under **Open questions** that reads "Does `Emulator.Resize` reflow? Resolved at milestone 4." with the actual finding, one of:

```markdown
- `Emulator.Resize` reflows primary-screen content. Verified by
  `internal/server/term/reflow_test.go` (2026-09-18).
```

or

```markdown
- `Emulator.Resize` truncates rather than reflows: narrowing drops the
  tail and widening cannot recover it. Verified by
  `internal/server/term/reflow_test.go` (2026-09-18). This is the same
  defect that forced gwae's ADR-004 emulator swap. Revisit the emulator
  choice before Plan 2.
```

- [ ] **Step 4: Commit**

```bash
git add internal/server/term/ docs/
git commit -m "test(term): resolve the emulator reflow question

The spec's one load-bearing unknown. Answer recorded in the spec."
```

---

### Task 10: Fourth running program — a working two-pane mux

Milestone 4. Two shells side by side, each with its own emulator, composited into one frame, with focus switching. The layout is hardcoded — Plan 2 replaces it with the real scrolling strip.

**Files:**
- Create: `internal/client/pane.go`
- Modify: `cmd/wideboi/main.go`

**Interfaces:**
- Consumes: `ptyx.Spawn`, `(*Pane).Kill`, `term.NewVT`, `compose.NewSurface`, `compose.Blit`, `compose.WriteString`, `hostterm.NewGuard`.
- Produces: `client.Pane` with `Start()`, `Surface() compose.Surface`, `Write([]byte)`, `Close()`.

- [ ] **Step 1: Write the pane wiring**

Create `internal/client/pane.go`:

```go
// Package client owns the host terminal: composition, rendering, input.
package client

import (
	"io"
	"sync/atomic"
	"time"

	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/server/ptyx"
	"github.com/lmorchard/wideboi/internal/server/term"
)

// Pane couples a PTY-backed process to an emulator and a draw surface.
//
// This type is temporary: Plan 2 splits it across the client/server seam,
// with the PTY and emulator server-side and the surface client-side.
type Pane struct {
	pty     *ptyx.Pane
	grid    term.Grid
	surface compose.Surface
	dirty   atomic.Bool
	cols    int
	rows    int
}

// NewPane spawns argv on a PTY sized cols x rows.
func NewPane(argv []string, cols, rows int, dir string) (*Pane, error) {
	p, err := ptyx.Spawn(argv, cols, rows, dir)
	if err != nil {
		return nil, err
	}
	return &Pane{
		pty:     p,
		grid:    term.NewVT(cols, rows),
		surface: compose.NewSurface(cols, rows),
		cols:    cols,
		rows:    rows,
	}, nil
}

// Start begins pumping bytes in both directions. Two goroutines are
// required, not one:
//
//	PTY  -> emulator : the child's output becomes cell state
//	emulator -> PTY  : encoded keystrokes and terminal replies
//
// The second is mandatory because Grid.SendKey writes to an io.Pipe and
// blocks until something reads. Without this drain, the first keypress
// deadlocks the process.
//
// Each goroutine recovers, so a parser panic in one pane cannot take
// the multiplexer down.
func (p *Pane) Start(onExit func()) {
	// PTY output -> emulator.
	go func() {
		defer func() {
			_ = recover() // a broken pane must not kill the multiplexer
			onExit()
		}()
		buf := make([]byte, 4096)
		for {
			n, err := p.pty.Master.Read(buf)
			if n > 0 {
				_, _ = p.grid.Write(buf[:n])
				p.dirty.Store(true)
			}
			if err != nil {
				return
			}
		}
	}()

	// Emulator output (keys, replies) -> PTY.
	go func() {
		defer func() { _ = recover() }()
		buf := make([]byte, 4096)
		for {
			n, err := p.grid.Read(buf)
			if n > 0 {
				if _, werr := p.pty.Master.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
}

// SendKey forwards a decoded key event to the pane's child, encoded as
// the bytes a terminal application expects.
func (p *Pane) SendKey(k uv.KeyEvent) { p.grid.SendKey(k) }

// Surface redraws the pane's cells if anything changed, and returns them.
func (p *Pane) Surface() compose.Surface {
	if p.dirty.Swap(false) {
		p.grid.Draw(p.surface, p.surface.Bounds())
	}
	return p.surface
}

// Write forwards raw bytes to the pane's child process, bypassing the
// emulator. Use this for pasted text, not for keystrokes.
func (p *Pane) Write(b []byte) (int, error) { return p.pty.Master.Write(b) }

// Size reports the pane's logical size.
func (p *Pane) Size() (cols, rows int) { return p.cols, p.rows }

// Close tears down the pane's entire process tree.
func (p *Pane) Close() error { return p.pty.Kill(2 * time.Second) }

var _ io.Writer = (*Pane)(nil)
```

Imports for this file:

```go
import (
	"io"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/server/ptyx"
	"github.com/lmorchard/wideboi/internal/server/term"
)
```

- [ ] **Step 2: Rewrite main as a two-pane loop**

Replace `run()` in `cmd/wideboi/main.go`:

```go
func run() error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cwd, _ := os.Getwd()

	t := uv.DefaultTerminal()
	scr := t.Screen()
	scr.EnterAltScreen()
	if err := t.Start(); err != nil {
		return fmt.Errorf("start terminal: %w", err)
	}

	width, height, err := t.GetSize()
	if err != nil {
		width, height = 80, 24
	}

	// Hardcoded layout: two equal columns with a one-cell divider.
	paneCols := (width - 1) / 2
	paneRows := height - 1 // one row of chrome at the bottom

	var panes []*client.Pane
	for i := 0; i < 2; i++ {
		p, err := client.NewPane([]string{shell}, paneCols, paneRows, cwd)
		if err != nil {
			return fmt.Errorf("pane %d: %w", i, err)
		}
		panes = append(panes, p)
	}

	quit := make(chan struct{})
	var once sync.Once
	for _, p := range panes {
		p.Start(func() { once.Do(func() { close(quit) }) })
	}

	guard := hostterm.NewGuard(func() error {
		for _, p := range panes {
			_ = p.Close()
		}
		scr.ExitAltScreen()
		_ = scr.Flush()
		return t.Stop()
	})
	guard.Arm(syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer guard.Stop()

	focus := 0
	frame := time.NewTicker(16 * time.Millisecond)
	defer frame.Stop()

	for {
		select {
		case <-quit:
			return nil

		case ev := <-t.Events():
			switch ev := ev.(type) {
			case uv.WindowSizeEvent:
				scr.Resize(ev.Width, ev.Height)
			case uv.KeyPressEvent:
				switch {
				case ev.MatchString("ctrl+q"):
					return nil
				case ev.MatchString("ctrl+o"):
					focus = (focus + 1) % len(panes)
				default:
					// SendKey, never Write(ev.String()) — String() is a
					// display name like "ctrl+c", not the byte \x03.
					panes[focus].SendKey(uv.KeyEvent(ev))
				}
			}

		case <-frame.C:
			// Composite: each pane's surface into the screen buffer.
			for i, p := range panes {
				x := i * (paneCols + 1)
				compose.Blit(scr, p.Surface(), image.Rect(x, 0, x+paneCols, paneRows))
			}
			for y := 0; y < paneRows; y++ {
				compose.WriteString(scr, paneCols, y, "│")
			}
			compose.WriteString(scr, 0, height-1,
				fmt.Sprintf(" focus: pane %d   ctrl+o switch   ctrl+q quit ", focus))

			scr.Render()
			_ = scr.Flush()
		}
	}
}
```

Update the imports:

```go
import (
	"fmt"
	"image"
	"log"
	"os"
	"sync"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/hostterm"
)
```

- [ ] **Step 3: Build**

Run: `make build`
Expected: no errors

- [ ] **Step 4: Verify by hand**

Run: `./bin/wideboi`

Expected:
- Two shell panes side by side with a `│` divider and a status line.
- Typing goes to pane 0. `ctrl+o` switches focus; typing now goes to pane 1.
- Run `ls` in each — output renders in the correct pane and does not bleed across the divider.
- Run a full-screen program (`top`) in one pane — it should lay out at the pane's width, not the terminal's.
- Press Enter, Backspace, and the arrow keys. All must behave normally; if any inserts literal text like `up` or `enter`, the `SendKey` path is wrong.
- Run `sleep 60` then press `ctrl+c`. It must interrupt. This is the control-byte encoding working end to end.
- `ctrl+q` quits, terminal restored.

**Known limitation, deliberately not fixed here:** resizing the host window resizes the screen buffer but does not resize the panes or their PTYs, so the layout will be wrong until restart. Plan 2 owns resize, because it needs the real layout core to decide new logical sizes.

- [ ] **Step 5: Verify no leaks across both panes**

```bash
./bin/wideboi
# pane 0:  sh -c 'exec -a wideboi-leak-a sleep 300' &
# ctrl+o
# pane 1:  sh -c 'exec -a wideboi-leak-b sleep 300' &
# ctrl+q
ps -axo command | grep -cE 'wideboi-leak-[ab]'
```

Expected: `0`

- [ ] **Step 6: Run everything and commit**

`golang.org/x/term` was used by Task 6 and is no longer imported, so tidy first.

```bash
make tidy
make check
git add cmd/ internal/ go.mod go.sum
git commit -m "feat: working two-pane multiplexer

Milestone 4. Two PTY-backed shells, one emulator each, composited into
one frame with focus switching. Layout is hardcoded; Plan 2 replaces it
with the real scrolling strip."
```

---

## Done criteria for Plan 1

- `make check` passes.
- `./bin/wideboi` shows two working shells side by side, focus switches with `ctrl+o`.
- No process survives any exit path — verified for normal quit, child exit, and SIGTERM.
- The reflow question is answered and recorded in the spec.

## What Plan 2 needs from this

- `term.Grid` is the emulator seam; Plan 2 moves it server-side unchanged.
- `compose.Blit` is the compositing primitive; Plan 2 feeds it `[]Placement` instead of hardcoded rects.
- `client.Pane` is explicitly temporary and gets split across the seam.
- The reflow answer decides whether the emulator choice stands.
