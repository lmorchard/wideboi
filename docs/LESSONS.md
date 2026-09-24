# Working lessons for this repo

Keep lessons here when they could save the next person a wasted round. Put session
history in `docs/dev-sessions/`. The former roadmap, `docs/BEYOND-V1.md`, now
lives as GitHub issues.

## Start each task in a worktree

The main checkout is shared. Another task can change its branch or stash its
working tree while an agent is editing there; that happened to the #180 patch
before it could be committed. Create a dedicated Git worktree before making
task changes or running a build that writes artifacts. Commit, test, and open
the PR from that worktree. Leave other worktrees and their uncommitted changes
alone.

## Retry GitHub CLI outside the sandbox before using the browser

The managed sandbox can make a valid `gh` keyring login appear invalid and
block access to `api.github.com`. If `gh auth status` or an API call fails
there, retry the command with sandbox escalation. A keyring login may work
normally outside the sandbox; use `gh` for issues and PRs before turning to
computer use.

## Probe the pinned terminal dependencies

`charmbracelet/x/vt` has no tagged release, and `charmbracelet/ultraviolet` is
pre-1.0. Before designing around either API, run a small Go probe against the
pinned versions. These behaviors have already surprised us:

- `*uv.Buffer` lacks `WidthMethod()` and does not implement `uv.Screen`;
  `uv.ScreenBuffer` does.
- `uv.KeyPressEvent.String()` gives a display name such as `"ctrl+q"`, not
  bytes to forward to a child.
- `SendKey` writes to an `io.Pipe` and blocks until a reader drains it. It
  emits nothing for events with `ModShift`. Use `SendText` for printable input,
  except when Ctrl or Alt changes the encoding.
- `uv.Buffer.Resize` truncates narrow lines by reslicing. `uv.Line` has no
  soft-wrap metadata, so exact reflow is impossible.
- `Emulator.Draw` paints only `Touched()` lines, but `Screen.Resize` clears
  that flag. Retouch the lines after a resize.
- Explicitly writing a wide glyph's placeholder cell makes `uv.Line.Set`
  blank the glyph. Advance by each cell's `Width`.
- The cursor setter is private. `TerminalScreen.Flush` also delays a cursor
  move until the next `Render`. `cmd/wideboi/present.go` runs the Render/Flush
  pair twice inside one synchronized update to avoid cursor flicker (#72).
  `SetSynchronizedUpdates` brackets each flush separately and leaves a gap.
- `TerminalScreen.ExitAltScreen` copies cursor visibility to the parent
  screen. Call `ShowCursor` before `ExitAltScreen` on every client teardown
  path, and check the final PTY bytes.
- `x/ansi` v0.11.8 ends an OSC at byte `0x9C` (C1 ST) even when it continues
  a UTF-8 character. `✳` (`E2 9C B3`) and many CJK characters (`本` =
  `E6 9C AC`) contain it, so Claude Code's title arrived as `"\xe2"` and its
  tail printed as text. `term.oscScanner` repairs this before the parser (#175).

Keep `term.Grid` narrow so upstream fixes stay behind one interface.

The input decoder has its own traps. Ultraviolet maps byte `0x08` to `ctrl+h`;
Backspace sends `0x7F`. But `ctrl+i`, `ctrl+m`, and `ctrl+[` decode as Tab,
Enter, and Escape. They cannot serve as distinct repeat bindings.
`internal/keys` enforces this. Probe `uv.EventDecoder` rather than inferring
key names from the ASCII table.

## Reflow only what resize damages

Resize damages the visible screen, not scrollback. `term.Reflow` repairs that
screen and leaves history alone. In the measured case, screen repair took
0.83 ms; rebuilding scrollback took 177 ms. Reflow history for display when
scrollback navigation needs it. See
`docs/dev-sessions/2026-09-18-wideboi-plan-2-layout-seam/spec.md`.

An alt-screen app such as `vim` or `top` repaints from its own model on
`SIGWINCH`; skip reflow for it. A shell usually redraws only its input line.
The terminal alone retains text already scrolled above it. A child that handles
`SIGWINCH` correctly does not prove that the old text survived resize.

## Treat `CellAt` results as live pointers

`uv.Line` is `[]Cell`, and `Line.At` returns `&l[x]`. `CellAt`,
`ScrollbackCellAt`, and similar methods return pointers into the emulator's
live array. `SafeEmulator` releases its read lock before returning the pointer.

Keeping one while a pump writes can race. Keeping one across `Resize` can
silently corrupt reflow: narrowing reslices the same backing arrays, so
writing an output row can overwrite a later source row. Copy a cell before
keeping it past the call: `cc := *c`. `Line.Set` copies the value, and the
emulator replaces whole cells.

The original reflow tests had only one content line; that fixture could not
expose corruption between rows. Use multiple rows when behavior depends on
their interaction.

## Test the actual path, and prove the test can fail

A test of a component with hand-built inputs proves that component, not its
caller or the path to the user. Two examples passed unit tests while the
features failed in the binary:

- `WipeTransition` worked with test-made frames, but `Client.Draw` supplied
  empty surfaces. Every focus change briefly blanked the screen. A small
  `HostScreen` interface made the caller testable.
- The OSC 133 handler matched `"A"` although the payload was `"133;A"`.
  Fixing it exposed a second missing link: status changes were not sending
  `MsgLayoutSnapshot`.

Test at the PTY wire for visible behavior. `make smoke` checks the bytes
wideboi writes; `scripts/ptycheck.py` checks signal exit under a real PTY;
`testdata/golden/` catches omitted output. `scripts/attachcheck.py` covers
the separate client/server transport that the in-process smoke suite cannot
exercise. Cell-buffer tests cannot see a cursor that was never rendered, and
an in-process test cannot expose a broken wire format.

Assert on output the program produced. Readline echoes typed commands, so
typing `echo marker` and searching output for `marker` proves nothing about
whether the command ran. `focus_pane_id` instead parses a status sequence
only wideboi emits. For a new test, break the behavior it guards, watch the
test fail for the intended reason, then restore it. Enumerate finite inputs
rather than choosing a few rows.

Check fixture shape and draw order as well as assertions. A one-row reflow
fixture misses cross-row corruption. A sliver-overflow test on the left side
missed a spill that the focused card painted over; the rightmost sliver had no
later draw to hide it. Test a boundary where later output cannot erase the
failure.

Some tests passed before their intended fixes because another path produced
the same status. A test that starts green against a known bug needs a different
assertion. Running the real binary remains necessary even after code review:
the same author can put the same mistaken assumption in both plan and code.

## Run fresh tests and restore the binary after a red check

Use `go test -count=1` for changed code, timing tests, and race tests. A cached
pass once hid 14 failures in 15 fresh runs. `make race` already disables the
cache.

The smoke and attach suites run `./bin/wideboi`. After deliberately breaking
code to verify a test, restore the source **and rebuild** before another
standalone run. Otherwise the suite keeps testing the broken binary.
`make check` rebuilds, which can make the stale-binary failure look
intermittent.

Repeat timing and concurrency runs. One settle-window change passed its first
25 runs and then failed in three different cases; parallel `make check`
passed four times before exposing two real races. Four fresh runs are a useful
minimum. Fix the cause rather than choosing a delay that happens to pass once.

## Pane teardown is a hangup

Closing a pane closes its PTY master (`ptyx.Hangup`). The kernel sends SIGHUP
to the shell and its foreground job; an interactive shell forwards it to its
jobs. Do not scan the process table or send extra signals. Jobs that opted out
with `nohup`, `disown`, an ignored HUP, or `setsid` may keep running. This is
the tmux model, pinned by `TestHangupLeavesANohupJobRunning`.

Each session runs in a separate `wideboi server`. When its owning client ends
without detaching, the server must hang up its panes:

- On a signal, the owner sends `MsgShutdown`, waits for the server to finish
  hanging up panes, restores the terminal, and re-raises the signal.
- If the owner dies without cleanup, the server detects EOF on the owner's
  socketpair without a preceding `MsgDetach`.

A detached server remains until `kill-session`, `q`, or a signal.

An older guarantee tried to kill even jobs that escaped the hangup. It could
not be made reliable: after a shell exits, an escapee may have no parent or
controlling TTY linking it to the pane. Worse, a `ps` field appeared blank
under `go test`; shifted columns made a cleanup test SIGKILL unrelated
developer processes. If process-table selection ever returns, require an
exact field count, probe it in the executing process, and dry-run the selected
PIDs before sending signals.

## Pin the environment for every harness process

`ptylib.spawn_in_pty` pins `SHELL`, `TERM`, and `PS1`. `attachcheck` also
starts `wideboi server` through `Popen`; it must use `attachcheck.bin_env`.
Without that pin, panes used the developer's themed login shell. Its prompt
had no `$`, startup could drop typed text, and attach tests became slow and
intermittent.

`verify-exit` reports any checkout `bin/wideboi` process parented by PID 1
as a stray. A previously detached session server fits that description. If
all exit cases report the same PID, compare its start time with the run. End
an older session and rerun; keep the PID 1 check because it catches real
orphans.

## Skip canvas resets when its size has not changed

Assigning `canvas.width` or `canvas.height` clears the bitmap even when the
value stays the same. Each pane's `PanePainter.resize` checks both backing
dimensions before assigning them. A `ResizeObserver` callback can arrive
without a real size change; in an idle, change-only browser session, no new
frame may arrive to repaint a canvas cleared by that callback.

Keep the observed canvas's CSS dimensions under flex layout control. Writing
inline width and height from its own `ResizeObserver` callback can feed layout
changes back into the observer. Send `MsgResize` only when the cell grid changes,
and have the server broadcast only when the shared minimum dimensions change:
each layout broadcast forces a full pane resend to every client.

## Change-only pane updates require complete bookkeeping

Before #85, every 33 ms frame resent every pane and repaired missed updates.
Now each mutation must bump `Grid.Generation`, including changes to cells,
cursor, mouse mode, size, and scroll offset. Three other rules keep client
mirrors current:

- Every layout snapshot forces a full resend. The client may have pruned a
  pane mirror or replaced an undersized one with a blank mirror.
- `paneSendMu` serializes broadcasts so an older render cannot arrive last
  while recording a newer generation.
- `paneGens` records delivery per client only after `SendServer` accepts it.

There is no periodic full resend because it would hide missing generation
bumps. Test a mutation after the pane is already busy: the first keystroke
changes its status and forces a resend even if `Write` forgot its bump.
`TestIdleSessionStopsSendingPaneUpdates` types twice for this reason.

## Keep interface values off the wire

`uv.Style` looks like plain data but contains `color.Color` interfaces;
`uv.KeyEvent` is itself an interface. The former gob transport encoded blank
cells, then failed when a child printed a concrete color type that was not
registered. The socket pump swallowed that error, so the client saw a
clean-looking EOF.

Do not maintain an open-ended registration list: upstream can add new color
types. Wire messages use concrete mirror types instead.
`protocol.TestWireTypesCarryNoInterfaces` walks all declared message types
and rejects interfaces and unexported fields. Round trips alone only cover
the values a test constructs; gob silently drops unexported fields. Socket
pumps must log errors before returning.

## Make logging's fatal path visible

Go 1.21+ makes `slog.SetDefault` redirect the standard `log` package. Our
handler writes to a file, which is right for ordinary logs while the alt
screen is active. It also made `log.Fatal` exit silently. `main` writes fatal
startup errors directly to `os.Stderr`.

## Use bindings that a real terminal sends

The old Alt bindings failed silently on macOS terminals that did not send
Option as Meta. Prefer input that terminals produce without a profile change,
such as control bytes. Tests that inject escape bytes only prove that the
decoder accepts them; they cannot prove a user's terminal sends them.

Check every binding with real decoder events. `"pgdn"` looked plausible but
Ultraviolet names the key `"pgdown"`, so PageDown never matched. Since
`MatchString` returns only a bool, a bad name looks like an unpressed key.
Enumerate the binding table in a test and assert that each entry routes.

Adding a default key can also break existing `[keys]` remaps:
`BuildBindings` rejects collisions. Before adding one, search remaps in
`README.md`, `config.example.toml`, `scripts/`, and tests, and record the
compatibility break in the PR. Adding `u` once collided with the documented
`scroll_up = "u"` example and several fixtures.

The help overlay is exactly 24 rows at 80x24, including its border. A new
overlay-only binding makes `TestHelpOverlayFitsAt80x24` fail. Group related
bindings with `Binding.HelpGroup` or redesign the overlay; do not raise the
limit. When `[keys]` unbinds one member of a group, update every grouped
description. `BuildBindings` clears `HelpGroup` on survivors so their own
`Long` descriptions appear. Check both `internal/client/help.go` and
`keys.BarItemsFor`.

## Check the merged tree after PR updates

A docs-only force-push to an open PR raced its merge. Both operations
succeeded, but the merge used the earlier head and lost the new content.
After handing over a PR for review, avoid rewriting its branch; add a normal
commit if needed. After merge, verify the merged tree contains the final
change with `git show <merge>:path`.

## Verify comments before turning them into work

A comment claimed `compose.WriteString` ignored glyph width, although
`WriteStyled` already advanced by `Cell.Width`. The claim became a roadmap
defect and nearly led to a duplicate writer. A comment about behavior is an
untested assertion. Probe the code and add a focused test before promoting
the claim to an issue. Defect reports should cite the code, not only prose.

## Measure terminal text in cells

Ultraviolet wraps a write to the final column in autowrap-toggle escapes.
That splits the raw output bytes and can break wire assertions. Truncate at
`cols - 1` and leave the last cell alone.

For truncation, count `Cell.Width`, not bytes or runes. A double-width glyph
uses two cells even when its rune count is one and its UTF-8 byte count is
three.

## Preserve signal disposition across teardown

An async shell job (`cmd &`, including targets under `make -j`) can inherit
ignored SIGINT. Go preserves inherited `SIG_IGN` for SIGHUP and SIGINT, while
`signal.Notify` still installs a handler. After `signal.Stop` or
`signal.Reset`, the original ignored disposition returns. Re-raising then
does nothing, leaving a process alive after it tears down the session.

Sample `signal.Ignored` before `Notify` and handle that case on exit. Test
with SIGINT or SIGHUP inherited through `sh -c 'trap "" INT; exec ...'`;
SIGTERM behaves differently. Do not decide from `syscall.Kill` returning:
signal delivery is asynchronous, so a following `os.Exit` can win the race.

## Recheck consumers when a value gains new possible inputs

`transport.NewSocketListener` once removed a stale fixed socket inside a
wideboi-owned directory. Adding `WIDEBOI_SOCK` made the same removal capable
of deleting any user-named file. The dangerous line was unchanged; the
configuration change broke its safety assumption. Review every consumer
when widening a value's domain.

Socket ownership now uses an exclusive `flock` on `<socket>.lock` for the
server's lifetime (#86). Keep these ordering rules:

- Never delete the lock file. A waiter could lock its old inode while a new
  process creates and locks another.
- Remove the socket before releasing the lock in `Close`, so the dying server
  cannot unlink a successor's socket.
- Probe for a live server while holding the lock. An older binary may own the
  socket without owning this newer lock.

## On the protobuf wire, absence decodes as zero

Proto3 omits zero values. A patch that hides the cursor sends no
`cursor_visible` field, and the receiver decodes it as `false`. Today
`protocol.ApplyPanePatch` and `PaneStore.patch` always replace
cursor and mouse fields, so this works. If patches ever update individual
fields, use schema `optional` presence; otherwise absence could be mistaken
for “unchanged.” Go and browser tests pin the current hidden-cursor behavior.

The Go codec is hand-written. A new struct field can disappear on the wire
while in-process tests pass. `TestCodecRoundTripsEveryField` populates every
exported field, and `TestWireSchemaCoversEveryWireType` checks schema oneofs.
Vitest does not typecheck: after web type changes, run `make check` so `tsc`
checks the tests too.

Protobuf `string` fields reject invalid UTF-8; gob never checked. One bad pane
title made a snapshot unencodable, the pump closed the owner's connection, and
the session ended (#175). The codec passes outbound strings through
`validUTF8`, and a server pump drops a message it cannot encode rather than
the peer.

## Bump `protocol.Version` when the wire changes

Sessions outlive rebuilds, so a new client meeting an old server is normal.
Without a version check, #166's switch from gob to protobuf showed up as
`protobuf frame too large: 4288679936`: the first gob bytes, `0xFFA01000`,
read as a length prefix. The Unix socket now opens with a hello that carries
`protocol.Version` (#174). The check only works if someone bumps the version.
Increase it for any change an older peer would misread or reject. The browser
also offers `wideboi.v<Version>` as a WebSocket subprotocol; update that value
with the Go version or an older browser could silently misapply a patch.

A peer that hangs up with your bytes unread looks different by platform.
macOS reads EOF; Linux reads `ECONNRESET`. The handshake passed on macOS
and failed in CI, where a server that found the session taken reset its
owner. Treat a reset as a hang-up, and run socket-closing changes in a Linux
container (`docker run golang:<go.mod version>`) before pushing.
