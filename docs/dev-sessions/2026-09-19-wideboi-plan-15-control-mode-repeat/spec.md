# wideboi Plan 15 — Control-mode repeat, hjkl, and a help overlay

**Date:** 2026-09-19
**Status:** Spec approved, plan pending

## Goal

Make control mode's stickiness explicit per keystroke instead of global:
**Ctrl-modified verbs stay in the mode, unmodified verbs act and leave.**

`C-b C-k C-k k` scrolls a pane's history up three times and drops you back
to typing. `C-b l` moves focus right once and drops you back to typing.

Three things ride along, because they are the same table and it is cheaper
to move it once:

1. Scroll-down, which **does not exist today** in any binding.
2. `hjkl` as the spatial verb set, which scroll-down forces anyway.
3. A `?` help overlay, which the resulting table no longer fits in the
   status bar without.

## Why

**The mode is sticky, always.** `C-b l` moves focus and leaves you in
control mode; you must then press Escape. Three keystrokes for one action,
and forgetting the third means your next character is silently swallowed —
the mode ignores unknown keys by design, so a user who thinks they are
typing gets nothing and no feedback. That is the papercut.

The prior art is tmux's `bind -r`, which makes a binding repeatable within
a `repeat-time` window. That is a guess about human intent and it is wrong
in both directions: too short and your second press escapes the mode, too
long and an unrelated keystroke gets eaten. Making the modifier carry the
signal removes the timer and the guess. It also matches the hand: you are
already holding Ctrl from the prefix.

**Scroll-down is missing.** `cmd/wideboi/router.go` binds `u` to
`routeScroll{Scroll: 10}` and nothing else touches scrollback. A user can
walk a pane's history up and has no binding to come back down. Plan 13's
spec called for `Ctrl+D` to scroll down; it was never implemented, and this
plan's rule claims the whole `ctrl+<letter>` space for "repeat", so
`Ctrl+D` is no longer available for a distinct verb. Settle it here or
inherit a worse choice later.

## The input model

Normal mode is unchanged: the prefix enters control mode, everything else
goes to the focused pane.

In control mode:

| Key | Action | Mode after |
| --- | --- | --- |
| `ctrl+<verb>` | perform the verb | **stays in control mode** |
| `<verb>` | perform the verb | exits |
| `?` | open the help overlay | overlay, then back to control mode |
| `esc` | nothing | exits |
| the prefix again | forward the literal prefix byte to the pane | exits |
| anything else | nothing | exits |

### Decisions inside the rule

**Unknown keys exit regardless of modifier.** A stray `ctrl+g` exits just
as a stray `z` does. The alternative — ctrl-anything keeps the mode,
because holding ctrl signals intent to continue — reads well until you
consider the failure it preserves: a keystroke that did nothing leaves you
in a mode that silently eats the next one. Uniform exit means no keystroke
can ever strand you. The cost is that a near-miss mid-repeat drops you out,
which is recoverable in one keypress.

This also *narrows* an existing hazard rather than widening it. Today every
unknown key keeps the mode; after this change none does.

**`ctrl+d` and `ctrl+q` behave exactly as `d` and `q`.** "Detach but stay
in control mode" is meaningless when the client is leaving. Binding them
identically beats leaving them unbound: an unbound key inside a mode that
swallows unknown keys is indistinguishable from a broken keyboard.

**`?` is a mode key, not a verb.** `ctrl+?` is not a combination terminals
reliably produce, so `?` could never have a repeat form — it is an
exception to the rule whichever way we file it. Grouping it with `esc`
makes the exception explicit instead of silent, and lets dismissal return
to control mode, which is what you want after looking up a binding.

## The verb table

### New bindings

| Key | Verb | Was |
| --- | --- | --- |
| `h` / `l` | focus left / right | unchanged |
| `j` / `k` | scroll history down / up | `k` is new; `j` **was smart-jump** |
| `n` | new column | unchanged |
| `w` | cycle width | unchanged |
| `x` | kill pane | unchanged |
| `a` | jump to a pane wanting attention | **was `j`** |
| `d` | detach (socket-attached clients only) | unchanged |
| `q` | quit | unchanged |
| `?` | help overlay | new |
| `esc` | leave control mode | unchanged |

`u` stops doing anything. Arrow keys keep their current aliases for focus,
and are one-shot: `ctrl+left` and `ctrl+right` are not combinations every
terminal produces, so they get no repeat form and are not bound.

`hjkl` is the point: horizontal moves focus, vertical moves history, which
is the arrangement a vim-trained hand guesses correctly without reading
anything. That arrangement is what evicts smart-jump from `j`; `a` for
"attention" matches what the verb does and what the status glyph means.

### Where it lives

Today the table exists twice — a `switch` in `cmd/wideboi/router.go` and a
`[]string` in `internal/client/client.go` — and the two are kept in
agreement by `scripts/smoke.py` asserting the *strings* match. Adding
scroll-down, moving smart-jump, and feeding an overlay would make it three
copies.

**One table, in a new `internal/keys` package**, carrying per verb: the
letter, the `protocol.VerbType`, a short label for the status bar, a long
description for the overlay, and whether the verb requires a detachable
session. `cmd/wideboi`'s router and `internal/client`'s bar and overlay all
read from it.

The package is small and has one job, so it stays inside the seam rule that
`internal/client` must not import `internal/server` — `keys` imports only
`internal/protocol`. It is also the natural thing a config file would later
write into, rather than something config has to reach around; see
`docs/BEYOND-V1.md`.

The alternative considered was keeping the copies and adding a test that
asserts they agree. Cheaper today, but it only catches drift someone
thought to write a test for, and it does nothing for remapping.

## The help overlay

`?` in control mode raises a centred box over everything. Any key dismisses
it and returns to control mode — **the dismissing key is swallowed**, not
also executed as a verb. Pressing `k` to clear the overlay must not scroll.
Overlay state is checked before the verb table, not after.

The router owns `help` as a mode flag, and `main` mirrors it to the client
after every key exactly as it already does with `SetControlMode` — so the
bar, the overlay and the router cannot disagree about which mode is active.
That mirroring already exists for a reason; reuse it rather than inventing
a second mechanism.

Content renders from `internal/keys`, so the overlay cannot drift from the
bindings it documents. It shows each verb's letter and long description,
the configured prefix by name (not a hardcoded `C-b` — `WIDEBOI_PREFIX`
exists), and the ctrl rule with `C-b C-k C-k k` worked through.

Three constraints:

- **It draws last and takes precedence over an active wipe.** `Client.Draw`
  returns early while a wipe is running; the overlay check goes above that.
  A wipe is decorative, a modal is not.
- **It must survive a viewport smaller than the box.** `make verify-exit`
  runs wideboi at 4x2, 1x1 and 0x0, and the last two are exactly where an
  unguarded centred-box origin goes negative. Clip to the viewport, drop
  rows that do not fit, never compute a negative origin.
- **Cursor hidden while it is up**, for the same reason control mode hides
  it: a visible cursor claims keystrokes are reaching a pane.

## What was measured, not assumed

**Every verb has a working ctrl form.** Decoded the raw byte a terminal
sends for each `ctrl+<letter>` through `uv.EventDecoder`, then asked
`MatchString` about the result:

| verb | byte | decodes as | `MatchString("ctrl+X")` |
| --- | --- | --- | --- |
| h l n w x j u d q a k | 0x08 0x0c 0x0e 0x17 0x18 0x0a 0x15 0x04 0x11 0x01 0x0b | `ctrl+h` … | all `true` |
| i | 0x09 | `tab` | `false` |

`ctrl+h` was the expected blocker and is not one: ultraviolet maps 0x08 to
`ctrl+h` unconditionally, and the Backspace key sends 0x7F. So `h` and `l`
— the two you most want to repeat — are clean.

**Reserved forever: `i`, `m`, `[`.** Their control bytes are Tab, Enter and
Escape, so a verb on any of those letters could never have a repeat form.
No verb may use them. This is enforced by a test in `internal/keys`, not
left as a comment.

**The new table does not fit the 80-column status bar.** Measured against
the existing 79-cell budget, with `d detach` present:

| bar | cells | fits |
| --- | --- | --- |
| descriptive labels, no rule hint | 81 | no |
| descriptive labels + `^=stay` hint | 89 | no |
| `hjkl move` merged + `^=stay` hint | 77 | yes |
| `hjkl move` merged + `? help`, no hint | 77 | yes |

The first row is the finding that matters: **adding scroll-down busts the
budget on its own**, independent of this plan's rule. The spec's "at 80
columns every verb fits" measurement and
`TestControlHelpFitsEveryVerbAt80Columns` were both written against a
smaller table and do not survive it.

Resolution: the bar carries `hjkl move` as one entry plus `? help`, and the
overlay does the teaching. The bar keeps its job — naming what exists — and
stops trying to also explain a modifier rule in eight cells.

## Migration hazards

**`j` silently changes meaning**, from smart-jump to scroll-down. A hand
that knows the old table will scroll when it meant to jump. Not
destructive, and the overlay is one keystroke away, but it is the one
change that fails quietly rather than doing nothing.

`u` → `k` is the same class and louder: `u` stops doing anything at all,
which is immediately obvious.

## Testing obligations

`docs/LESSONS.md` is unusually specific about this area, because two of its
entries were written by defects in exactly this code.

- **Enumerate, do not sample.** The router test covers every table entry ×
  {plain, ctrl} × {detachable, not}. `MatchString` returns `false`
  indistinguishably for "key not pressed" and "name I can never produce" —
  that is how the `pgdn` binding shipped dead through two plans.
- **Type the real bytes in `scripts/smoke.py`.** A unit test that
  synthesises `KeyPressEvent{Code:'l', Mod:ModCtrl}` tests the router, not
  the binding. New cases send actual `0x02 0x0c 0x0c 0x6c` and assert focus
  moved three columns and the bar left control mode.
- **`internal/keys` guards its own shape:** no duplicate letters, no letter
  from the reserved set, every entry has both labels and a verb.
- **Overlay geometry at 0x0, 1x1 and 4x2** — unit test, no panic, no
  negative origin.
- **Break every new test and watch it go red** before calling it done. This
  repo has had three tests pass against the bug they were written to catch.

Rewrites: `case_control_mode_names_every_verb_at_80_columns` in `smoke.py`
and `TestControlHelpFitsEveryVerbAt80Columns` in `help_test.go`. The golden
snapshot captures startup only, where the normal-mode status line is
unchanged, so it should not move — if it does, that is a finding, not a
file to regenerate.

## Out of scope

- **Config-driven remapping.** Recorded in `docs/BEYOND-V1.md`.
  `internal/keys` is built as the thing config would populate, but this
  plan ships a fixed table.
- **Smart-jump does not cycle.** `VerbSmartJump` picks the first match from
  a Go map iteration, which is randomised, so repeating it does not walk
  through the panes wanting attention. Repeating it under the new rule
  therefore does something arbitrary. Pre-existing, out of scope, worth its
  own fix.
- **Scroll granularity.** `j`/`k` keep the existing ±10 lines. Whether that
  should be a half-page relative to pane height is a separate question.
