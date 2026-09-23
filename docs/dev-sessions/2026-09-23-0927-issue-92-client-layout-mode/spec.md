# Client-owned layout mode Spec

**Goal:** Make layout mode (cards / scroll) a per-client presentation choice that starts from the client's own config on every attach, remove the server's layout state along with its now-pointless placement computation, and show each client its current layout on the status line.

**Source:** https://github.com/lmorchard/wideboi/issues/92 (also closes https://github.com/lmorchard/wideboi/issues/47 and https://github.com/lmorchard/wideboi/issues/91)

## Current state

See `research.md` for references.

- The mode is session state on the server (`internal/server/server.go:43`). `wideboi server` sets it from config via `SetLayout` (`cmd/wideboi/main.go:280`). `VerbToggleCards` flips it (`server.go:305-315`), and every client copies it from `MsgLayoutSnapshot.Layout` (`internal/client/client.go:143-144`).
- The client resolves `--layout`/`WIDEBOI_LAYOUT`/TOML in `config.Load` and never uses the result (`runClient`, `main.go:358`). `wideboi attach --layout scroll` does nothing.
- An accidental `C-b c` therefore persists for the whole life of the session, across reattach, with nothing on screen to say so. This is what prompted the issue; see #91.
- Apart from the toggle, the server uses the mode only to compute `MsgLayoutSnapshot.Placements` (`server.go:644`). The client reads those only when `len(Columns)==0` (`client.go:149-150`), and in that case they are always nil (#47).
- Viewport state (`scrollX`, `cardFirst`) is already per-client, because each side has its own `Strip` and `SyncColumns` copies only columns and focus (`internal/layout/layout.go:347-357`).

## Desired end state

- Every client decides its own layout, and nothing about layout is stored in the session.
- **Starting mode:** each attach (plain `wideboi`, including reattach, and `wideboi attach`) starts in the mode resolved by that client's own `config.Load`, with the usual precedence: flag > `WIDEBOI_LAYOUT` > TOML > `cards`.
- **Toggle:** `C-b c` flips only the client it was pressed in. No message goes to the server. Other attached clients are unaffected. The flip is forgotten on detach.
- **Toggle animation:** the toggle animates the change of geometry, as it does today.
- **Server:** has no layout mode, no `SetLayout`, no toggle handling, and does not compute placements.
- **`MsgLayoutSnapshot`:** has no `Layout` and no `Placements`. The client always computes placements from `Columns`, including when there are none.
- **Indicator (#91):** the normal status line shows this client's mode, `cards` or `scroll`, right-aligned before the prefix hint: `focus: [pane 2 ★]  [1 ●]      cards · C-b for commands`. When the line is too narrow, the hint drops first and the tag stays. The tag drops only when it no longer fits either. The control-mode menu is unchanged.
- **`wideboi --help`:** describes `--layout` / `WIDEBOI_LAYOUT` as the starting layout for this client.

## Design decisions

- **Default comes from the client's own config on every attach.**
  - **Why:** layout is presentation, and presentation is already per-client. Config was already resolved on the client and discarded.
  - **Rejected:** remembering a toggle per client across reattach (needs storage keyed by something, for little value); a server-held "starting mode" that clients copy (keeps the server-side notion we are removing).
- **Toggle is a new client-local action kind (e.g. `ActionToggleLayout`) on key `c`, keeping the ActionName `toggle_cards`.**
  - **Why:** the help overlay is the precedent for a local action (`internal/keys/keys.go:44-60`, `cmd/wideboi/router.go:150-156`). Keeping the ActionName means existing `[keys]` remaps in config keep working. It keeps `NoRepeat` and stays off the status bar.
  - **Rejected:** keeping it as `ActionVerb` and intercepting it client-side. That leaves an action that looks server-bound but is not.
- **`VerbToggleCards` stays in the enum as a reserved slot and the server ignores it.**
  - **Why:** `VerbGrowWidth` and `VerbShrinkWidth` follow it (`internal/protocol/messages.go:18-22`), and deleting it would renumber them on the wire. An old client talking to a new server then gets a harmless no-op.
- **Drop `MsgLayoutSnapshot.Layout` and `.Placements`, and remove server placement computation (folds in #47).**
  - **Why:** with no mode, the server cannot compute placements meaningfully, and the client fallback only ever copied nil. Gob tolerates missing fields in both directions, so mixed old and new builds still connect. A new client ignores an old server's fields, and an old client sees Layout = 0 (scroll).
  - **Rejected:** hardwiring the server to one strategy to keep filling `Placements` (geometry nobody reads).
- **The toggle animates.** The local toggle triggers the same motion that snapshot-driven geometry changes trigger today (`client.go:164-166`), so the feel is unchanged.
- **`wideboi server` accepts `--layout` and ignores it.**
  - **Why:** the config file is shared across subcommands, and a `layout = ...` line must not break `wideboi server`. Erroring would be hostile.
- **Client mirrors are pruned by column, not by placement.**
  - **Background:** `HandleServerMsg` deletes the mirror of every pane that has no placement (`internal/client/client.go`, "Prune inactive mirrors"). That was safe while only a snapshot could change placements, because since #85 every snapshot is followed by a forced resend of every pane (`internal/server/server.go`, `broadcastPaneUpdates(ctx, true)`).
  - **Why change it:** a local toggle changes placements with no snapshot and no resend. Any pane it reveals is covered only by the timing of the last forced resend. This is the "widening what a value can be re-scopes every existing use of it" lesson: the prune was correct under an invariant the toggle removes.
  - **Change:** prune mirrors and `mouseTracking` against the snapshot's `Columns`, i.e. panes that still exist.
  - **Rejected:** relying on the forced resend. It works by coincidence of ordering, and #85's comment already says it exists only to undo this prune.
- **The indicator is right-aligned and outlives the hint (#91).**
  - **Why:** the left edge is pinned by the harness. `scripts/smoke.py` (`FOCUS_LITERAL`, `_focus_digit_re`) tracks focus by the digit at column 13/14 of `focus: [pane N`, so anything added in front of it breaks focus tracking in every smoke and attach case. On the right, the position doesn't move as pane statuses come and go. The mode is live state, and the hint is a string you learn once, so the hint is the first to go.
  - **Rejected:** a tag after the statuses (it moves sideways); replacing the hint (loses new users' only pointer to the prefix); a header-row marker (row 0 carries the card-mode `+N` markers); a control-mode menu entry (the menu is full at 80 columns, per the comment on the `toggle_cards` binding).
  - The labels come from a new `protocol.LayoutMode.String()`, so the tag and `%v` in test failures agree. `config` keeps its own string literals; that isn't refactored here.
- **Delete `parseLayout`** (`main.go:208-229`) and its tests. Nothing outside the tests calls it, and its comment describes the behaviour being removed.

## Patterns to follow

- Local action plumbing: `ActionHelp` → router state → `cli.SetHelpVisible` (`cmd/wideboi/router.go:134-164`, `main.go:533-534`, `client.go:841-855`).
- Recompute-then-draw locally: `SendResize` recomputes placements client-side (`client.go:929-931`).
- Motion arming on a geometry change: `client.go:164-166`.
- Mode to strategy: always through `layout.ApplyMode` (`internal/layout/layout.go:84-91`).
- Wire hygiene tests must stay green: `internal/protocol/wire_test.go:42,89,126`, and the round-trip list at `internal/transport/wire_test.go:158`.
- Smoke cases assert on bytes wideboi produced (`docs/LESSONS.md`, "A smoke test that types a command…"). Prove each new test red first.

## Tests that change

- **Removed or rewritten:**
  - Server toggle tests (`internal/server/status_test.go:172`, `:192`). The width invariant still matters, so it moves client-side: a toggle never sends anything that could resize.
  - Server placement-count asserts that depend on the server's strategy (`internal/server/server_test.go:54,82,241,253,303`). Rewrite them against column data, or delete them where placement is now purely a client concern.
  - `parseLayout` tests (`cmd/wideboi/main_test.go:163,171`).
  - `keys_test.go:260`: now asserts the new action kind.
- **Client tests that selected a mode via the snapshot** (`cards_test.go`, `motion_test.go`, `mouse_test.go`, `screen_test.go`) switch to setting the mode on the client. That is a mechanical change: the helper sets the mode instead of the snapshot field.
- **New tests:**
  - **Client toggle:** flips the strategy, recomputes placements, arms motion, and sends no message.
  - **Initial mode:** comes from `NewClient`'s input or setter, not from the snapshot.
  - **Two attached clients** in different modes stay different after snapshots.
  - **smoke:** `C-b c`, detach, and reattach comes back in cards (the default), asserted on wire geometry.
  - **Status line (#91):** unit tests for the tag in each mode, the hint dropping before the tag, and the tag dropping when nothing fits. A smoke assertion that `C-b c` changes the tag on the wire.
  - **attachcheck or smoke:** `wideboi attach --layout scroll` against a server whose owner is in cards renders scroll.

## What we're NOT doing

- Showing the layout in the control-mode menu or the help overlay (#91 is the normal status line only).
- Remembering a client's toggle across reattach.
- Moving focus client-side. Focus stays session state because input routing depends on it.
- Any change to strategies' geometry (`CardStrategy`, `ScrollStrategy`), `cardFirst`/`scrollX`, or the card look.
- Removing the `VerbToggleCards` constant, or any other protocol renumbering.
- Changing the other broadcast paths (per-pane update filtering, #85).
- Reworking the seam-check allowlist beyond what this change requires.
- Relaxing #85's forced resend after a snapshot. Pruning by column may make it unnecessary, but that is a separate change with its own verification.
- Moving `LayoutMode` out of `internal/protocol`, even though it no longer crosses the wire. Only the doc comment changes.

## Open questions

- **Should the client-side config for layout also be read by `wideboi attach` from a *different* config file than the server's?** Default: it's whatever `config.Load` resolves for the attaching process. No special handling.
- **Mixed-version sessions (old server, new client) will show whichever mode the new client configured, while an old client shows scroll.** Default: acceptable, and not tested beyond the gob tolerance already covered by wire tests.
