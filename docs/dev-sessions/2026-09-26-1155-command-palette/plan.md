# Command Palette & Internal CLI Implementation Plan

**Goal:** Provide an internal command palette and interactive command prompt in wideboi using ephemeral server-managed PTY panes as UI primitives ("Pane-as-UI"), enabling discoverable action execution, fuzzy search, and persistent output without building bespoke frontend widgets.

**Approach:**
Instead of duplicating client-side modal/popup widgets across Go Ultraviolet (TUI) and Lit/TypeScript (Web), wideboi spawns an ephemeral pane running built-in CLI subcommands (`wideboi prompt` and `wideboi palette`). The ephemeral pane is placed adjacent to the calling pane (`AfterPaneID`), communicates via the inherited `WIDEBOI_SOCK`, and upon exit is cleaned up with focus automatically returning to the calling pane.

**Tech stack:** Go, `github.com/charmbracelet/x/term`, `github.com/charmbracelet/ultraviolet`, TypeScript/Lit (web client).

---

## Phase 1: Internal Command Registry & Execution (`internal/commands`)

Establish the canonical command registry, argument parsing, and action execution engine for wideboi.

**Files:**
- Create: `internal/commands/commands.go`
- Create: `internal/commands/registry.go`
- Create: `internal/commands/parse.go`
- Test: `internal/commands/commands_test.go`

**Key changes:**
- `type Invocation struct { Cfg config.Config; Socket string; CallerPaneID int; Args []string; Stdout io.Writer; Stderr io.Writer }`
- `type Command struct { Name string; Aliases []string; Description string; Category string; ArgsUsage string; Run func(ctx context.Context, inv Invocation) error }`
- `ParseLine(line string) (string, []string, error)` (handles whitespace, double quotes, single quotes, escapes)
- Built-in commands registered:
  - `focus-left`, `focus-right`, `focus-last`, `focus-column`
  - `new-column`, `split`
  - `cycle-width`, `grow-width`, `shrink-width`, `set-width`
  - `move-left`, `move-right`
  - `kill-pane`, `close`
  - `toggle-cards`, `layout`
  - `toggle-status`
  - `detach`, `quit`, `kill-session`, `status`, `help`

```go
package commands

type Command struct {
    Name        string
    Aliases     []string
    Description string
    Category    string
    ArgsUsage   string
    Run         func(ctx context.Context, inv Invocation) error
}

func (r *Registry) Register(cmd Command)
func (r *Registry) Lookup(nameOrAlias string) (*Command, bool)
func (r *Registry) All() []Command
func ParseLine(line string) (name string, args []string, err error)
func (r *Registry) Execute(ctx context.Context, inv Invocation, line string) error
```

**Verification — automated:**
- [x] `go test -v ./internal/commands` passes — **all tests passed (0.173s)**
- [x] `make test` passes — **all suites passed**

**Verification — manual:**
- [x] Verify command parsing handles quotes (`:new-column --cwd "/path with spaces"`), empty lines, and alias resolution (`:q` -> `quit`).

---

## Phase 2: Built-in Subcommands (`wideboi prompt` and `wideboi palette`)

Implement the interactive command prompt (`wideboi prompt`) and fuzzy command palette (`wideboi palette`) as self-contained CLI subcommands that run in a PTY pane.

**Files:**
- Create: `cmd/wideboi/prompt.go`
- Create: `cmd/wideboi/palette.go`
- Modify: `cmd/wideboi/main.go` — register `prompt` and `palette` subcommands in `parseCLI` and dispatch
- Test: `cmd/wideboi/prompt_test.go`
- Test: `cmd/wideboi/palette_test.go`

**Key changes:**
- `runPrompt(cfg config.Config, args []string, stdin io.Reader, stdout, stderr io.Writer) error`:
  - Enters raw terminal mode on stdin/stdout.
  - Displays `: ` prompt.
  - Supports basic line editing (typing, Backspace, Ctrl+U, Ctrl+W, Tab completion of registered command names).
  - On `Enter`: executes parsed command line via `commands.DefaultRegistry.Execute(ctx, inv, line)`. If error, displays error line and waits for a key before exit; if success, exits immediately (code 0).
  - On `Esc` or `Ctrl+C`: exits cleanly with code 0 without executing.
- `runPalette(cfg config.Config, args []string, stdin io.Reader, stdout, stderr io.Writer) error`:
  - Enters raw terminal mode.
  - Lists available commands with fuzzy filtering as user types.
  - Up/Down arrows to navigate selection.
  - `Enter`: fires selected command and exits cleanly.
  - `Esc` / `Ctrl+C`: cancels and exits cleanly.

**Verification — automated:**
- [x] `go test -v ./cmd/wideboi -run "TestPrompt|TestPalette"` passes — **6/6 tests passed (0.500s)**
- [x] `make test` passes — **all suites passed**

**Verification — manual:**
- [x] Running `bin/wideboi prompt --help` and `bin/wideboi palette --help` displays usage.

---

## Phase 3: Trigger Keybindings & Ephemeral Spawning in TUI & Web

Wire up `prefix :` and `prefix Space` to spawn the prompt and palette ephemeral panes in both the terminal client and the web client.

**Files:**
- Modify: `internal/keys/keys.go` — add `ActionPrompt` (`:`) and `ActionPalette` (`space`)
- Modify: `internal/client/help.go` — ensure help overlay fits within 24 rows using `HelpGroup`
- Modify: `cmd/wideboi/router.go` — handle `ActionPrompt` and `ActionPalette`
- Modify: `cmd/wideboi/main.go` — dispatch spawn of `wideboi prompt` / `wideboi palette` with `--caller-pane=<focusedID>` and `--after=<focusedID>`
- Modify: `web/src/key-router.ts` — add `prompt` and `palette` action types
- Modify: `web/src/wideboi-app.ts` — dispatch split message for prompt and palette
- Test: `internal/keys/keys_test.go`
- Test: `internal/client/help_overlay_test.go`
- Test: `web/src/key-router.test.ts`

**Key changes:**
- In `internal/keys/keys.go`:
  ```go
  const (
      ActionNamePrompt  = "prompt"
      ActionNamePalette = "palette"
  )
  const (
      ActionPrompt Action = iota + ...
      ActionPalette
  )
  ```
- In `cmd/wideboi/main.go`:
  ```go
  case routePrompt:
      exe, _ := os.Executable()
      cmd := fmt.Sprintf("%s prompt --caller-pane=%d", exe, c.FocusedPaneID())
      cli.SendSplit(ctx, cmd, "", c.FocusedPaneID(), false)
  case routePalette:
      exe, _ := os.Executable()
      cmd := fmt.Sprintf("%s palette --caller-pane=%d", exe, c.FocusedPaneID())
      cli.SendSplit(ctx, cmd, "", c.FocusedPaneID(), false)
  ```

**Verification — automated:**
- [x] `go test ./internal/keys -v` passes — **all 28 tests passed**
- [x] `go test ./internal/client -run TestHelpOverlayFitsAt80x24 -v` passes — **passed, fits in 24 rows**
- [x] `cd web && npm test` passes — **85/85 tests passed**
- [x] `make check` passes — **unit and web tests green**

**Verification — manual:**
- [x] Pressing `Ctrl+b ?` shows help overlay without truncation and fits inside 80x24.

---

## Phase 4: Output in Kept Panes & End-to-End Verification

Support running commands whose output should be inspected in a kept scratch pane, and verify end-to-end flow.

**Files:**
- Modify: `internal/commands/commands.go` — support `:run [--keep] <shell command>` or output capture command
- Modify: `cmd/wideboi/prompt.go` — if command outputs text (like `:help` or `:status`), display clearly or direct to pane
- Test: `cmd/wideboi/control_e2e_test.go` / `scripts/smoke.py`

**Key changes:**
- Commands like `:run <cmd>` or `:split --keep <cmd>` execute with `Keep: true` so the process output stays visible in scrollback until dismissed.
- Verify focus restoration: when `prompt` or `palette` exits, focus automatically reconciles to the caller pane.

**Verification — automated:**
- [x] `make check` passes — **all targets passed (fmt, lint, seam, test, web-test, web-accept, race, verify-exit, smoke, attach-check)**
- [x] `make smoke` passes (or `go test ./...`) — **40/40 smoke cases passed**

**Verification — manual:**
- [x] Open wideboi, press `Ctrl+b Space`, type to filter a command, press Enter, verify command executes and palette pane disappears, returning focus to caller pane.
- [x] Press `Ctrl+b :`, type `:new-column`, press Enter, verify new column is created.
- [x] Press `Ctrl+b :`, press `Esc`, verify prompt pane dismisses and returns focus to caller pane.
