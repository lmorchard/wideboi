# Detach from plain wideboi Implementation Plan

**Goal:** Plain `wideboi` spawns a background server it owns, so `C-b d`
works everywhere. Nothing it spawned outlives the owning client unless that
client detached.

**Approach:** The session is always client-over-socket. Two explicit
lifecycle messages, `MsgDetach` and `MsgShutdown`, are acknowledged by the
server hanging up. The owner reaches the server over an inherited socketpair,
and EOF on it without a prior detach ends the session. The server arms the
same `hostterm.Guard` the in-process path used, and the in-process path is
deleted.

**Tech stack:** Go (`syscall.Socketpair`, `net.FileConn`, `os/exec`
ExtraFiles + Setsid), gob wire, Python pty harness (`scripts/*.py`).

**Constants used throughout (all in `cmd/wideboi`):**
- `signalExitMargin = 500ms` (exists, main.go:29)
- `shutdownCeiling = server.CloseGrace + server.CloseResidual + signalExitMargin` (≈4s). This is the budget `run` sleeps today (main.go:440-446).
- `detachCeiling = 2 * time.Second`

One commit per phase: `Phase N: <name>`. Every pty-harness change gets four
runs before it is ticked (CLAUDE.md: "A timing or concurrency change is
unverified until it has repeated").

---

## Phase 1: Shutdown end to end — `MsgShutdown`, server signal guard, `kill-session`

The first slice that works on its own: an explicit `wideboi server` can be
shut down cleanly, both by `wideboi kill-session` and by a signal, and the
signal path now reaps escapees. Today it takes Go's default disposition and
skips `srv.Close()` entirely.

**Files:**
- Modify: `internal/protocol/messages.go` — add `MsgShutdown`
- Modify: `internal/protocol/wire_test.go` — add to `wireTypes`
- Modify: `internal/transport/socket.go` — `gob.Register(protocol.MsgShutdown{})`
- Modify: `internal/transport/wire_test.go` — roundtrip `MsgShutdown` as a `ClientMessage`
- Modify: `internal/server/server.go`:
  - lifecycle messages are handled in `handleClientConnLoop`
  - `Close` closes client transports after reaping
- Create: `internal/server/lifecycle_test.go`
- Create: `cmd/wideboi/hangup.go` — `hangUp`, the ceilings
- Modify: `cmd/wideboi/main.go`:
  - `runServer` arms a guard
  - `kill-session` subcommand, `runKillSession`, help text
- Modify: `cmd/wideboi/cli_test.go` — `kill-session` parses as a subcommand
- Modify: `cmd/wideboi/main_test.go` — `TestKillSessionShutsDownAServer`
- Modify: `scripts/attachcheck.py`:
  - plant an escapee in `case_server_reaps_its_panes_on_signal`
  - new kill-session cases

**Key changes:**

```go
// messages.go, beside the other client→server messages
// MsgShutdown asks the server to end the session: reap every pane, hang
// up on every client, and exit. The hang-up is the acknowledgement --
// it happens only after the reaping has finished.
type MsgShutdown struct{}
```

(Probed: gob encodes and decodes an empty struct through an interface
without complaint.)

```go
// server.go -- handleClientConnLoop, the receive branch
case msg, ok := <-tp.ClientSendChan():
	if !ok {
		s.dropClient(tp)
		return
	}
	if _, ok := msg.(protocol.MsgShutdown); ok {
		// Close hangs up on every transport, this one included, and
		// only after reaping. Called here, not under s.mu, for the
		// same reason the Close below is (#43).
		_ = s.Close()
		return
	}
	s.handleClientMsg(ctx, msg)

// dropClient removes tp and closes it. It is the existing !ok body,
// lifted out unchanged (comment included) so Phase 2's detach can
// share it.
func (s *Server) dropClient(tp transport.Transport) {
	s.mu.Lock()
	s.removeTransportLocked(tp)
	s.mu.Unlock()
	if cl, ok := tp.(io.Closer); ok {
		_ = cl.Close()
	}
}
```

At the end of `Server.Close`, inside `closeOnce`, after the escapee
`kill -9` loop:

```go
		// Hang up on every client last. For a client waiting on a
		// shutdown, the closed connection is the only acknowledgement
		// it gets, so it must not arrive until the reaping above has
		// finished.
		s.mu.Lock()
		tps := s.transports
		s.transports = nil
		s.mu.Unlock()
		for _, tp := range tps {
			if cl, ok := tp.(io.Closer); ok {
				_ = cl.Close()
			}
		}
```

```go
// cmd/wideboi/hangup.go
package main

// shutdownCeiling bounds the wait for a server to reap its panes and
// hang up: the pane grace, the kill residual, and the margin, the same
// budget the in-process path used to sleep.
const shutdownCeiling = server.CloseGrace + server.CloseResidual + signalExitMargin

// detachCeiling bounds the wait for the server to acknowledge a detach,
// which involves no reaping.
const detachCeiling = 2 * time.Second

// hangUp sends msg -- MsgDetach or MsgShutdown -- and waits for the
// server to close the connection, which is its acknowledgement. It
// reports whether that happened within ceiling. It drains whatever
// arrives in the meantime; nothing is drawn after a hang-up.
func hangUp(ctx context.Context, conn *transport.ClientSocketConn, msg transport.ClientMessage, ceiling time.Duration) bool {
	if !conn.SendClient(ctx, msg) {
		return false
	}
	timer := time.NewTimer(ceiling)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-conn.ServerSendChan():
			if !ok {
				return true
			}
		case <-timer.C:
			return false
		}
	}
}
```

```go
// main.go -- runServer, after NewServer/SetLayout/SetWidthPresets
	// The server is as responsible for its panes as the in-process
	// binary was, and there is no terminal to restore -- teardown is
	// the whole job. Without this, SIGTERM took Go's default
	// disposition, srv.Close never ran, and a nohup'd job in a pane
	// outlived the server.
	guard := hostterm.NewGuard(func() error {
		err := srv.Close()
		_ = sl.Close() // the re-raise skips defers, and a corpse socket is litter
		return err
	})
	defer guard.Stop()
	guard.Arm(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
```

```go
// main.go
func runKillSession(cfg config.Config) error {
	conn, err := net.Dial("unix", cfg.Socket)
	if err != nil {
		return fmt.Errorf("no wideboi server running at %s: %w", cfg.Socket, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)
	if !hangUp(ctx, cc, protocol.MsgShutdown{}, shutdownCeiling) {
		return fmt.Errorf("wideboi server at %s did not shut down within %s", cfg.Socket, shutdownCeiling)
	}
	return nil
}
```

`parseCLI` adds `"kill-session"` to the subcommand set. `main` dispatches
it. `printHelp` adds
`wideboi [flags] kill-session  End the session: close every pane and stop the server`.

**Tests (written first):**

```go
// internal/server/lifecycle_test.go -- white-box, like transport_close_test.go
func newBareServer(tps ...transport.Transport) *Server {
	return &Server{
		strip: layout.NewStrip(), panes: make(map[int]*Pane),
		escapees: make(map[int]struct{}), stopCh: make(chan struct{}),
		transports: tps,
	}
}

// A shutdown hangs up on every client, not only the one that asked:
// the other attached clients must see their connection end too.
func TestShutdownClosesServerAndHangsUpEveryClient(t *testing.T) {
	asker := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	other := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(asker, other)
	asker.ClientSend <- protocol.MsgShutdown{}
	runLoopUntilReturn(t, s, asker) // runs handleClientConnLoop, fails after 2s
	select {
	case <-s.stopCh:
	default:
		t.Fatal("MsgShutdown did not close the server")
	}
	if !asker.closed.Load() || !other.closed.Load() {
		t.Errorf("shutdown left a client connected: asker=%v other=%v",
			asker.closed.Load(), other.closed.Load())
	}
}
```

`runLoopUntilReturn` is the goroutine-plus-2s-select block already written
twice in `transport_close_test.go`, lifted into a helper in the new file.
(The existing two tests stay as they are.)

```go
// cmd/wideboi/main_test.go -- real socket, real panes, shortened grace
// SetCloseGrace is test-only inside package server, so this runs with the
// real 2s grace: /bin/sh ignores SIGTERM, so expect ~2.5s.
func TestKillSessionShutsDownAServer(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "s.sock")
	sl, err := transport.NewSocketListener(sockPath)
	if err != nil {
		t.Fatalf("NewSocketListener: %v", err)
	}
	defer sl.Close()
	srv := server.NewServer(nil, "/bin/sh", "")
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.ListenSocket(ctx, sl)
	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run(ctx) }()

	// One attached client, so there are panes to reap and a bystander
	// to be hung up on.
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)
	client.NewClient(cc, 80, 24, "C-b").Attach(ctx)
	select {
	case <-cc.ServerSendChan():
	case <-time.After(3 * time.Second):
		t.Fatal("attached client never heard from the server")
	}

	if err := runKillSession(config.Config{Socket: sockPath}); err != nil {
		t.Fatalf("kill-session: %v", err)
	}
	select {
	case <-runDone:
	case <-time.After(shutdownCeiling):
		t.Fatal("server kept running after kill-session")
	}
	// The bystander's stream must end: drain until closed.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-cc.ServerSendChan():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("kill-session did not hang up on the attached client")
		}
	}
}
```

Two cases in `scripts/attachcheck.py`:
- `case_kill_session_ends_the_session`:
  - start `Server()` and one `Client()`
  - record `descendants(srv.proc.pid)`
  - run `subprocess.run([BIN, "kill-session"], env=bin_env(), timeout=10)`
  - assert: rc 0; `srv.proc.wait(timeout=8)` returns; `still_alive(kids, 2.0)` is empty; the socket file is gone; `wait_for_exit(c.pid, 3.0)` shows the attached client exited 0
- `case_kill_session_without_a_server_says_so`: rc != 0, and the message contains `no wideboi server running`.

`case_server_reaps_its_panes_on_signal` gets teeth:
- Before `srv.stop()`, type `nohup sleep 987653 >/dev/null 2>&1 &\r` into the client.
- Wait for it to show up in `descendants(srv.proc.pid)` (up to 5s).
- Then assert it is gone after `stop()`.

**Red proof:** with only the test changes applied, the escapee case must fail
(`sleep 987653` survives SIGTERM), and the kill-session cases must fail
(unknown subcommand → it tries to start a session with no tty). Record both in
`notes.md`.

**Verification — automated:**
- [x] Red proof recorded for the escapee case and the kill-session cases — **escapee survived SIGTERM; kill-session tried to start a session; server test timed out at 2s** (notes.md)
- [x] `go test ./internal/server -run 'Shutdown|Dropped' -v` passes — **ok**
- [x] `go test ./cmd/wideboi -run 'KillSession|ParseCLI' -v` passes — **ok (cmd/wideboi 4.2s)**
- [x] `make quick` passes — **via make check, exit 0**
- [x] `python3 scripts/attachcheck.py` passes, 4 runs — **10/10 ×4, ~33s each** (after pinning SHELL/PS1 for Server(); see notes)
- [x] `make check` passes — **exit 0, 34.7s**

**Verification — manual:**
- [ ] `wideboi server &`, `wideboi attach`, then `wideboi kill-session` from another terminal: the attached client drops back to the shell cleanly
- [ ] `wideboi kill-session` with nothing running prints a one-line error

---

## Phase 2: Explicit detach, and `q` means quit, for attached clients

`C-b d` sends `MsgDetach` and waits for the server to hang up. `C-b q` sends
`MsgShutdown`, so it now does what its label says in attached clients.
`wideboi attach` gets a signal guard, so a SIGTERM restores the terminal
instead of stranding the alt screen. Ownership doesn't exist yet, so
server-side `MsgDetach` is only "drop and hang up". Phase 3 gives it meaning
for the owner.

**Files:**
- Modify: `internal/protocol/messages.go` — add `MsgDetach`
- Modify: `internal/protocol/wire_test.go`, `internal/transport/socket.go`, `internal/transport/wire_test.go` — register and roundtrip it
- Modify: `internal/server/server.go` — `handleClientConnLoop` handles `MsgDetach`
- Modify: `internal/server/lifecycle_test.go` — `TestDetachHangsUpOnlyThatClient`
- Modify: `cmd/wideboi/main.go`:
  - `runAttach` separates quit from detach
  - add the guard and `stopped`/`hungUp` state
- Modify: `scripts/attachcheck.py`:
  - `Client.quit()`
  - `case_quit_from_an_attached_client_ends_the_session`

**Key changes:**

```go
// messages.go
// MsgDetach tells the server this client is leaving and the session is
// not. The server hangs up on the sender; for the client that owns the
// session it also gives the ownership up, for good.
type MsgDetach struct{}
```

```go
// server.go -- handleClientConnLoop, after the MsgShutdown check
	if _, ok := msg.(protocol.MsgDetach); ok {
		s.dropClient(tp)
		return
	}
```

The `runAttach` loop body (it becomes `runClient` in Phase 3; the structure
here carries over unchanged):

```go
	var (
		screenLock sync.Mutex
		stopped    atomic.Bool // a signal's teardown has begun
		hungUp     atomic.Bool // the connection is over; nothing left to tell the server
	)
	...
	// Registered after `defer cancel()` so it runs first: teardown still
	// needs a live ctx to send.
	guard := hostterm.NewGuard(func() error {
		stopped.Store(true)
		screenLock.Lock()
		defer screenLock.Unlock()
		scr.ExitAltScreen()
		_ = scr.Flush()
		return t.Stop()
	})
	defer guard.Stop()
	guard.Arm(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	...
		case msg, ok := <-cConn.ServerSendChan():
			if !ok {
				hungUp.Store(true)
				if stopped.Load() {
					// A signal's teardown is running on the guard's
					// goroutine and will re-raise when it finishes.
					// Returning first would exit 0 and beat it.
					_ = guard.Stop()
					time.Sleep(signalExitMargin)
				}
				if err := cConn.Err(); err != nil { ... unchanged ... }
				return nil
			}
	...
				case routeDetach:
					if !hangUp(ctx, cConn, protocol.MsgDetach{}, detachCeiling) {
						slog.Warn("server did not acknowledge the detach", "ceiling", detachCeiling)
					}
					hungUp.Store(true)
					return nil
				case routeQuit:
					if !hangUp(ctx, cConn, protocol.MsgShutdown{}, shutdownCeiling) {
						return fmt.Errorf("wideboi server did not shut down within %s", shutdownCeiling)
					}
					hungUp.Store(true)
					return nil
```

Only Phase 3's owner teardown reads `hungUp`. It is set here so that the
loop's structure doesn't change between phases.

The frame branch skips drawing when `stopped.Load()`, as `run` did
(main.go:482-492).

**Tests (written first):**

```go
// lifecycle_test.go
// A detach ends one connection and nothing else: the server keeps
// running and the other client stays attached.
func TestDetachHangsUpOnlyThatClient(t *testing.T) {
	leaver := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	stayer := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(leaver, stayer)
	leaver.ClientSend <- protocol.MsgDetach{}
	runLoopUntilReturn(t, s, leaver)
	if !leaver.closed.Load() {
		t.Error("detaching client was not hung up on")
	}
	if stayer.closed.Load() {
		t.Error("a detach hung up on a different client")
	}
	select {
	case <-s.stopCh:
		t.Fatal("a detach stopped the server")
	default:
	}
}
```

Red: before the `MsgDetach` branch exists, `handleClientMsg` ignores the
message, the loop never returns, and `runLoopUntilReturn` fails at 2s.

`scripts/attachcheck.py`:
- `Client.quit(timeout=8.0)` mirrors `detach()` but writes `q`.
- `case_quit_from_an_attached_client_ends_the_session`:
  - start `Server()` and a `Client()`
  - record `descendants(srv.proc.pid)`
  - `c.quit()`
  - assert: the client exited 0; `srv.proc.wait(timeout=8)` returns; the recorded kids are gone; the socket is gone

Red: today `q` detaches, so the server stays alive.

The existing `case_detach_leaves_the_session_running` must stay green
unchanged.

**Verification — automated:**
- [x] Red proof recorded for `TestDetachHangsUpOnlyThatClient` and the quit case — **loop timeout / server left running** (notes.md)
- [x] `go test ./internal/server -run 'Detach|Shutdown|Dropped' -v` passes — **ok**
- [x] `make quick` passes — **via make check, exit 0**
- [x] `python3 scripts/attachcheck.py` passes, 4 runs — **12/12 ×4** (includes the added signalled-client case)
- [x] `make check` passes — **exit 0**

**Verification — manual:**
- [ ] `wideboi server &` + `wideboi attach`:
  - `C-b d` leaves the server running
  - reattach, then `C-b q`: the server is gone (`pgrep -f 'wideboi server'` is empty)
- [ ] `kill -TERM` an attached client from another terminal: the host terminal comes back usable, not stuck in the alt screen, and the server is still running (now also automated: `case_signalled_attached_client_restores_and_detaches`)

---

## Phase 3: Owned sessions — plain `wideboi` spawns its server; delete the in-process path

This is the feature itself. Plain `wideboi` with no server running spawns one
over an inherited socketpair and owns it. Every test that used to run the
in-process binary now runs this path. That is also why the test changes land
in this phase: `make check` cannot be green with half of them.

**Files:**
- Create: `cmd/wideboi/spawn.go` — `spawnServer`
- Modify: `cmd/wideboi/main.go`:
  - `run` becomes dial-or-spawn
  - `runAttach` becomes `runClient(cfg, bindings, conn, owner)`
  - the old in-process body of `run` is deleted
  - `runServer` takes an owner fd
  - `--owner-fd` flag
  - help text
- Modify: `internal/server/server.go` — `owner` field, `SetOwner`, owner EOF ends the session, owner detach gives up ownership
- Modify: `internal/server/lifecycle_test.go` — owner tests
- Modify: `internal/logger/logger.go` — `Path(component)`, used by `Init` and by the startup-failure message
- Modify: `cmd/wideboi/cli_test.go` — `--owner-fd` parses; its value is not mistaken for a subcommand
- Modify: `scripts/ptylib.py` — `server_child(pid)`
- Modify: `scripts/ptycheck.py`:
  - follow grandchildren
  - track the server
  - assert the socket is removed
- Modify: `scripts/smoke.py`:
  - a per-session socket in a per-run dir
  - `quit_and_reap` uses descendants
  - the 80-column case now requires `d detach`
  - update the comments that say a plain wideboi never binds
- Modify: `scripts/golden.py` — update the comment on the never-created socket
- Regenerate: `testdata/golden/startup.txt` (`make golden`, and review the diff)
- Modify: `scripts/attachcheck.py` — owner cases

**Key changes — server:**

```go
// Server gains:
	// owner is the connection of the client that launched this session,
	// or nil when the session is ownerless: started as `wideboi
	// server`, or given up by a detach. Once nil it stays nil;
	// reattaching does not re-own, because nobody's terminal is tied to
	// the session any more.
	owner transport.Transport

// SetOwner marks tp -- already passed to NewServer -- as the owning
// client's connection. If it closes without a MsgDetach first, the
// session ends: that is how an owner killed by SIGKILL, which runs no
// code at all, still takes its panes with it.
func (s *Server) SetOwner(tp transport.Transport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.owner = tp
}
```

`handleClientConnLoop`:

```go
	if !ok {
		if s.dropClient(tp) {
			slog.Info("owning client left without detaching; ending the session")
			_ = s.Close()
		}
		return
	}
	...
	if _, ok := msg.(protocol.MsgDetach); ok {
		s.dropClient(tp) // an owner detaching gives ownership up, not the session
		return
	}

// dropClient now reports whether tp was the owner, and clears ownership
// under the same lock that removes the transport.
func (s *Server) dropClient(tp transport.Transport) (wasOwner bool) {
	s.mu.Lock()
	s.removeTransportLocked(tp)
	if tp == s.owner {
		s.owner = nil
		wasOwner = true
	}
	s.mu.Unlock()
	if cl, ok := tp.(io.Closer); ok {
		_ = cl.Close()
	}
	return wasOwner
}
```

The `MsgDetach` path calls the same `dropClient`, so the owner detaching
clears `s.owner` *before* its EOF arrives. A second `dropClient` for the
same tp (the `!ok` that follows a detach) finds `s.owner` already nil, so
it doesn't end the session. `Close` also sets `s.owner = nil` where it
takes the transports.

Messages on one conn are processed in order by one goroutine, so a
`MsgDetach` is always handled before the EOF that follows it.

**Key changes — spawning (`cmd/wideboi/spawn.go`):**

```go
// spawnServer starts `wideboi server` in the background as the session
// this process will own, and returns this side of the private
// connection to it.
//
// The connection is a socketpair inherited as fd 3, not a dial of the
// listening socket: nothing can race the spawner to it, nothing can
// forge it, and it works before the server has bound anything.
//
// Both ends are made close-on-exec under ForkLock before anything else
// can fork -- darwin has no SOCK_CLOEXEC. If the server inherited *our*
// end too, it would hold its own owner connection open, and the EOF
// that tells it its owner died would never come. ExtraFiles clears the
// flag on fd 3 in the child, which is the one copy it should have.
func spawnServer(args []string) (net.Conn, error) {
	syscall.ForkLock.RLock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("socketpair: %w", err)
	}
	ours := os.NewFile(uintptr(fds[0]), "wideboi-owner")
	theirs := os.NewFile(uintptr(fds[1]), "wideboi-owner-server")
	defer ours.Close()   // FileConn dups it
	defer theirs.Close() // the child has its own copy after Start

	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locating the wideboi binary: %w", err)
	}
	// The user's own flags, verbatim, so the server resolves exactly the
	// config this process did. Env and cwd are inherited.
	cmd := exec.Command(exe, append([]string{"server", "--owner-fd", "3"}, args...)...)
	cmd.ExtraFiles = []*os.File{theirs}
	// Its own session: the host terminal's SIGHUP and ^C belong to the
	// client, which decides what they mean for the session.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Stdin/Stdout/Stderr left nil: /dev/null. The server logs to its file.
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting wideboi server: %w", err)
	}
	// Reap it if it exits while we are alive; if we die first, init does.
	go func() { _ = cmd.Wait() }()

	conn, err := net.FileConn(ours)
	if err != nil {
		return nil, fmt.Errorf("owner connection: %w", err)
	}
	return conn, nil
}
```

**Key changes — `runServer(cfg, ownerFD int)`:**

```go
	if ownerFD >= 0 {
		f := os.NewFile(uintptr(ownerFD), "wideboi-owner")
		conn, err := net.FileConn(f)
		f.Close() // FileConn dups it with close-on-exec; the original would leak into every pane
		if err != nil {
			return fmt.Errorf("owner connection on fd %d: %w", ownerFD, err)
		}
		ownerConn = transport.NewServerSocketConn(conn, 256)
		ownerConn.RunPumps(ctx)
	}
	...
	srv := server.NewServer(ownerConn, cfg.Shell, cwd) // nil-safe: NewServer skips a nil tp
```

`srv.SetOwner(ownerConn)` is called only when it is non-nil. Watch for the
Go typed-nil trap: pass a `transport.Transport` variable that is literally
nil, not a nil `*ServerSocketConn`.

`NewSocketListener` has to move ahead of the owner-fd handling, so that a
bind failure exits before any pane spawns. It logs with `slog.Error` before
returning, because the spawned server's stderr is /dev/null.

`parseCLI`:
- adds `--owner-fd`/`-owner-fd` to the skip-next list
- adds `fs.IntVar(&opts.ownerFD, "owner-fd", -1, "")`
- does not document it in `printHelp`, since it's internal

**Key changes — client (`main.go`):**

```go
func run(cfg config.Config, bindings []keys.Binding) error {
	if conn, err := net.Dial("unix", cfg.Socket); err == nil {
		return runClient(cfg, bindings, conn, false)
	}
	conn, err := spawnServer(os.Args[1:])
	if err != nil {
		return err
	}
	return runClient(cfg, bindings, conn, true)
}

func runAttach(cfg config.Config, bindings []keys.Binding) error {
	conn, err := net.Dial("unix", cfg.Socket)
	if err != nil { ...unchanged message... }
	return runClient(cfg, bindings, conn, false)
}
```

`runClient` is Phase 2's `runAttach` body taking `conn` and `owner`, with
these differences:
- The guard teardown shuts down an owned session before restoring the terminal:

```go
	guard := hostterm.NewGuard(func() error {
		stopped.Store(true)
		// An owner's session dies with it, unless the connection has
		// already ended (a detach, a quit, or the server hanging up).
		// Shut it down *before* restoring the terminal, and wait: the
		// panes are then reaped before this process re-raises, which
		// is the order verify-exit asserts. If the ceiling passes,
		// restore and exit anyway -- the server still sees our EOF,
		// with no detach before it, and ends the session itself.
		var late bool
		if owner && !hungUp.Load() {
			late = !hangUp(ctx, cConn, protocol.MsgShutdown{}, shutdownCeiling)
		}
		screenLock.Lock()
		defer screenLock.Unlock()
		scr.ExitAltScreen()
		_ = scr.Flush()
		err := t.Stop()
		if late {
			fmt.Fprintf(os.Stderr, "wideboi: server did not confirm shutdown within %s\n", shutdownCeiling)
		}
		return err
	})
```

- Startup failure. Track `gotMsg bool`, set on the first message. On `!ok`
  with `owner && !gotMsg && cConn.Err() == nil`, return
  `fmt.Errorf("wideboi server exited during startup; see %s", logger.Path("server"))`.
- `cli.SetDetachable(true)` and `router{detachable: true}` for every client.
  Every session is on a socket now. The `NeedsDetach` machinery stays; see
  NOT doing.
- `cli.Draw(scr, nil, nil)`. Phase 4 removes the parameters.

Delete from `main.go`:
- the old in-process `run` body (main.go:361-500)
- the `transport.NewInProcChannel` use
- the imports only it needed

The `server` import stays, for `runServer` and the ceilings.

`printHelp`: `wideboi [flags]  Start a session (in the background), or attach if one is running`.

**Key changes — `logger.Path`:**

```go
// Path is where component's log lives. Exposed so an error can point at
// the log of a process whose stderr goes nowhere.
func Path(component string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("wideboi-%d", os.Getuid()), component+".log")
}
```

`Init` uses it; its `MkdirAll` stays in `Init`.

**Tests (written first):**

```go
// lifecycle_test.go
// An owner that vanishes without a word (SIGKILL) takes the session with it.
func TestOwnerEOFWithoutDetachEndsTheSession(t *testing.T) {
	owner := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	other := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(owner, other)
	s.SetOwner(owner)
	close(owner.ClientSend)
	runLoopUntilReturn(t, s, owner)
	select {
	case <-s.stopCh:
	default:
		t.Fatal("owner EOF without detach left the session running")
	}
	if !other.closed.Load() {
		t.Error("ending the session did not hang up on the other client")
	}
}

// Once the owner detaches, its EOF is ordinary, and the session is
// ownerless for good.
func TestOwnerDetachGivesUpOwnership(t *testing.T) {
	owner := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(owner)
	s.SetOwner(owner)
	owner.ClientSend <- protocol.MsgDetach{}
	close(owner.ClientSend)
	runLoopUntilReturn(t, s, owner)
	select {
	case <-s.stopCh:
		t.Fatal("an owner's detach ended the session")
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner != nil {
		t.Error("detached owner still recorded as owner")
	}
}

// A non-owner's EOF is a detach, as it always was.
func TestNonOwnerEOFLeavesTheSession(t *testing.T) {
	owner := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	guest := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(owner, guest)
	s.SetOwner(owner)
	close(guest.ClientSend)
	runLoopUntilReturn(t, s, guest)
	select {
	case <-s.stopCh:
		t.Fatal("a non-owner leaving ended the session")
	default:
	}
	if owner.closed.Load() {
		t.Error("a non-owner leaving hung up on the owner")
	}
}
```

Red: `SetOwner` does not exist, so this doesn't compile. Implement the stub
first so the tests fail on the assertions themselves.

`scripts/ptylib.py`:

```python
def server_child(pid: int) -> int | None:
    """The `wideboi server` a plain wideboi spawned, or None yet."""
    for p, pp, cmd in ps_rows():
        if pp == pid and " server" in cmd:
            return p
    return None
```

`scripts/ptycheck.py` `run_check`:
- The startup wait becomes: wait until `server_child(pid)` is non-None and `len(pane_children(srv)) >= 2`, same ceiling.
- `shells = pane_children(srv)`
- `tracked = shells + [escapee] + [(srv, "wideboi server")]`
- `plant_escapee` already walks `descendants(pid)`, which reaches grandchildren.
- After the leak check, add the assertion: `never_sock` must not exist, via `os.path.exists`, polled up to 2s. The server removes the socket on shutdown, and a corpse socket is how a skipped teardown would show up.
- Rename `never_sock` to `sock` and rewrite its comment. It is created now, by the spawned server, per run.
- Update the module docstring: assertion 3's shells are the server's children, and the server itself is tracked.
- `find_stray_wideboi` is unchanged. A server that outlives its owner is reparented to pid 1, which the scan already reports.

Red proof for ptycheck: run it against a build where the client's guard skips
the `hangUp(MsgShutdown)`. It must fail, either on "reaped" (the 2s window
closes before the EOF-fallback reap finishes) or on the stray scan. Record
which in notes. If it passes, the test has no teeth, so stop and tell Les.

`scripts/smoke.py`:
- `NEVER_SOCK` becomes a per-run `RUNTIME_DIR = tempfile.mkdtemp(prefix="wideboi-smoke-")`, cleaned up with `atexit.register(shutil.rmtree, RUNTIME_DIR, True)` (the attachcheck pattern). Each `Session` gets its own socket, `os.path.join(RUNTIME_DIR, f"s{n}.sock")`, with `n` from an `itertools.count()` under `SPAWNED_LOCK`. Parallel cases must not share a socket: the first would bind it, and the rest would attach to that session.
- `quit_and_reap`: `kids = [p for p, _ in descendants(self.pid)]`, which now includes the server and its shells.
- `case_control_mode_names_every_entry_at_80_columns` must show `b"d detach"`, and its comment says why.
- The comment on `main()`'s `--jobs` option ("a plain wideboi never binds a socket") is rewritten: each session has its own socket.

`scripts/golden.py`: the comment on the socket says it is created and removed
per capture. Then run `make golden` and review the diff. Record in notes
what changed and why.

`scripts/attachcheck.py` gets a `Plain` helper: `spawn_in_pty([BIN], COLS,
ROWS, True, {"WIDEBOI_SOCK": socket_path()})`, the same shape as `Client`,
with `detach()`/`quit()`/`kill()`. The cases:
- `case_plain_wideboi_detaches_and_the_session_survives`:
  1. plain; type marker; record `srv = server_child(plain.pid)` and `descendants(srv)`
  2. `plain.detach()` exits 0
  3. `srv` still alive, and the socket exists
  4. `Client()` shows the marker
  5. `kill-session` returns rc 0
  6. `srv` and the recorded kids are gone; the socket is gone
- `case_plain_wideboi_offers_detach`: `C-b` shows `d detach`
- `case_sigkilled_owner_takes_the_session_with_it`:
  1. plain; plant `nohup sleep 987652 &`; record `srv` and `descendants(srv)`
  2. `os.kill(plain.pid, SIGKILL)`
  3. `still_alive([srv, *kids], within=shutdownCeiling+2 = 6.0)` is empty
  4. the socket is gone
- `case_plain_wideboi_attaches_to_a_running_server`: with `Server()` up, plain wideboi attaches as a non-owner. `plain.kill()` (SIGKILL) leaves `srv` alive. This is the non-owner EOF path.

**Verification — automated:**
- [x] Red proofs recorded: lifecycle owner tests and each new attachcheck case — **owner tests failed on their assertions; owner attachcheck cases failed on "never spawned a server" against the Phase 2 binary** (notes.md)
- [!] ptycheck against the no-shutdown build must fail — **DOES NOT HOLD.** With the client's MsgShutdown handshake disabled, the server's owner-EOF fallback still reaps everything inside ptycheck's 2s window, so ptycheck passes. With *both* disabled it fails loudly (4 survivors + socket left). So ptycheck guards "nothing outlives the owner" but not "reaped before the client dies". Les chose to accept and record this rather than add a timing-sensitive assertion. See notes.md.
- [x] `go test ./internal/server -run 'Owner|Detach|Shutdown|Dropped' -v` passes — **ok**
- [x] `go test ./cmd/wideboi -v` passes — **ok**
- [x] `make quick` passes — **via make check, exit 0**
- [x] `make seam-check` passes (no new client↔server import) — **via make check**
- [x] `make verify-exit` passes, 4 runs — **via make check ×4, all exit 0**
- [x] `make smoke` passes, 4 runs (golden included) — **via make check ×4; 28/28**
- [x] `make attach-check` passes, 4 runs — **via make check ×4; 16/16**
- [x] `make check` passes — **exit 0 ×4, ~51s each**
- [!] Golden diff reviewed and explained in notes.md — **there was no diff.** The snapshot matched unchanged, so nothing was regenerated. See notes.md.

**Verification — manual:**
- [ ] Plain `wideboi`:
  - `C-b` shows `d detach`
  - `C-b d` returns to the shell, and `pgrep -f 'wideboi server'` shows it still running
  - `wideboi` again reattaches to the same panes
- [ ] Plain `wideboi`, then close the terminal window: afterwards `pgrep -f 'wideboi server'` is empty (SIGHUP to the owner ends the session)
- [ ] Plain `wideboi`, `C-b q`: back at the shell, and no server is left
- [ ] Rendering feels the same as before, typing and scrolling included. If it doesn't, file a latency issue rather than tuning it here.

---

## Phase 4: Remove the dead in-process render hooks; docs

Opt-out from TDD: a pure removal of API nothing calls any more, plus
documentation. The existing tests are the guard.

**Files:**
- Modify: `internal/client/client.go`:
  - `Draw(scr HostScreen) bool`
  - `drawToScreenLocked(scr)`
  - `composeFrameLocked(dst, st)`
  - the sliver path always blits `c.mirrors[id].Surface`
  - the cursor always comes from `c.cursorInfos`
- Modify: every `internal/client/*_test.go` call site: `\.Draw\((\w+), nil, nil\)` becomes `.Draw($1)`. Mechanical: 41 sites, all currently `nil, nil`.
- Modify: `cmd/wideboi/main.go` — `cli.Draw(scr)`
- Modify: `internal/server/server.go` — delete `DrawPane` and `CursorInfo`. Their only caller was the deleted `run`, which was verified by grep during planning.
  - Update the doc comments that cite "DrawPane's precedent" (server.go:378, 627, 658) to cite `PaneSize`, which stays and follows the same discipline.
- Modify: `README.md`:
  - plain `wideboi` now runs a background server you can detach from
  - `C-b q` ends the session
  - `wideboi kill-session`
- Modify: `docs/LESSONS.md`:
  - "Teardown is the load-bearing guarantee" gets the restated contract: nothing outlives the owning client unless it detached; an ownerless server lives until `kill-session`, `q`, or a signal; the order is still reap first, then restore, then re-raise, now across a process boundary
  - the gob section's "`make smoke` only ever runs the in-process binary" gets a note that this stopped being true with #25
- Modify: `Makefile` — the `attach-check` comment ("smoke is structurally blind to this seam") and the `verify-exit` comment get the same correction.

**Verification — automated:**
- [x] `grep -rn "DrawPane\|srv.CursorInfo" --include='*.go' .` finds nothing — **0 matches**
- [x] `make check` passes — **exit 0, 51s; 28/28 smoke, 16/16 attach**

**Verification — manual:**
- [ ] README reads correctly for a first-time user: start, detach, reattach, kill

---

## Spec coverage

| Spec requirement | Phase |
|---|---|
| Plain wideboi spawns and owns a server | 3 |
| Existing server: attach as a non-owner | 3 (`case_plain_wideboi_attaches_to_a_running_server`) |
| `C-b d` everywhere; owner detach gives ownership up for good | 2, 3 |
| `C-b q` ends the session from any client | 2 |
| Signal to the owner: shutdown, wait, restore, re-raise | 3 |
| Owner SIGKILL: EOF fallback | 3 |
| Non-owner EOF or signal = detach | 2 (guard), 3 (EOF test) |
| Server signal guard | 1 |
| `kill-session` | 1 |
| No idle timeout | none needed, nothing added |
| In-process path removed | 3, 4 |
| Startup-failure message; no retry on a race | 3 |
| Golden regenerated and reviewed | 3 |

## Noted, not fixing (record in memory / follow-up)

- `Server.Close` empties `s.panes` *before* calling `pollDescendantsLocked`
  (server.go:736-744), so its final descendant poll walks nothing. Escapees
  are caught by the 1s poll or by `ptyx.Kill`'s own walk, not by this call.
- With every client now detachable, `NeedsDetach`/`SetDetachable`/`router.detachable`
  are always true, so the plumbing is dead. That belongs to #44.
