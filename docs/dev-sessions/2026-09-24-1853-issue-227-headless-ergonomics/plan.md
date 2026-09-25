# Headless agent ergonomics Implementation Plan

**Goal:** `split --keep` → `wait` → `capture` → `close` works end to end, with
`split` auto-spawning a server. Status JSON and CLI argument handling are made
honest, and SKILL.md is rewritten to match.

**Approach:**
- Capture the exit status at the single reaper in ptyx.
- Kept panes treat the *reap*, not pty EOF, as their end. They stay in
  `s.panes` with an exit code.
- `wait` is a server-side waiter list that is served when a pane exits.
- `split` auto-spawns by reusing `spawnServer` plus the owner handshake, then
  detaches.
- One protocol bump, 8 → 9, covers every wire change.

**Tech stack:** Go, protobuf via `buf` (`make proto`), and the Python pty
scripts for status consumers.

Conventions for every phase:
- Test first; watch it fail for the stated reason.
- Waits are ceilings (poll observed state), never fixed sleeps.
- One commit per phase: `Phase N: <name>`.
- `make quick` is the edit loop. Run `make check` at the end of phases 2, 4, 5
  and 6.
- Timing-sensitive tests (phases 3–5) are run 4× with
  `go test -count=4 -run <Test> ./pkg`.

---

## Phase 1: CLI argument hygiene (`send` extra operands, `split` quoting)

`send` rejects stray operands. `split` preserves argv quoting.

**Files:**
- Modify `cmd/wideboi/control.go`:
  - `runSend`: require exactly 2 operands.
  - `runSplit`: build the command with `shellJoin`.
- Create `shellJoin` + `shellQuote` in `cmd/wideboi/control.go`.
- Modify `cmd/wideboi/control_e2e_test.go`: the multi-arg `&&` splits at :68
  and :159 become single shell strings.
- Modify `cmd/wideboi/main.go`: help text for `send` says to quote the text.
- Test `cmd/wideboi/control_test.go`: add `TestShellJoin`,
  `TestSendRejectsExtraOperands`.

**Key changes:**

```go
// shellJoin turns split's operands into the string handed to $SHELL -c.
// One operand is already a shell command and passes through verbatim;
// several are argv, so each is quoted and the words survive intact.
func shellJoin(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
```

In `runSend`:

```go
if len(rest) != 2 {
	return fmt.Errorf("usage: wideboi send [flags] <pane-id> <text> (quote text containing spaces)")
}
```

Tests:

```go
func TestShellJoin(t *testing.T) {
	cases := []struct{ in []string; want string }{
		{[]string{"make test && echo ok"}, "make test && echo ok"},
		{[]string{"grep", "a b", "f"}, `'grep' 'a b' 'f'`},
		{[]string{"echo", "it's"}, `'echo' 'it'\''s'`},
	}
	for _, c := range cases {
		if got := shellJoin(c.in); got != c.want {
			t.Errorf("shellJoin(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
```

- Add a round-trip test: `sh -c "printf '%s|' " + shellJoin([]string{"a b", "c'd"})`
  outputs `a b|c'd|`.
- `TestSendRejectsExtraOperands`: `runSend(cfg{Socket: "/nonexistent"},
  []string{"1", "echo", "one"}, &buf)` returns an error containing "quote".
  The operand check runs before any dial, so no server is needed.

**Verification — automated:**
- [x] New tests fail before the change — **TestShellJoin: undefined; extra-operands: got dial error, not usage error** (plan said nil; it dialed)
- [x] `make quick` passes — **all Go pkgs ok, 50 web tests passed**
- [x] `go test -count=1 -run 'TestControl|TestShellJoin|TestSend' ./cmd/wideboi` passes — **ok 0.9s**

**Verification — manual:**
- [x] `wideboi send 1 echo one two` prints the usage error; nothing is typed — **verified from binary, rc=1, no dial**

---

## Phase 2: `status --json` cleanup

Statuses become names, columns become snake_case, and every consumer is updated.

**Files:**
- Modify `internal/protocol/messages.go`:
  - `PaneStatus.String()`, `MarshalText`, `UnmarshalText`.
  - json tags on `ColumnData`.
- Modify `cmd/wideboi/status.go`: the table uses `status.String()`; delete
  `statusName`.
- Modify `cmd/wideboi/status_test.go`: assert string statuses and snake_case
  column keys. The existing "unmarshals into MsgLayoutSnapshot" check must
  keep passing via `UnmarshalText`.
- Check `cmd/wideboi/osc_status_test.go:80`: it parses into
  `map[int]protocol.PaneStatus`, which `UnmarshalText` keeps working.
- Modify `scripts/attachcheck.py:974`: use `"height"`.
- Modify `scripts/traffic.py:698,707-708`: use `"width"`/`"height"`.
- Test `internal/protocol/messages_test.go` (create if absent):
  `TestPaneStatusText`.

**Key changes:**

```go
var paneStatusNames = [...]string{
	StatusIdle: "idle", StatusWorking: "working", StatusNeedsInput: "needs_input",
	StatusDone: "done", StatusFailed: "failed",
}

func (s PaneStatus) String() string {
	if s >= 0 && int(s) < len(paneStatusNames) {
		return paneStatusNames[s]
	}
	return "idle"
}

func (s PaneStatus) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

func (s *PaneStatus) UnmarshalText(b []byte) error {
	for i, n := range paneStatusNames {
		if n == string(b) {
			*s = PaneStatus(i)
			return nil
		}
	}
	return fmt.Errorf("unknown pane status %q", b)
}

type ColumnData struct {
	PaneID int `json:"pane_id"`
	Width  int `json:"width"`
	Height int `json:"height"`
}
```

Before relying on this, grep for other JSON encodings of `PaneStatus`/`ColumnData`
(`grep -rn 'json.Marshal\|json.NewEncoder' --include=*.go`). If any exist,
list them in notes.md. None were expected from research.

`TestPaneStatusText`: every status round-trips through Marshal/UnmarshalText,
`json.Marshal(map[int]PaneStatus{1: StatusNeedsInput})` gives
`{"1":"needs_input"}`, and an unknown name errors.

In `status_test.go`, decode the raw JSON into `map[string]any` and assert:
- `pane_statuses["1"] == "idle"`
- `columns[0]` has key `"pane_id"`, not `"PaneID"`.

**Verification — automated:**
- [x] New assertions fail first (status is `0`, key is `PaneID`) — **UnmarshalText undefined; columns[0] had PaneID/Width/Height**
- [x] `make quick` passes — **all ok, 50 web**
- [x] `make check` passes (runs attachcheck + traffic consumers) — **rc=0; 38 passed / 26 passed / 9 playwright**

**Verification — manual:**
- [x] `wideboi status --json` against a live session shows `"working"`/`"idle"` strings and `pane_id`/`width`/`height` — **verified on throwaway server**
- [x] `wideboi status` table shows `needs_input` spelling — **table uses String(); live run showed `idle` (verified name path, needs_input via unit test)**

---

## Phase 3: exit status capture + `split --keep`

A kept pane survives its process's exit, with its screen and exit code, until closed.

**Files:**
- Modify `internal/server/ptyx/pane.go`: record the exit status in the reaper;
  add `ExitCode()`.
- Modify `internal/server/pane.go`:
  - `keep` flag and exit state.
  - `Status()` override.
  - `ExitStatus()`, `markExited()`, `reapedExitCode()`.
  - `Resize` skips the pty ioctl once exited.
- Modify `internal/server/server.go`:
  - `StartupPane.Keep`.
  - `spawnPaneWithSpecLocked` kept-pane path; `watchKeptPane`.
  - Split handler passes Keep and sets `startupComplete`.
  - Send handler rejects exited panes.
  - `sendPaneMetadataTo` includes exit fields.
- Modify `internal/protocol/messages.go`:
  - `MsgSplitRequest.Keep`.
  - `MsgPaneMetadata.Exited`/`ExitCode`.
- Modify `internal/protocol/wirepb/wideboi.proto`:
  - `MsgSplitRequest`: `bool keep = 4;`.
  - `MsgPaneMetadata`: `bool exited = 4; int32 exit_code = 5;`.
- Modify `internal/protocol/codec.go`: map the new fields in both directions.
- Run `make proto` to regenerate `wideboi.pb.go` and
  `web/src/gen/.../wideboi_pb.ts`.
- Modify `internal/protocol/version.go`: `Version = 9`, plus a history line:
  "9: split keep, pane exit metadata, wait request/response (#227)".
- Modify `web/src/client.ts:37` → `wideboi.v9`, and the version strings in
  `web/src/client.test.ts`, `web/src/lifecycle.test.ts`, and
  `web/tests/{cards,horizontal-viewport,lifecycle,vertical-viewport}.spec.js`.
- Modify `cmd/wideboi/control.go`: `runSplit` gets a `--keep` bool.
  `reorderFlags` needs no change (bool flag).
- Modify `cmd/wideboi/main.go`: help text for `split --keep`.
- Modify the wire tests: add the new fields to the roundtrip fixtures in
  `internal/protocol/wire_test.go` and `internal/transport/wire_test.go`.
- Test `internal/server/ptyx/pane_test.go` (or the existing ptyx test file):
  `TestExitCode`.
- Test `internal/server/control_test.go`: `TestKeptPaneSurvivesExit`,
  `TestUnkeptPaneStillCloses`.

**Key changes (ptyx):**

```go
// exitCode is valid once done is closed; the close publishes it.
exitCode int

go func() {
	_ = cmd.Wait()
	p.exitCode = exitStatus(cmd.ProcessState)
	close(p.done)
}()

// ExitCode reports the child's exit status once it has been reaped:
// its exit code, or 128+signal if a signal killed it, as a shell reports.
func (p *Pane) ExitCode() (code int, reaped bool) {
	select {
	case <-p.done:
		return p.exitCode, true
	default:
		return 0, false
	}
}

func exitStatus(ps *os.ProcessState) int {
	if ps == nil {
		return -1
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}
```

`TestExitCode`:
- `Spawn([]string{"/bin/sh", "-c", "exit 3"}, 80, 24, t.TempDir())`, wait on
  `Done()` with a 5s ceiling, and expect `(3, true)`.
- `"kill -TERM $$"` gives `128+15`.
- `ExitCode()` before exit gives `reaped=false`. To test that, spawn
  `"read x"`, check, then `Hangup`.

**Key changes (Pane):**

```go
keep     bool // set before Start; see Server.watchKeptPane
exitMu   sync.Mutex
exited   bool
exitCode int

// ExitStatus reports whether a kept pane's process has exited, and its code.
func (p *Pane) ExitStatus() (code int, exited bool) { p.exitMu.Lock(); defer p.exitMu.Unlock(); return p.exitCode, p.exited }
func (p *Pane) markExited(code int)                 { p.exitMu.Lock(); p.exited, p.exitCode = true, code; p.exitMu.Unlock() }

// reapedExitCode is the child's status if it has been reaped; custom panes have none.
func (p *Pane) reapedExitCode() (int, bool) {
	if p.pty == nil {
		return 0, false
	}
	return p.pty.ExitCode()
}

func (p *Pane) Status() protocol.PaneStatus {
	if code, exited := p.ExitStatus(); exited {
		if code == 0 {
			return protocol.StatusDone
		}
		return protocol.StatusFailed
	}
	return p.grid.Status()
}
```

- In `Pane.Resize`, skip `p.pty.Resize` when `ExitStatus()` reports exited.
  The grid still resizes, so the retained screen reflows like any column.
- Read `Resize` first to place the guard next to the existing pty call.

**Key changes (Server):**

In `spawnPaneWithSpecLocked`, replace the `p.Start(...)` block:

```go
if spec.Keep {
	p.keep = true
	// A kept pane ends when its child is reaped, not at pty EOF: a
	// background job can hold the pty open past the exit, and the
	// exit code only exists after the reap.
	p.Start(func() {})
	go s.watchKeptPane(id, p)
} else {
	p.Start(func() {
		// Called when PTY reader hits EOF
		s.onPaneExit(id)
	})
}
```

```go
// watchKeptPane records a kept pane's exit and leaves the pane in place,
// screen intact, until something closes it.
func (s *Server) watchKeptPane(id int, p *Pane) {
	select {
	case <-p.pty.Done():
	case <-p.closed:
		return
	}
	code, _ := p.pty.ExitCode()
	p.markExited(code)
	s.broadcastLayout(context.Background())
}
```

Phase 4 adds `s.notifyWaiters(id, code, "")` inside `watchKeptPane`, after
`markExited`.

`handleClientMsg` changes:
- Split: `spec := StartupPane{Command: m.Command, Dir: m.Cwd, Keep: m.Keep}`.
  On success, also set `s.startupComplete = true`. An auto-spawned server
  (phase 5) never attaches, and `runServer` only runs auto-cleanup for a
  server whose startup completed (`cmd/wideboi/main.go:439-440`).
- Send, before `p.Write`:
  ```go
  if _, exited := p.ExitStatus(); exited {
  	sendResp = &protocol.MsgSendInputResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d has exited", m.PaneID)}
  	break
  }
  ```
- `sendPaneMetadataTo`: `code, exited := p.ExitStatus()`, then set
  `Exited: exited, ExitCode: code` on the message.

Messages:
- `MsgSplitRequest` gains `Keep bool \`json:"keep"\``.
- `MsgPaneMetadata` gains `Exited bool \`json:"exited"\`` and
  `ExitCode int \`json:"exit_code"\``.
- Spec note: `exit_code` is always present in JSON and meaningful only when
  `exited` is true. That's simpler than a pointer through proto; record the
  deviation in notes.md.

`TestKeptPaneSurvivesExit`:
- Build the `Server` fixture from `TestServerPaneControlRequests`
  (`internal/server/control_test.go:35-47`), with no pre-seeded pane.
- `handleClientMsg(ctx, tp, MsgSplitRequest{Command: "echo kept-output; exit 3", Keep: true})`.
- Take the `MsgSplitResponse` and poll, with a 5s ceiling, until
  `s.panes[id].ExitStatus()` reports exited.
- Assert:
  - code is 3.
  - `Status() == StatusFailed`.
  - the pane is still in `s.panes` and in `s.strip`.
  - `MsgCaptureRequest` text contains "kept-output".
  - `MsgSendInputRequest` returns an error containing "has exited".
- Then `MsgClosePaneRequest` removes it.

`TestUnkeptPaneStillCloses`:
- Keep a seeded long-lived pane so the server does not close.
- Split without Keep (`exit 0`) and poll until the pane is absent from
  `s.panes`.

**Verification — automated:**
- [x] New tests fail first (no `ExitCode`, no `Keep` field) — **ExitCode undefined; codec roundtrip dropped Keep/Exited/ExitCode; server test: "kept pane 1 was removed on exit"**
- [x] `make proto` regenerates cleanly; `make proto-check` shows no diff after commit — **see commit follow-up check**
- [x] `make quick` passes (includes web tests with the v9 strings) — **all ok, 50 web**
- [x] `go test -race -count=4 -run 'TestKeptPane|TestUnkeptPane' ./internal/server` passes — **ok 7.7s (incl. drain tests added; see notes)**
- [x] `go test -count=4 -run TestExitCode ./internal/server/ptyx` passes — **ok**

**Verification — manual:**
- [ ] In a live session, `wideboi split --keep 'echo hi; exit 3'`: — **agent-verified on a throwaway server: status failed, metadata exited/3, capture `hi`, send refused, close of last pane ended server. TUI kill-pane key left for Les.**
  - the column stays, showing `hi` and the failed glyph.
  - `status --json` shows `"exited": true, "exit_code": 3`.
  - the TUI kill-pane key removes it.

---

## Phase 4: `wideboi wait`

This phase adds a blocking wait that exits with the pane process's exit code.

**Files:**
- Modify `internal/protocol/messages.go`: `MsgWaitRequest`, `MsgWaitResponse`.
- Modify `internal/protocol/wirepb/wideboi.proto`:
  - `message MsgWaitRequest { int32 pane_id = 1; }`
  - `message MsgWaitResponse { int32 pane_id = 1; int32 exit_code = 2; string error = 3; }`
  - `ClientMessage` oneof: `MsgWaitRequest wait_request = 16;`
  - `ServerMessage` oneof: `MsgWaitResponse wait_response = 13;`
- Modify `internal/protocol/codec.go`: Marshal/Unmarshal cases for both.
- Run `make proto`. There is no version bump (v9 from phase 3 is unreleased).
- Modify the wire tests: add both types to `wireTypes` in
  `internal/protocol/wire_test.go` and to `TestEveryMessageTypeRoundtrips` in
  `internal/transport/wire_test.go`.
- Modify `internal/server/server.go`:
  - `waiters map[int][]transport.Transport` field.
  - `MsgWaitRequest` handler.
  - `notifyWaiters`, `finishWaiters`.
  - Hooks in `watchKeptPane`, `onPaneExit` and `removePaneLocked`.
- Modify `cmd/wideboi/control.go`:
  - `rpcQuery` treats timeout 0 as none and returns `errRPCTimeout`.
  - `runWait`.
  - `reorderFlags` learns `-timeout/--timeout`.
- Modify `cmd/wideboi/main.go`:
  - `wait` in the parseCLI subcommand list (with `split` etc.).
  - dispatch.
  - help text.
- Test `internal/server/control_test.go`: `TestWaitRequest`.
- Test `cmd/wideboi/control_e2e_test.go`: `TestWaitE2E`.

**Key changes (server):**

```go
// waiters holds the transports blocked in `wideboi wait` on each pane,
// answered when that pane's process exits (or it closes first).
waiters map[int][]transport.Transport

case protocol.MsgWaitRequest:
	p, ok := s.panes[m.PaneID]
	if !ok {
		waitResp = &protocol.MsgWaitResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d not found", m.PaneID)}
		break
	}
	if code, exited := p.ExitStatus(); exited {
		waitResp = &protocol.MsgWaitResponse{PaneID: m.PaneID, ExitCode: code}
		break
	}
	if s.waiters == nil {
		s.waiters = make(map[int][]transport.Transport)
	}
	s.waiters[m.PaneID] = append(s.waiters[m.PaneID], tp)
```

`waitResp` is sent after unlock, alongside the other responses
(`server.go:726-742`).

```go
// notifyWaiters answers everyone waiting on pane id. The send is bounded:
// a waiter whose connection has stalled must not hold up the others.
func (s *Server) notifyWaiters(id, code int, errMsg string) {
	s.mu.Lock()
	tps := s.waiters[id]
	delete(s.waiters, id)
	s.mu.Unlock()
	resp := protocol.MsgWaitResponse{PaneID: id, ExitCode: code, Error: errMsg}
	for _, tp := range tps {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		tp.SendServer(ctx, resp)
		cancel()
	}
}

// finishWaiters answers waiters on a pane that has been closed. Close
// hung it up and waited for the reap, so the code is normally there; a
// child that outlived the grace has none to give.
func (s *Server) finishWaiters(id int, p *Pane) {
	if code, ok := p.reapedExitCode(); ok {
		s.notifyWaiters(id, code, "")
		return
	}
	s.notifyWaiters(id, 0, fmt.Sprintf("pane %d closed before its process exited", id))
}
```

Hooks:
- `watchKeptPane`: after `p.markExited(code)`, call `s.notifyWaiters(id, code, "")`.
- `onPaneExit`: after `_ = p.Close()`, call `s.finishWaiters(id, p)`. This must
  come before the `s.Close()` for the last pane, so the waiter hears before
  the transports close.
- `removePaneLocked`: change `go p.Close()` to
  `go func() { _ = p.Close(); s.finishWaiters(id, p) }()`.
- Close-server ordering: the handler's last-pane `s.Close()` fires 50ms after
  the close response (`server.go:743-749`), racing `finishWaiters`.
  Acceptable. The documented flow waits *before* closing, and a waiter that
  loses the race gets "server closed connection".

Race argument (put it in a comment on the handler):
- The handler checks `ExitStatus()` and registers under `s.mu`.
- `notifyWaiters` takes `s.mu` after `markExited`.
- So a waiter either sees the exit or is registered before it is collected.
  For unkept panes, the pane leaves `s.panes` under `s.mu` before
  `finishWaiters` runs.

**Key changes (CLI):**

```go
var errRPCTimeout = errors.New("timeout waiting for server response")

// in rpcQuery: timeout <= 0 waits indefinitely
var timeoutC <-chan time.Time
if timeout > 0 {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	timeoutC = timer.C
}
// ... case <-timeoutC: return zero, errRPCTimeout
```

```go
// exitWaitTimeout is timeout(1)'s code for "gave up waiting".
const exitWaitTimeout = 124

// runWait blocks until a pane's process exits and returns its exit code.
func runWait(cfg config.Config, args []string, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var timeout time.Duration
	var session, socket string
	fs.DurationVar(&timeout, "timeout", 0, "give up after this long (exit 124); default waits forever")
	fs.StringVar(&session, "L", "", "session name")
	fs.StringVar(&session, "session", "", "session name")
	fs.StringVar(&socket, "s", "", "unix domain socket path")
	fs.StringVar(&socket, "socket", "", "unix domain socket path")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 0, err
	}
	applySessionFlags(&cfg, session, socket)
	rest := fs.Args()
	if len(rest) != 1 {
		return 0, fmt.Errorf("usage: wideboi wait [--timeout <dur>] <pane-id>")
	}
	paneID, err := strconv.Atoi(rest[0])
	if err != nil {
		return 0, fmt.Errorf("invalid pane id %q: %w", rest[0], err)
	}
	resp, err := rpcQuery[protocol.MsgWaitResponse](cfg, protocol.MsgWaitRequest{PaneID: paneID}, timeout)
	if errors.Is(err, errRPCTimeout) {
		fmt.Fprintf(stderr, "wideboi: timed out after %s waiting for pane %d\n", timeout, paneID)
		return exitWaitTimeout, nil
	}
	if err != nil {
		return 0, err
	}
	if resp.Error != "" {
		return 0, errors.New(resp.Error)
	}
	return resp.ExitCode, nil
}
```

`main.go` dispatch:

```go
case "wait":
	code, err := runWait(cfg, opts.subcommandArgs, os.Stderr)
	fatal(err)
	os.Exit(code)
```

`TestWaitRequest` (server, in-process):
1. Kept pane running `read x; exit 7`. Send `MsgWaitRequest` and assert no
   `MsgWaitResponse` arrives within 100ms. That's a negative check, and a
   short ceiling is fine. Then `MsgSendInputRequest{Data: "go\r"}`, and take a
   `MsgWaitResponse` with a 5s ceiling: `ExitCode == 7`.
2. A second `MsgWaitRequest` on the same (now exited, kept) pane responds
   immediately with 7.
3. Unkept pane running `read x; exit 5`, plus a seeded long-lived pane: wait,
   send input, and the response gives 5.
4. Unknown pane: the response carries an error.
5. Pane running `sleep 30`, kept: wait, then `MsgClosePaneRequest`. The
   response is either a code of 128+1 (SIGHUP) or an error. Assert that one
   of the two arrives within the close grace plus 2s.

`TestWaitE2E` (binary, extending the `buildWideboiBinary` pattern):
- `split --keep 'exit 4'` then `wait <id>`: process exit code 4.
- `wait --timeout 200ms <id-of-sleep-30-pane>`: exit 124.
- `wait 999`: exit 1 with "not found" on stderr.

**Verification — automated:**
- [x] New tests fail first — **codec: "in neither envelope"; server: timeout waiting for MsgWaitResponse; E2E written after the CLI (TDD slip), proven by breaking runWait → "process exit 0, want 4"**
- [x] `make quick` passes — **via make check**
- [x] `go test -race -count=4 -run 'TestWait' ./internal/server ./cmd/wideboi` passes — **ok / ok**
- [x] `make check` passes — **rc=0**

**Verification — manual:**
- [x] `P=$(wideboi split --keep 'sleep 2; exit 9'); wideboi wait $P; echo $?` prints 9 after ~2s — **rc=9 after 2s on throwaway server**

---

## Phase 5: `split` auto-spawns a server

`split` against a session with no server starts one and detaches from it.

**Files:**
- Modify `cmd/wideboi/main.go`: `cliOptions.globalArgs` is set in parseCLI at
  the split/send/... break, as a copy of `flagArgs`. Pass it to `runSplit`.
- Modify `cmd/wideboi/control.go`:
  - split `rpcQuery` into `rpcQuery` (dial + handshake + `rpcOn`) and
    `rpcOn(conn)`.
  - add `connectOrSpawn`.
  - `runSplit` uses them and detaches, or shuts down on failure.
- Test `cmd/wideboi/main_test.go`: parseCLI `globalArgs` for
  `-L work split --keep x` is `["-L", "work"]`.
- Test `cmd/wideboi/control_e2e_test.go`: `TestSplitAutoSpawnE2E`.

**Key changes:**

```go
// rpcOn sends req on an already-handshaken connection and waits for Resp.
// The caller owns conn and its pumps' lifetime through ctx.
func rpcOn[Resp any](ctx context.Context, cc *transport.ClientSocketConn, req transport.ClientMessage, timeout time.Duration) (Resp, error)
```

`rpcQuery` becomes dial → `handshakeServer` → `NewClientSocketConn` +
`RunPumps` → `rpcOn`.

```go
// connectOrSpawn returns a handshaken connection to the session's
// server, starting one in the background if none answers. spawned
// reports whether conn is the owner connection of a server we started,
// which the caller must detach from (or shut down) when done.
func connectOrSpawn(cfg config.Config, serverFlags []string) (conn net.Conn, spawned bool, err error) {
	if conn, err := net.Dial("unix", cfg.Socket); err == nil {
		return conn, false, handshakeServer(conn, cfg.Socket)
	}
	conn, exited, err := spawnServer(cfg.Socket, serverFlags)
	if err != nil {
		return nil, false, err
	}
	// The handshake is the readiness signal: the server binds its
	// listener before it says hello (see runServer).
	if _, err := transport.Handshake(conn); err != nil {
		conn.Close()
		if errors.Is(err, io.EOF) {
			switch serverExitCode(exited, reapCeiling) {
			case exitSessionTaken:
				// Another process started this session first; use theirs.
				conn, err := dialWithin(cfg.Socket, takenCeiling)
				if err != nil {
					return nil, false, err
				}
				return conn, false, handshakeServer(conn, cfg.Socket)
			case -1:
			default:
				return nil, false, startupExitError(cfg.Socket)
			}
		}
		return nil, false, describeHandshakeErr(cfg.Socket, err, "")
	}
	return conn, true, nil
}
```

- `serverFlags` is `opts.globalArgs`, plus `-L <session>` or `-s <socket>`
  when those were given after `split` (the `runSplit` locals). The spawned
  server then resolves the same socket.
- `spawnServer` → `serverArgs` prepends `server --owner-fd 3`.
- Before writing this, confirm that `handshakeServer` and
  `describeHandshakeErr` have the signatures used here
  (`cmd/wideboi/handshake.go`).

`runSplit` tail:

```go
conn, spawned, err := connectOrSpawn(cfg, serverFlags)
if err != nil {
	return err
}
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
cc := transport.NewClientSocketConn(conn, 256)
cc.RunPumps(ctx)
resp, err := rpcOn[protocol.MsgSplitResponse](ctx, cc, req, 5*time.Second)
if spawned {
	// Hand the session to itself: detached, it lives until its last
	// pane closes. A server we started for nothing goes away again.
	if err == nil && resp.Error == "" {
		hangUp(ctx, cc, protocol.MsgDetach{}, detachCeiling)
	} else {
		hangUp(ctx, cc, protocol.MsgShutdown{}, shutdownCeiling)
	}
}
```

The existing error/print handling follows after this.

`TestSplitAutoSpawnE2E`:
- Socket at `filepath.Join(t.TempDir(), "auto.sock")`, with no server started.
- `bin -s sock split --keep 'echo auto-spawned; exit 2'` exits 0 and prints
  an id.
- `bin -s sock wait <id>` exits 2.
- `bin -s sock capture <id>` contains "auto-spawned".
- `bin -s sock close <id>`: then poll `bin -s sock status` until it exits
  non-zero, with a 5s ceiling. The last pane closed, so the server exited and
  nothing leaks.
- `t.Cleanup`: `bin -s sock kill-session`, ignoring errors, as a backstop.
- `split --cwd /nonexistent true` on a fresh socket exits non-zero, and
  `status` on that socket fails, showing the spawned server was shut down.
  If spawn succeeds with a bad cwd (the error surfaces only in the child),
  record the actual behaviour in notes.md and adjust the assertion to match.
  Don't weaken it silently.

**Verification — automated:**
- [x] New tests fail first (split errors "no wideboi server running") — **exactly that; globalArgs test: got []**
- [x] `go test -count=4 -run TestSplitAutoSpawnE2E ./cmd/wideboi` passes — **-race -count=4 with TestWaitE2E/TestControl: ok 11s; no stray servers; also green on Linux via new `make linux-test`**
- [x] `make check` passes (verify-exit catches stray servers) — **rc=0**

**Verification — manual:**
- [ ] `wideboi -L scratch-227 split --keep 'echo hi'` with no session running, then `wideboi -L scratch-227` attaches and shows the pane; `close` it and the session ends — **agent-verified without the interactive attach (status showed the pane; close ended the session; not in `ls`). The attach itself is left for Les.**

---

## Phase 6: SKILL.md and README

This phase is docs-only, so it has no TDD. Every example must be executed, not
just read.

**Files:**
- Rewrite `docs/skills/wideboi-control/SKILL.md`:
  - Session targeting stays.
  - `split` documents `--keep`, auto-spawn, and one-string vs multi-arg
    commands.
  - `send` covers literal text, exactly one text operand, and control keys via
    `$'\x03'` (Ctrl-C) and `$'\x1b'` (Esc).
  - `capture` is unchanged.
  - `wait` covers exit codes, 128+signal, `--timeout` → 124, and needing
    `--keep` (or an early start) to observe exit.
  - `close` is unchanged.
  - `status --json` gets its real shape, with an example.
  - Pattern A: `split --keep` → `wait` → `capture` → `close`.
  - Pattern B: REPL. Poll `capture` for the prompt instead of `sleep`.
  - Smoke test: no `server &`; auto-spawn plus wait.
- Modify `README.md`: the control subcommand section gains `--keep` and
  `wait`.
- Modify `docs/LESSONS.md`, only if the session taught something evergreen
  (e.g. "a kept pane ends at reap, not EOF").

**Verification — automated:**
- [x] Extract the smoke-test block from SKILL.md and run it against `./bin/wideboi` (`make build` first): exits 0 — **`smoke: ok`, rc=0; the REPL pattern also ran verbatim, rc=0**
- [x] `make check` passes — **rc=0**

**Verification — manual:**
- [ ] Les reads SKILL.md

---

## Self-review

- **Spec coverage:**

  | Spec item | Phase |
  |---|---|
  | 1 auto-spawn | P5 |
  | 2 `--keep` | P3 |
  | 3 `wait` | P4 |
  | 4 send to exited | P3 |
  | 5 status JSON | P2 (+ exit fields in P3) |
  | 6 send operands | P1 |
  | 7 split quoting | P1 |
  | 8 SKILL.md | P6 |

- **Deviation from the spec:** `exit_code` is always present in JSON, with
  `exited` saying whether it is meaningful (P3 note). The spec said "present
  only when exited".
- **Type names used across phases:**
  - `ExitCode()` (ptyx).
  - `ExitStatus()`, `markExited`, `reapedExitCode` (Pane).
  - `watchKeptPane`, `notifyWaiters`, `finishWaiters` (Server).
  - `rpcOn`, `connectOrSpawn`, `errRPCTimeout`, `exitWaitTimeout`,
    `shellJoin`, `shellQuote` (CLI).

  All are consistent.
