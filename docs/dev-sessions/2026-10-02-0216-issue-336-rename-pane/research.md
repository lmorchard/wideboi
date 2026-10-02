# Codebase Research: Pane Titles and Renaming

## 1. Pane Title Discovery, Storage, and Resolution in Server

- **PTY Read & Terminal Title Discovery**:
  - `internal/server/pane.go:171-187`: The `pty-reader` reads child PTY bytes and forwards them via `p.grid.Write(cleaned)`.
  - `internal/server/term/grid.go:319-331`: `vtGrid` initializes `vt.SafeEmulator` with `Title: func(s string) { g.title.Store(&s) }`.
  - `internal/server/term/oscfix.go:50-138, 165-176`: `oscScanner` intercepts OSC byte sequences, repairs any `0x9C` bytes inside UTF-8 multi-byte characters, and calls `setTitle(string(title))` on string terminator for OSC 0, 1, or 2.
  - `internal/server/term/grid.go:559-564`: `vtGrid.Title()` atomically reads `g.title`. If unset, it returns `""`.
- **Pane Title Resolution**:
  - `internal/server/pane.go:413`: `Pane.Title()` currently returns `p.grid.Title()`.
  - `internal/server/server.go:801`: `Server.paneTitlesLocked()` iterates `s.panes` and collects `out[id] = p.Title()`.
  - `internal/server/server.go:972-990`: In `Server.Run`, every 33ms frame tick checks `!sameStringMap(s.paneTitlesLocked(), s.lastTitles)`. If changed, `s.broadcastLayout(ctx)` is triggered.
  - `internal/server/server.go:641-659`: `updateDashboardLocked` gathers `titles := s.paneTitlesLocked()` and passes `infos` into `s.dashboard.Render(...)`.
  - `internal/server/dashboard.go:72, 98-105`: The status dashboard renders the `TITLE` column from `p.Title` (truncated to 24 chars, or `"-"` if empty).

## 2. Protocol Distribution and Serialization

- **Wire Messages Carrying Titles**:
  - `internal/protocol/messages.go:340-342`: `MsgLayoutSnapshot.PaneTitles` (`map[int]string`) carries all pane titles.
  - `internal/protocol/messages.go:169-173`: `MsgPaneNotification.Title` carries a title for notification alerts.
  - `internal/protocol/wirepb/wideboi.proto:131`: `map<int32, string> pane_titles = 3;` in `message MsgLayoutSnapshot`.
- **Wire Codec**:
  - `internal/protocol/codec.go:167-173`: `MarshalServer` converts `PaneTitles` to protobuf `map[int32]string`, validating UTF-8 via `validUTF8(title)`.
  - `internal/protocol/codec.go:325-329`: `UnmarshalServer` maps protobuf `pane_titles` back to Go `map[int]string`.
  - `internal/protocol/codec_test.go:35-120`: Tests codec round trips and validates that all message types are covered.
- **Protocol Versioning**:
  - `internal/protocol/version.go:39`: `Version = 21` (pre-change).
  - `docs/LESSONS.md:442-452`: Increase `protocol.Version` for any change an older peer would misread or reject; browser subprotocol `wideboi.v<Version>` must match.

## 3. UI Display Across Clients

- **TUI Client**:
  - `internal/client/viewport.go:142`: `Client.applySnapshotLocked` stores `c.paneTitles = m.PaneTitles`.
  - `internal/client/render.go:168-207`: `drawPaneHeaderLocked` renders `title := st.paneTitles[id]` into row Y=0 of the pane card, after the badge capsule.
  - `internal/client/render.go:79-85`: Cards layout mode renders headers for all placed columns, including occluded sliver spines.
- **Web Client**:
  - `web/src/wideboi-app.ts:547-562`: `layoutSnapshot` message updates session state via `reduceSession`.
  - `web/src/session-state.ts:135`: `state.paneTitles = snapshot.paneTitles ?? {}`.
  - `web/src/wideboi-app.ts:1750`: Passes `.cardLabel=${`[${column.paneId}] ${this.paneTitles[column.paneId] || 'Terminal'}`}` to `<wideboi-pane>`.
  - `web/src/wideboi-pane.ts:60-77, 363`: Displays `.card-label` horizontally in full cards, or vertically rotated as sliver spine (`writing-mode: vertical-rl`).
  - `web/src/wideboi-app.ts:1768-1811`: Toolbar tabs render `<span class="tab-title">${title}</span>`.
  - `web/src/components/mobile-bar.ts:68-78`: Mobile pane dropdown renders `[id] {title}`.
  - `web/src/wideboi-app.ts:1409-1525`: `handlePromptCommand` handles prompt commands (`:new`, `:width`, etc.).

## 4. Commands and CLI Dispatch

- **Internal Commands**:
  - `internal/commands/commands.go:34-41`: `Command` definition (`Name`, `Aliases`, `Description`, `Category`, `ArgsUsage`, `Run`).
  - `internal/commands/registry.go:19-67`: Thread-safe registry mapping names and aliases to commands.
  - `internal/commands/registry.go:151-403`: `registerBuiltins` registers built-in commands.
  - `internal/commands/commands.go:137-169`: `SendClientMsg` sends client messages to the server unix socket.
  - `internal/commands/commands.go:205-267`: `RPCQuery[Resp]` performs request-response queries over the socket.
- **CLI Subcommand Structure**:
  - `cmd/wideboi/main.go:127`: Subcommand list in `parseCLI` (`split`, `send`, `capture`, `close`, `wait`, `upgrade-server`, `web`, `prompt`, `palette`, `trust`, `untrust`).
  - `cmd/wideboi/main.go:316-379`: Switch statement dispatching subcommands to runner functions.
  - `cmd/wideboi/control.go`: Implements subcommand runners (`runSplit`, `runClose`, etc.).
  - `internal/server/server.go:400`: Server injects `WIDEBOI_PANE_ID=<id>` into child process environments.

## 5. Persistence and In-Place Upgrades

- **In-Place Upgrades (#250)**:
  - `internal/server/upgrade.go:48-60`: `UpgradePane` carries pane metadata across `syscall.Exec`.
  - `internal/server/upgrade.go:140-147`: `buildUpgradeStateLocked` captures `UpgradePane` per pane, including `GridSnap` via `term.Snapshotter`.
  - `internal/server/upgrade.go:320-350`: `RestoreState` reconstructs panes and restores grid snapshots.
  - `internal/server/term/grid.go:917, 1070`: `term.GridSnapshot` stores and restores `Title: g.Title()` (the underlying terminal title).
- **Session Resurrection (#326)**:
  - Currently an open issue (#326) planned to serialize session layout snapshots to disk.
