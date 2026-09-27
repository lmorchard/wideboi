# Console TUI Copy/Paste Fix Implementation Plan

**Goal:** Provide reliable copy and paste in the console TUI client by enabling host bracketed paste, handling `uv.PasteEvent`, forwarding bracketed paste to children, and adding local OS clipboard fallback alongside OSC 52.

**Approach:** Enable bracketed paste on the host screen (`scr.EnableBracketedPaste()`) and handle `uv.PasteEvent` via `cli.SendInput`. On the server, check `p.grid.BracketedPaste()` to wrap pasted data with `\x1b[200~` / `\x1b[201~` when the child terminal requested it. In `writeClipboard`, emit OSC 52 and asynchronously write to the local clipboard via platform commands (`pbcopy` / `xclip`) when not under SSH.

**Tech stack:** Go, `github.com/charmbracelet/ultraviolet`, `charmbracelet/x/ansi`, Unix ptys.

---

## Phase 1: Host Bracketed Paste & `uv.PasteEvent` Handling

Enable bracketed paste on the host screen in `cmd/wideboi` and route `uv.PasteEvent` to the focused pane via `cli.SendInput`.

**Files:**
- Modify: `cmd/wideboi/main.go` — enable bracketed paste on `scr` after alt-screen enter; handle `case uv.PasteEvent:` in `events` loop.
- Test: `cmd/wideboi/main_test.go` — test `uv.PasteEvent` dispatching through client to `protocol.MsgInput{Data: ...}`.

**Key changes:**
In `cmd/wideboi/main.go`:
```go
// Inside gotMsg screen setup:
scr.EnterAltScreen()
scr.EnableBracketedPaste()
enableMouse(scr, cfg)
```
In `cmd/wideboi/main.go` event loop:
```go
case uv.PasteEvent:
	cli.ClearSelection()
	cli.SendInput(ctx, []byte(ev.Content))
```

**Verification — automated:**
- [x] `go test -count=1 ./cmd/wideboi` passes — **ok (15.153s)**
- [x] `make quick` passes — **all packages + 151 web tests passed**

**Verification — manual:**
- [ ] In terminal, pasting multiline text or long string sends data to focused pane.

---

## Phase 2: Child Bracketed Paste Forwarding on Server

Expose `BracketedPaste() bool` on `term.Grid` and wrap `MsgInput.Data` in bracketed paste markers (`\x1b[200~` and `\x1b[201~`) if the child pane has bracketed paste enabled. Also route `MsgInput.Data` through `p.SendBytes` to maintain queue ordering and non-blocking `s.mu`.

**Files:**
- Modify: `internal/server/term/grid.go` — add `BracketedPaste() bool` to `Grid` interface and implement on `vtGrid`.
- Modify: `internal/server/pane_wedge_test.go`, `internal/server/status_test.go` — implement `BracketedPaste() bool` on test grids.
- Modify: `internal/server/handlers.go` — check `p.grid.BracketedPaste()` before queueing `m.Data` via `p.SendBytes`.
- Test: `internal/server/paste_test.go` — test that `MsgInput{Data: ...}` is wrapped when bracketed paste is on, and unbracketed when off.

**Key changes:**
In `internal/server/term/grid.go`:
```go
type Grid interface {
	...
	BracketedPaste() bool
}

func (g *vtGrid) BracketedPaste() bool {
	return g.bracketedPaste.Load()
}
```

In `internal/server/handlers.go`:
```go
if len(m.Data) > 0 {
	data := m.Data
	if p.grid.BracketedPaste() {
		data = append([]byte("\x1b[200~"), append(data, []byte("\x1b[201~")...)...)
	}
	p.SendBytes(data)
} else if !m.Key.IsZero() {
	p.SendKey(m.Key.Decode())
}
```

**Verification — automated:**
- [x] `go test -count=1 ./internal/server/...` passes — **all tests in internal/server, ptyx, term passed**
- [x] `make quick` passes — **all packages + 151 web tests passed**

**Verification — manual:**
- [ ] Multiline paste into a shell (e.g. zsh/bash) appears as bracketed paste without immediately executing intermediate newlines.

---

## Phase 3: Local Clipboard Fallback alongside OSC 52

Add local OS clipboard fallback (`pbcopy` on Darwin, `wl-copy`/`xclip` on Linux) to `writeClipboard` when running locally (not in an SSH session).

**Files:**
- Create: `cmd/wideboi/clipboard.go` — `writeLocalClipboard(text string)` with OS detection, SSH check, and bounded timeout.
- Modify: `cmd/wideboi/main.go` — invoke `writeLocalClipboard(text)` in `writeClipboard`.
- Create: `cmd/wideboi/clipboard_test.go` — tests for SSH check and local clipboard command execution.

**Key changes:**
In `cmd/wideboi/clipboard.go`:
```go
func isSSHSession() bool {
	return os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != ""
}

func writeLocalClipboard(text string) {
	if isSSHSession() || text == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.CommandContext(ctx, "pbcopy")
		case "linux":
			if _, err := exec.LookPath("wl-copy"); err == nil {
				cmd = exec.CommandContext(ctx, "wl-copy")
			} else if _, err := exec.LookPath("xclip"); err == nil {
				cmd = exec.CommandContext(ctx, "xclip", "-selection", "clipboard")
			} else if _, err := exec.LookPath("xsel"); err == nil {
				cmd = exec.CommandContext(ctx, "xsel", "--clipboard", "--input")
			}
		}
		if cmd == nil {
			return
		}
		cmd.Stdin = strings.NewReader(text)
		_ = cmd.Run()
	}()
}
```

**Verification — automated:**
- [x] `go test -count=1 ./cmd/wideboi` passes — **all tests passed**
- [x] `make check` passes 4x — **4x consecutive runs 100% green across all 9 gates (fmt, lint, seam, test, web-test, web-accept, race, verify-exit, smoke, attach-check)**

**Verification — manual:**
- [ ] Drag-select text in a local terminal pane, release mouse, and paste (`Cmd+V`) in an external app (e.g. TextEdit / browser).
