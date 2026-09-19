# wideboi Plan 6 — modal input

**Date:** 2026-09-19
**Parent spec:** `../2026-09-18-wideboi-v1-foundations/spec.md`
**Status:** design decided, implementation plan pending

## Why

v1 binds every verb to `alt`, which requires the terminal to send Option as
Meta. On macOS most terminals do not, by default. Measured on the author's own
machine after v1 shipped:

```
iTerm2 profile 'Default'
    Left Option  = 0 : Normal   <- composes ˜ ∆ ø rather than sending Meta
    Right Option = 0 : Normal
```

The result is that **every shortcut silently does nothing** and the program
looks broken. Plan 5 documented the fix and added `ctrl+q`/`ctrl+o` as an
escape hatch, but that treats the symptom: the binding still depends on
per-terminal, per-profile configuration that varies by platform, by terminal,
and over SSH.

`ctrl+b` is a single byte (`0x02`). Every terminal sends it, everywhere, with
no configuration. The dependency disappears rather than being documented.

## The decision

**A tmux-style prefix replaces the `alt` bindings entirely.**

- `ctrl+b` enters control mode. Configurable from day one.
- In control mode, verbs are plain unmodified letters.
- Control mode is **sticky**: it stays active until `Escape`, so `C-b l l l`
  moves three columns.
- `C-b C-b` sends a literal `0x02` to the focused pane.
- The `alt+*` bindings are **removed**, not kept alongside.

### Why prefix-only rather than both

Keeping `alt` would have cost nothing to implement — the matrix already exists
— and would have given a one-keystroke fast path to users whose terminal is
configured. It was rejected for a simpler reason: one documented way to do
things. The README loses its Option-as-Meta section entirely, and there is no
"it depends on your terminal" caveat anywhere in the product.

### Two consequences that fall out

**We claim fewer keys, not more.** Today wideboi takes `alt+*`, plus `ctrl+q`
and `ctrl+o` — and those last two exist *only* as the escape hatch for a broken
Option key. The prefix is that hatch, so both return to the pane. Net: one key
claimed instead of three.

This matters because Plan 5 removed four `ctrl` bindings precisely for stealing
keys the shell needs, and `ctrl+b` is readline's backward-char. Adding it back
is the same category of theft — justified only because it is *one* key, it is
documented, it is escapable via double-tap, and it is rebindable. Those
conditions are load-bearing, not decoration. **Configurability ships in the
first cut, not later.**

**Modal display dissolves the status-line truncation problem.** v1's status
line fights to fit seven verbs into 80 columns and loses — Plan 5 had to drop
`alt+u/d scroll` to keep `alt+q quit` on screen. Modal, normal mode shows
almost nothing and control mode shows the full verb menu with the whole width
to itself. Structural fix, not a squeeze.

## Rejected: preview-and-commit

The original sketch was `prefix → navigate → Enter to commit → exit`. Rejected
for focus movement.

"Preview" only means something if the viewport does *not* move while you
navigate; otherwise it is identical to moving focus and `Enter` is a keystroke
that does nothing. If the viewport doesn't move, you need a second visual
affordance — a highlight distinct from focus — which is a new concept to learn
on top of a new mode.

The real problem it was reaching for is genuine: with twelve columns,
`l l l l l l` would fire six animated transitions. The cheaper answer is
**sticky mode plus coalescing** — do not begin the transition until input
settles. That is the "retarget in flight" idea from the animation design,
applied to wipes: one transition for six keypresses, no preview concept
required.

Preview-and-commit does earn its place in the deferred **card layout**, where
browsing a fan before settling is a genuinely distinct action. Recorded there,
not here.

## Scope

In:

- Prefix key handling, configurable, with double-tap literal passthrough.
- Sticky control mode with `Escape` to exit, and a visible mode indicator.
- The v1 verb set rebound to unmodified keys in control mode: focus left/right,
  new column, cycle width, kill pane, smart jump, scroll, quit.
- Removal of every `alt+*` binding, and of `ctrl+q`/`ctrl+o` as global claims.
- Status line reworked for two modes.
- README rewritten: the Option-as-Meta section is deleted outright.

Out:

- Animation, including the wipe design. Independent of this.
- Card layout and its preview-and-commit interaction.
- Config file plumbing beyond what the prefix needs. If no config file exists
  yet, a single constant plus an environment variable is enough for the first
  cut; do not build a config subsystem for one setting.

## Open questions

1. **Prefix collision when running inside tmux.** Both would claim `ctrl+b`.
   Configurability covers it, but the README should say so explicitly and
   suggest a value.
2. **What the mode indicator looks like.** It must be unmissable — a mode you
   cannot tell you are in is worse than no mode. Reverse-video segment, a
   distinct colour, or a persistent `-- COMMAND --` are all plausible; this is
   a "show, don't tell" question worth mocking before choosing.
3. **Timeout, or Escape only?** tmux's prefix is one-shot with no timeout.
   Sticky mode needs an exit, and `Escape` alone may strand a user who does not
   know it. A short idle timeout as a backstop is worth considering, but it
   fights with coalescing — decide them together.
4. **Does any verb deserve to stay unprefixed?** Quitting from a wedged state
   argues for it, but that is what the signal path is for. Probably no.

## Testing

Per `docs/LESSONS.md`, and specifically:

- Every new smoke case asserts on the wire and is verified red before green.
- The double-tap literal passthrough needs a `cat -v` case proving `^B` reaches
  the pane — the same shape as Plan 5's control-key case, including the
  `stty -icanon -iexten` setup that case needed.
- A case proving `ctrl+q` and `ctrl+o` now reach the pane, since this plan
  gives them back.
- Mode entry and exit must be observable on the wire, which the mode indicator
  makes possible.
