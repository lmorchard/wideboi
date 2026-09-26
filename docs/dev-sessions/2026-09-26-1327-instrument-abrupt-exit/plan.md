# Instrument abrupt session exits — Implementation Plan

**Goal:** Every session end leaves a durable, explained record in `exits.log`,
and a client that loses its session says so.

**Approach:** A shared append-only `exits.log` beside the sockets, written
through one `internal/logger` helper. The server records a close reason at
every `Close` trigger (first wins). `cmd/wideboi` writes the records, including
from inside the signal guard. The owner client records the server's reaped
status and prints a notice after restoring the terminal. Exit codes don't
change.

**Tech stack:** Go (`log/slog`, `os/exec`, `syscall`), existing Python pty
harness (`scripts/attachcheck.py`).

**Deviations from spec (flagged for review):**
- A `server started` record is added. A start with no matching exit for that
  pid then means a hard death (SIGKILL, crash, OOM) that no in-process code
  could record. The spec's events can't show that on their own.
- The owner also prints the notice when the server it spawned ended with
  anything other than `exit status 0`, not only when reconnecting fails. A
  signalled server that is reaped promptly takes the `server closed the
  connection` branch (`cmd/wideboi/main.go:817-822`), which is silent today
  and would stay silent under the spec as written.

Shared names used across phases:

```go
// internal/logger
func ExitsPath(socket string) string // filepath.Join(filepath.Dir(socket), "exits.log")
func SessionOf(socket string) string // strings.TrimSuffix(filepath.Base(socket), ".sock")
func AppendExit(socket, component, event string, args ...any) error
```

Close reasons (string constants in `internal/server/closereason.go`):
`ReasonOwnerLeft = "owner-left"`, `ReasonShutdownRequest = "shutdown-request"`,
`ReasonLastPaneClosed = "last-pane-closed"`, `ReasonLastPaneExited = "last-pane-exited"`,
`ReasonContextCancelled = "context-cancelled"`, `ReasonSignal = "signal"`,
`ReasonUnspecified = "unspecified"`. `Server.CloseFor(reason, attrs...)` is the
one entry point that records a reason (exported, because `cmd/wideboi`'s signal
guard needs it too).

---

## Phase 1: `exits.log` + server start/exit records with close reasons

Delivers: every non-signal server end writes a `server exit` line naming its
reason, and every start writes `server started`.

**Files:**
- Modify: `internal/logger/logger.go` — add `ExitsPath`, `SessionOf`, `AppendExit`.
- Test: `internal/logger/logger_test.go`
- Create: `internal/server/closereason.go` — reason constants, `CloseFor`, `CloseReason`.
- Modify: `internal/server/server.go` — each `Close` trigger calls `CloseFor`.
- Modify: `cmd/wideboi/main.go` (`runServer`) — `server started` after the
  listener binds; `server exit` after `Run` returns (non-signal path), before
  auto-cleanup.
- Test: `internal/server/lifecycle_test.go`, `cmd/wideboi/cleanup_test.go`
  (extend cases 1 and 4).

**Key changes:**

```go
// AppendExit appends one record to the exits log beside socket. It
// survives every cleanup path by design: one line per session event, so a
// session that vanished can be explained afterwards. One short O_APPEND
// write per record keeps lines whole with several writers.
func AppendExit(socket, component, event string, args ...any) error {
	var buf bytes.Buffer
	h := newHandler(&buf, slog.LevelInfo)
	attrs := append([]any{"session", SessionOf(socket), "component", component, "pid", os.Getpid()}, args...)
	slog.New(h).Info(event, attrs...)
	f, err := os.OpenFile(ExitsPath(socket), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(buf.Bytes())
	return err
}
```

```go
// internal/server/closereason.go
type closeReason struct {
	reason string
	attrs  []any
}

// CloseFor records why the session is ending, then closes it. The
// first reason wins: Close is idempotent and later triggers are echoes.
func (s *Server) CloseFor(reason string, attrs ...any) error {
	s.reasonMu.Lock()
	if s.reason == nil {
		s.reason = &closeReason{reason: reason, attrs: attrs}
		slog.Info("server closing", append([]any{"reason", reason}, attrs...)...)
	}
	s.reasonMu.Unlock()
	return s.Close()
}

// CloseReason reports why the server closed; ReasonUnspecified if it
// was closed without one (a direct Close) or has not closed.
func (s *Server) CloseReason() (string, []any)
```

`reasonMu sync.Mutex` and `reason *closeReason` are new fields on `Server`.
They're deliberately not under `s.mu`, since callers of `Close` must not hold
`s.mu` (#43).

Call-site mapping:
- `server.go:350-354` owner EOF → `CloseFor(ReasonOwnerLeft)`. Keep its existing log line.
- `server.go:356-363` `MsgShutdown` → `CloseFor(ReasonShutdownRequest)`. Phase 2 adds requester attrs.
- `server.go:473-474` ctx done → `CloseFor(ReasonContextCancelled)`.
- `server.go:942-952` close-pane of last pane → `CloseFor(ReasonLastPaneClosed, "paneID", id)`.
  Capture the pane id into the goroutine.
- `server.go:1178-1180` `onPaneExit` → `CloseFor(ReasonLastPaneExited, "paneID", id)`. Phase 4 adds pid/status.

In `runServer`:

```go
// after srv.ListenSocket(ctx, sl):
_ = logger.AppendExit(cfg.Socket, "server", "server started", "ownerFD", ownerFD, "restored", os.Getenv("WIDEBOI_RESTORE_STATE") != "")

// after Run returns and the signalled branch (which never gets here: the re-raise kills us):
cleanup := cfg.AutoCleanupEnabled && !signalled.Load() && err == nil && startupOK
reason, attrs := srv.CloseReason()
rec := append([]any{"reason", reason}, attrs...)
rec = append(rec, "err", errString(err), "startupComplete", startupOK, "autoCleanup", cleanup)
_ = logger.AppendExit(cfg.Socket, "server", "server exit", rec...)
if cleanup { ...existing block... }
```

`errString(err)` returns `""` for nil. It's a tiny helper in `main.go`.

**Tests (write first, watch fail):**
- `TestAppendExitWritesOneRecord`: two calls, file has 2 lines, each with
  `msg="server exit"`, `session=s`, `component=server`, `pid=<os.Getpid()>`,
  and the extra attr.
- `TestAppendExitConcurrentLinesStayWhole`: 20 goroutines × 20 records. Every
  line parses as a record (starts `time=` and has `component=`), 400 lines total.
- `TestExitsPathBesideSocket`: `ExitsPath("/a/b/x.sock") == "/a/b/exits.log"`.
- lifecycle: `TestShutdownRecordsReason` (MsgShutdown → `CloseReason() ==
  ReasonShutdownRequest`), `TestOwnerEOFRecordsReason`, `TestDirectCloseIsUnspecified`,
  `TestFirstCloseReasonWins` (`CloseFor(A)` then `CloseFor(B)` → A).
- cleanup_test case 1 (clean exit with auto-cleanup, logs removed): assert
  `exits.log` in the temp dir survives and holds `server started` and
  `server exit` with `reason=shutdown-request` and `autoCleanup=true`.

**Verification — automated:**
- [x] New tests fail before the implementation — **logger: `undefined: ExitsPath/SessionOf/AppendExit`; server: `s.CloseReason undefined`; cleanup case 1: `exits.log missing after a clean exit`**
- [x] `go test ./internal/logger ./internal/server -run 'Exit|Reason' -v` passes — **logger ok; 4 reason tests PASS; TestServerAutoCleanup ok**
- [x] `make quick` passes — **exit 0 (after gofmt)**

**Verification — manual:**
- [x] (run by agent, scratch TMPDIR) Run `bin/wideboi -L p1 server &`, `bin/wideboi -L p1 kill-session`, then
      `cat $TMPDIR/wideboi-$(id -u)/exits.log`: `server started` and
      `server exit reason=shutdown-request` appear, and `p1.server.log` is gone
      (auto-cleanup) while `exits.log` stays

---

## Phase 2: who asked for the shutdown

Delivers: `reason=shutdown-request` carries `requesterPID` and `requester`
ancestry by executable name.

**Files:**
- Create: `internal/server/ancestry.go` — `processAncestry(pid int) string`, `parsePsTable`.
- Test: `internal/server/ancestry_test.go`
- Modify: `internal/server/server.go` — `peerPIDs map[transport.Transport]uint32`
  set in `admitSocketConn`, deleted in `removeTransportLocked`, exported
  `SetPeerPID(tp, pid)` for the owner; the `MsgShutdown` branch reads it.
- Modify: `cmd/wideboi/main.go` (`runServer`) — keep the owner handshake's
  `peer` and call `srv.SetPeerPID(ownerConn, peer.PID)` after `NewServer`.

**Key changes:**

```go
// processAncestry names pid and its ancestors by executable, nearest
// first ("wideboi<-zsh<-claude"), for a log line. Names only: command
// lines here carry secrets (--websocket-token). Logging only -- nothing
// may ever act on this, and a row that does not parse is skipped rather
// than guessed at (see the kill-path lesson).
func processAncestry(pid int) string {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,comm=").Output()
	if err != nil {
		return "unknown: " + err.Error()
	}
	table := parsePsTable(string(out))
	var names []string
	for i := 0; i < 8 && pid > 1; i++ {
		row, ok := table[pid]
		if !ok {
			break
		}
		names = append(names, filepath.Base(row.comm))
		pid = row.ppid
	}
	if len(names) == 0 {
		return "unknown"
	}
	return strings.Join(names, "<-")
}

type psRow struct {
	ppid int
	comm string
}

// parsePsTable reads "pid ppid comm" rows. comm is the rest of the line
// and may contain spaces; a row whose pid or ppid is not an integer is
// dropped.
func parsePsTable(out string) map[int]psRow
```

`MsgShutdown` branch:

```go
if _, ok := msg.(protocol.MsgShutdown); ok {
	pid := s.peerPID(tp)
	_ = s.CloseFor(ReasonShutdownRequest, "requesterPID", pid, "requester", processAncestry(int(pid)))
	return
}
```

The ancestry lookup runs before `Close`, while the requester is still waiting
for the hangup, so it is alive. `peerPID(tp)` takes `s.mu` briefly and returns
0 if unknown. `processAncestry(0)` returns `"unknown"`.

**Tests (write first, watch fail):**
- `TestParsePsTable`: fixture with a normal row, a comm containing spaces
  (`Visual Studio Code Helper`), a blank-field row, a non-numeric row, and
  leading whitespace. Only the valid rows are kept, with comm intact.
- `TestProcessAncestryOfSelf`: `processAncestry(os.Getpid())` starts with
  `filepath.Base(os.Args[0])` truncated the way `ps` truncates comm. Assert
  `strings.HasPrefix` on the first 15 chars, and that it has at least 2 names.
- lifecycle: `TestShutdownRecordsRequesterPID`: `s.SetPeerPID(asker, 4242)`,
  MsgShutdown → attrs contain `requesterPID` 4242.
- e2e in `cmd/wideboi/main_test.go` `TestKillSessionShutsDownAServer`
  (extend): after kill-session, the temp dir's `exits.log` has
  `reason=shutdown-request` and `requester=` whose first name is `wideboi`
  (the kill-session child is the built binary).

**Verification — automated:**
- [x] New tests fail before implementation — **`undefined: parsePsTable / processAncestry`**. The `TestKillSessionShutsDownAServer` extension asserts `srv.CloseReason()` rather than `exits.log`: that test calls `runKillSession` in-process against a bare `server.NewServer`, so `runServer` (which writes exits.log) is not involved. It was written after the implementation; the attr it checks did not exist before.
- [x] `go test ./internal/server -run 'Ancestry|PsTable|Requester' -v` passes — **4 PASS; e2e PASS, logged `requester="wideboi.test<-go<-zsh<-claude<-..."`**
- [x] `make quick` passes (includes `seam-check`: no new client↔server import) — **exit 0**

**Verification — manual:**
- [x] (run by agent, scratch TMPDIR, `--websocket-token sekrit`) From inside a wideboi pane, `bin/wideboi kill-session` on a scratch
      session: the `exits.log` line reads like `requester=wideboi<-zsh<-...`
      with no command-line arguments in it — **`requester=wideboi<-wideboi<-launchd`; `grep -c sekrit exits.log` = 0**

---

## Phase 3: signals are named

Delivers: a signalled server writes `server signalled` before teardown and
`server exit reason=signal` after it.

**Files:**
- Modify: `internal/hostterm/guard.go` — record the caught signal. Add `Signal() os.Signal`.
- Test: `internal/hostterm/signal_test.go`
- Modify: `cmd/wideboi/main.go` (`runServer`) — declare `guard` before
  `NewGuard` so the stop func can read it. Record inside the stop func.
- Test: `cmd/wideboi/cleanup_test.go` case 3 (already SIGTERMs a child server).

**Key changes:**

```go
// guard.go
type Guard struct {
	once sync.Once
	stop func() error
	err  error
	sig  atomic.Value // os.Signal; set before stop runs on the signal path
}

// Signal reports the signal whose arrival ran the shutdown function, or
// nil if it ran for any other reason. Valid inside the shutdown function.
func (g *Guard) Signal() os.Signal {
	s, _ := g.sig.Load().(os.Signal)
	return s
}
```

In `Arm`'s goroutine: `g.sig.Store(s)` immediately before `_ = g.Stop()`.

`runServer`:

```go
var guard *hostterm.Guard
guard = hostterm.NewGuard(func() error {
	signalled.Store(true)
	sig := guard.Signal()
	var err error
	if sig != nil {
		// Written first: if teardown wedges, this line is the evidence.
		_ = logger.AppendExit(cfg.Socket, "server", "server signalled", "signal", sig.String())
		err = srv.CloseFor(server.ReasonSignal, "signal", sig.String())
		// Code after Run never runs on this path (the re-raise), so the
		// exit record is written here.
		_ = logger.AppendExit(cfg.Socket, "server", "server exit", "reason", server.ReasonSignal, "signal", sig.String(), "err", errString(err))
	} else {
		err = srv.Close()
	}
	// The re-raise skips defers, and a socket nobody answers is
	// litter the next server has to step over.
	_ = sl.Close()
	return err
})
```

**Tests (write first, watch fail):**
- `TestGuardReportsTheSignal`: follows the `WIDEBOI_SIGNAL_CHILD` child
  pattern. The child's stop func writes `fmt.Sprint(g.Signal())` to the
  marker. The parent SIGTERMs it and asserts the marker reads `terminated`.
- `TestGuardSignalNilOnPlainStop`: `g.Stop()` directly, and `Signal()` is nil
  inside the stop func.
- cleanup_test case 3: `exits.log` has `server signalled signal=terminated`
  followed by `server exit reason=signal`.

**Verification — automated:**
- [x] New tests fail before implementation — **`g.Signal undefined`; case 3: `exits.log lacks a signalled record followed by an exit record`**
- [x] `go test ./internal/hostterm ./cmd/wideboi -run 'Guard|AutoCleanup' -v` passes — **both guard tests PASS; TestServerAutoCleanup ok**
- [x] `make quick` passes — **exit 0**
- [x] `make verify-exit` passes (signal teardown order unchanged) — **exit 0, all sizes/signals OK**

**Verification — manual:**
- [x] (run by agent, scratch TMPDIR) `bin/wideboi -L p3 server &`, then `kill -TERM <pid>`: `exits.log` shows
      both lines, and the socket is gone — **wait status 143; `server signalled signal=terminated` then `server exit reason=signal`; no p3.sock**

---

## Phase 4: pane exits are logged

Delivers: every pane end logs pid and wait status to the server log. The last
pane's exit is in the `server exit` record.

**Files:**
- Modify: `internal/server/ptyx/pane.go` — record `exitDesc` alongside
  `exitCode` in `Spawn`'s and `Adopt`'s reapers. Add `ExitDescription() (string, bool)`.
- Test: `internal/server/ptyx/pane_test.go`
- Modify: `internal/server/pane.go` — `func (p *Pane) ExitSummary() (pid int, status string)`.
  Returns `0, "no process"` for custom panes (`p.pty == nil`), and
  `pid, "not reaped"` if not yet reaped.
- Modify: `internal/server/server.go` — `onPaneExit` and the
  `removePaneLocked` goroutine log `pane ended` after `p.Close()`. `onPaneExit`
  passes pid/status into `CloseFor(ReasonLastPaneExited, ...)`.
- Test: `internal/server/lifecycle_test.go` or an existing pane-exit test file.

**Key changes:**

```go
// ptyx reaper (Spawn):
_ = cmd.Wait()
p.exitCode = exitStatus(cmd.ProcessState)
p.exitDesc = describeState(cmd.ProcessState, nil)
close(p.done)

// describeState is the reaped status as os.ProcessState words it
// ("exit status 1", "signal: killed"), for logs.
func describeState(ps *os.ProcessState, err error) string {
	if err != nil {
		return "wait failed: " + err.Error()
	}
	if ps == nil {
		return "unknown"
	}
	return ps.String()
}
```

`Adopt`: `alreadyExited` → `fmt.Sprintf("exit code %d (before upgrade)", exitCode)`.
The wait branch → `describeState(state, err)`.

`onPaneExit`, after `_ = p.Close()` (which waits for the reap, up to the grace):

```go
pid, status := p.ExitSummary()
slog.Info("pane ended", "paneID", id, "pid", pid, "status", status, "how", "exited")
...
if shouldClose {
	drainAnswered(answered)
	_ = s.CloseFor(ReasonLastPaneExited, "paneID", id, "pid", pid, "status", status)
}
```

The `removePaneLocked` goroutine logs the same line with `"how", "closed"`
after its `p.Close()`.

**Tests (write first, watch fail):**
- ptyx: `TestExitDescriptionNamesSignal`: spawn `sh -c 'kill -KILL $$'`,
  wait on `Done()`, `ExitDescription()` == `"signal: killed"`.
  `TestExitDescriptionExitStatus`: `sh -c 'exit 3'` → `"exit status 3"`.
- server: a server with one real pane running `sh -c 'exit 7'` and no keep.
  After the server closes, `CloseReason()` is `ReasonLastPaneExited` with
  attrs containing `status` = `"exit status 7"`. Use the existing helper that
  spawns real panes. If none fits, `NewServer(nil, "/bin/sh", t.TempDir())`
  plus `spawnPaneWithSpecLocked` under `s.mu`, the way `control_test.go` does.

**Verification — automated:**
- [x] New tests fail before implementation — **`p.ExitDescription undefined`; `attrs = [paneID 1], want ... status="exit status 7"`**. Adaptations: `describeState` prefers the ProcessState over Wait's error (Wait errors on any non-zero exit); pane attr renamed `pid`→`panePID` because the exits record already has the writer's `pid`.
- [x] `go test ./internal/server/... -run 'ExitDescription|LastPane' -v` passes — **ptyx TestExitDescription + TestExitCode PASS; TestLastPaneExitRecordsStatus PASS; full ./internal/server ok**
- [x] `make quick` passes — **exit 0**

**Verification — manual:**
- [x] (run by agent, scratch TMPDIR) Scratch session: `bin/wideboi -L p4 split sh -c 'sleep 1; exit 5'`. The
      server log shows `pane ended ... status="exit status 5"` and
      `exits.log` shows `server exit reason=last-pane-exited status="exit status 5"`

---

## Phase 5: the client says how it ended

Delivers: `client exit` records, the owner's `server reaped` record, and a
visible notice when the session is lost.

**Files:**
- Modify: `cmd/wideboi/spawn.go` — the channel carries `serverExitStatus{Code int; Desc string}`.
  The reaper records `server reaped` (client log + `exits.log`).
- Modify: `cmd/wideboi/main.go`:
  - `serverExitCode` reads `.Code`; `runClient` takes `<-chan serverExitStatus`.
  - `runClient` gets `endReason string` and a deferred `client exit` record.
  - The give-up paths print the notice.
- Test: `cmd/wideboi/*_test.go` for any `spawnServer`/`serverExitCode` callers
  (update types), plus `scripts/attachcheck.py` (new and extended cases).

**Key changes:**

```go
type serverExitStatus struct {
	Code int    // ProcessState.ExitCode(): -1 for a signal death
	Desc string // ProcessState.String(): "exit status 0", "signal: terminated"
}

// spawn.go reaper
go func() {
	err := cmd.Wait()
	st := serverExitStatus{Code: -1, Desc: "wait failed: " + fmt.Sprint(err)}
	if cmd.ProcessState != nil {
		st = serverExitStatus{Code: cmd.ProcessState.ExitCode(), Desc: cmd.ProcessState.String()}
	}
	slog.Info("server reaped", "serverPID", cmd.Process.Pid, "status", st.Desc)
	if socket != "" {
		_ = logger.AppendExit(socket, "client", "server reaped", "serverPID", cmd.Process.Pid, "status", st.Desc)
	}
	code <- st
}()
```

`runClient`:

```go
endReason := "returned"
defer func() {
	if sig := guard.Signal(); sig != nil {
		endReason = "signal " + sig.String()
	}
	_ = logger.AppendExit(cfg.Socket, "client", "client exit", "reason", endReason, "owner", owner, "err", errString(retErr))
}()
```

`runClient`'s result is renamed to `retErr` (named return). The deferred
record is registered after `guard` exists. `endReason` is set at each
existing exit site: `"quit"` (routeQuit), `"detached"`, `"connection failed"`
(the `currentConn.Err()` branch), `"server closed the connection"` (owner,
`serverExit` fired), `"server gone, reconnect failed: <err>"`, and
`"startup failed"` (the `!gotMsg` owner branch).

Notice. Same pattern as `printDetachNotice`: restore first, then print.

```go
// printEndNotice says a session ended under the user rather than at
// their request, and where to find out why.
func printEndNotice(w io.Writer, socket, why string) {
	fmt.Fprintf(w, "[wideboi: the session at %s ended (%s); see %s]\n", socket, why, logger.ExitsPath(socket))
}
```

- **Reconnect-failed path** (`main.go:831-834`):
  1. If owner, wait for the server's status with
     `st, ok := awaitServerStatus(serverExit, reapCeiling)` (the same select
     as `serverExitCode`, returning the struct and whether it arrived).
  2. `why := "server gone"`, plus `"; server " + st.Desc` if it arrived.
  3. `signalled := stopped.Load(); _ = guard.Stop()`.
  4. `if !signalled { printEndNotice(os.Stderr, cfg.Socket, why) }`, then
     `return nil`. Exit 0 is unchanged.
- **Owner `server closed the connection` branch** (`main.go:819-822`): take
  `st` from the channel (it's the case that fired). If `st.Desc != "exit status 0"`,
  restore the terminal and print the notice with `"server " + st.Desc`. Return
  nil in both cases.

**Tests (write first, watch fail):**
- `TestPrintEndNotice`: output names the socket, the reason, and
  `ExitsPath(socket)`.
- attachcheck, extend `case_kill_session_*` (the one at ~`attachcheck.py:735-755`):
  the client tail after `ALT_SCREEN_EXIT` contains `the session at` and
  `exits.log`. Exit is still 0 (existing assertion).
- attachcheck, new `case_owner_reports_signalled_server`:
  1. `Client(plain=True)`, wait for the prompt.
  2. Find the spawned server: the child of the client pid whose argv has
     `server --owner-fd`. Use `pgrep -P <client pid>`; the harness already
     pins the environment, so there is exactly one child.
  3. `os.kill(server_pid, SIGTERM)` and wait for the client to exit (ceiling 5s).
  4. Assert the tail contains `signal: terminated` and `exits.log`, and exit
     status is 0.
  5. Assert `exits.log` in the runtime dir has `server signalled`,
     `server reaped ... status="signal: terminated"`, and `client exit`.
- Go e2e, if the attachcheck case proves awkward: none planned. attachcheck is
  the harness for owner-client behaviour.

**Verification — automated:**
- [x] New checks fail before implementation (record messages) — **kill-session case: `the client printed no end notice naming exits.log`; owner case: `exits.log lacks 'msg="client exit"'`**. Adaptations: the client's signal record is written in its guard (a deferred record never runs once `awaitReRaise` re-raises); the give-up path sets `hungUp` before teardown so the owner guard does not try a shutdown over the dead connection.
- [x] `make quick` passes — **exit 0**
- [x] `make attach-check` passes, run 4 times (timing-sensitive, per CLAUDE.md) — **28 passed, 0 failed ×4**
- [x] `make verify-exit` passes — **exit 0**

**Verification — manual:**
- [ ] Les: start `wideboi` on a scratch session, `kill -TERM` its server pid
      from another terminal. The terminal is restored, and the notice names
      `signal: terminated` and the `exits.log` path
      *(agent-driven equivalent via the attachcheck harness: exit 0, notice `ended (server gone; server signal: terminated); see …/exits.log`, and exits.log holds started → signalled → exit → reaped → client exit)*

---

## Phase 6: sweep removals are recorded

Delivers: every socket, token or log the auto-cleanup sweep removes is
recorded, with the sweeping session.

**Files:**
- Modify: `cmd/wideboi/cleanup.go` — `runAutoCleanupSweep(dir, bySocket string)`
  writes each removal line to `exits.log` via a line-splitting writer.
- Modify: `cmd/wideboi/main.go:494` — pass `cfg.Socket`.
- Test: `cmd/wideboi/cleanup_test.go`

**Key changes:**

```go
// exitsWriter turns cleanup's "removed dead socket x.sock" lines into
// exits records, so a sweep that removes a socket someone still wanted
// leaves a trace.
type exitsWriter struct{ exitsSocket, by string }

func (w exitsWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimSpace(string(p)), "\n") {
		if line != "" {
			_ = logger.AppendExit(w.exitsSocket, "sweep", "sweep removed", "what", line, "by", logger.SessionOf(w.by))
		}
	}
	return len(p), nil
}

func runAutoCleanupSweep(dir, bySocket string) error {
	// exitsSocket only locates exits.log: ExitsPath takes Dir of it.
	return cleanupDeadArtifacts(exitsWriter{exitsSocket: filepath.Join(dir, "sweep.sock"), by: bySocket}, dir, false)
}
```

`cleanupDeadArtifacts` already emits one `Fprintf` per removal, so each
`Write` is one line. The split just keeps that robust.

**Tests (write first, watch fail):**
- `TestAutoCleanupSweepRecordsRemovals`: in a temp dir, create a dead
  `ghost.sock` (a regular file named `.sock` doesn't answer a dial) and a
  `ghost.web-token`, then run `runAutoCleanupSweep(dir, dir+"/me.sock")`.
  `exits.log` has two `sweep removed` lines with `by=me`, naming
  `ghost.sock` and `ghost.web-token`.

**Verification — automated:**
- [x] New test fails before implementation — **`too many arguments in call to runAutoCleanupSweep`**. Also extended `TestAutoCleanupSweepPreservesDeadLogs` to assert exits.log survives both the sweep and `runCleanup`.
- [x] `go test ./cmd/wideboi -run 'Sweep|Cleanup' -v` passes — **5 PASS**
- [x] `make check` passes (full gate) — **exit 0**

**Verification — manual:**
- [x] None beyond the test: the behaviour is file-only

---

## Phase 7: retry the reproduction with instrumentation

Delivers: evidence from the instrumented build, and instructions Les can
follow to reproduce by hand.

**Files:**
- Modify: `scripts/repro-gotest-in-pane.sh` — print the scratch `exits.log` at the end.
- Modify: `docs/dev-sessions/.../notes.md` — findings.
- Modify: `docs/LESSONS.md` — add two lessons: "a clean exit deletes its own
  logs; read `exits.log`", and the `sun_path` TMPDIR length gotcha.

**Steps:**
1. Run `scripts/repro-gotest-in-pane.sh` 4 times. Record the outcome and the
   `exits.log` contents each time.
2. Variant closer to the incident: an owner-attached session driven through
   `scripts/ptylib.py`, with `--websocket 127.0.0.1:0`, running the same
   `go test` in a pane. If this needs more than a small script, stop and hand
   the manual repro to Les instead.
3. Write up for Les: build from this branch, `tail -f
   $TMPDIR/wideboi-$(id -u)/exits.log` in a separate terminal (not inside
   wideboi), then run the code-review workload as before.

**Verification — automated:**
- [x] Repro script ran 4 times; outcomes recorded in `notes.md` — **in-pane script 4/4 survived (after the race fix; before it, go test itself failed 2/4 on the upgrade e2e); owner+websocket variant `scripts/repro-owner-gotest.py` 4/4 survived, 0 sweep removals**

**Verification — manual:**
- [ ] Les runs the manual repro with the instrumented build and shares `exits.log`
