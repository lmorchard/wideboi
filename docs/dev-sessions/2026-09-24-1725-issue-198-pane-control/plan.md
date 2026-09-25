# Agent-Friendly Pane Control Subcommands Implementation Plan

**Goal:** Implement `split`, `send`, `capture`, and `close` subcommands in wideboi to enable deterministic, scriptable pane control by agents and shell tools using pane IDs.

**Approach:** Introduce dedicated request/response wire protocol messages to guarantee synchronous acknowledgement and error reporting for unknown panes. Implement robust terminal text extraction supporting scrollback and line limits in `term.Grid`. Wire these into server handlers and new CLI subcommands in `cmd/wideboi`, with full protocol versioning and end-to-end tests.

**Tech stack:** Go, Protobuf (`buf`), Unix domain sockets, `vt.SafeEmulator`, `flag.FlagSet`.

---

## Phase 1: Wire Protocol Messages, Codec, and Version Bump

Define new request/response protobuf messages for split, send, capture, and close operations. Implement Go struct definitions, codec translation, reflection tests, transport round-trip tests, and bump `protocol.Version` to 8.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto` — add protobuf messages and update `ClientMessage` and `ServerMessage` oneofs
- Modify: `internal/protocol/messages.go` — declare exported Go message structs
- Modify: `internal/protocol/codec.go` — implement marshal and unmarshal cases
- Modify: `internal/protocol/wire_test.go` — register new types in `wireTypes`
- Modify: `internal/transport/wire_test.go` — add instantiated message literals to `TestEveryMessageTypeRoundtrips`
- Modify: `internal/protocol/version.go` — bump `Version` to 8
- Modify: `web/src/client.ts` — bump offered subprotocol to `wideboi.v8`
- Modify: `web/src/lifecycle.test.ts`, `web/src/client.test.ts`, `web/tests/*.spec.js` — update references to `wideboi.v8`
- Test: `internal/protocol/codec_test.go`

**Key changes:**

In `internal/protocol/wirepb/wideboi.proto`:
```protobuf
message MsgSplitRequest {
  string command = 1;
  string cwd = 2;
  int32 after_pane_id = 3;
}

message MsgSplitResponse {
  int32 pane_id = 1;
  string error = 2;
}

message MsgSendInputRequest {
  int32 pane_id = 1;
  bytes data = 2;
}

message MsgSendInputResponse {
  int32 pane_id = 1;
  string error = 2;
}

message MsgCaptureRequest {
  int32 pane_id = 1;
  bool scrollback = 2;
  int32 lines = 3;
}

message MsgCaptureResponse {
  int32 pane_id = 1;
  string text = 2;
  string error = 3;
}

message MsgClosePaneRequest {
  int32 pane_id = 1;
}

message MsgClosePaneResponse {
  int32 pane_id = 1;
  string error = 2;
}
```

In `internal/protocol/messages.go`:
```go
type MsgSplitRequest struct {
	Command     string
	Cwd         string
	AfterPaneID int32
}

type MsgSplitResponse struct {
	PaneID int32
	Error  string
}

type MsgSendInputRequest struct {
	PaneID int32
	Data   []byte
}

type MsgSendInputResponse struct {
	PaneID int32
	Error  string
}

type MsgCaptureRequest struct {
	PaneID     int32
	Scrollback bool
	Lines      int32
}

type MsgCaptureResponse struct {
	PaneID int32
	Text   string
	Error  string
}

type MsgClosePaneRequest struct {
	PaneID int32
}

type MsgClosePaneResponse struct {
	PaneID int32
	Error  string
}
```

**Verification — automated:**
- [x] `make proto` generates Go and TypeScript protobuf bindings cleanly — **verified**
- [x] `go test -v ./internal/protocol -run TestWireTypes` passes — **verified**
- [x] `go test -v ./internal/protocol -run TestCodec` passes — **verified**
- [x] `go test -v ./internal/transport -run TestEveryMessageTypeRoundtrips` passes — **verified**
- [x] `make test` passes — **verified**

**Verification — manual:**
- [x] Check git diff on generated protobuf files to verify clean generation without unexpected changes. — **verified**

---

## Phase 2: Terminal Text Capture in `term.Grid` and `Pane`

Add `CaptureText(scrollback bool, maxLines int) string` to extract screen lines, handling wide characters, trailing space trimming, scrollback prepend, line limits, and trailing blank row trimming.

**Files:**
- Modify: `internal/server/term/grid.go` — declare `CaptureText` on `Grid` interface and implement on `vtGrid`
- Modify: `internal/server/pane.go` — implement `CaptureText` on `Pane` with locking (`resizeMu.Lock`, `renderMu.RLock`)
- Test: `internal/server/term/grid_test.go` — unit tests for `vtGrid.CaptureText`
- Test: `internal/server/pane_test.go` — unit tests for `Pane.CaptureText`

**Key changes:**

In `internal/server/term/grid.go`:
```go
type Grid interface {
    // ...
    CaptureText(scrollback bool, maxLines int) string
}

func (g *vtGrid) CaptureText(scrollback bool, maxLines int) string {
    g.writeResizeMu.Lock()
    defer g.writeResizeMu.Unlock()

    cols, rows := g.em.Size()
    sbLen := g.em.ScrollbackLen()

    totalLines := rows
    if scrollback {
        totalLines += sbLen
    }

    startLine := 0
    if maxLines > 0 && maxLines < totalLines {
        startLine = totalLines - maxLines
    }

    var lines []string
    for idx := startLine; idx < totalLines; idx++ {
        var sb strings.Builder
        for x := 0; x < cols; {
            var cell *uv.Cell
            if scrollback && idx < sbLen {
                cell = g.em.ScrollbackCellAt(x, idx)
            } else {
                y := idx
                if scrollback {
                    y = idx - sbLen
                }
                cell = g.em.CellAt(x, y)
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
    }

    // Trim trailing empty lines at the bottom of the viewport
    for len(lines) > 0 && lines[len(lines)-1] == "" {
        lines = lines[:len(lines)-1]
    }

    if len(lines) == 0 {
        return ""
    }
    return strings.Join(lines, "\n") + "\n"
}
```

In `internal/server/pane.go`:
```go
func (p *Pane) CaptureText(scrollback bool, maxLines int) string {
    p.resizeMu.Lock()
    defer p.resizeMu.Unlock()
    p.renderMu.RLock()
    defer p.renderMu.RUnlock()

    select {
    case <-p.closed:
        return ""
    default:
    }
    return p.grid.CaptureText(scrollback, maxLines)
}
```

**Verification — automated:**
- [x] `go test -v ./internal/server/term -run TestCaptureText` passes — **verified**
- [x] `go test -v ./internal/server -run TestPaneCaptureText` passes — **verified**
- [x] `go test -race ./internal/server/term -run TestCaptureText` passes — **verified**
- [x] `make test` passes — **verified**

**Verification — manual:**
- [x] Verify wide glyphs (emoji, CJK) do not produce duplicated characters or off-by-one column artifacts in captured text. — **verified via TestCaptureTextWideCharacters**

---

## Phase 3: Server Request Handling and Pane Lifecycle

Add `Dir` to `StartupPane` so pane working directories can be configured. Handle `MsgSplitRequest`, `MsgSendInputRequest`, `MsgCaptureRequest`, and `MsgClosePaneRequest` in `Server.handleClientMsg`, returning acknowledged response messages directly to the requesting transport.

**Files:**
- Modify: `internal/server/server.go` — add `Dir` to `StartupPane`, handle `spec.Dir` in `spawnPaneWithSpecLocked`, and implement message handlers in `handleClientMsg`
- Test: `internal/server/server_test.go` — unit tests for each new request and response

**Key changes:**

In `internal/server/server.go`:
```go
type StartupPane struct {
    Command string
    Dir     string
    Width   int
}

// In spawnPaneWithSpecLocked:
dir := s.cwd
if spec.Dir != "" {
    dir = spec.Dir
}
p, err := NewPane(id, argv, paneCols, paneRows, dir)

// In handleClientMsg:
case protocol.MsgSplitRequest:
    var resp protocol.MsgSplitResponse
    spec := StartupPane{Command: m.Command, Dir: m.Cwd}
    p, err := s.spawnPaneWithSpecLocked(spec, int(m.AfterPaneID))
    if err != nil {
        resp = protocol.MsgSplitResponse{Error: err.Error()}
    } else {
        resp = protocol.MsgSplitResponse{PaneID: int32(p.ID())}
        createdPaneID = p.ID()
        needBroadcast = true
    }
    splitResponse = &resp

case protocol.MsgSendInputRequest:
    var resp protocol.MsgSendInputResponse
    p, ok := s.panes[int(m.PaneID)]
    if !ok {
        resp = protocol.MsgSendInputResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d not found", m.PaneID)}
    } else {
        p.Write(m.Data)
        resp = protocol.MsgSendInputResponse{PaneID: m.PaneID}
    }
    sendResponse = &resp

case protocol.MsgCaptureRequest:
    var resp protocol.MsgCaptureResponse
    p, ok := s.panes[int(m.PaneID)]
    if !ok {
        resp = protocol.MsgCaptureResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d not found", m.PaneID)}
    } else {
        text := p.CaptureText(m.Scrollback, int(m.Lines))
        resp = protocol.MsgCaptureResponse{PaneID: m.PaneID, Text: text}
    }
    captureResponse = &resp

case protocol.MsgClosePaneRequest:
    var resp protocol.MsgClosePaneResponse
    p, ok := s.panes[int(m.PaneID)]
    if !ok {
        resp = protocol.MsgClosePaneResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d not found", m.PaneID)}
    } else {
        s.removePaneLocked(int(m.PaneID))
        needBroadcast = true
        resp = protocol.MsgClosePaneResponse{PaneID: m.PaneID}
        go p.Close()
    }
    closeResponse = &resp
```

**Verification — automated:**
- [x] `go test -v ./internal/server -run TestPaneControlRequests` passes — **verified via TestServerPaneControlRequests**
- [x] `make test` passes — **verified**

**Verification — manual:**
- [x] Confirm request/response handling sends directly to the requesting client transport outside `s.mu` and does not disturb attached UI clients. — **verified**

---

## Phase 4: CLI Subcommands (`split`, `send`, `capture`, `close`)

Implement the CLI command runners, client connection helper, flag parsing, help text, and documentation.

**Files:**
- Create: `cmd/wideboi/control.go` — implements `runSplit`, `runSend`, `runCapture`, `runClose`, and shared RPC helper `connectAndRequest`
- Modify: `cmd/wideboi/main.go` — register subcommands in `parseCLI`, dispatch in `main()`, update `printHelp`
- Modify: `README.md` — document new subcommands with examples
- Test: `cmd/wideboi/main_test.go` — CLI flag parsing tests

**Key changes:**

In `cmd/wideboi/main.go`:
- Register `"split"`, `"send"`, `"capture"`, `"close"` in `parseCLI`.
- Collect subcommand args for dedicated flag parsing.
- Update `printHelp` to describe `split`, `send`, `capture`, and `close`.

In `cmd/wideboi/control.go`:
```go
// Shared RPC helper: connects to cfg.Socket, performs handshake, runs ClientSocketConn pumps,
// sends request message, and waits for corresponding response message with a timeout.
func rpcExchange(cfg config.Config, req transport.ClientMessage, timeout time.Duration) (transport.ServerMessage, error)

func runSplit(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) error
func runSend(cfg config.Config, args []string, stderr io.Writer) error
func runCapture(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) error
func runClose(cfg config.Config, args []string, stderr io.Writer) error
```

Command specifications:
- `wideboi split [--cwd <dir>] [--after <pane-id>] [command...]` -> prints `<id>\n` to stdout.
- `wideboi send <pane-id> <text> [--enter|-e]` -> sends literal text (with `\r` appended if `--enter`), exits 0.
- `wideboi capture <pane-id> [--scrollback|-S] [--lines|-n <count>]` -> prints text to stdout.
- `wideboi close <pane-id>` -> closes pane, exits 0.

**Verification — automated:**
- [x] `go test -v ./cmd/wideboi -run TestParseCLI` passes — **verified**
- [x] `make lint` passes — **verified**
- [x] `make test` passes — **verified**

**Verification — manual:**
- [x] Check `wideboi help` output to verify descriptions, usage, and flags are well-formatted. — **verified**

---

## Phase 5: End-to-End Integration Tests & Verification

Build end-to-end integration tests that launch a live server, invoke `split`, `send`, `capture`, and `close` via the real CLI / binary, test error cases (invalid IDs, dead server), and verify against the full test gate.

**Files:**
- Create: `cmd/wideboi/control_e2e_test.go` — integration test testing all four subcommands against a live server
- Modify: `docs/LESSONS.md` (if any new lessons discovered during implementation)

**Verification — automated:**
- [x] `go test -v ./cmd/wideboi -run TestControlSubcommandsE2E` passes — **verified (0.86s)**
- [x] `make quick` passes — **verified**
- [x] `make check` passes (fmt-check, lint, seam-check, test, web-test, web-accept, race, verify-exit, smoke, attach-check) — **verified**

**Verification — manual:**
- [x] Run sample manual workflow:
  ```bash
  ./bin/wideboi split echo "hello agent"
  ./bin/wideboi capture 1
  ./bin/wideboi send 1 "uptime" -e
  ./bin/wideboi close 1
  ```
  — **verified in TestControlSubcommandsE2E**
