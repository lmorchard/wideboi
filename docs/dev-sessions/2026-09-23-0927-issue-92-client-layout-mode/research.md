# Research: layout mode today

Documentarian pass, 2026-09-23. Facts only; design lives in `spec.md`.

## Lifecycle

- `protocol.LayoutMode` (`internal/protocol/messages.go:165-179`): `LayoutScroll = 0`, `LayoutCards = 1`. Doc comment: "Shared session state, like focus"; zero value is scroll for wire compat.
- Resolution: `config.Load` (`internal/config/config.go:71`). Default `"cards"` (81) → TOML (109-111) → `WIDEBOI_LAYOUT` (138-140) → `--layout`/`-l` flag (155-157). Validation 170-179.
- `main` runs `config.Load` once for every subcommand (`cmd/wideboi/main.go:170`).
- `parseLayout` (`main.go:208-229`) has **no non-test caller**; only `main_test.go:165,178`.
- Only `runServer` uses the mode: `srv.SetLayout(cfg.LayoutMode)` (`main.go:280`).
- `runClient` (`main.go:358`), used by both `attach` and plain `wideboi`, reads LogLevel, Socket, MouseEnabled, Prefix(Label), bindings. **It never reads `cfg.LayoutMode`.** When attaching to an existing server, the local `--layout`/env/TOML value is resolved and discarded.
- Spawned server (`cmd/wideboi/spawn.go`): `serverArgs` forwards the user's argv (72-84) and the env is inherited (66-67), so it sees the same `--layout`/`WIDEBOI_LAYOUT`. fd 3 carries only the owner connection.
- `NewServer` leaves `s.layout` at zero = `LayoutScroll` (`internal/server/server.go:92-116`). `SetLayout` (68-73) exists because of that.
- Toggle: `VerbToggleCards` (`server.go:305-315`) flips the mode, `ApplyMode`, deliberately no resize, then `broadcastLayout`.
- Nothing persists it beyond the server process's lifetime.
- To clients: `broadcastLayout` (`server.go:642-689`) → `MsgLayoutSnapshot.Layout` (653) → `Client.HandleServerMsg` sets `c.layoutMode` and `ApplyMode(c.strip, …)` unconditionally (`internal/client/client.go:143-144`, comment 134-142).
- `layout.ApplyMode` (`internal/layout/layout.go:84-91`): the shared helper both sides use.

## Key path

- `internal/keys/keys.go:44-60` Action kinds: `ActionVerb` (has `Verb`), `ActionScroll`, `ActionQuit`, `ActionDetach`, `ActionHelp`, `ActionExit`. Comment 41-43: non-verb actions "are not verbs the server knows about".
- Toggle row `keys.go:153-154`: `toggle_cards`, key `c`, `ActionVerb`/`VerbToggleCards`, `NoRepeat`, no `BarGroup`. `BuildBindings` forces `BarGroup=""` for it (370-371).
- `VerbToggleCards` was appended to the verb enum for wire stability (`messages.go:18-20`).
- `cmd/wideboi/router.go` `fire` (134-164) maps actions to routes. `ActionHelp` is the precedent for a purely local action (router state `help=true`, route `routeIgnore`; mirrored into `Client.helpVisible` via `SetHelpVisible`, `main.go:533-534`, `client.go:841-855`).
- Dispatch `main.go:486-534`: `routeVerb` → `cli.SendVerb` → `MsgVerb` (`client.go:883-885`).
- Other client-local state: control mode, help overlay, mouse selection/copy. `SendResize` recomputes placements locally and also sends `MsgResize` (`client.go:929-931`).

## Readers of mode / strategy

- Client: `c.layoutMode` read at `client.go:538` (scroll dividers) and `559-576` (card dividers). `c.strip` is its own `Strip` (`client.go:97`); `ComputePlacements` at 148 (snapshot) and 930 (resize). Hidden counts/`+N` (656-710) are mode-agnostic. Motion (`motion.go`) and mouse (`mouse.go`) work off placements only; a geometry change on toggle animates (`client.go:164-166`).
- Server: `s.layout` read only at 306 (toggle) and 653 (snapshot). Strategy used only in `broadcastLayout` (644) to fill `Placements`. Resize, pane updates, mouse and scroll are all strategy-independent (`server.go:436-449, 505-535, 691-705, 336-344`).
- `Strip` strategy state: `scrollX` (ScrollStrategy), `cardFirst` (CardStrategy) (`layout.go:36-43`). `SyncColumns` (347-357) copies columns + focus only, so each side's `scrollX`/`cardFirst` is already per-instance.

## `MsgLayoutSnapshot` (`messages.go:181-191`)

| Field | Server source | Client use |
|---|---|---|
| `Columns` | `ToColumnData(strip.Columns())` | `SyncColumns`; non-empty selects local compute |
| `Placements` | server `ComputePlacements(s.cols, s.rows)` | only when `len(Columns)==0` (149-150), when it is nil anyway (strategies return nil for zero columns) |
| `FocusPaneID` | strip focus | sync + `c.focusPaneID` |
| `PaneStatuses`, `PaneTitles` | server | copied |
| `Layout` | `s.layout` | `c.layoutMode` + `ApplyMode` |

Wire: gob (`internal/transport/socket.go:75-84`). Hygiene tests `internal/protocol/wire_test.go:42,89,126`; round-trip list `internal/transport/wire_test.go:158` (its snapshot omits `Layout`).

## Tests pinning current behaviour

- Config: `config_test.go:22,51,136,182` (default cards, precedence, invalid, example TOML).
- CLI: `cli_test.go:60,89,115,197`; `main_test.go:163,171` (parseLayout).
- Keys: `keys_test.go:260` TestToggleCardsIsOnC (exactly one ActionVerb/VerbToggleCards on `c`).
- Server: `status_test.go:172` TestToggleCardsFlipsLayoutMode (also pins the NewServer scroll default), `:192` TestToggleCardsLeavesColumnWidthsAlone. `server_test.go:54,82,124,241,253,303` placement-count asserts that rely on the server's default scroll strategy.
- Client: `cards_test.go:44,69,90` (apply/revert/default-scroll from snapshot), `:509,533` (empty snapshot still applies mode), `:550,573` (dividers per mode), plus many card/scroll rendering, motion (`motion_test.go`) and mouse (`mouse_test.go`) tests that select a mode *via the snapshot*.
- **No test covers sharing across clients or surviving reattach.** `main_test.go:17` checks reattach focus only.
- Scripts: `smoke.py:623` case_card_layout_toggles (C-b c moves the cursor column, twice restores). `smoke.py:647`, `:308`, `:333` pass `--layout scroll` to plain `wideboi`. `attachcheck.py` starts `wideboi server` with no layout flag; comment at 583 assumes card layout; 244 requires a divider glyph. `smoke.py` does not pin `WIDEBOI_LAYOUT` or `XDG_CONFIG_HOME`.
