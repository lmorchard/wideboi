# Working lessons for this repo

> The "roadmap doc" these lessons refer to was `docs/BEYOND-V1.md`. Its
> contents now live as GitHub issues; the file was removed once everything
> valid in it had been filed. The lessons still hold — a planning list is a
> planning list whatever it is stored in.

Evergreen. Not session-scoped — session artifacts live under `docs/dev-sessions/`.
Add to this when the project teaches you something that would cost the next person
a wasted round.

## The dependencies are pre-1.0 and do not behave as you would assume

`charmbracelet/x/vt` has **no tagged release** and is pinned to a pseudo-version.
`charmbracelet/ultraviolet` is pre-1.0. Between them they have surprised us at
least eight times, each of which would have cost a fix round if it had been
discovered during implementation rather than during planning.

**Probe before you plan.** A throwaway Go program in `/tmp` that imports the
pinned versions and prints what an API actually does takes two minutes. Things
found that way, none of which were guessable:

- `*uv.Buffer` does not satisfy `uv.Screen` — it lacks `WidthMethod()`. `uv.ScreenBuffer` does.
- `uv.KeyPressEvent.String()` returns a display name (`"ctrl+q"`), not forwardable bytes.
- `SendKey` writes to an `io.Pipe` and **blocks until something reads**; call it without a concurrent drain and the process deadlocks.
- `SendKey` emits **nothing at all** for any event carrying `ModShift`. Use `SendText` for printable input — but not when Ctrl/Alt is held, since those change the encoding.
- `uv.Buffer.Resize` truncates on narrow (`Lines[i][:width]`) and there is no soft-wrap metadata anywhere (`type Line []Cell`), so correct reflow is impossible and a heuristic is required.
- `Emulator.Draw` paints only `Touched()` lines and `Screen.Resize` clears `Touched`, so a pane renders blank after a resize until something re-touches it.
- Writing a wide glyph's placeholder cell explicitly trips `uv.Line.Set`'s partial-overwrite protection and blanks the whole glyph. Advance by each cell's own `Width`.
- There is no public cursor setter — `setCursor` is unexported.
- `TerminalScreen.Flush` calls `rend.MoveTo` to position the cursor, but `MoveTo` writes to `rend.buf`, which only flushes to the screen's output buffer on the *next* `TerminalScreen.Render`. A single `Render(); Flush()` therefore shows the cursor on the last cell drawn, which with busy background cards means a cursor flickering between them (#72). `cmd/wideboi/present.go` runs the pair twice, inside one mode-2026 synchronized update, so every frame ends with the cursor where it was asked to be. `SetSynchronizedUpdates` would not do: it brackets each `Flush` on its own, leaving the gap between the two open.

The `term.Grid` interface exists precisely so upstream surprises stay confined to
one file. Keep it narrow, and fix upstream gaps behind it rather than forking.

## Resize damages the visible screen only — scrollback is untouched

Measured, not assumed. This is why `term.Reflow` handles the screen and
deliberately leaves history alone: repairing the screen costs 0.83 ms, rebuilding
scrollback costs 177 ms, and scrollback is not damaged in the first place. It will
need reflowing *for display* when scroll-back navigation lands, which is a
presentation concern and non-destructive to defer.

Full reasoning: `docs/dev-sessions/2026-09-18-wideboi-plan-2-layout-seam/spec.md`.

## What a terminal owes its children, and what it does not

An app owns what it is currently drawing; the terminal owns what has already been
written. Once a program emits text it forgets it — only the terminal holds it.

- **Alt-screen apps** (`vim`, `top`, `htop`) repaint from their own model on `SIGWINCH`. Skip reflow entirely; anything we do is overwritten.
- **Shells and line-oriented programs** redraw only the current input line. Everything scrolled above is ours, and no signal can ask for it back.

A child receiving `SIGWINCH` and working perfectly is not evidence that resize was
handled — it only means the child adapted. Check what happened to the text.

## Test at the wire, and prove every test can fail

Two user-visible defects shipped in Plan 1 past eight reviews: every shifted key
was dropped, and no cursor was ever rendered. Both were found in ten minutes of
manual use. The rendering tests asserted against our own cell buffer, where a
cursor cannot appear, so no assertion could have failed.

- `make smoke` asserts on the bytes wideboi writes to the pty. New features get a case there.
- `scripts/ptycheck.py` asserts the signal-exit contract under a real pty.
- `testdata/golden/` holds a wire snapshot — the technique that catches *omitted* behaviour, which table tests structurally cannot.

Three separate times a test here passed against the bug it was written to catch.
**Break the thing a new test guards, watch it go red, restore it.** For finite
input spaces, enumerate in a loop rather than hand-picking rows.

## Teardown is the load-bearing guarantee

A multiplexer that leaks background processes is worse than useless — the work
keeps burning CPU with no window left to find it in. `ptyx.Kill` does three kills
per pane because each catches processes the others miss, and the ordering is
deliberate: `closePanes` runs **before** the render lock, so children are reaped
even when rendering is wedged against a stalled consumer.

Known parked gaps, recorded with reasoning in the v1 spec: a root that exits
before `Kill` leaves escapees unsignalled, and `ps -axo` parsing is unverified on
Linux.

Since #25 every session runs in a separate `wideboi server`, so the guarantee
crosses a process boundary and has to be stated more carefully: **nothing
outlives the client that started the session, unless that client detached.**
There are two routes, and each one covers what the other can't:

- A signal to the owner sends `MsgShutdown` and waits for the server to hang
  up, which it does only after reaping. The order is still reap, then restore
  the terminal, then re-raise.
- An owner that dies without running any code (SIGKILL) is caught by the
  server: the owner's socketpair reaches EOF with no `MsgDetach` before it.

That second route is quick enough to satisfy `verify-exit` on its own, so
ptycheck cannot see whether the first one ran. It does fail when both are
removed. A detached, ownerless server has no terminal left to take it down,
so it lives until `kill-session`, a `q`, or a signal, and it arms the same
guard to reap on that signal.

**A `ps` walk by parent pid cannot see a process whose parent has died.** It
is reparented to launchd (pid 1) at once, and from then on no walk from a pane
root reaches it: not the 1s background poll, not `ptyx.Kill`'s snapshot. So a
double-forked or `setsid` escapee planted in a test is out of reach of *every*
walk. A test that plants one fails before and after any change to when or
where the walk runs, which makes it useless as proof of such a change. #83's
test plan fell into exactly this, and the fix it proposed (moving Close's
final poll) turned out to duplicate `Kill`'s walk (#87). Before planning a
reaper change, ask whether the process in question is still in the tree at
the moment the walk runs. If it is not, the fix needs a different mechanism,
not a better-timed walk.

That mechanism turned out to be the **controlling tty** (#88). A reparented
process keeps it, and for a pane that tty is the pane's pty, so `Kill` now
also snapshots the root's `TTYMates`. This is measured, not assumed: on macOS
`ps -o sess` prints 0 for everything, so session ID is no use, while `tty`
survives reparenting. What stays out of reach is a process that calls
`setsid` itself (a daemon), because that also drops its controlling tty.
macOS offers no subreaper and no working `NOTE_TRACK`, so that is the limit.
It is the same limit tmux has.

## The harness's environment pin only covers what it starts on a pty

`ptylib.spawn_in_pty` pins `SHELL`, `TERM` and `PS1`. attachcheck starts
`wideboi server` with a plain `Popen` instead, and nothing pinned that
environment, so its panes ran the developer's own login shell: a themed zsh,
whose prompt has no `$` and whose line editor sometimes drops text typed
while it starts. The result was an intermittent "planted job never appeared",
and `attach-check` was spending about half its runtime waiting on zsh. Anything
the harness starts off a pty needs the same pin (`attachcheck.bin_env`).

## Change-only sends trade self-healing for bookkeeping

Until #85 the server resent every pane to every client on each 33ms frame.
That was wasteful, and it also repaired every dropped or out-of-order update
a frame later without anyone having to think about it. Sending only on change
removed that. Three things now carry the guarantee instead, and each one fails
silently as a stale pane:

- **Every mutation path bumps `Grid.Generation`.** A path that changes cells,
  cursor, mouse mode, size or scroll offset without bumping never reaches a
  client until something unrelated changes that pane.
- **Every layout snapshot forces a full resend.** The client prunes mirrors of
  panes that aren't placed, and replaces a mirror that a placement outgrew
  with a blank one. An "unchanged" pane can therefore be missing on the client.
- **Pane broadcasts are serialized (`paneSendMu`).** Several goroutines
  broadcast. Two overlapping rounds could deliver an older render last while
  recording the newer generation.

Delivery is tracked per client (`paneGens`), recorded only when `SendServer`
accepts. There is deliberately no periodic full resend: it would hide a missing
bump.

A missing bump also hides from a quick manual check. Typing into an idle pane
flips its status to Working, and that status snapshot forces every pane to be
resent, so the first keystroke always arrives however broken the generation
is. The first version of the wire test had exactly this blind spot and passed
with `Write`'s bump deleted. Test a mutation path while the pane is already
busy (`TestIdleSessionStopsSendingPaneUpdates` types twice for this reason).

## Reviews verify code against the plan — and one author wrote both

Model selection that worked: cheap models where the plan contains the complete
code, mid-tier where debugging judgment is needed, **the most capable model for
reviews of concurrent or safety-critical code**. That last one repeatedly caught
what cheaper reviews did not.

But no review tier substitutes for running the binary. When the same author writes
the plan and the code, the review inherits both blind spots.

## `CellAt` returns a live pointer. Clone before you keep it.

This bug shape has now appeared **three times** in this repo, which makes it a
hazard of the API rather than three coincidences.

`uv.Line` is `[]Cell` and `Line.At` is `return &l[x]`. So `CellAt`,
`ScrollbackCellAt` and friends hand you a pointer **into the emulator's live
backing array** — and `SafeEmulator` releases its read lock before you get it.
Two consequences, both of which bit us:

1. **Hold it past the lock and it is a data race.** `Draw`'s scrollback branch
   dereferenced one while a pump goroutine mutated the same slot.
2. **Hold it across a `Resize` and it is silent data loss.** `uv.Buffer.Resize`
   narrows by reslicing in place, so the arrays survive. A capture-then-reflow-
   then-write-back loop therefore clobbers source rows that later output rows
   still need, and the first line smears over everything below it.

The second one destroyed pane content on every narrowing, survived an entire
plan plus twelve reviews, and was found only by driving the real binary.

The rule: **if a cell pointer outlives the call that produced it, copy the
cell.** `cc := *c` is enough — `Line.Set` copies by value, and the emulator
replaces whole cells rather than mutating their fields.

## A struct that looks like plain data can still be full of interfaces

`uv.Style` has five fields and reads like a value type. Three of them are
`color.Color` — an interface. `uv.KeyEvent` does not read like a struct at all;
it *is* an interface. Both were put on the wire, and gob refuses to encode an
interface value whose concrete type is not registered.

The failure had three properties that together cost three fix attempts:

1. **It fires on content, not on code paths.** Every blank cell encoded fine.
   The stream died the moment a child printed something coloured — a shell
   prompt, two seconds in. Reproductions that waited 1.5s saw a working attach.
2. **The error surfaced on a pump goroutine**, which did `if err != nil {
   return }` and closed the connection. From the client that is EOF, which is
   also exactly what a clean detach looks like. Exit 0, no message.
3. **No unit test could see it.** `make smoke` only ever ran the in-process
   binary, so nothing it asserts is ever serialised. A wire format that cannot
   encode a coloured cell passed the entire suite.

Registering the concrete types is the obvious fix and the trap. The set is
open-ended — `ansi.ReadStyleColor` alone produces `ansi.IndexedColor`,
`color.RGBA`, `color.CMYK` and `color.Transparent`, `uv.ReadStyle` adds
`ansi.BasicColor`, and the next upstream branch adds more. A registration table
cannot tell you what it is missing, and what it is missing fails at runtime on
a user's prompt.

The rule: **wire types get concrete mirrors, and a test enforces it.**
`protocol.TestWireTypesCarryNoInterfaces` walks every message type by
reflection and fails on any interface it can reach. "Contains no interface" is
a property of the declared type, so it can be checked once for all values;
a roundtrip test only ever covers the values it happens to build. The same
walk also catches unexported fields, which gob drops silently rather than
refusing — the worse of the two failures.

Corollary, learned the same day: **a pump that gives up must say why.** All
four socket pumps swallowed their error. Adding one `slog.Error` turned a
three-attempt hunt into a single line naming the exact type and the exact
missing registration.

Corollary for the harness: **a test suite that only runs one transport tests
one transport.** `scripts/attachcheck.py` exists because `smoke.py` structurally
could not fail here, however many cases it grew.

## `slog.SetDefault` also hijacks the standard `log` package

Go 1.21+ repoints `log`'s default output at the slog handler. Here that handler
writes to a file under the runtime dir, so `log.Fatal` — the one message a user
most needs, `no wideboi server running at ...` — became a silent exit 1.

Wanted for everything else: `log.Printf` from anywhere would otherwise land on
stderr, which is live alt-screen real estate. But the fatal path has to bypass
it. `main` writes to `os.Stderr` directly.

## `ctrl+h` is not Backspace, and three other letters are not themselves

Plan 15 nearly died on an assumption. Binding every verb to a ctrl form
looked impossible because `ctrl+h` is ASCII 0x08, which is BS — so the
focus-left key, the one you most want to repeat, would collide with
Backspace.

It does not. Ultraviolet maps 0x08 to `ctrl+h` unconditionally
(`decoder.go`'s `parseControl`), and the Backspace key sends 0x7F. All
ten verb letters (`h l j k n w x a d q`) have working ctrl forms; `?`
and `esc` have no ctrl form to begin with.

What *is* taken, and permanently: `ctrl+i` decodes as `tab`, `ctrl+m` as
`enter`, `ctrl+[` as `escape`. Those three letters can never carry a
binding with a repeat form. `internal/keys` enforces it, because the
failure mode is a key that silently does nothing when Ctrl is held.

The general lesson is the repo's existing one, applied again: **probe the
pinned dependency, do not reason from the ASCII table.** Two minutes with
`uv.EventDecoder` answered what an afternoon of arguing would not have.

## A smoke test that types a command and then greps for its own text proves nothing

Two cases in `scripts/smoke.py` did exactly that — `s.type("echo marker\r")`
then `if b"marker" not in s.output()`. Readline *echoes what you type*, so
the assertion matches whether or not the shell ever ran the command. Both
cases passed against inverted premises for exactly that reason:

- `case_control_mode_is_sticky` asserted the semantics this plan deliberately
  removed ("if the mode were one-shot the second h would land in a pane as a
  literal letter") and kept passing after the mode stopped being sticky.
- `case_osc133_status_and_smart_jump` reported OK for the entire life of a
  feature that had never worked. Plan 16 fixed the feature and restored the
  case, this time asserting through `focus_pane_id` — and proved it can fail
  by reverting the handler and watching it go red.

The rule to draw: **assert on output the program produced, not on bytes you
sent.** `focus_pane_id` works because it parses the status line's escape
sequences, which only wideboi can emit. A command's *output* works only if it
is distinguishable from its own echoed command line — `echo marker` is not.

## A unit test that supplies its own inputs proves the mechanism, not the wiring

The lesson above is about tests that assert on bytes they sent. This is its
sibling one layer up: tests that assert on *inputs they constructed*.

Plan 16 fixed two features that were fully implemented, unit-tested, green,
and non-functional in production, for the same reason.

- `WipeTransition` was tested against frames the test wrote into itself
  (`"AAA..."`, `"BBB..."`). Its only caller passed two surfaces straight from
  `compose.NewSurface` and never drew into them, so every focus change blanked
  the screen for ~128 ms. The mechanism was proven; the wiring was never
  touched.
- The OSC 133 handler was tested against nothing at all — `internal/server/term`
  had no OSC test — and matched `HasPrefix(s, "A")` against a payload that is
  actually `"133;A"`. No status glyph had ever rendered.

And when the handler was fixed, the glyphs *still* did not appear: `PaneStatuses`
rides on `MsgLayoutSnapshot`, which was only sent for verbs, spawns and kills.
Fixing a dead component can just reveal the next dead link in the chain.

Two rules:

**Something has to test the caller.** A component that takes data from a caller
is only half-covered by a test that hands it data directly. The seam between
"the mechanism" and "the thing that feeds it" is where both of these bugs
lived, and a green suite said nothing about it.

**If the caller is untestable, that is the bug to fix first.** Nothing tested
`Client.Draw` because it took a concrete `*uv.TerminalScreen`, which a unit
test cannot cheaply build. Narrowing it to a `HostScreen` interface — three
cursor methods on top of `uv.Screen` — was the whole of what stood between
this defect and a test that catches it. Look for load-bearing functions no
test calls, and ask whether the reason is mechanical.

Corollary for the red step: **check that a new test fails for the reason you
think.** Two of Plan 16's planned assertions passed before the fix — one
because `Write`'s activity fallback set the same status the test wanted, one
because the status is `Working` immediately after any write either way. Both
were replaced. A test written against a broken system and passing on arrival
is evidence about the test, not the system.

Plan 17 hit the same trap in a form worth naming, because it is specific to
compositing: **something drawn later can paint over the failure you are
looking for.** A test that a sliver's title does not overflow its width
watched the *leftmost* sliver and passed against deliberately broken
truncation — `composeFrameLocked` draws left slivers, then the focused card,
then right slivers, so the focused card had already covered the spill by the
time the test read the screen. Retargeted at the rightmost sliver, which
nothing is drawn after, it failed correctly. When asserting that something
does not escape its bounds, assert it where nothing else can tidy up.

## A force-push to an open PR can race the merge, silently

Plan 16 was reviewed, then force-pushed once more with a docs-only commit
recording a measurement, then merged. The merge landed the **pre-force-push**
head: the pushed tip `013218e` contained the new section 8 content, the
squashed merge commit on `main` did not, and nothing anywhere reported a
problem. The push succeeded. The merge succeeded. The work was gone.

It surfaced a session later, by accident, when a `grep` for content that
should have been in the roadmap doc came back empty.

What makes this nastier than an ordinary lost commit is that every signal is
green. `--force-with-lease` does not help — it guards against overwriting
commits you have not fetched, not against a reviewer merging a head you have
since replaced. The PR page shows the new commit. The merge button merges
whatever it resolved earlier.

Two habits:

- **Once a PR is handed over for review, stop force-pushing.** If something
  needs adding, say so and wait, or push a normal commit on top and let the
  squash pick it up — a new commit is visible to the merge in a way a
  rewritten head is not.
- **After a merge, verify the merged tree contains your last change**, not
  just that the branch merged. `git show <merge>:path | grep <thing>` is one
  line and is the only thing that would have caught this at the time.

## An unverified comment becomes an unverified roadmap entry

`compose.WriteString`'s doc comment described, in careful detail, a bug the
code did not have. It said width was ignored and a double-width glyph would
push the rest of the line left. `WriteStyled` had advanced by `cell.Width`
the whole time.

That would have been harmless on its own. It wasn't, because the comment got
believed twice:

- The roadmap's parked-defect list carried a row — "`compose.Text`/`WriteString`
  ignore `Cell.Width`" — sourced from the comment rather than the code. Half
  of it was true (`Text` really does emit one rune per cell) and half was
  fiction, and the row read as one finding.
- Plan 17 budgeted a phase to build a width-aware writer, on the strength of
  the row. It was most of the way to a redundant function before a probe
  showed `"日X"` already landing `X` at column 2.

Nothing tested the claim either way, which is what let it drift: the comment
was written when it was true, or was never true, and no run would have said
so. **A comment asserting a behavioural property is an untested assertion.**
When one is load-bearing enough to appear in the roadmap, pin it —
`TestWriteStyledAdvancesByMeasuredWidth` is four lines and would have stopped
all of this.

The corollary for the roadmap specifically: **a defect row should cite the
code, not the comment.** The roadmap — now the GitHub issue list — is
re-read at the start of planning and its rows become work. A row sourced from prose inherits whatever
that prose got wrong, and the error compounds — by the time someone acts on
it, two documents agree and neither is the code.

## Never write the terminal's last column

Ultraviolet brackets a write to the final column with autowrap-toggle escapes
(`ESC[?7l` … `ESC[?7h`). On screen this is invisible. On the wire it splits your
text across escape sequences, so a raw-byte assertion sees `alt+` and `q` rather
than `alt+q`. Truncate to `cols - 1` and leave the last cell alone.

Related: measure truncation in **cells**, not bytes or runes.
`compose.WriteString` advances by `Cell.Width`, so a double-width glyph consumes
two columns while `len()` counts three bytes and a rune count counts one. All
three disagree, and only `Cell.Width` is right.

## `go test` caches, and a cached pass looks exactly like a real one

An implementer here re-ran a concurrency test after a change, saw 20/20 clean,
and nearly reported it. It was the result cache. With `-count=1` the true rate
was 14 failures in 15 runs.

**Always pass `-count=1`** when a test's outcome depends on code you just
changed, on scheduling, or on the race detector. `make race` does this; ad-hoc
runs must too.

## A red check leaves a broken binary behind

The smoke and attach suites run `./bin/wideboi`, not the source. Proving a
new smoke case can fail means breaking the code, rebuilding, and watching it
go red. Then restoring the source is only half of the undo. Until the next
`make build`, every run tests the sabotaged binary.

It cost a round once already. Four straight "28 passed, 1 failed" runs looked
like a flake in the new case, and they were really the deliberately broken
build. `make check` rebuilds, which is why it came back green and made the
standalone runs look even more like a timing problem.

**Restore and rebuild in the same command as the break**, and treat any
standalone `scripts/smoke.py` result as suspect until you know which binary
it ran.

## The shape of your test fixture is part of your coverage

Every reflow test in this repo wrote exactly **one line** of content. With one
line, the aliasing bug above is benign — the destination row was blank, so
nobody's source got clobbered. That single-line fixture shape is the entire
reason a content-destroying bug survived a plan and twelve reviews.

When a bug class depends on interaction *between* rows, columns, panes or
messages, a fixture with one of the thing cannot see it. Ask what the fixture's
shape makes structurally invisible, not just whether the assertion is right.

## A binding nobody typed is a binding nobody verified

`cmd/wideboi/main.go` matched the key name `"pgdn"`. Ultraviolet's name
for that key is `"pgdown"`, so `MatchString("pgdn")` is false for every
event that will ever exist — PageDown scroll never worked, from the
commit that introduced it.

Nothing could have caught it. `MatchString` takes a string and returns a
bool; an unparseable name is indistinguishable from a key the user did
not press. There is no error, no panic, no log line. The binding sat in
the matrix through two plans looking exactly like the `pgup` line above
it, which did work.

**Any binding table entry that no test types is decoration.** When the
matching API swallows bad names silently, enumerate the table in a test
and assert each entry routes somewhere — the assertion is cheap and it
is the only thing standing between a typo and a feature that does not
exist.

## A binding that depends on terminal configuration is a broken binding

v1 bound every verb to `alt`. On macOS, terminals do not send Option as
Meta by default, so on the author's own machine **every shortcut did
nothing** — and did it silently, with no error and no clue. It survived
a full plan, twelve reviews and a merge, because every test drove the
key *bytes* directly and so could never observe that a real terminal
would not produce them.

Plan 5 treated this by documenting the fix and adding `ctrl+q`/`ctrl+o`
as an escape hatch. That was the wrong layer: it accepted a dependency
on per-terminal, per-profile configuration and tried to explain it.
Plan 6 removed the dependency.

The general form: **prefer input that every terminal produces
unconditionally over input that most terminals can be configured to
produce.** A control byte is one of the former; a Meta-modified key is
one of the latter. When you cannot avoid the latter, the question to ask
is not "have we documented it" but "what does a user see when their
terminal does not do this" — and the answer must not be "nothing".

Corollary for tests: a test that synthesises the input bytes is testing
your decoder, not your binding. Neither the unit tests nor the pty smoke
suite could have caught this, because both typed the escape sequence an
already-configured terminal would send.

## A new default binding is a breaking change to every config that uses its key

`[keys]` remapping and the default table share one namespace, and
`BuildBindings` rejects a collision rather than letting one side win.
So adding a default key is not additive: any config that already
remapped some other action onto that key stops loading, with a
duplicate-key error at startup.

#80/#74 bound `y`, `u`, `tab` and `0`-`9`. The documented remap example
was `scroll_up = "u"`, and it had been copied into five places: the
README, the smoke config case, and fixtures in `internal/keys`,
`internal/client` and `internal/config`. Each one failed as a
collision, and each was moved to `e`. Users who followed the README
got the same error, which only a note in the PR warned them about.

**Before adding a default key, grep for remaps onto it**
(`= "<key>"` in `README.md`, `config.example.toml`, `scripts/` and
`*_test.go`), and put the compat break in the PR description.

## The help overlay is exactly full at 80x24

Bindings with no `BarGroup` are listed only in the overlay, and the
overlay has to fit on the smallest common terminal or the footer
("any key closes this") is clipped. Since #80/#74 it is **exactly 24
rows** at 80x24, with no room to spare. It got there by collapsing
pairs onto shared lines with `Binding.HelpGroup` (`h/l`, `j/k`,
`o/p`, `y/u`, and one `0-9` line for the digits).

`TestHelpOverlayFitsAt80x24` fails on the next overlay-only binding.
That is the test working. The fix is to group the new binding with a
related one, or to redesign the overlay (columns, scrolling). Raising
the limit is not a fix. Count rows, not lines: the border adds two,
and forgetting it is how #80/#74's plan predicted 26 rows when it was
really 28.

## Go respects an inherited `SIG_IGN` for SIGHUP and SIGINT

POSIX requires a shell to set SIGINT and SIGQUIT to `SIG_IGN` for an
asynchronous list, so anything started as `cmd &` — which is every target under
`make -j` — inherits them ignored. Go's runtime deliberately honours that for
**SIGHUP and SIGINT only** (`sigInstallGoHandler`), and installs its own
handler over an ignored SIGTERM.

`signal.Notify` installs a handler regardless, so a guard still *catches* the
signal and tears down correctly. `signal.Stop` and `signal.Reset` then restore
the **original** disposition — `SIG_IGN` — and the conventional
`syscall.Kill(os.Getpid(), sig)` re-raise is discarded. `wideboi &` followed by
Ctrl-C tore the session down, restored the terminal, and then lived forever,
immune to every signal it had armed.

Two things this cost, both worth knowing in advance:

- **A test written against SIGTERM cannot reproduce it.** The first attempt
  failed for the wrong reason — the child died by the signal — because Go
  installs its own handler over an ignored SIGTERM. Use SIGINT or SIGHUP, and
  inherit the disposition through `sh -c 'trap "" INT; exec ...'` rather than
  `signal.Ignore`, which exercises Go's bookkeeping instead of the real case.
- **Deciding after a failed re-raise is a race.** `Kill` can return before the
  signal is delivered, so "did `Kill` return?" is not a test — `os.Exit`
  sometimes wins and the process exits `128+signo` when it should have died
  *by* the signal. Sample `signal.Ignored` at arm time, before `Notify`.

## Widening what a value can be re-scopes every existing use of it

`transport.NewSocketListener` unlinks a stale socket so a server that died
without cleaning up does not block the next one. That was safe for as long as
the path was a fixed `default.sock` inside a wideboi-owned directory: the only
thing it could ever delete was its own corpse.

Adding a `WIDEBOI_SOCK` override — a small, obviously-useful escape hatch —
turned that same unlink into *delete any file the user names*.
`WIDEBOI_SOCK=~/notes.txt wideboi server` removed the file before failing to
listen. Confirmed by a test that writes a regular file and watches it vanish.

**When you widen the domain of a value, re-read every consumer of it.** The new
configuration did not introduce the `os.Remove`; it removed the invariant that
had made the `os.Remove` harmless. Nothing about the diff that added the
override looked dangerous, because the dangerous line was somewhere else and
unchanged.

## One green run is how a flaky suite presents

This has now been paid for three times, in three different shapes:

- A `quiet=0.10` settle window gave the best number and passed 25/25 on the
  first run, then failed on each of the next three — three *different* cases,
  never the same twice.
- Parallel `make check` passed four consecutive runs, then failed 1 in 8, in
  two different suites. Two real races that contention exposed.
- `go test` caching (see above) showed 20/20 clean where `-count=1` showed 14
  failures in 15.

**A timing or concurrency change is not verified until it has repeated.** Run
it four times, and prefer the slower number that holds over the faster one that
does not. Both parallelism failures above were fixed rather than tuned around
once the repeat runs pointed at them — a fixed `time.sleep` before an
assertion, and a startup wait that returned before the thing being waited for
existed.

## `verify-exit` reports your own detached session as a stray

`scripts/ptycheck.py`'s stray scan scopes by parentage: a copy of
`bin/wideboi` whose parent is pid 1 counts as leaked, because that is what an
orphan looks like. A detached session server also has pid 1 as its parent. So
if you have a session running from this checkout's `bin/wideboi` while
`make check` runs, every `verify-exit` case fails with the same
`wideboi server --owner-fd 3` pid, and every other assertion passes.

Before blaming the branch, check the stray's start time against the run's. A
server that predates the run is yours: end that session and rerun. Don't
loosen the scan. The ppid-1 rule is the only thing that catches a real
orphan.

## Unbinding one member of a group breaks every line that describes the group

`[keys]` can unbind an action with `[]` (#99). Pairs like `h`/`l` share one
help-overlay line, "focus the column left / right", through `HelpGroup`. The
spec thought through the status bar's move-group label (drop the missing
letter) and missed the help line, so `focus_right = []` left `h` advertising a
direction that no longer existed. Review caught it; `BuildBindings` now clears
`HelpGroup` on the survivors so they fall back to their own `Long`.

**When a change can remove a member from anything grouped — `BarGroup`,
`HelpGroup`, `HelpKey` — check every consumer of the group,** not just the one
the change is about. `internal/client/help.go` and `keys.BarItemsFor` are the
two today.
