# Dump-Pane CLI & Command Implementation Plan

**Goal:** Implement `wideboi dump-pane` (and alias `capture`) across terminal extraction, wire protocol, CLI, and internal command registry with pagination (`--offset`, `--limit`), line counting (`-c, --count`), and ANSI/plain formatting, and update the agent skill documentation.

**Approach:** Extend `term.Grid` and `Pane` with `DumpText` supporting ANSI/plain rendering and window slicing. Add `MsgDumpPaneRequest`/`MsgDumpPaneResponse` to Protobuf and wire protocol, bumping `protocol.Version` to 23. Implement `runDumpPane` in `cmd/wideboi/control.go` and register `dump-pane` in `internal/commands/registry.go`. Update `docs/skills/wideboi-control/SKILL.md`.

**Tech stack:** Go, Protobuf / Buf, Ultraviolet (`uv.Style`, `uv.StyleDiff`), ANSI (`x/ansi`).

---

## Phase 1: Terminal Grid & Pane Text Extraction (`DumpText`)

Extend `term.Grid` and `Pane` to extract lines across screen and scrollback with support for offset/limit pagination, tail line limiting, total line counting, and ANSI styling.

**Files:**
- Modify: `internal/server/term/grid.go` — Add `DumpText(scrollback bool, offset int, limit int, tailLines int, ansi bool) (string, int)` to `Grid` interface and `vtGrid` implementation; delegate `CaptureText` to `DumpText`.
- Modify: `internal/server/pane.go` — Add `DumpText` to `Pane`, delegating to `p.grid.DumpText` under `p.renderMu.RLock()`.
- Test: `internal/server/term/grid_test.go` — Add tests for `DumpText` covering plain/ANSI, offset, limit, tailLines, and count.
- Test: `internal/server/pane_capture_test.go` — Test `Pane.DumpText`.

**Key changes:**
In `internal/server/term/grid.go`:
```go
type Grid interface {
    // ... existing methods ...
    CaptureText(scrollback bool, maxLines int) string
    DumpText(scrollback bool, offset int, limit int, tailLines int, ansi bool) (string, int)
}

func (g *vtGrid) DumpText(scrollback bool, offset int, limit int, tailLines int, ansiFormat bool) (string, int) {
    g.writeResizeMu.Lock()
    defer g.writeResizeMu.Unlock()

    cols, rows := g.em.Width(), g.em.Height()
    sbLen := g.em.ScrollbackLen()

    totalLines := rows
    if scrollback {
        totalLines += sbLen
    }

    startLine := 0
    count := totalLines

    if tailLines > 0 && offset == 0 && limit == 0 {
        if totalLines > tailLines {
            startLine = totalLines - tailLines
        }
        count = tailLines
    } else if offset > 0 || limit > 0 {
        if offset > 0 {
            startLine = offset
        }
        if limit > 0 {
            count = limit
        } else {
            count = totalLines - startLine
        }
    }

    if startLine < 0 {
        startLine = 0
    }
    if startLine >= totalLines || count <= 0 {
        return "", totalLines
    }
    endLine := startLine + count
    if endLine > totalLines {
        endLine = totalLines
    }

    var lines []string
    for idx := startLine; idx < endLine; idx++ {
        inScrollback := scrollback && idx < sbLen
        row := idx
        if scrollback && !inScrollback {
            row = idx - sbLen
        }

        if !ansiFormat {
            var sb strings.Builder
            for x := 0; x < cols; {
                var cell *uv.Cell
                if inScrollback {
                    cell = g.em.ScrollbackCellAt(x, row)
                } else {
                    cell = g.em.CellAt(x, row)
                }
                if cell == nil || cell.Content == "" {
                    sb.WriteByte(' ')
                    x++
                    continue
                }
                sb.WriteString(cell.Content)
                w := cell.Width
                if w <= 0 {
                    w = 1
                }
                x += w
            }
            lines = append(lines, strings.TrimRight(sb.String(), " "))
        } else {
            // ANSI formatting: find rightmost non-empty or styled cell
            lastX := -1
            for x := 0; x < cols; {
                var cell *uv.Cell
                if inScrollback {
                    cell = g.em.ScrollbackCellAt(x, row)
                } else {
                    cell = g.em.CellAt(x, row)
                }
                if cell != nil && (cell.Content != "" && cell.Content != " " || !cell.Style.IsZero()) {
                    w := 1
                    if cell.Width > 0 {
                        w = cell.Width
                    }
                    lastX = x + w - 1
                }
                if cell != nil && cell.Width > 0 {
                    x += cell.Width
                } else {
                    x++
                }
            }

            if lastX < 0 {
                lines = append(lines, "")
                continue
            }

            var sb strings.Builder
            var curStyle uv.Style
            for x := 0; x <= lastX; {
                var cell *uv.Cell
                if inScrollback {
                    cell = g.em.ScrollbackCellAt(x, row)
                } else {
                    cell = g.em.CellAt(x, row)
                }
                var cellStyle uv.Style
                cellContent := " "
                cellWidth := 1
                if cell != nil {
                    cellStyle = cell.Style
                    if cell.Content != "" {
                        cellContent = cell.Content
                    }
                    if cell.Width > 0 {
                        cellWidth = cell.Width
                    }
                }

                if !cellStyle.Equal(&curStyle) {
                    diff := cellStyle.Diff(&curStyle)
                    sb.WriteString(diff)
                    curStyle = cellStyle
                }
                sb.WriteString(cellContent)
                x += cellWidth
            }
            if !curStyle.IsZero() {
                sb.WriteString(ansi.ResetStyle)
            }
            lines = append(lines, sb.String())
        }
    }

    for len(lines) > 0 && lines[len(lines)-1] == "" {
        lines = lines[:len(lines)-1]
    }

    if len(lines) == 0 {
        return "", totalLines
    }
    return strings.Join(lines, "\n") + "\n", totalLines
}
```

**Verification — automated:**
- [ ] `go test -v ./internal/server/term -run TestDumpText` passes
- [ ] `go test -v ./internal/server -run TestPaneCapture` passes
- [ ] `make quick` passes

**Verification — manual:**
- [ ] Inspect generated ANSI SGR diffs for styled text, verifying resets occur at line ends.

---

## Phase 2: Wire Protocol, Codec, Protobuf & Version Bump

Add `MsgDumpPaneRequest` and `MsgDumpPaneResponse` to Protobuf and Go wire types, bump protocol version to 23, and handle request in server.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto` — Add `MsgDumpPaneRequest` (ClientMessage field 23) and `MsgDumpPaneResponse` (ServerMessage field 21).
- Generate: `internal/protocol/wirepb/wideboi.pb.go` and `web/src/gen/internal/protocol/wirepb/wideboi_pb.ts` via `make proto`.
- Modify: `internal/protocol/messages.go` — Add `MsgDumpPaneRequest` and `MsgDumpPaneResponse` struct definitions.
- Modify: `internal/protocol/codec.go` — Add (un)marshaling for `MsgDumpPaneRequest` and `MsgDumpPaneResponse`.
- Modify: `internal/protocol/version.go` — Bump `Version` from 22 to 23.
- Modify: `web/src/version.ts` — Bump `PROTOCOL_VERSION` from 22 to 23.
- Modify: `internal/server/handlers.go` — Add `handleDumpPaneRequestLocked` and wire into `handleClientMsg` and `applyEffects`.
- Test: `internal/protocol/wire_test.go` — Verify round trip of `MsgDumpPaneRequest` and `MsgDumpPaneResponse`.
- Test: `internal/server/control_test.go` — Test `MsgDumpPaneRequest` handling end-to-end through `Server`.

**Key changes:**
In `internal/protocol/messages.go`:
```go
type MsgDumpPaneRequest struct {
    PaneID     int
    Scrollback bool
    Offset     int
    Limit      int
    TailLines  int
    ANSI       bool
    CountOnly  bool
}

type MsgDumpPaneResponse struct {
    PaneID     int
    Text       string
    Error      string
    TotalLines int
    Offset     int
    Lines      int
}
```

In `internal/server/handlers.go`:
```go
case protocol.MsgDumpPaneRequest:
    s.handleDumpPaneRequestLocked(tp, m, eff)

func (s *Server) handleDumpPaneRequestLocked(tp transport.ServerTransport, m protocol.MsgDumpPaneRequest, eff *msgEffects) {
    p, ok := s.panes[m.PaneID]
    if !ok {
        eff.dumpResp = &protocol.MsgDumpPaneResponse{
            PaneID: m.PaneID,
            Error:  fmt.Sprintf("pane %d not found", m.PaneID),
        }
        return
    }
    if m.CountOnly {
        _, total := p.DumpText(m.Scrollback, 0, 0, 0, false)
        eff.dumpResp = &protocol.MsgDumpPaneResponse{
            PaneID:     m.PaneID,
            TotalLines: total,
        }
        return
    }
    text, total := p.DumpText(m.Scrollback, m.Offset, m.Limit, m.TailLines, m.ANSI)
    eff.dumpResp = &protocol.MsgDumpPaneResponse{
        PaneID:     m.PaneID,
        Text:       text,
        TotalLines: total,
        Offset:     m.Offset,
        Lines:      m.Limit,
    }
}
```

**Verification — automated:**
- [ ] `make proto` regenerates Go and TypeScript protobuf sources without error
- [ ] `go test -v ./internal/protocol -run TestWireTypesCarryNoInterfaces` passes
- [ ] `go test -v ./internal/protocol -run TestCodecRoundTripsEveryField` passes
- [ ] `go test -v ./internal/server -run TestDumpPane` passes
- [ ] `make quick` passes

**Verification — manual:**
- [ ] Inspect git diff of `wideboi.proto` and generated files to ensure fields 23 and 21 are sequentially added.

---

## Phase 3: Root CLI Command `wideboi dump-pane` & `capture` Alias

Add `runDumpPane` in `cmd/wideboi/control.go`, wire it into `cmd/wideboi/main.go`, and alias `capture` to it.

**Files:**
- Modify: `cmd/wideboi/main.go` — Add `dump-pane` to subcommand list in `parseCLI` and dispatch in `main()`; route `capture` to `runDumpPane`.
- Modify: `cmd/wideboi/control.go` — Implement `runDumpPane(cfg config.Config, args []string, stdout, stderr io.Writer) error`. Reimplement `runCapture` as wrapper calling `runDumpPane`.
- Test: `cmd/wideboi/control_test.go` — Add unit tests for `dump-pane` flag parsing, pane resolution, file writing, count-only, and error messages.

**Key changes:**
In `cmd/wideboi/control.go`:
```go
func runDumpPane(cfg config.Config, args []string, stdout, stderr io.Writer) error {
    fs := flag.NewFlagSet("dump-pane", flag.ContinueOnError)
    fs.SetOutput(stderr)

    var scrollback bool
    var scrollbackLines int
    var offset int
    var limit int
    var countOnly bool
    var ansi, plain bool
    var output string
    var session, socket string

    // Support -s, --scrollback (bool or optional lines) and -n, --lines
    // Parse flags, resolve paneID from arg or WIDEBOI_PANE_ID
    // If output != "", write to output file with os.WriteFile(output, []byte(resp.Text), 0666)
    // If countOnly, print fmt.Fprintf(stdout, "%d\n", resp.TotalLines)
    // Else print fmt.Fprint(stdout, resp.Text)
}
```

**Verification — automated:**
- [ ] `go test -v ./cmd/wideboi -run TestDumpPane` passes
- [ ] `go test -v ./cmd/wideboi -run TestCapture` passes
- [ ] `make quick` passes

**Verification — manual:**
- [ ] Test `dump-pane` with `-o /tmp/test-dump.txt` and verify contents and file permissions.

---

## Phase 4: Internal Command Registry & Prompt Integration

Register `dump-pane` in `internal/commands/registry.go` with aliases `dump` and `capture`.

**Files:**
- Modify: `internal/commands/registry.go` — Register `dump-pane` command with aliases `dump` and `capture`.
- Test: `internal/commands/commands_test.go` — Add test verifying `dump-pane` execution in registry.

**Key changes:**
In `internal/commands/registry.go`:
```go
r.Register(Command{
    Name:        "dump-pane",
    Aliases:     []string{"dump", "capture"},
    Description: "Dump the screen or scrollback of a pane",
    Category:    "Panes",
    ArgsUsage:   "[pane-id] [-s|--scrollback] [--offset <N>] [--limit|-n <N>] [-c|--count] [--ansi|--plain] [-o|--output <file>]",
    Run: func(ctx context.Context, inv Invocation) error {
        // Parse args and flags
        // Resolve targetID from args or inv.CallerPaneID
        // RPCQuery MsgDumpPaneRequest
        // Route output to inv.Stdout or -o <file>
    },
})
```

**Verification — automated:**
- [ ] `go test -v ./internal/commands -run TestDumpPaneCommand` passes
- [ ] `make quick` passes

**Verification — manual:**
- [ ] Verify `dump-pane` appears in `wideboi help` / internal command list.

---

## Phase 5: Agent Skill & Documentation Updates + End-to-End Verification

Update `docs/skills/wideboi-control/SKILL.md` and run the full test suite including pty smoke and attach suites.

**Files:**
- Modify: `docs/skills/wideboi-control/SKILL.md` — Document `dump-pane` with pagination, line count, ANSI/plain, output file, and `capture` alias.
- Test: `cmd/wideboi/control_e2e_test.go` — Add e2e test for `dump-pane` against a running server.

**Verification — automated:**
- [ ] `make quick` passes
- [ ] `make check` passes (including race, smoke, attach-check, verify-exit)

**Verification — manual:**
- [ ] Verify `docs/skills/wideboi-control/SKILL.md` formatting and clarity.
