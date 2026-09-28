# Native In-Client Command Palette, Prompt, and Session Environment Implementation Plan

**Goal:** Replace out-of-process ephemeral panes for command palette and prompt with native in-client UI in both web and TUI clients, and inject active session environment variables (`WIDEBOI_SOCK` and `WIDEBOI_SESSION`) into all spawned panes.

**Approach:** Allow matching `WIDEBOI_SOCK` and `WIDEBOI_SESSION` in config layering, and pass them to all panes spawned by the server. Implement native in-client command palette and prompt in both the web app (Lit dialog with search input and direct client message dispatch) and the console TUI (in-process status bar prompt and floating palette overlay using `commands.DefaultRegistry.Execute`), eliminating ephemeral pane spawning and CLI detachment choreography.

**Tech stack:** Go, TypeScript / Lit, Ultraviolet TUI compose, Unix domain sockets.

---

## Phase 1: Session Environment Injection and Config Layering

Allow matching `session` and `socket` in `applySessionLayer` and inject `WIDEBOI_SOCK` / `WIDEBOI_SESSION` into all spawned panes.

**Files:**
- Modify: `internal/config/config.go` — allow matching `session != "" && socket != ""` when `socket == SessionSocketPath(session)`.
- Test: `internal/config/config_test.go` — test matching session and socket in environment layer.
- Modify: `internal/server/ptyx/pane.go` — allow passing additional environment variables to `Spawn(argv, cols, rows, dir, env ...string)`. Strip existing `WIDEBOI_SOCK` / `WIDEBOI_SESSION` and append active ones.
- Modify: `internal/server/pane.go` — pass env from `Server` to `NewPane` and `ptyx.Spawn`.
- Modify: `internal/server/server.go` — store `session` and `socket` on `Server` (`SetSession(session, socket string)` and `ListenSocket`), providing them to `spawnPaneWithSpecLocked`.
- Modify: `cmd/wideboi/main.go` — in `runServer`, set `srv.SetSession(cfg.Session, cfg.Socket)`.
- Test: `internal/server/control_test.go` (or `pane_test.go`) — verify spawned panes inherit `WIDEBOI_SOCK` and `WIDEBOI_SESSION`.

**Key changes:**
In `internal/config/config.go`:
```go
func applySessionLayer(cfg *Config, layer, session, socket string) error {
	switch {
	case session != "" && socket != "":
		if socket != SessionSocketPath(session) {
			return fmt.Errorf("%s sets both a session name (%q) and a socket path (%q); set one, not both", layer, session, socket)
		}
		cfg.Socket = socket
		cfg.Session = session
	case session != "":
		if err := validateSessionName(session); err != nil {
			return fmt.Errorf("%s: %w", layer, err)
		}
		cfg.Socket = SessionSocketPath(session)
		cfg.Session = session
	case socket != "":
		cfg.Socket = socket
		cfg.Session = ""
	}
	return nil
}
```

In `internal/server/ptyx/pane.go`:
```go
func Spawn(argv []string, cols, rows int, dir string, extraEnv ...string) (*Pane, error) {
	...
	// Strip stale session variables and append extraEnv
	env := filterEnv(os.Environ(), "WIDEBOI_SOCK", "WIDEBOI_SESSION")
	cmd.Env = append(env, "TERM=xterm-256color")
	cmd.Env = append(cmd.Env, extraEnv...)
```

**Verification — automated:**
- [x] `go test -count=1 ./internal/config/...` passes — **ok 0.115s**
- [x] `go test -count=1 ./internal/server/...` passes — **all suites pass (ptyx, term, server)**
- [x] `make quick` passes — **gofmt, vet, seam-check, go test, npm test pass**

**Verification — manual:**
- [ ] Spawned pane has `$WIDEBOI_SOCK` pointing to the running session socket.

---

## Phase 2: Web Client Native Command Palette & Prompt

Upgrade `<wideboi-command-menu>` into an interactive, searchable Command Palette with prompt capabilities and direct client message execution, removing ephemeral pane spawning.

**Files:**
- Modify: `web/src/components/command-menu.ts` — add search input, live filtering, keyboard navigation (Up/Down/Enter), and command execution dispatch.
- Modify: `web/src/wideboi-app.styles.ts` — style the search input and selected command states in the palette.
- Modify: `web/src/wideboi-app.ts` — remove `openPrompt()` and `openPalette()`, replace with opening the command palette dialog (with prompt mode when invoked via `:`). Handle direct command executions.
- Test: `web/test/command-menu.test.ts` (or `wideboi-app.test.ts`) — test search input, keyboard filtering, and execution.

**Key changes:**
In `web/src/components/command-menu.ts`:
- Add `@state() private searchQuery = ''` and `@state() private selectedIndex = 0`.
- Add auto-focused `<input type="text" class="command-palette-input" placeholder="Type a command or :prompt...">`.
- Render filtered list based on `searchQuery`.
- Support Up/Down arrows to adjust `selectedIndex`, Enter to trigger `selected` command, and direct `:` prompt input.

In `web/src/wideboi-app.ts`:
- Replace `openPalette()` and `openPrompt()` with `this.openCommandPalette(mode)`.
- Dispatch actions (`VerbType.NEW_COLUMN`, `VerbType.KILL_PANE`, etc.) directly without sending `wideboi prompt` / `wideboi palette` to the server.

**Verification — automated:**
- [x] `cd web && npm test` passes — **20 test files passed (155 tests)**
- [x] `make web-accept` passes — **50 browser acceptance tests passed (8.2s)**

**Verification — manual:**
- [ ] Pressing `Ctrl+b Space` opens interactive palette with search input.
- [ ] Typing filters items; arrow keys select; Enter executes without opening a new pane.
- [ ] Pressing `Ctrl+b :` opens prompt directly.

---

## Phase 3: Console TUI Native In-Process Prompt & Palette

Implement status bar prompt (`:`) and floating palette overlay (`Space`) in the console client using `commands.DefaultRegistry.Execute`, retiring ephemeral pane spawning and detach files.

**Files:**
- Create: `internal/client/prompt.go` — status bar command prompt input handling (`StartPrompt`, `PromptEdit`, `PromptCommit`, `PromptCancel`).
- Create: `internal/client/palette.go` — floating palette overlay state and renderer (`StartPalette`, `PaletteEdit`, `PaletteNavigate`, `PaletteCommit`, `drawPaletteOverlay`).
- Modify: `internal/client/client.go` — integrate prompt and palette state into `Client`.
- Modify: `internal/client/statusbar.go` — render prompt input in status bar when active (`: <query>_`).
- Modify: `cmd/wideboi/main.go` — handle `routePrompt` and `routePalette` via `cli.StartPrompt()` and `cli.StartPalette()`. Remove detach file creation and CLI split requests for prompt/palette.
- Modify: `cmd/wideboi/prompt.go` and `cmd/wideboi/palette.go` — remove ephemeral pane detach handling or retain minimal non-interactive CLI support if desired.
- Test: `internal/client/prompt_test.go` and `internal/client/palette_test.go` — test in-process prompt and palette input, navigation, and execution.

**Key changes:**
In `internal/client/statusbar.go`:
```go
if c.prompt != nil {
	statusText := truncateRunes(fmt.Sprintf(": %s_  Enter run · Esc cancel", c.prompt.query), budget)
	...
}
```

In `cmd/wideboi/main.go`:
```go
case routePrompt:
	cli.StartPrompt()
case routePalette:
	cli.StartPalette()
```

In `cmd/wideboi/event loop`:
- When prompt is active: printable keys call `cli.PromptEdit(text, false)`, Backspace calls `cli.PromptEdit("", true)`, Enter calls `cli.PromptCommit(ctx)`, Esc calls `cli.PromptCancel()`.
- When palette is active: printable keys / Backspace call `cli.PaletteEdit`, Up/Down call `cli.PaletteNavigate`, Enter calls `cli.PaletteCommit(ctx)`, Esc calls `cli.PaletteCancel()`.

**Verification — automated:**
- [x] `go test -count=1 ./internal/client/...` passes — **ok 0.196s**
- [x] `go test -count=1 ./cmd/wideboi/...` passes — **ok 15.037s**
- [x] `make quick` passes — **gofmt, vet, seam-check, go test, npm test pass**

**Verification — manual:**
- [ ] In console client, pressing `Ctrl+b :` displays `: ` prompt in status bar.
- [ ] In console client, pressing `Ctrl+b Space` opens centered palette box with live search and Up/Down navigation.
- [ ] Executing a command (e.g. `new-column` or `set-width 80`) executes instantly without spawning prompt/palette panes.

---

## Phase 4: Final Gate Verification & Regression Checks

Run full gate checks including race tests, verify-exit, smoke, and attach-check.

**Files:**
- Modify: `docs/dev-sessions/2026-09-27-1812-issue-311-target-active-session/notes.md` — record implementation notes and outcomes.

**Verification — automated:**
- [x] `make fmt-check` passes — **gofmt clean**
- [x] `make lint` passes — **go vet clean**
- [x] `make test` passes — **all Go package tests pass**
- [x] `make web-test` passes — **all 20 test files pass (155 tests)**
- [x] `make race` passes — **all packages pass with -race -count=1**
- [x] `make verify-exit` passes — **all 7 exit signal/size scenarios pass**
- [x] `make check` passes — **full gate passes (fmt-check, lint, seam-check, test, web-test, web-accept, race, verify-exit, smoke, golden, attach-check)**

**Verification — manual:**
- [ ] Both web and TUI prompt/palette operate seamlessly against named, custom, and default sessions.
