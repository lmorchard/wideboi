### 1. Server Storage and Tracking of Panes
*   **Terminal Storage:** The multiplexer server tracks active sessions in `internal/server/server.go` (line 24) via a `panes map[int]*Pane` inside the `Server` struct. 
*   **Pane State:** Each `Pane` (`internal/server/pane.go:26`) encapsulates a PTY and its associated emulator state using a `grid term.Grid` (line 29). 
*   **Grid Interface:** The `term.Grid` interface (`internal/server/term/grid.go:52`) defines how the server accesses the underlying emulator (`github.com/charmbracelet/ultraviolet`). It provides methods to read metadata like `Title()` (line 82) and `Status()` (line 87).
*   **Server Cache:** The `Server` struct caches `lastStatuses map[int]string` and `lastTitles map[int]string` (`internal/server/server.go:37-38`) to detect layout mutations and trigger layout broadcasts only when necessary.

### 2. The Metadata Channel (OSC 133 and Titles)
Metadata updates from the terminal child processes are intercepted natively within the emulator instance in `internal/server/term/grid.go`:
*   **Titles:** Tracked via `vt.Callbacks.Title` registered on the emulator (`line 247`). It captures OSC 0/1/2 updates, which an agent harness (like Claude Code) can use to push context. 
*   **OSC 133 (Command/Agent Status):** A custom OSC handler is registered for command 133 in `NewVTWithIdleTimeout` (`line 255`).
    *   The payload is parsed by splitting on `;` (`line 265`).
    *   `A` and `B` denote prompt starts/ends, mapping to `StatusNeedsInput` (`lines 272-277`).
    *   `C` indicates command execution, mapping to `StatusWorking` (`line 279`).
    *   `D` indicates completion, mapping to `StatusDone` (or `StatusFailed` if an exit code field is present and non-zero) (`lines 280-288`).
*   **Authoritative Latch:** Processing a valid OSC 133 sequence asserts an atomic `g.sawAuthoritativeStatus` latch (`line 299`), which disables the fallback idle timeout heuristics permanently for that pane.

### 3. Architecture for Broadcasting State Updates
Client updates are edge-triggered and generation-based to prevent duplicate rendering:
*   **Generation Tracking:** Every `term.Grid` maintains a `Generation() uint64` counter (`internal/server/term/grid.go:95`) that advances when cells, cursors, or scroll positions mutate.
*   **Pane Updates:** In `internal/server/server.go`, `broadcastPaneUpdates` (`line 677`) compares each pane's generation against a state map `paneGens map[transport.Transport]map[int]uint64` (`line 45`). A `protocol.MsgPaneUpdate` is constructed and dispatched over the transport only to clients whose tracked generation is missing or out of sync (`lines 696-699`). If a client transport refuses the message, its record is deleted to prompt a retry (`line 744`).
*   **Layout Snapshots:** Changes to column widths, pane focus, titles, or status glyphs trigger `broadcastLayout` (`internal/server/server.go:629`). This bundles the current tree state into a single `protocol.MsgLayoutSnapshot` sent to all transports. 

### 4. Special Panes / TUIs (Control Mode / Help Overlay)
There are no "special panes" managed by the multiplexer tree. Instead, client-side TUIs are rendered directly onto the user's terminal via composition over the layout:
*   **Help Overlay Implementation:** The control mode help overlay is drawn by `drawHelpOverlay` in `internal/client/help.go` (`line 77`). 
*   **Composition Engine:** It builds a bordered box using box-drawing characters and paints it on top of the layout using `compose.WriteStyled(scr, x, y, text, style)` (`line 127`), located in `internal/client/compose/surface.go` (`line 59`).
*   **Direct Cell Manipulation:** `WriteStyled` iterates over strings and applies `uv.NewCell` directly to the active `uv.Screen` coordinates, setting attributes (like `uv.AttrReverse`) before calling `s.SetCell()` (`internal/client/compose/surface.go:64-67`).
*   **Rendering Invariants:** The overlay explicitly calculates budget constraints (`internal/client/help.go:95-114`) and deliberately never touches the final column of the terminal to prevent the emulator from emitting autowrap escape sequences across the wire (`internal/client/help.go:74-76`).
