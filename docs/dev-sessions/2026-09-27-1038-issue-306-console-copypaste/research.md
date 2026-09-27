# Research: Console TUI Copy/Paste (#306)

## 1. Input Processing & Paste in Console Client

- **`cmd/wideboi/main.go:1066-1076`**: Screen setup enables alt-screen and mouse (`enableMouse`), but never calls `scr.EnableBracketedPaste()`.
- **`cmd/wideboi/main.go:1088-1181`**: Event loop on `events = t.Events()`. Switch only handles:
  - `uv.WindowSizeEvent`
  - `uv.KeyPressEvent`
  - `uv.MouseEvent`
  `uv.PasteEvent` (emitted by ultraviolet's reader upon receiving `\x1b[200~...\x1b[201~`) is **unhandled** and silently dropped.
- **`internal/client/client.go:638-649`**: `cli.SendInput(ctx context.Context, data []byte)` exists and wraps bytes into `protocol.MsgInput{PaneID: focusedID, Data: data}`. This method is never invoked anywhere in `cmd/wideboi`.
- **`cmd/wideboi/router.go:153-159`**: When unbracketed keystrokes arrive rapidly during a paste, if any character matches `r.prefix` (default `Ctrl+B`), the router switches to `control = true`, causing subsequent characters of the pasted payload to be interpreted as wideboi control commands or discarded.
- **`internal/server/pane.go:22`**: `keyQueueDepth = 256`. Rapid unbracketed key streaming through `p.SendKey(k)` fills `p.input` (buffer 256) and drops excess characters via `p.dropped.Add(1)`.
- **`internal/server/handlers.go:510-514`**:
  ```go
  if len(m.Data) > 0 {
      _, _ = p.Write(m.Data)
  } else if !m.Key.IsZero() {
      p.SendKey(m.Key.Decode())
  }
  ```
  `m.Data` writes directly to `p.Write(m.Data)` (pty write) without checking if the child process requested bracketed paste mode.
- **`internal/server/term/grid.go:622-624`**: Child terminal mode `ansi.ModeBracketedPaste` is tracked in `g.bracketedPaste.Store(on)`, but this is only exposed for snapshot restore (`GridSnapshot`). `Grid` interface does not currently expose `BracketedPaste() bool`.

## 2. Selection & Clipboard in Console Client

- **`cmd/wideboi/main.go:1242-1248`**: `enableMouse` activates SGR mouse drag tracking (`SetMouseMode(uv.MouseModeDrag)`). This causes the host terminal to report mouse events to wideboi rather than performing native text selection, unless the user holds a modifier bypass (Option on macOS, Shift on Linux).
- **`internal/client/mouse.go:104-184`**: Mouse click and drag creates and updates `c.sel`. On `MouseReleaseEvent`, if `c.sel.anchor != c.sel.cursor`, `c.selectionText` extracts the selected text from `lastRenderedScreen`.
- **`cmd/wideboi/main.go:1174-1180`**: If `cli.HandleMouse` returns `text != ""`, it calls `writeClipboard(scr, text)`.
- **`cmd/wideboi/main.go:1258-1261`**:
  ```go
  func writeClipboard(scr *uv.TerminalScreen, text string) {
      _, _ = scr.WriteString(ansi.SetSystemClipboard(text))
      _ = scr.Flush()
  }
  ```
  `writeClipboard` writes OSC 52 (`\x1b]52;c;<base64>\x07`).
  - Terminal.app does not support OSC 52.
  - iTerm2 disables OSC 52 clipboard access by default.
  - tmux requires `set -g set-clipboard on`.
- **`internal/client/mouse.go:213-217`**: `ClearSelection()` drops `c.sel`. It is called on every `KeyPressEvent`.
- **No keyboard copy binding**:
  - `Cmd+C` in Terminal/iTerm2 copies native selection (which is empty).
  - `Ctrl+C` sends SIGINT to child pane.
  - No wideboi keybinding exists in `internal/keys/keys.go` to copy the active selection or write it to clipboard.
- **No native clipboard fallback**:
  - Wideboi has no fallback to local clipboard tools (`pbcopy` on macOS, `wl-copy`/`xclip` on Linux) when running locally.
