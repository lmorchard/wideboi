# Keep the session when the owner is lost — Implementation Plan

**Goal:** a dropped ssh connection never ends a wideboi session. An owner
SIGHUP detaches, an owner EOF with no detach keeps the session, and
`wideboi ls` shows which sessions are detached and where their web server
listens.

**Approach:** add one on-by-default config option,
`keep_session_on_owner_loss`, which both the client and the server read. The
client picks `MsgDetach` over `MsgShutdown` on SIGHUP. The server skips
`CloseFor(ReasonOwnerLeft)` on owner EOF. `ls` gets a client count through a
new `MsgLayoutSnapshot.AttachedClients` field. With the option off, behaviour
is identical to today.

**Tech stack:** Go, protobuf (buf), Python pty harnesses (`ptycheck.py`,
`attachcheck.py`).

Run everything from `.worktrees/sighup-detaches`. The edit loop is
`make quick`; the gate is `make check`, run four times for the phases that
touch signal and teardown timing (phases 2 and 3), per CLAUDE.md.

---

## Phase 1: Config option

This phase adds `keep_session_on_owner_loss` end to end in config, but
nothing reads it yet.

**Files:**
- Modify: `internal/config/config.go`
  - Add the field next to `AutoCleanup` (:46-47).
  - TOML merge next to :275-276.
  - Env handling next to :367-373.
  - CLI handling next to :424-427.
  - Derive the effective value next to :491.
- Modify: `cmd/wideboi/main.go`
  - Flag next to :86.
  - Help line next to :206.
- Modify: `config.example.toml` — add an entry after `auto_cleanup` (:103-106).
- Test: `internal/config/config_test.go`, `cmd/wideboi/cli_test.go`

**Key changes:**

```go
// Config
// KeepSessionOnOwnerLoss is a pointer so an absent key reads as the
// default (on) rather than as false. Read KeepSessionOnOwnerLossEnabled.
KeepSessionOnOwnerLoss        *bool `toml:"keep_session_on_owner_loss"`
KeepSessionOnOwnerLossEnabled bool  `toml:"-"`

// ConfigFlags
EndSessionOnOwnerLoss bool

// file layer
if fileCfg.KeepSessionOnOwnerLoss != nil {
	cfg.KeepSessionOnOwnerLoss = fileCfg.KeepSessionOnOwnerLoss
}
// env layer
if env := getenv("WIDEBOI_KEEP_SESSION_ON_OWNER_LOSS"); env != "" {
	v, err := parseBoolEnv("WIDEBOI_KEEP_SESSION_ON_OWNER_LOSS", env)
	if err != nil {
		return Config{}, nil, err
	}
	cfg.KeepSessionOnOwnerLoss = &v
}
// flag layer
if flags.EndSessionOnOwnerLoss {
	v := false
	cfg.KeepSessionOnOwnerLoss = &v
}
// derived
cfg.KeepSessionOnOwnerLossEnabled = cfg.KeepSessionOnOwnerLoss == nil || *cfg.KeepSessionOnOwnerLoss
```

```go
// main.go
fs.BoolVar(&opts.flags.EndSessionOnOwnerLoss, "end-session-on-owner-loss", false,
	"end the session when its owning terminal hangs up or the owner dies without detaching")
// help text
//      --end-session-on-owner-loss End the session if the owning terminal hangs up
```

```toml
# Keep the session running when the terminal that started it goes away
# without detaching: a dropped ssh connection or a closed window (SIGHUP),
# or an owner killed outright. The session is left detached; see
# `wideboi ls` and `wideboi kill-session`. SIGINT and SIGTERM still end it.
# Set to false to end the session with its terminal. Defaults to true.
# keep_session_on_owner_loss = true
```

**Tests (write first, watch fail):**
- `TestLoadKeepSessionOnOwnerLoss`, a copy of the shape of
  `TestLoadAutoCleanup`. It checks:
  - the default is true
  - TOML `false` disables it and TOML `true` enables it
  - env `false`/`0`/`no`/`off`/`FALSE` disables it
  - env `bogus` returns an error
  - `ConfigFlags{EndSessionOnOwnerLoss: true}` disables it, even over env
    `true`
- `TestParseCLIEndSessionOnOwnerLoss`, a copy of
  `TestParseCLIDisableAutoCleanup`. It checks that the flag parses, and that
  it survives `serverArgs` so the spawned server sees the same setting.

**Verification — automated:**
- [x] New tests fail before the implementation (compile error or assertion) — **build failed: unknown field EndSessionOnOwnerLoss / KeepSessionOnOwnerLossEnabled**
- [x] `go test ./internal/config -run TestLoadKeepSessionOnOwnerLoss -v` passes — **PASS**
- [x] `go test ./cmd/wideboi -run TestParseCLIEndSessionOnOwnerLoss -v` passes — **PASS**
- [x] `make quick` passes — **exit 0, 16 packages ok (after make fmt)**

**Verification — manual:**
- [x] `./bin/wideboi --help` shows `--end-session-on-owner-loss` — **seen in help output**

---

## Phase 2: Server keeps the session on owner EOF

When the option is on, an owner connection that closes without `MsgDetach`
drops ownership, keeps the session, and records `owner lost, session kept`
in exits.log.

**Files:**
- Modify: `internal/server/server.go`
  - Add the fields next to `owner` (:94-99).
  - Add setters next to `SetOwner` (:257-266).
  - Update the `SetOwner` doc comment.
- Modify: `internal/server/handlers.go:49-53` — branch on the option.
- Modify: `cmd/wideboi/main.go` `runServer` — wire the option and the
  exits.log hook next to `SetOwner` (:410-413).
- Test: `internal/server/lifecycle_test.go`
- Test: `scripts/attachcheck.py`

**Key changes:**

```go
// server.go, fields
// keepOnOwnerLoss keeps the session when the owner's connection ends
// with no MsgDetach before it. Off, that EOF ends the session. Set
// before Run.
keepOnOwnerLoss bool
// onOwnerLost is called when keepOnOwnerLoss kept the session.
onOwnerLost func()

// SetKeepSessionOnOwnerLoss chooses what an owner's EOF without a
// detach means: the session is kept, as if it had detached, or ended.
func (s *Server) SetKeepSessionOnOwnerLoss(keep bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keepOnOwnerLoss = keep
}

// SetOnOwnerLost registers fn to run when an owner's EOF leaves the
// session running, so the post-mortem trail records it.
func (s *Server) SetOnOwnerLost(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onOwnerLost = fn
}
```

```go
// handlers.go
if !ok {
	if s.dropClient(ctx, tp) {
		s.mu.Lock()
		keep, lost := s.keepOnOwnerLoss, s.onOwnerLost
		s.mu.Unlock()
		if keep {
			slog.Info("owning client left without detaching; keeping the session")
			if lost != nil {
				lost()
			}
		} else {
			slog.Info("owning client left without detaching; ending the session")
			_ = s.CloseFor(ReasonOwnerLeft)
		}
	}
	return
}
```

`dropClient` already sets `s.owner = nil`, so from here on the session is
ownerless, as it would be after a detach. Reattaching still does not re-own.

```go
// main.go runServer, beside SetOwner
srv.SetKeepSessionOnOwnerLoss(cfg.KeepSessionOnOwnerLossEnabled)
srv.SetOnOwnerLost(func() {
	_ = logger.AppendExit(cfg.Socket, "server", "owner lost, session kept", "ownerPID", ownerPID)
})
```

`newBareServer` leaves `keepOnOwnerLoss` false, so the existing tests keep
exercising the "end" behaviour unchanged. The production default comes from
config, not from the zero value.

**Tests (write first, watch fail):**

```go
// An owner lost without a word -- a dropped ssh connection, SIGKILL --
// leaves the session running when configured to keep it: ownership is
// given up exactly as a detach would, and the other client stays.
func TestOwnerEOFWithoutDetachKeepsTheSessionWhenConfigured(t *testing.T) {
	owner := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	other := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := newBareServer(owner, other)
	s.SetOwner(owner)
	s.SetKeepSessionOnOwnerLoss(true)
	var lost atomic.Int32
	s.SetOnOwnerLost(func() { lost.Add(1) })

	close(owner.ClientSend)
	runLoopUntilReturn(t, s, owner)

	select {
	case <-s.stopCh:
		t.Fatal("owner EOF ended the session despite keep-on-owner-loss")
	default:
	}
	if other.closed.Load() {
		t.Error("keeping the session hung up on the other client")
	}
	if lost.Load() != 1 {
		t.Errorf("onOwnerLost called %d times, want 1", lost.Load())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner != nil {
		t.Error("lost owner still recorded as owner")
	}
}
```

- Update the comment on `TestOwnerEOFWithoutDetachEndsTheSession` to say it
  covers the option being off, which is the bare server's zero value. The
  assertions stay.
- `attachcheck.py`:
  - Give `owned_session(fail, args=())` an `args` parameter and pass it to
    `Client(plain=True, args=args)`.
  - Rename `case_sigkilled_owner_takes_the_session_with_it` to
    `case_sigkilled_owner_ends_the_session_when_configured`. It calls
    `owned_session(fail, args=("--end-session-on-owner-loss",))`, and its
    assertions are unchanged.
  - Add `case_sigkilled_owner_leaves_the_session_running`:
    1. `owned_session(fail)` with default config.
    2. Type `echo owned-marker\r` and wait for the marker.
    3. `c.kill()`, `time.sleep(0.5)`.
    4. Assert `srv` is still alive and the socket exists.
    5. Assert `exits.log` in `runtime_dir()` contains
       `owner lost, session kept`, using the file-read pattern of
       `case_owner_reports_a_signalled_server` (:785).
    6. Assert `Client(startup=SETTLE * 2)` shows the marker.
    7. `second.kill()`.
    8. `kill_session()`, then assert `srv` and its children are gone within
       2s and the socket was removed.
    9. `finally: reap_everything([srv, *kids])`.
  - Register both cases in `CASES` (:1071).

**Verification — automated:**
- [x] The new Go test fails before the handler change, with the "ended the
  session" failure — **`owner EOF ended the session despite keep-on-owner-loss`** (setters added first so it failed on the assertion, not the build)
- [x] `go test ./internal/server -run 'TestOwner' -v` passes — **4 PASS**
- [x] `make build && python3 scripts/attachcheck.py` passes. The new keep case
  fails against a binary built before this phase (prove it) — **phase-1 binary: `a SIGKILLed owner took its session with it despite the default keep`; new binary: 29 passed, 0 failed**
- [x] `make quick` passes — **covered by make check**
- [x] `make check` passes 4× in a row — **exit 0 ×4**

**Verification — manual:**
- [x] None beyond phase 3's ssh check

---

## Phase 3: Owner SIGHUP detaches

With the option on, a SIGHUP'd owner sends `MsgDetach`, restores the
terminal, and dies by SIGHUP. The server and panes survive. SIGINT, SIGTERM
and SIGQUIT still shut the session down.

**Files:**
- Modify: `cmd/wideboi/main.go` `runClient` guard stop func (:785-823)
- Modify: `cmd/wideboi/hangup.go` — add `ownerFarewell`, next to `hangUp`
- Test: `cmd/wideboi/hangup_test.go`
- Modify: `scripts/ptycheck.py` — add `--expect-session-kept` and repeatable
  `--env KEY=VALUE`
- Modify: `Makefile` `verify-exit` (:137-179) — split the SIGHUP case into
  two cases, keep and end, and update the header comment

**Key changes:**

```go
// hangup.go
// ownerFarewell is what an owning client tells its server when sig ends
// it. A hangup means the terminal went away -- a dropped ssh connection,
// a closed window -- and with keep set that leaves the session running,
// detached. Every other signal is a deliberate stop and ends it.
func ownerFarewell(sig os.Signal, keep bool) (msg transport.ClientMessage, ceiling time.Duration) {
	if keep && sig == syscall.SIGHUP {
		return protocol.MsgDetach{}, detachCeiling
	}
	return protocol.MsgShutdown{}, shutdownCeiling
}
```

```go
// main.go guard stop func
stopped.Store(true)
sig := guard.Signal()
farewell, ceiling := ownerFarewell(sig, cfg.KeepSessionOnOwnerLossEnabled)
_, detaching := farewell.(protocol.MsgDetach)
if sig != nil {
	_ = logger.AppendExit(cfg.Socket, "client", "client exit",
		"reason", "signal", "signal", sig.String(), "owner", owner,
		"detached", owner && detaching)
}
// An owner's session dies with it -- unless the terminal hung up and
// the session is kept, when it detaches instead -- or unless the
// connection has already ended. Say so *before* restoring the terminal,
// and wait: ...(existing comment on ordering)...
var late bool
if owner && !hungUp.Load() {
	cConnLock.Lock()
	currentConn := cConn
	cConnLock.Unlock()
	late = !hangUp(ctx, currentConn, farewell, ceiling)
}
...
if late {
	what := "shutdown"
	if detaching {
		what = "detach"
	}
	fmt.Fprintf(os.Stderr, "wideboi: server did not confirm the %s within %s\n", what, ceiling)
}
```

A plain `guard.Stop()` (sig == nil) gets `MsgShutdown`, as it does today. An
unacknowledged detach still ends in our EOF, and with the option on phase 2
keeps the session anyway.

**Tests (write first, watch fail):**

```go
func TestOwnerFarewellDetachesOnlyOnAKeptHangup(t *testing.T) {
	cases := []struct {
		sig        os.Signal
		keep       bool
		wantDetach bool
	}{
		{syscall.SIGHUP, true, true},
		{syscall.SIGHUP, false, false},
		{syscall.SIGTERM, true, false},
		{syscall.SIGINT, true, false},
		{syscall.SIGQUIT, true, false},
		{nil, true, false},
	}
	for _, c := range cases {
		msg, ceiling := ownerFarewell(c.sig, c.keep)
		_, detach := msg.(protocol.MsgDetach)
		if detach != c.wantDetach {
			t.Errorf("ownerFarewell(%v, keep=%v) = %T, want detach=%v", c.sig, c.keep, msg, c.wantDetach)
		}
		want := shutdownCeiling
		if c.wantDetach {
			want = detachCeiling
		}
		if ceiling != want {
			t.Errorf("ownerFarewell(%v, keep=%v) ceiling = %v, want %v", c.sig, c.keep, ceiling, want)
		}
	}
}
```

`ptycheck.py`:
- `--env KEY=VALUE` (repeatable, `action="append"`) is merged into the
  `{"WIDEBOI_SOCK": sock}` overlay passed to `spawn_in_pty`. `pinned_env`
  applies the overlay after stripping inherited `WIDEBOI_*`, so the value
  sticks.
- With `--expect-session-kept`, the steps after "died by signal" and the
  alt-screen check are:
  1. `time.sleep(0.5)`, then assert every tracked pid is alive, using
     `still_alive(pids, 0.0) == pids`. Print `OK: server and pane shells
     survived the hangup`.
  2. Assert the socket still exists.
  3. Run `subprocess.run(harness_args(binary, "kill-session"),
     env=pinned_env({"WIDEBOI_SOCK": sock, **extra_env}), timeout=15)` and
     assert returncode 0.
  4. Continue into the existing leak, socket and stray assertions unchanged.
     After the kill-session these assert the same "everything gone"
     contract.
- The docstring's point 3 gains: "unless `--expect-session-kept`, when they
  must survive the signal and go with an explicit kill-session instead."

`Makefile` verify-exit:

```make
	python3 scripts/ptycheck.py --size 80x24 --signal SIGINT & pids="$$pids $$!"; \
	python3 scripts/ptycheck.py --size 80x24 --signal SIGHUP --expect-session-kept & pids="$$pids $$!"; \
	python3 scripts/ptycheck.py --size 80x24 --signal SIGHUP \
		--env WIDEBOI_KEEP_SESSION_ON_OWNER_LOSS=false & pids="$$pids $$!"; \
```

The header comment says: a SIGHUP'd owner detaches by default, so its case
asserts survival and then an explicit kill-session, and a second SIGHUP case
with the option off keeps the old contract.

**Verification — automated:**
- [x] `TestOwnerFarewellDetachesOnlyOnAKeptHangup` fails to compile, then
  passes — **`undefined: ownerFarewell`, then PASS**
- [x] The SIGHUP `--expect-session-kept` ptycheck case fails against the
  phase-2 binary (the server is torn down) before the main.go change (prove
  it) — **`the session did not survive the owner's SIGHUP: pid(s) [...] gone`**
- [x] `make verify-exit` passes — **exit 0, 7 cases, 0 FAIL**
- [x] `make quick` passes — **covered by make check**
- [x] `make check` passes 4× in a row — **exit 0 ×4**

**Verification — manual:**
- [x] Pty probe: owner SIGHUP → exits.log `client exit reason=signal signal=hangup owner=true detached=true`, session alive, kill-session 0
- [ ] Les: ssh in, start `wideboi -L hup`, open a second pane, then kill the
  ssh connection (`~.`). Reconnect: `wideboi ls` lists `hup`, and
  `wideboi attach -L hup` shows both panes. exits.log shows
  `client exit ... signal=hangup ... detached=true`.

---

## Phase 4: `wideboi ls` shows attachment state and web address

`ls` prints three columns: name, state (`detached` / `N client(s)` / `?`),
and web address (`scheme://addr` / `-` / `?`). The state comes from a new
snapshot field. The address comes from the existing web status request, and
**never from its token-bearing `URL` field** (`internal/server/web.go:304`).

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto:123-128` — add
  `int32 attached_clients = 5;` to `MsgLayoutSnapshot`
- Regenerate: `make proto` (Go in `internal/protocol/wirepb`, TS in
  `web/src/gen`)
- Modify: `internal/protocol/messages.go:321-330` — `AttachedClients int` on
  `MsgLayoutSnapshot`
- Modify: `internal/protocol/codec.go:156-173,282-300` — map it both ways
- Modify: `internal/protocol/version.go` — bump to `16` (**revised: 17** — main took 16 for #270 during the session) and add
  `// 17 adds the attached client count to layout snapshots.` Les approved
  this bump. Old sessions show `?` in `ls` until they are upgraded in place.
- Modify: `internal/server/server.go:873-878` —
  `AttachedClients: s.attachedCountLocked()`
- Modify: `cmd/wideboi/sessions.go` — add `sessionInfo`, `querySession`,
  `describeClients`, `describeWeb`, `writeSessionList`, and have `runList`
  call `writeSessionList`
- Test: `internal/protocol/codec_test.go`, `internal/server` (new test next to
  the snapshot tests), `cmd/wideboi/sessions_test.go`
- Modify: `scripts/attachcheck.py:632,646` — parse the first column

**Key changes:**

```go
// sessions.go
// sessionInfo is what `wideboi ls` reports about one live session.
type sessionInfo struct {
	clients int
	web     *protocol.MsgWebServerControlResponse
}

// querySession asks the server at sock how many clients are attached and
// whether its web server is up. Mirrors runStatus's exchange
// (status.go:38-68): a status request answered by a layout snapshot, and a
// web status request answered by a control response.
func querySession(sock string) (sessionInfo, error) {
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return sessionInfo{}, err
	}
	defer conn.Close()
	if err := handshakeServer(conn, sock); err != nil {
		return sessionInfo{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)
	if !cc.SendClient(ctx, protocol.MsgStatusRequest{}) ||
		!cc.SendClient(ctx, protocol.MsgWebServerControlRequest{Action: protocol.WebServerActionStatus}) {
		return sessionInfo{}, fmt.Errorf("status request to %s failed", sock)
	}
	var info sessionInfo
	var haveSnap bool
	for !haveSnap || info.web == nil {
		select {
		case msg, ok := <-cc.ServerSendChan():
			if !ok {
				return sessionInfo{}, fmt.Errorf("%s closed before sending state", sock)
			}
			switch m := msg.(type) {
			case protocol.MsgLayoutSnapshot:
				info.clients, haveSnap = m.AttachedClients, true
			case protocol.MsgWebServerControlResponse:
				info.web = &m
			}
		case <-ctx.Done():
			return sessionInfo{}, ctx.Err()
		}
	}
	return info, nil
}

// describeClients is the state column of `wideboi ls`.
func describeClients(n int) string {
	switch n {
	case 0:
		return "detached"
	case 1:
		return "1 client"
	default:
		return fmt.Sprintf("%d clients", n)
	}
}

// describeWeb is the web column of `wideboi ls`: where the session's web
// server listens. Built from Addr, never from URL -- URL carries the
// auth token, and ls output lands in scrollback.
func describeWeb(web *protocol.MsgWebServerControlResponse) string {
	if web == nil || !web.Running || web.Addr == "" {
		return "-"
	}
	scheme := "http"
	if web.TLSEnabled {
		scheme = "https"
	}
	return scheme + "://" + web.Addr
}

// writeSessionList prints each live session in dir with its state and
// web address: `wideboi ls`.
func writeSessionList(w io.Writer, dir string) error {
	names, err := listSessions(dir)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	for _, n := range names {
		info, err := querySession(filepath.Join(dir, n+".sock"))
		if err != nil {
			fmt.Fprintf(tw, "%s\t?\t?\n", n)
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", n, describeClients(info.clients), describeWeb(info.web))
	}
	return tw.Flush()
}

func runList(w io.Writer) error { return writeSessionList(w, config.SessionDir()) }
```

**Tests (write first, watch fail):**
- `TestDescribeClients`: a table where 0 gives `detached`, 1 gives
  `1 client` and 3 gives `3 clients`.
- `TestDescribeWebNeverShowsTheToken`, a table:
  - nil gives `-`
  - `{Running:false}` gives `-`
  - `{Running:true, Addr:"127.0.0.1:8080", TLSEnabled:true, URL:"https://127.0.0.1:8080/#token=SECRET", Token:"SECRET"}`
    gives `https://127.0.0.1:8080`, and the result does not contain `SECRET`
  - `{Running:true, Addr:"0.0.0.0:9000"}` gives `http://0.0.0.0:9000`
- `TestWriteSessionListShowsAttachmentState` in `sessions_test.go`:
  1. Start a real `server.NewServer(nil, "/bin/sh", "")` on a
     `/tmp/wb…/busy.sock` listener, following the setup in
     `TestKillSessionShutsDownAServer` (main_test.go:115-145), and attach one
     `client.NewClient` to it.
  2. Start a second server, `idle.sock`, with no clients.
  3. Add the plain accept-and-close listener from
     `TestListSessionsNamesOnlyLiveSessions` as `mute.sock`.
  4. Assert that the whitespace-split fields of each line are
     `[busy 1 client -]`, `[idle detached -]` and `[mute ? ?]`.

  A running web server is not started here. `describeWeb`'s table covers
  that, and starting TLS listeners in this test adds nothing.
- Codec round-trip test: `AttachedClients: 2` survives
  `MarshalServer`/unmarshal, following the existing snapshot round-trip test
  in `codec_test.go`.
- Server test: after one attach, the broadcast snapshot carries
  `AttachedClients == 1`, and an unattached status connection does not
  count.
- `attachcheck.py`:
  - `names = [line.split()[0] for line in ls.stdout.decode().splitlines()]`
    at both call sites.
  - At :632, also assert that each line's second field is `detached`, since
    both servers have no clients.

**Verification — automated:**
- [x] New tests fail before the implementation — **build failed: AttachedClients / describeClients undefined**
- [x] `make proto-check` passes (generated code committed) — **exit 0 after commit** (it diffs against HEAD, so it fails pre-commit by design)
- [x] `go test ./cmd/wideboi -run 'TestDescribe|TestWriteSessionList|TestListSessions' -v` passes — **5 PASS**
- [x] `make quick` passes — **covered by make check**
- [x] `make check` passes — **exit 0 (smoke 41/41, attach 29/29)**

**Verification — manual:**
- [x] With the built binary and a private `TMPDIR=/tmp/wbls.XXXX`:
  - start one session detached, one attached, and one with `--websocket
    127.0.0.1:0`
  - `wideboi ls` shows all three states and the web address
  - no token appears in the output
  — **probe output: `busy  1 client  -` / `idle  detached  -` / `web  detached  https://127.0.0.1:58397`; no token**
- [!] Plan said to bump `version.go` only. **Also required:** `web/src/version.ts` and a schema hash (v17 after rebase) in `internal/protocol/version_guard_test.go` (`TestWireSchemaMatchesProtocolVersion` / `TestWebClientVersionMatchesGoProtocolVersion` enforce it). Done in this phase.

---

## Phase 5: Docs

This phase is docs only, so there is no TDD.

**Files:**
- Modify: `docs/LESSONS.md:161-169` — rewrite the owner rule:
  - On SIGHUP, with `keep_session_on_owner_loss` on (the default), the owner
    detaches.
  - On any other signal, it sends `MsgShutdown`.
  - An owner EOF without a detach keeps the session unless the option is off.
  - Add why: a dropped ssh connection took a session with a live second
    client on 2026-09-26.
- Modify: `docs/MANUAL.md:100-106`:
  - Replace "tears down the entire session" with: closing the window or
    losing the connection leaves the session running detached, while SIGINT
    or SIGTERM to the owner, `Ctrl+b q` or `kill-session` end it.
  - Mention `wideboi ls` and its state column.
  - Add the option to the config example (:474 area) and to the env table
    (:543 area).
- Modify: `README.md:44-56` — one sentence: sessions survive a dropped
  connection, and `wideboi ls` shows the detached ones.

**Verification — automated:**
- [x] `make quick` passes — **exit 0**

**Verification — manual:**
- [ ] Les reads the LESSONS and MANUAL diffs

---

## Scope reminders (from the spec's NOT list)

No changes to SIGINT/SIGTERM/SIGQUIT, attach-client signal handling,
`control.go`, the desktop quit dialog, re-owning on reattach, idle reaping,
or `wideboi status` output.
