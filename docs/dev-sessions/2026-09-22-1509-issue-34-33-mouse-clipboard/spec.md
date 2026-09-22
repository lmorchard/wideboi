# Mouse support and OSC 52 clipboard Spec

**Goal:** Click to focus, wheel to scroll, and drag to copy, with the copy
reaching the local clipboard over SSH. Mouse events go to child programs that
ask for them.

**Source:** #34 (Mouse: click-to-focus and drag-select), #33 (Clipboard over OSC 52)

These ship together because they depend on each other. Turning on mouse capture
takes drag-select away from the host terminal, so #34 without #33 would make
copying worse.

## Current state

See `research.md`. Load-bearing facts:

- Nothing handles the mouse, and there's no clipboard code. The event loops in
  `cmd/wideboi/main.go` (`run`, `runAttach`) handle only resize and key events.
- The compositor already paints in ascending Z (`internal/client/client.go`,
  `composeFrameLocked`). The issue text saying otherwise is out of date.
  Hit-testing walks placements in descending Z.
- Row 0 is the header row, the bottom row is the status bar, and panes sit in
  between. Slivers are chrome, not content.
- Clients can't yet ask to focus a specific pane. `Strip.FocusPaneID` exists
  on the server (`internal/layout/layout.go:233`).
- vt reports a child's DEC mouse modes through `Callbacks.EnableMode` /
  `DisableMode`, and `SafeEmulator.SendMouse` encodes an event for the child in
  the mode it asked for. Probed against the pinned version: `?1002h` fires
  `EnableMode(DECMode 1002)`, and a click at 0-based (3,2) reaches the child as
  `ESC[<0;4;3M`.

## Desired end state

1. **Mouse capture on by default.** Both host-terminal paths enable
   `uv.MouseModeDrag` (DEC 1002) with SGR encoding. `Terminal.Stop` → `Reset`
   already turns it back off (`research.md`). A `mouse = false` config key
   switches capture off entirely, which gives back the terminal's own
   selection and wheel.
2. **Click to focus.** A left press on any placement (header, content or
   sliver) whose pane isn't focused sends a new `MsgFocusPane{PaneID}`. The
   server calls `strip.FocusPaneID` and broadcasts, the same way smart jump does.
   Hit-testing uses what is on screen right now (`currentPlacementsLocked`), in
   descending Z.
3. **Wheel.** A wheel event over a pane's content either goes to that pane's
   child (if the child tracks the mouse, see 5) or becomes
   `MsgScroll{PaneID, ±3}`. Focus doesn't change.
4. **Drag to select and copy on release.** In a pane whose child doesn't track
   the mouse:
   - A left press in the content rect anchors a selection. If the pane wasn't
     focused, it gets focus on release, and only if the pointer didn't move (a
     click). A drag on an unfocused pane selects without focusing it. *(Changed
     after Copilot review: focusing on press re-dealt the layout mid-drag and
     threw the selection away.)*
   - Dragging extends the selection, clamped to the content rect where it
     started (the rect it had at press time). A pane never selects across into
     its neighbour.
   - The selection is a stream: anchor to cursor in reading order, with the
     rows in between taken full width within the pane.
   - Selected cells are drawn in reverse video over the composed frame.
   - On release, if the selection covers at least one cell, its text is read
     from the composed screen (what you see is what you copy). Each row has
     trailing spaces trimmed, rows are joined with `\n`, and wide-glyph
     continuation cells are skipped. The client then writes
     `ansi.SetSystemClipboard(text)` to the host terminal.
   - A press and release on the same cell copies nothing.
   - The highlight stays after release. It clears on the next mouse press, the
     next key press, or when the selected pane's placement changes.
5. **Forwarding to children that ask.** The server tracks each pane's
   mouse-tracking state (DEC 9/1000/1002/1003 set or clear) through vt's mode
   callbacks, and ships it as a `MouseTracking bool` on `MsgPaneUpdate`. For the
   **focused** pane with tracking on, the client forwards press, release, drag
   and wheel events inside its content as `MsgMouse`. Coordinates are
   pane-local (`X - Dst.Min.X + Src.Min.X`, the same for Y). Once a press
   starts a forwarded drag, its motion and release keep going to that pane,
   clamped to its rect. The server hands the event to
   `grid.SendMouse`, and vt encodes it in the child's own mode.
   - A press on an **unfocused** tracking pane only focuses it and is not
     forwarded. The child never sees half of a click.
   - There's no wideboi selection inside a tracking pane. The host terminal's
     bypass modifier (usually Shift, or Option in macOS terminals) still does
     native selection there.
6. **Modes.** While the help overlay is up, mouse events are ignored.
   Control mode doesn't affect the mouse, and the mouse doesn't change the mode.

## Design decisions

- **Decision:** the client does hit-testing and selection. The server only
  learns about focus changes, scrolls and forwarded events.
  - **Why:** placements are computed client-side (CLAUDE.md), and the
    composed screen is the only place "what you see" exists, in both
    in-process and attached mode.
  - **Rejected:** server-side selection over pane content plus scrollback.
    Les chose visible-text, single-pane selection.
- **Decision:** add a new `MsgFocusPane` rather than a verb carrying an ID.
  - **Why:** `VerbType` values are relative commands with no payload. Adding a
    field to `MsgVerb` for one verb muddies every other use of it.
- **Decision:** add a new `MsgMouse` with concrete fields (`PaneID, X, Y,
  Button int, Mod int, Kind int`: press/release/motion/wheel), decoded to a
  `uv.MouseEvent` on the server.
  - **Why:** `uv.MouseEvent` is an interface, and interfaces can't go on the
    wire (LESSONS: "A struct that looks like plain data…").
    `TestWireTypesCarryNoInterfaces` enforces this.
- **Decision:** ship `MouseTracking` on `MsgPaneUpdate`, not on the layout
  snapshot.
  - **Why:** the mode changes because the child wrote bytes, which dirties the
    pane, which sends an update anyway. Snapshots only go out on layout or
    status changes.
- **Decision:** copy on release; OSC 52 goes out of band as raw bytes on the
  `TerminalScreen`, not in a cell.
  - **Why:** uv drops OSC sequences from cells on purpose, because a repaint
    would fire them again (`research.md`). The client returns the text from its
    mouse handler, and `main` writes it under `screenLock` and flushes.
- **Decision:** `mouse = false` opt-out, default on.
  - **Why:** capture is intrusive, and some people will prefer the terminal's
    own selection. A pointer in the TOML struct distinguishes "absent" from
    `false`.

## Patterns to follow

- Message handling: `internal/server/server.go` `handleClientMsg`. Follow
  `MsgScroll` for per-pane dispatch, and the `VerbSmartJump` case for focus by
  ID plus `needBroadcast`.
- Wire registration: `internal/transport/socket.go:75-78`. Add the new types
  to the roundtrip list in `internal/transport/wire_test.go:~149`.
- Grid callbacks: add to the existing `vt.Callbacks` in
  `internal/server/term/grid.go` `NewVTWithIdleTimeout`. Use an atomic like
  `cursorVisible`. Adding to the `Grid` interface means updating the fakes
  (`statusGrid`, `newBlockingGrid`).
- Client tests: drive `Client` with `HandleServerMsg` and a fake transport, and
  assert on the messages sent and on the composed frame
  (`internal/client/cards_test.go`).
- The router stays key-only. Mouse routing is a pure client method,
  `HandleMouse(ctx, uv.MouseEvent) (copy string)`, which the event loop calls.
- Wire-level proof in `scripts/smoke.py`: feed SGR mouse bytes and assert on
  bytes only wideboi emits (the focus change in the status line, and
  `ESC]52;c;<base64>` for a drag).

## What we're NOT doing

- Scrollback-aware selection, auto-scroll while dragging past an edge, and
  word/line selection on double/triple click.
- Hover motion forwarding. With DEC 1002 we only see motion while a button is
  held, so children that ask for 1003 get drag motion only.
- Pixel, UTF-8 and urxvt mouse encodings. vt only encodes X10 and SGR.
- Clicking a `+N` hidden marker, dragging to resize columns, and
  right/middle-click actions in non-tracking panes.
- Pasting (reading OSC 52). `SendInput` / paste handling is a separate matter.
- tmux passthrough configuration. The README says tmux needs
  `set -g set-clipboard on`.

## Open questions

- Wheel step: default **3 rows** per notch, matching common terminals.
- Selection highlight: default **reverse video** (`uv.AttrReverse` toggled
  on each cell's existing style), with no configurable colour.
