# Palette/Prompt `detach` and `set-width` Implementation Plan

**Goal:** Allow `set-width` and `detach` invoked from the command palette or `:` prompt to execute their intended actions instead of silently doing nothing.

**Approach:** Allow unattached command connections to adjust column widths on the server (`!s.attachedTransports[tp]`), preserve query arguments on palette selection, and route detach through a client-provided detach trigger file passed to ephemeral panes.

**Tech stack:** Go, wideboi internal commands, client/server transport, PTY/CLI subcommands.

---

## Phase 1: `set-width` Execution and Validation

Allow unattached connections (commands/scripts) to set column widths on the server, while keeping viewer clients restricted from overriding host geometry. Validate `CallerPaneID > 0` in `set-width`.

**Files:**
- Modify: `internal/server/server.go` — update `MsgSetPaneWidth` check to `(tp == s.sizeOwner || !s.attachedTransports[tp])`
- Modify: `internal/commands/registry.go` — validate `inv.CallerPaneID > 0` in `set-width` command
- Test: `internal/commands/commands_test.go` — add tests for `set-width` command execution against server
- Test: `internal/server/width_owner_test.go` — verify unattached connections can set width while attached viewers cannot

**Key changes:**
In `internal/server/server.go:806`:
```go
	case protocol.MsgSetPaneWidth:
		if (tp == s.sizeOwner || !s.attachedTransports[tp]) && m.Width >= layout.MinColumnWidth && m.Width <= layout.MaxColumnWidth {
			if old, ok := s.strip.ColumnWidth(m.PaneID); ok && old != m.Width {
				s.strip.SetColumnWidth(m.PaneID, m.Width)
				needResizePanes = true
				needBroadcast = true
			}
		}
```

In `internal/commands/registry.go`:
```go
		{
			Name:        "set-width",
			Aliases:     []string{"width"},
			Description: "Set the width of the pane in columns",
			Category:    "Layout",
			ArgsUsage:   "<columns>",
			Run: func(ctx context.Context, inv Invocation) error {
				if len(inv.Args) < 1 {
					return fmt.Errorf("usage: set-width <columns>")
				}
				if inv.CallerPaneID <= 0 {
					return fmt.Errorf("no focused pane")
				}
				width, err := strconv.Atoi(inv.Args[0])
				if err != nil {
					return fmt.Errorf("invalid width: %w", err)
				}
				if width < layout.MinColumnWidth || width > layout.MaxColumnWidth {
					return fmt.Errorf("width must be between %d and %d", layout.MinColumnWidth, layout.MaxColumnWidth)
				}
				return SendClientMsg(ctx, inv, protocol.MsgSetPaneWidth{
					PaneID: inv.CallerPaneID,
					Width:  width,
				})
			},
		},
```

**Verification — automated:**
- [ ] `go test ./internal/server -run TestPerPaneWidthOwnerTransfersOnClaim` passes
- [ ] `go test ./internal/commands -run TestSetWidthCommand` passes
- [ ] `make quick` passes

**Verification — manual:**
- [ ] Verify `set-width 60` changes the pane width in a real session

---

## Phase 2: Palette Argument Preservation

Enable the command palette to execute commands that take arguments typed into the search query (e.g., `set-width 60`, `width 70`).

**Files:**
- Modify: `cmd/wideboi/palette.go` — parse and append query arguments when executing selected command
- Test: `cmd/wideboi/palette_test.go` — test argument forwarding from palette input

**Key changes:**
In `cmd/wideboi/palette.go:185`:
```go
		case '\r', '\n': // Enter
			matches := filterCommands(allCmds, query.String())
			_, _ = io.WriteString(stdout, "\x1b[H\x1b[2J")
			if len(matches) > 0 && selected < len(matches) {
				execLine := matches[selected].Name
				// If user entered trailing arguments beyond the command/alias match, preserve them:
				trimmedQuery := strings.TrimSpace(query.String())
				fields := strings.Fields(trimmedQuery)
				if len(fields) > 1 {
					execLine = matches[selected].Name + " " + strings.Join(fields[1:], " ")
				}
				_ = commands.DefaultRegistry.Execute(context.Background(), inv, execLine)
			}
			return nil
```
And in non-interactive mode (`palette.go:72`):
```go
		if len(matches) > 0 && query != "" {
			execLine := matches[0].Name
			fields := strings.Fields(strings.TrimSpace(query))
			if len(fields) > 1 {
				execLine = matches[0].Name + " " + strings.Join(fields[1:], " ")
			}
			_ = commands.DefaultRegistry.Execute(context.Background(), inv, execLine)
		}
```

**Verification — automated:**
- [ ] `go test ./cmd/wideboi -run TestPalette` passes
- [ ] `make quick` passes

**Verification — manual:**
- [ ] Type `width 70` into palette, press Enter, verify command executes with arg 70

---

## Phase 3: `detach` Execution via Client Trigger File

Support detaching the client that opened the palette or prompt by coordinating via a client-provided detach trigger file.

**Files:**
- Modify: `internal/commands/commands.go` — add `DetachFile string` to `Invocation`
- Modify: `internal/commands/registry.go` — in `detach` command, write to `inv.DetachFile` if non-empty
- Modify: `cmd/wideboi/prompt.go` — add `--detach-file` CLI flag, set `inv.DetachFile`
- Modify: `cmd/wideboi/palette.go` — add `--detach-file` CLI flag, set `inv.DetachFile`
- Modify: `cmd/wideboi/main.go` — pass `--detach-file` when spawning prompt/palette; in event loop, check trigger file and execute client detach teardown
- Test: `internal/commands/commands_test.go` — test `detach` command with `DetachFile`
- Test: `cmd/wideboi/main_test.go` or `cmd/wideboi/palette_test.go` — verify detach triggering

**Key changes:**
In `internal/commands/commands.go`:
```go
type Invocation struct {
	Cfg          config.Config
	Socket       string
	CallerPaneID int
	DetachFile   string
	Args         []string
	Stdout       io.Writer
	Stderr       io.Writer
}
```

In `internal/commands/registry.go:321`:
```go
		{
			Name:        "detach",
			Aliases:     []string{"d"},
			Description: "Detach the current client from the session",
			Category:    "Session",
			Run: func(ctx context.Context, inv Invocation) error {
				if inv.DetachFile != "" {
					return os.WriteFile(inv.DetachFile, []byte("detach\n"), 0600)
				}
				return SendClientMsg(ctx, inv, protocol.MsgDetach{})
			},
		},
```

In `cmd/wideboi/main.go`:
```go
// Inside client event loop or helper:
var detachTriggerFile string

// When spawning prompt/palette:
detachTriggerFile = filepath.Join(os.TempDir(), fmt.Sprintf("wideboi-detach-%d-%d.tmp", os.Getpid(), time.Now().UnixNano()))
cmd := fmt.Sprintf("%s palette --caller-pane=%d --socket=%s --detach-file=%s",
	shellQuote(exe), cli.FocusedPaneID(), shellQuote(cfg.Socket), shellQuote(detachTriggerFile))
cli.SendSplit(ctx, cmd, "", cli.FocusedPaneID(), false)

// On event loop tick or MsgLayoutSnapshot:
if detachTriggerFile != "" {
	if _, err := os.Stat(detachTriggerFile); err == nil {
		_ = os.Remove(detachTriggerFile)
		detachTriggerFile = ""
		// Execute clean client detach
		...
	}
}
```

**Verification — automated:**
- [ ] `go test ./internal/commands -run TestDetachCommand` passes
- [ ] `go test ./cmd/wideboi -run TestPalette` passes
- [ ] `make quick` passes

**Verification — manual:**
- [ ] Open palette, select `detach`, verify the client detaches cleanly and prints detach notice

---

## Phase 4: Full Verification and Check Gate

Run all test suites and check acceptance criteria.

**Files:**
- Test scripts and smoke tests

**Verification — automated:**
- [ ] `make fmt-check` passes
- [ ] `make lint` passes
- [ ] `make test` passes
- [ ] `make race` passes
- [ ] `make check` passes
