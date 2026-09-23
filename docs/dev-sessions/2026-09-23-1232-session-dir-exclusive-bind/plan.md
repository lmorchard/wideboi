# Named sessions and an exclusive bind Implementation Plan

**Goal:** Several named wideboi sessions per user (#27), and an atomic "who owns this socket" decision so concurrent plain `wideboi` share one session (#86).

**Approach:** A server holds `flock(LOCK_EX|LOCK_NB)` on `<socket>.lock` for its whole life. When it can't get the lock it exits with code 3, and a spawning owner that sees code 3 redials and attaches as a normal client. A session name is shorthand for `$TMPDIR/wideboi-<uid>/<name>.sock`. `wideboi ls` dials those sockets. Logs sit beside the socket.

**Tech stack:** Go (`syscall.Flock`, `os/exec`), the Python pty harness (`scripts/`).

Verification per phase uses `make quick` (fmt, vet, seam, go test; ~5s) and `make check` (the full gate; ~50s). Commit per phase: `Phase N: <name>`. Git signing: prefix commits with `SSH_AUTH_SOCK=/Users/lmorchard/.bitwarden-ssh-agent.sock`.

---

## Phase 1: Exclusive bind via lifetime flock

A second server for a socket can no longer probe, remove or steal it. It exits with code 3 ("session taken"). This closes #86's variant 2 by construction.

**Files:**
- Modify: `internal/transport/socket.go` — lock in `NewSocketListener`, release in `Close`, `ErrSessionTaken`.
- Test: `internal/transport/socket_test.go` — new tests.
- Modify: `cmd/wideboi/main.go` — the `server` subcommand exits with code 3 on `ErrSessionTaken`.
- Modify: `scripts/attachcheck.py` — "a second server refuses to steal the socket" asserts exit code 3.
- Modify: `scripts/ptycheck.py`, `scripts/golden.py` — sockets move from bare `$TMPDIR/wideboi-*-<pid>.sock` into a private `mkdtemp` dir, removed at exit. Otherwise every run leaves a `.lock` behind in `$TMPDIR`.

**Tests first** (`socket_test.go`; use `os.MkdirTemp("/tmp", "wb")` for darwin's sun_path limit, as `TestSocketListenerAndConnRoundTrip` does):

```go
// Two opens of one file conflict under flock even within a process, so
// this exercises the real exclusion without a second binary.
func TestSecondListenerIsRefusedAndLeavesTheSocketAlone(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.sock")
	first, err := NewSocketListener(path)
	if err != nil { t.Fatal(err) }
	defer first.Close()
	before, _ := os.Stat(path)

	_, err = NewSocketListener(path)
	if !errors.Is(err, ErrSessionTaken) { t.Fatalf("second listener: err = %v, want ErrSessionTaken", err) }
	if !strings.Contains(err.Error(), "already listening") { t.Errorf("message %q lost 'already listening'", err) }
	after, serr := os.Stat(path)
	if serr != nil || !os.SameFile(before, after) { t.Fatalf("the first listener's socket was replaced or removed") }
	if c, err := net.Dial("unix", path); err != nil { t.Fatalf("first listener unreachable: %v", err) } else { c.Close() }
}

func TestListenerRebindsOnceTheOwnerCloses(t *testing.T) { /* first.Close(); NewSocketListener(path) succeeds */ }

// A SIGKILLed server leaves its socket file but not its lock.
func TestStaleSocketWithoutALockIsReclaimed(t *testing.T) {
	// l, _ := net.Listen("unix", path); l.(*net.UnixListener).SetUnlinkOnClose(false); l.Close()
	// -> the socket file exists, nobody holds the lock -> NewSocketListener succeeds.
}
```

`TestNewSocketListenerRefusesToRemoveANonSocket` stays as it is.

**Key changes** (`socket.go`):

```go
// ErrSessionTaken means another live server holds the session's lock.
var ErrSessionTaken = errors.New("session taken")

type SocketListener struct {
	listener net.Listener
	path     string
	lock     *os.File // held for the listener's life; see NewSocketListener
	mu       sync.Mutex
	closed   bool
}

// NewSocketListener binds a Unix domain socket at path.
//
// Ownership of path is an exclusive flock on path+".lock", held for as
// long as the server lives. The kernel drops it when the process dies,
// SIGKILL included, so a held lock always means a live server, and a
// socket file whose lock we hold is a corpse, safe to remove. That
// replaced a dial probe, whose probe/remove/listen window let two
// servers starting together each bind, and the second unlink the
// first's socket (#86).
//
// The lock file is never deleted: unlinking it would let a waiter lock
// the old inode while a newcomer locks a fresh one. os.OpenFile sets
// O_CLOEXEC, so pane processes never inherit the lock and can't keep
// the name taken after the server has gone.
func NewSocketListener(path string) (*SocketListener, error) {
	lock, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening lock for %s: %w", path, err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("a wideboi server is already listening at %s: %w", path, ErrSessionTaken)
		}
		return nil, fmt.Errorf("locking %s.lock: %w", path, err)
	}
	// ... existing Lstat / non-socket refusal / Remove / net.Listen,
	// with lock.Close() on every error return ...
	return &SocketListener{listener: l, path: path, lock: lock}, nil
}

// Close: listener.Close(), os.Remove(path), THEN lock.Close(). Remove
// before unlocking, or a successor could bind in between and have its
// fresh socket unlinked by us.
```

Rewrite the stale-socket comment above the removed probe to say it's safe *because we hold the lock*.

`main.go`, `server` case:

```go
// exitSessionTaken is `wideboi server`'s status when another server
// already holds the session. A spawning plain wideboi reads it to
// attach instead of reporting a failure (#86). 1 is any other error;
// 128+n is a signal.
const exitSessionTaken = 3

case "server":
	if err := runServer(cfg, opts.ownerFD); errors.Is(err, transport.ErrSessionTaken) {
		fmt.Fprintln(os.Stderr, "wideboi:", err)
		os.Exit(exitSessionTaken)
	} else {
		fatal(err)
	}
```

`attachcheck.py`: in `case_second_server_refuses_to_steal_the_socket`, replace `returncode == 0` with `returncode != 3` → `fail(f"second server exited {rc}, want 3 (session taken)")`, and keep the "already listening" and first-alive assertions.

`ptycheck.py` / `golden.py`: `run_dir = tempfile.mkdtemp(prefix="wideboi-ptycheck-")`, `sock = os.path.join(run_dir, "s.sock")`, `atexit.register(shutil.rmtree, run_dir, True)`. ptycheck's "server removed its socket" assertion is unchanged.

**Verification — automated:**
- [x] The new transport tests fail before the `socket.go` change (the second listener currently fails with a probe error that isn't `ErrSessionTaken`, and the stale-socket test passes both before and after, which is expected: it guards against a regression) — **Second-listener test failed: `err = a wideboi server is already listening at …, want ErrSessionTaken`; the other two passed as expected**
- [x] `go test ./internal/transport -run 'Listener|Stale' -count=4 -v` passes — **5 tests × 4 PASS**
- [x] `make quick` passes — **all packages ok**
- [x] `python3 scripts/attachcheck.py --only "second server"` passes — **1 passed**
- [x] `make check` passes — **rc=0; smoke 36 passed, attach 20 passed** (the first run failed: ptycheck lacked `import shutil`; fixed)
- [x] After `make check`, `ls $TMPDIR | grep wideboi-ptycheck` shows nothing left behind — **`find -newer marker` empty. Ten older `wideboi-golden-*` dirs from Sep 21 predate this session; left alone**

**Verification — manual:**
- [x] `bin/wideboi server & sleep 1; bin/wideboi server; echo $?` prints "already listening" and `3`; the first server survives — **agent-run on a private socket: rc=3, first alive, kill-session rc=0, only `s.sock.lock` left**

---

## Phase 2: A losing owner attaches instead of erroring

Two plain `wideboi` launched together end up in one session (#86 variant 1).

**Files:**
- Modify: `cmd/wideboi/spawn.go` — `spawnServer` also returns an exit-status channel.
- Modify: `cmd/wideboi/main.go` — `runClient` takes `serverExit <-chan int` in place of `owner bool`; `errSessionTaken`; `run()` redials; `dialWithin`.
- Test: `scripts/attachcheck.py` — new case, plus `Client(gate=...)`.

**Test first** (attachcheck). It must fail on the Phase 1 code, where the loser prints "exited during startup" and exits 1. Run it before changing Go.

```python
def case_two_plain_wideboi_at_once_share_one_session(fail):
    """#86: two plain wideboi on one free socket, released together.
    Both dials fail and both spawn a server; the loser's server finds
    the lock held. The user asked for a wideboi, so the loser attaches
    to the winner's session instead of reporting an error."""
    sock = socket_path()
    if os.path.exists(sock):
        os.remove(sock)
    fifo = os.path.join(runtime_dir(), "go.fifo")
    os.mkfifo(fifo)
    a = b = None
    try:
        a = Client(plain=True, gate=fifo, startup=0.3)
        b = Client(plain=True, gate=fifo, startup=0.3)
        # Opening the write end releases both `: < fifo` at once.
        os.close(os.open(fifo, os.O_WRONLY))
        for name, c in (("first", a), ("second", b)):
            if not c.wait_for(lambda out: focus_pane_id(out, ROWS) is not None):
                fail(f"the {name} plain wideboi never drew a session")
            if wait_for_exit(c.pid, 0.0) is not None:
                fail(f"the {name} plain wideboi exited instead of attaching")
        # Only one spawned server may remain; the loser's exits at once.
        deadline = time.monotonic() + 3.0
        servers = [s for s in (server_child(a.pid), server_child(b.pid)) if s]
        while len(servers) != 1 and time.monotonic() < deadline:
            time.sleep(0.05)
            servers = [s for s in (server_child(a.pid), server_child(b.pid)) if s]
        if len(servers) != 1:
            fail(f"want exactly one server, found {len(servers)}")
        a.type(b"echo race-marker\r")
        if not b.wait_for(lambda out: b"race-marker" in out):
            fail("the two clients are not in the same session")
        if not os.path.exists(sock):
            fail("the session's socket file is gone")
    finally:
        pids = []
        for c in (a, b):
            if c is not None:
                pids += [p for p, _ in descendants(c.pid)]
                c.kill()
        reap_everything(pids)
        os.remove(fifo)
```

`Client.__init__` gains `gate=None`. When it's set, argv becomes `["/bin/sh", "-c", ': < "$0"; exec "$@"', gate, *argv]`, which blocks on opening the fifo until a writer opens it. Register the case in `CASES` after "plain wideboi attaches to a running server".

**Key changes:**

`spawn.go`:

```go
// spawnServer ... also returns exited, which delivers the server's exit
// code once it has been reaped (-1 if a signal killed it). An owner
// whose server quits before saying anything reads it to tell "another
// server already had the session" from a real failure.
func spawnServer(args []string) (net.Conn, <-chan int, error) {
	...
	exited := make(chan int, 1)
	go func() {
		_ = cmd.Wait()
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		exited <- code
	}()
	...
	return conn, exited, nil
}
```

`main.go`:

```go
// errSessionTaken is runClient's report that the server it spawned
// found the session already held; run attaches to that one instead.
var errSessionTaken = errors.New("session taken")

// takenCeiling bounds the wait for the winning server to listen. It
// holds the lock before it binds, so it's normally milliseconds away.
const takenCeiling = 5 * time.Second

// reapCeiling bounds the wait for a spawned server's exit status after
// its owner connection has closed; the reap follows the close promptly.
const reapCeiling = 2 * time.Second

func run(cfg config.Config, bindings []keys.Binding) error {
	if conn, err := net.Dial("unix", cfg.Socket); err == nil {
		return runClient(cfg, bindings, conn, nil)
	}
	conn, exited, err := spawnServer(os.Args[1:])
	if err != nil {
		return err
	}
	err = runClient(cfg, bindings, conn, exited)
	if !errors.Is(err, errSessionTaken) {
		return err
	}
	// Another wideboi started this session between our dial and our
	// server's bind (#86). Join it: the user asked for a wideboi.
	slog.Info("another server took the session first; attaching to it", "socketPath", cfg.Socket)
	conn, err = dialWithin(cfg.Socket, takenCeiling)
	if err != nil {
		return fmt.Errorf("another wideboi took the session at %s first, but it did not answer within %s: %w",
			cfg.Socket, takenCeiling, err)
	}
	return runClient(cfg, bindings, conn, nil)
}

// dialWithin dials socket until it answers or ceiling passes: a wait
// for observed state, with the ceiling as the timeout.
func dialWithin(socket string, ceiling time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(ceiling)
	for {
		conn, err := net.Dial("unix", socket)
		if err == nil || time.Now().After(deadline) {
			return conn, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}
```

`runClient(cfg, bindings, conn, serverExit <-chan int)`: first line `owner := serverExit != nil`, with a doc comment saying the owner is exactly the process that spawned the server. `runAttach` passes `nil`. In the `owner && !gotMsg` branch:

```go
if owner && !gotMsg {
	if serverExitCode(serverExit, reapCeiling) == exitSessionTaken {
		return errSessionTaken
	}
	return fmt.Errorf("wideboi server exited during startup; see %s", logger.Path("server"))
}

// serverExitCode waits up to ceiling for a spawned server's exit code;
// -1 if it doesn't arrive.
func serverExitCode(exited <-chan int, ceiling time.Duration) int {
	select {
	case code := <-exited:
		return code
	case <-time.After(ceiling):
		return -1
	}
}
```

The deferred `guard.Stop()` restores the terminal before `run` re-enters `runClient`, so the loser flickers the alt screen once. That's accepted.

**Verification — automated:**
- [x] The new case fails on Phase 1's code; record which assertion fails — **3/3 fail: `plain wideboi exited instead of attaching: … wideboi server exited during startup`; the revised test (see notes) fails the same way on Phase 1's binary**
- [x] `python3 scripts/attachcheck.py --only "at once"` passes 4 times in a row — **6/6 OK after typing into the winner (the first draft failed 2/4, see notes)**
- [x] `make quick` passes
- [x] `make check` passes — **rc=0; smoke 36, attach 21 passed**

**Verification — manual:**
- [ ] Open two terminals and start `wideboi` in both as close together as possible (or `wideboi & wideboi`). Both show the same panes, and neither prints an error

---

## Phase 3: Per-session logs beside the socket

**Files:**
- Modify: `internal/logger/logger.go` — `Path(socket, component)`, `Init(path, level)`.
- Test: `internal/logger/logger_test.go` — path derivation.
- Modify: `cmd/wideboi/main.go` — three callers (the `Init` calls in `runServer` and `runClient`, and the startup error).
- Modify: `scripts/ptylib.py` — `keep_or_discard(run_dir, rc)` helper.
- Modify: `scripts/attachcheck.py`, `scripts/smoke.py`, `scripts/ptycheck.py`, `scripts/golden.py` — the private run dir is removed only on success, so the logs survive a failure. Replace the `atexit` rmtree with `rc = main(); keep_or_discard(DIR, rc); sys.exit(rc)`. attachcheck's "check client.log and server.log" hint names the new filenames.
- Modify: `README.md:194` — the log location.

**Test first:**

```go
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
```

**Key changes:**

```go
// Path is where component's log for the session at socket lives:
// beside the socket, named after it. Per session, so several sessions
// don't interleave in one file, and a test harness's private socket
// directory gets private logs. Exposed so an error can point at the log
// of a process whose stderr goes nowhere.
func Path(socket, component string) string {
	return strings.TrimSuffix(socket, ".sock") + "." + component + ".log"
}

// Init initializes file-based structured logging to path, recording
// level and above.
func Init(path string, level slog.Level) (*os.File, error) // body as today, minus Path()
```

The callers become `logger.Init(logger.Path(cfg.Socket, "server"), cfg.LogLevel)` and so on. `config.Load` already creates the socket's directory.

```python
def keep_or_discard(run_dir: str, rc: int) -> None:
    """Removes a suite's private run dir on success. On failure it's
    kept, because the servers' and clients' logs are written beside
    their sockets and are the first thing to read."""
    if rc == 0:
        shutil.rmtree(run_dir, True)
    else:
        print(f"logs kept in {run_dir}")
```

In ptycheck, `main()` runs several sizes. Its run dir becomes a module-level `RUN_DIR`, shared by all sizes, each with its own `s<N>.sock`, as smoke does.

**Verification — automated:**
- [x] `go test ./internal/logger -run TestPathSitsBesideTheSocket` fails before the change and passes after — **build failure `have (string, string) want (string)` before; ok after**
- [x] `make quick` passes
- [x] `make check` passes, and `ls $TMPDIR | grep -E 'wideboi-(attach|smoke|ptycheck|golden)'` is empty afterwards — **rc=0; smoke 36, attach 21; `find -newer marker` empty. Keep-on-failure checked directly: a failed run printed `logs kept in …`, and the empty and successful dirs were removed**

**Verification — manual:**
- [ ] Run `wideboi`, then quit. `$TMPDIR/wideboi-$UID/default.server.log` and `default.client.log` have fresh lines, and the old `server.log` doesn't

---

## Phase 4: Session names (`-L` / `--session` / `WIDEBOI_SESSION` / TOML `session`)

**Files:**
- Modify: `internal/config/config.go` — `SessionDir`, `SessionSocketPath`, `SessionName`, `validateSessionName`, the `Session` field, per-layer resolution.
- Test: `internal/config/config_test.go`.
- Modify: `cmd/wideboi/main.go` — the `-L`/`--session` flag and `skipNext`, help text, `printDetachNotice`. Delete the dead `defaultSocketPath()` (main.go:32-43).
- Test: `cmd/wideboi/cli_test.go`.

**Tests first** (`config_test.go`, with a fake `getenv` and an empty config home, as the existing tests do):

```go
func TestSessionNameMapsToASocketInTheSessionDir(t *testing.T) {
	// flags{Session:"work"} -> cfg.Socket == SessionSocketPath("work") == SessionDir()/work.sock
}
func TestSessionAndSocketLayering(t *testing.T) {
	// table: {toml, env, flags} -> want socket or want error
	// toml session=a                      -> SessionSocketPath("a")
	// toml session=a, env WIDEBOI_SOCK=/x -> /x          (later layer wins)
	// env WIDEBOI_SOCK=/x, flag -L b      -> SessionSocketPath("b")
	// env WIDEBOI_SESSION=a, flag -s /y   -> /y
	// flag -L b and flag -s /y            -> error containing "not both"
	// env WIDEBOI_SESSION=a and WIDEBOI_SOCK=/x -> error
	// toml session=a and socket=/x        -> error
}
func TestSessionNameValidation(t *testing.T) {
	// ok: "work", "a.b", "x_1", "default", "9"
	// bad: "", ".hidden", "-x", "a/b", "../up", "sp ace"
}
func TestSessionNameRecognisesOnlySessionDirSockets(t *testing.T) {
	// SessionName(SessionSocketPath("work")) == ("work", true)
	// SessionName("/tmp/elsewhere.sock") == ("", false)
	// SessionName(SessionDir()+"/bad name.sock") == ("", false)
}
```

`cli_test.go`: `TestParseCLIFlags` gains `-L work` and `--session work` cases. `TestParseCLIFlagsWithSubcommands` gains `-L w attach` and `attach -L w`. `TestDetachNoticeNamesTheSocketOnlyWhenNeeded` gains `SessionSocketPath("work")` → the notice contains `wideboi -L work kill-session` and no `-s`. `TestPrintHelp` requires `--session` and `WIDEBOI_SESSION`.

**Key changes** (`config.go`):

```go
// SessionDir holds the sockets of named sessions, and so their locks
// and logs.
func SessionDir() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("wideboi-%d", os.Getuid()))
}

// SessionSocketPath is where the session called name listens.
func SessionSocketPath(name string) string { return filepath.Join(SessionDir(), name+".sock") }

// DefaultSocketPath is the session a bare wideboi means.
func DefaultSocketPath() string { return SessionSocketPath("default") }

var sessionNameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// validateSessionName keeps a name a single, visible path element that
// can't be mistaken for a flag.
func validateSessionName(name string) error {
	if !sessionNameRE.MatchString(name) {
		return fmt.Errorf("session name %q: use letters, digits, '_', '-' and '.', not starting with '.' or '-'", name)
	}
	return nil
}

// ValidSessionName reports whether -L could address name. Shared with
// `wideboi ls`, so both apply one rule.
func ValidSessionName(name string) bool { return validateSessionName(name) == nil }

// SessionName reports the name a user would pass to -L for socket, if
// it is a named session's socket at all.
func SessionName(socket string) (string, bool) {
	if filepath.Dir(socket) != SessionDir() || !strings.HasSuffix(socket, ".sock") {
		return "", false
	}
	name := strings.TrimSuffix(filepath.Base(socket), ".sock")
	return name, ValidSessionName(name)
}

// applySessionLayer applies one precedence layer's choice of session:
// a name, a path, or neither. Both at once is ambiguous, so it's an
// error rather than a silent preference.
func applySessionLayer(cfg *Config, layer, session, socket string) error {
	switch {
	case session != "" && socket != "":
		return fmt.Errorf("%s sets both a session name (%q) and a socket path (%q); set one, not both", layer, session, socket)
	case session != "":
		if err := validateSessionName(session); err != nil {
			return fmt.Errorf("%s: %w", layer, err)
		}
		cfg.Socket = SessionSocketPath(session)
	case socket != "":
		cfg.Socket = socket
	}
	return nil
}
```

- `Config` gains ``Session string `toml:"session"` ``, doc: "as configured; Socket is what's used". `ConfigFlags` gains `Session string`.
- In `Load`, replace the three `Socket` assignments (TOML at 115-117, env at 144-146, flag at 161-163) with `applySessionLayer(&cfg, "config file", fileCfg.Session, fileCfg.Socket)`, `applySessionLayer(&cfg, "environment", getenv("WIDEBOI_SESSION"), getenv("WIDEBOI_SOCK"))` and `applySessionLayer(&cfg, "command line", flags.Session, flags.Socket)`, each returning its error.

`main.go`:
- In `parseCLI`'s skipNext list add `arg == "-L" || arg == "-session" || arg == "--session"`. Add `fs.StringVar(&opts.flags.Session, "L", "", "session name")` and the same for `"session"`.
- Help flags: `-L, --session <name>   Session to start or attach to (default: "default"; its socket is $TMPDIR/wideboi-<uid>/<name>.sock)`. The `-s` line becomes "Unix domain socket path, instead of a session name". Env: `WIDEBOI_SESSION  Session name override`.
- `printDetachNotice`:

```go
// ... The commands name the session the way the user would: nothing for
// the default, -L for another named session, -s for a socket elsewhere.
func printDetachNotice(w io.Writer, socket string) {
	target := " -s " + socket
	if name, ok := config.SessionName(socket); ok {
		target = " -L " + name
		if name == "default" {
			target = ""
		}
	}
	fmt.Fprintf(w, "[wideboi detached; the session is still running at %s]\n", socket)
	fmt.Fprintf(w, "[reattach: wideboi%s   end it: wideboi%s kill-session]\n", target, target)
}
```

**Verification — automated:**
- [x] The new config and cli tests fail first (they don't compile, then they fail on assertions), then pass — **config: 8 layering subtests plus the mapping test failed on assertions once the helpers existed; cli: `flag provided but not defined: -L`, help missing `-L, --session`; all pass now**
- [x] `make quick` passes
- [x] `make check` passes — **rc=0; smoke 36, attach 21**

**Verification — manual:**
- [ ] `wideboi -L work`, detach: the notice says `wideboi -L work`. `wideboi -L work` reattaches. `wideboi -L work kill-session` ends it
- [x] `wideboi -L work -s /tmp/x.sock` errors with "not both" — **agent-run: `command line sets both a session name ("work") and a socket path …; set one, not both`, rc=1**
- [x] `wideboi -L ../x` errors with the name rule — **agent-run: `command line: session name "../x": use letters, digits, …`, rc=1**

---

## Phase 5: `wideboi ls`

**Files:**
- Create: `cmd/wideboi/sessions.go` — `listSessions`, `runList`.
- Test: `cmd/wideboi/sessions_test.go`.
- Modify: `cmd/wideboi/main.go` — the `ls`/`list-sessions` subcommand (the detection loop at 76, the dispatch in `main`, help).
- Modify: `cmd/wideboi/cli_test.go` — subcommand parsing.
- Test: `scripts/attachcheck.py` — a named-sessions case.
- Modify: `scripts/attachcheck.py` — `Server(args=(), env=None, sock=None)`.

**Test first** (`sessions_test.go`):

```go
func TestListSessionsNamesOnlyLiveSessions(t *testing.T) {
	dir, _ := os.MkdirTemp("/tmp", "wb") // sun_path limit
	t.Cleanup(func() { os.RemoveAll(dir) })
	live, err := transport.NewSocketListener(filepath.Join(dir, "alive.sock"))
	if err != nil { t.Fatal(err) }
	defer live.Close()
	go func() { for { c, err := live.Accept(); if err != nil { return }; c.Close() } }()
	// A corpse: a socket file nobody answers.
	l, _ := net.Listen("unix", filepath.Join(dir, "dead.sock"))
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()
	os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0600)

	got, err := listSessions(dir)
	if err != nil { t.Fatal(err) }
	if want := []string{"alive"}; !slices.Equal(got, want) { t.Fatalf("listSessions = %v, want %v", got, want) }
}

func TestListSessionsOfAMissingDirIsEmpty(t *testing.T) { /* listSessions("/nonexistent/x") == nil, nil */ }
```

**Key changes** (`sessions.go`):

```go
// listSessions names the live sessions in dir, sorted: the *.sock files
// with valid session names that answer a dial. A socket nobody answers
// is a dead server's leftover. It's skipped, not removed; the next
// server to bind that name reclaims it under the lock.
func listSessions(dir string) ([]string, error) {
	socks, err := filepath.Glob(filepath.Join(dir, "*.sock"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, sock := range socks { // Glob sorts
		name := strings.TrimSuffix(filepath.Base(sock), ".sock")
		if !config.ValidSessionName(name) { // only what -L can address
			continue
		}
		conn, err := net.DialTimeout("unix", sock, time.Second)
		if err != nil {
			continue
		}
		conn.Close()
		names = append(names, name)
	}
	return names, nil
}

// runList prints the live sessions, one per line: `wideboi ls`.
func runList(w io.Writer) error {
	names, err := listSessions(config.SessionDir())
	if err != nil {
		return err
	}
	for _, n := range names {
		fmt.Fprintln(w, n)
	}
	return nil
}
```

`main.go`: add `arg == "ls" || arg == "list-sessions"` to the detection loop, normalising `list-sessions` to `ls`, and add `case "ls": fatal(runList(os.Stdout))`. Help: `wideboi ls                 List running sessions (alias: list-sessions)`.

Does a server survive a dial that closes without sending? Yes: the old probe in `NewSocketListener` did exactly this for as long as it existed. Checked by the attachcheck case below, which lists and then kills.

attachcheck case. Named sessions live in `$TMPDIR/wideboi-<uid>/`, so the case points `TMPDIR` at a short private dir. It's short because sun_path is 104 bytes, and the default mkdtemp under `/var/folders/...` plus `wideboi-<uid>/<name>.sock` gets close.

```python
def case_named_sessions_are_independent(fail):
    """#27: two names are two sessions. ls lists both, and ending one
    leaves the other."""
    tmp = tempfile.mkdtemp(prefix="wb", dir="/tmp")
    env = {k: v for k, v in bin_env().items() if k != "WIDEBOI_SOCK"}
    env["TMPDIR"] = tmp
    sdir = os.path.join(tmp, f"wideboi-{os.getuid()}")
    alpha = beta = None
    try:
        alpha = Server(args=("-L", "alpha"), env=env, sock=os.path.join(sdir, "alpha.sock"))
        beta = Server(args=("-L", "beta"), env=env, sock=os.path.join(sdir, "beta.sock"))
        ls = subprocess.run([BIN, "ls"], capture_output=True, timeout=10, env=env)
        if ls.stdout.decode().split() != ["alpha", "beta"]:
            fail(f"ls printed {ls.stdout!r}, want alpha and beta")
        r = subprocess.run([BIN, "-L", "alpha", "kill-session"], capture_output=True, timeout=15, env=env)
        if r.returncode != 0:
            fail(f"kill-session -L alpha exited {r.returncode}")
        alpha.proc.wait(timeout=8)
        if not beta.alive():
            fail("ending alpha ended beta")
        ls = subprocess.run([BIN, "ls"], capture_output=True, timeout=10, env=env)
        if ls.stdout.decode().split() != ["beta"]:
            fail(f"after kill, ls printed {ls.stdout!r}, want beta")
    finally:
        for s in (alpha, beta):
            if s is not None:
                s.stop()
        shutil.rmtree(tmp, True)
```

`Server.__init__(self, timeout=10.0, args=(), env=None, sock=None)`: `self.sock = sock or socket_path()`, argv `[BIN, *args, "server"]`, `env=env or bin_env()`.

**Verification — automated:**
- [x] `go test ./cmd/wideboi -run ListSessions` fails first (it doesn't compile), then passes — **`undefined: listSessions` before; ok after. `TestParseCLIList`/help failed on assertions until the subcommand was wired**
- [x] `python3 scripts/attachcheck.py --only "named sessions"` passes — **4/4 OK; on Phase 4's binary it fails (`ls` runs as a plain wideboi, exits 1)**
- [x] `make quick` passes
- [x] `make check` passes — **rc=0; smoke 36, attach 22; no new dirs in `$TMPDIR` or `/tmp`**

**Verification — manual:**
- [ ] With `wideboi -L a` detached and `wideboi` running, `wideboi ls` prints `a` and `default`

---

## Phase 6: Docs

Doc-only, so no TDD.

**Files:**
- Modify: `README.md` — a short "Sessions" subsection (`-L`, `ls`, `WIDEBOI_SESSION`, that plain `wideboi` means `default`, and that two started together share one session). Add `-L` to the flag list near 180, `WIDEBOI_SESSION` to the env list near 192, and make sure the log path at 194 matches Phase 3.
- Modify: `config.example.toml` — a commented `# session = "default"` beside `# socket`, noting that the two are exclusive.
- Modify: `docs/LESSONS.md` — extend the `WIDEBOI_SOCK` / stale-unlink lesson (546-560): ownership is now the lifetime flock. Never delete the lock file, and remove the socket before releasing the lock.

**Verification — automated:**
- [x] `make quick` passes — **including `TestLoadConfigExampleToml`, which parses the updated example**
- [x] `bin/wideboi help` matches the README's flag and env lists — **the new `-L`/`-s` lines and `WIDEBOI_SESSION` match. Two older differences remain, both predating this work (the `-c` default's second line, and 'Show this help text'); left alone**

**Verification — manual:**
- [ ] Les reads the README Sessions section
