# Terminal Metadata Channels in Wideboi

This document details how Wideboi consumes and propagates out-of-band terminal
metadata (Operating System Command / OSC escape sequences) emitted by child
processes (interactive shells, CLI tools, and coding agents).

---

## 1. The Metadata Channel: CSI vs. OSC

In ANSI/VT terminal emulators, escape sequences divide into two main channels:

- **CSI (`ESC [ ...`)**: In-band display and grid manipulation. CSI controls cursor
  positioning, text attributes, erasing, color palettes, and terminal modes (such as
  the alternate screen buffer or bracketed paste).
- **OSC (`ESC ] <cmd> ; <payload> <ST|BEL>`)**: Out-of-band metadata channel. OSC
  sequences communicate higher-level status and environmental metadata between a
  running application and the terminal emulator (or host desktop). They do not place
  cells on the grid.

Wideboi's server terminal emulator (`charmbracelet/x/vt`) intercepts OSC sequences
at the PTY boundary (`internal/server/term/grid.go`). Relevant sequences update
pane metadata, which is broadcast to clients to drive layout chrome and card slivers.

---

## 2. Supported Protocols & Mappings

Wideboi supports three classes of terminal metadata:

### A. Terminal & Window Titles (OSC 0, 1, 2)

- **Formats**:
  - `ESC ] 0 ; <title> BEL` (set window title and icon name)
  - `ESC ] 1 ; <title> BEL` (set icon name)
  - `ESC ] 2 ; <title> BEL` (set window title)
- **Engine**: Handled natively by `x/vt` via `Callbacks.Title func(string)`.
- **Consumption**:
  - `Grid.Title() string` exposes the current title atomically.
  - Full pane headers render the title alongside pane ID and status glyph.
  - Card slivers in card fan layout (`PlacementSliver`) render the title on the
    top row, truncated to sliver width (typically 4 columns).

### B. Shell Integration / Semantic Prompts (OSC 133)

- **Origin**: FinalTerm specification, adopted by iTerm2, VS Code, WezTerm, Ghostty.
- **Formats**:
  - `ESC ] 133 ; A [; <params>] BEL`: Prompt start.
  - `ESC ] 133 ; B [; <params>] BEL`: Prompt end (command input start).
  - `ESC ] 133 ; C [; <params>] BEL`: Command executed / output start.
  - `ESC ] 133 ; D [; <exitcode> [; <params>]] BEL`: Command finished with exit code.
- **Mapping to `term.PaneStatus`**:
  - `A` / `B` &rarr; `StatusNeedsInput` (`!`)
  - `C` &rarr; `StatusWorking` (`»`)
  - `D` or `D;0` &rarr; `StatusDone` (`✓`)
  - `D;<non-zero>` &rarr; `StatusFailed` (`✗`)
- **Payload parsing**: `x/vt` hands handlers the full payload including the command
  number (e.g. `"133;A"`). Wideboi parses by splitting on `;` and matching parameter 1.

### C. Progress Reporting (OSC 9;4)

- **Origin**: ConEmu / Windows Terminal progress reporting protocol, adopted by
  Ghostty, WezTerm, Foot.
- **Emitted By**: CLI tools, progress libraries, and AI agents (e.g., Claude Code).
- **Formats**: `ESC ] 9 ; 4 ; <state> [; <progress>] BEL`
  - State `0`: Clear progress (idle / turn completed).
  - State `1`: Set progress percentage (`1;50` = 50%).
  - State `2`: Error state.
  - State `3`: Indeterminate / busy (active task / turn in progress).
  - State `4`: Warning / paused state.
- **Mapping to `term.PaneStatus`**:
  - State `3` / `1` &rarr; `StatusWorking` (`»`)
  - State `0` &rarr; `StatusDone` (`✓`) or `StatusIdle`
  - State `2` &rarr; `StatusFailed` (`✗`)
  - State `4` &rarr; `StatusNeedsInput` (`!`)

---

## 3. Status State Machine & Precedence

Wideboi tracks pane state across two tiers:

```
                  ┌─────────────────────────────────────────┐
                  │          Authoritative Sources          │
                  │        (OSC 133 and OSC 9;4)            │
                  └────────────────────┬────────────────────┘
                                       │
                               Recognized Sequence
                                       ▼
                              Latches Authoritative
                             (sawAuthoritativeStatus)
                                       │
            ┌──────────────────────────┴──────────────────────────┐
            ▼                                                     ▼
 [Authoritative Latched]                              [Authoritative Unlatched]
 Updates driven strictly                             Fallback Activity Heuristic:
 by incoming OSC sequences.                           - Any Write sets StatusWorking
 (Write fallback & idle decay                         - Status decays to StatusIdle
  are disabled).                                        after idleTimeout (default 3s)
```

### The Fallback Heuristic
When a child pane emits no semantic escape sequences (e.g. a plain shell or batch
command without shell integration), Wideboi uses a write heuristic:
- Any PTY output via `Grid.Write()` sets `StatusWorking`.
- If no writes occur for `idleTimeout` (default 3 seconds), `Grid.Status()` decays
  to `StatusIdle`.

### The Authoritative Latch
Once a pane receives a *recognized* OSC 133 or OSC 9;4 sequence, it latches
authoritative mode (`sawAuthoritativeStatus = true`).
- This permanently disables the activity heuristic on `Write()` and the 3-second
  idle decay timeout for that pane.
- Unrecognized or malformed sequences must **not** latch, ensuring fallback
  heuristics remain active for children with invalid or unexpected payloads.

### Broadcast & Delivery
Status and title changes are edge-triggered:
- `broadcastLayoutIfStatusChanged` checks whether the current status glyphs or
  titles differ from the last broadcast.
- The snapshot (`protocol.MsgLayoutSnapshot`) sends `PaneStatuses` and `PaneTitles`
  across the client/server seam to attached clients.

---

## 4. Child Agent Capabilities & Environment Notes

Interactive CLI agents (such as Claude Code) often gate their use of metadata
channels based on terminal capability detection:

1. **OSC 9;4 Progress Gating**:
   Claude Code checks runtime terminal capabilities before emitting progress:
   - Enabled if `ConEmuANSI`, `ConEmuPID`, or `ConEmuTask` is present.
   - Enabled if `TERM_PROGRAM=ghostty` with `TERM_PROGRAM_VERSION >= 1.2.0`.
   - Enabled if `TERM_PROGRAM=iTerm.app` with `TERM_PROGRAM_VERSION >= 3.6.6`.
   - Disabled if running under unconfigured terminals or raw `xterm-256color`
     without a recognized `TERM_PROGRAM`.
   *Implication*: Wideboi passes `os.Environ()` to child panes, preserving parent
   terminal environment variables where applicable.

2. **Multiplexer Wrapping**:
   Some tools check for `TMUX` or `STY`. When detected, sequences might be wrapped
   in DCS passthrough (`ESC P tmux; ... ESC \`). Wideboi panes do not run tmux, so
   internal DCS passthrough wrapping is not required within child panes.

3. **Card Sliver Geometry**:
   Card slivers in card fan layout (`PlacementSliver`) have narrow width allocations
   (`MinSliverWidth = 4`). A sliver renders `label := strings.TrimSpace(glyph + " " + title)`
   truncated to sliver width. With a 1-cell glyph, a space, and a multi-byte symbol,
   arbitrary trailing title text is clipped.
