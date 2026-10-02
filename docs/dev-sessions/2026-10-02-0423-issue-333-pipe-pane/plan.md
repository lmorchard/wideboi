# Pipe-Pane Implementation Plan

**Goal:** Implement `wideboi pipe-pane` to tap and stream the raw unparsed PTY byte stream of a pane in real time to stdout or a file, with root CLI, internal command registry, and agent skill documentation.

**Approach:** Fan out raw PTY reads in `internal/server/pane.go` to non-blocking tap subscribers. Add `MsgPipePaneRequest` and `MsgPipePaneResponse` to wire protocol, bumping `protocol.Version` to 24. Implement streaming reader loop in `cmd/wideboi/control.go` and register `pipe-pane` in `internal/commands/registry.go`.

**Tech stack:** Go, Protobuf / Buf, Unix domain sockets, PTY.

---

## Phase 1: Server Pane Tap Fan-Out Infrastructure

Implement tap registration, non-blocking broadcast with overflow protection, and teardown in `Pane`.

**Files:**
- Modify: `internal/server/pane.go` — Add `tapMu sync.RWMutex`, `taps map[uint64]chan []byte`, `nextTapID uint64`, `AddTap`, `RemoveTap`, `broadcastRawBytes`, and `closeTaps`. Forward raw bytes from PTY reader pump before `qs.process`.
- Create: `internal/server/pane_tap_test.go` — Unit tests for tap delivery, multiple subscribers, drop on overflow, and teardown on pane exit.

**Key changes:**
In `internal/server/pane.go`:
```go
type Pane struct {
    // ... existing fields ...
    tapMu     sync.RWMutex
    taps      map[uint64]chan []byte
    nextTapID uint64
}

func (p *Pane) AddTap(ch chan []byte) uint64 {
    p.tapMu.Lock()
    defer p.tapMu.Unlock()
    if p.taps == nil {
        p.taps = make(map[uint64]chan []byte)
    }
    p.nextTapID++
    id := p.nextTapID
    p.taps[id] = ch
    return id
}

func (p *Pane) RemoveTap(id uint64) {
    p.tapMu.Lock()
    defer p.tapMu.Unlock()
    delete(p.taps, id)
}

func (p *Pane) broadcastRawBytes(chunk []byte) {
    p.tapMu.RLock()
    defer p.tapMu.RUnlock()
    if len(p.taps) == 0 {
        return
    }
    cp := append([]byte(nil), chunk...)
    for _, ch := range p.taps {
        select {
        case ch <- cp:
        default:
            // drop chunk on slow consumer to protect live pane
        }
    }
}

func (p *Pane) closeTaps() {
    p.tapMu.Lock()
    defer p.tapMu.Unlock()
    for id, ch := range p.taps {
        close(ch)
        delete(p.taps, id)
    }
}
```

In `(p *Pane) Start(onExit func())` reader loop (`internal/server/pane.go:178`):
```go
n, err := p.pty.Master.Read(buf)
if n > 0 {
    p.broadcastRawBytes(buf[:n])
    // ... queryScanner processing ...
}
if err != nil {
    p.closeTaps()
    return
}
```

**Verification — automated:**
- [ ] `go test -v ./internal/server -run TestPaneTap` passes
- [ ] `make quick` passes

**Verification — manual:**
- [ ] Verify `p.closeTaps()` runs on both normal exit and `p.Close()`.

---

## Phase 2: Wire Protocol, Codec, Protobuf Schema & Version Bump

Add `MsgPipePaneRequest` and `MsgPipePaneResponse` to wire protocol, generate protobuf bindings, bump protocol version to 24, and implement server streaming handler.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto` — Add `MsgPipePaneRequest` (ClientMessage field 24) and `MsgPipePaneResponse` (ServerMessage field 22).
- Generate: `internal/protocol/wirepb/wideboi.pb.go` and `web/src/gen/internal/protocol/wirepb/wideboi_pb.ts` via `make proto`.
- Modify: `internal/protocol/messages.go` — Add Go structs.
- Modify: `internal/protocol/codec.go` — Add codec converters.
- Modify: `internal/protocol/version.go` — Bump `Version` from 23 to 24.
- Modify: `web/src/version.ts` — Bump `PROTOCOL_VERSION` from 23 to 24.
- Modify: `internal/protocol/version_guard_test.go` — Add hash for v24.
- Modify: `internal/protocol/wire_test.go` — Add types to `wireTypes`.
- Modify: `internal/transport/wire_test.go` — Add round trip test.
- Modify: `internal/server/handlers.go` — Add `handlePipePaneRequestLocked` to stream chunks to transport.
- Test: `internal/server/control_test.go` — Add test for `MsgPipePaneRequest`.

**Key changes:**
In `internal/protocol/messages.go`:
```go
type MsgPipePaneRequest struct {
    PaneID int `json:"pane_id"`
}

type MsgPipePaneResponse struct {
    PaneID int    `json:"pane_id"`
    Data   []byte `json:"data,omitempty"`
    Closed bool   `json:"closed,omitempty"`
    Error  string `json:"error,omitempty"`
}
```

In `internal/server/handlers.go`:
```go
func (s *Server) handlePipePaneRequestLocked(ctx context.Context, tp transport.Transport, m protocol.MsgPipePaneRequest) {
    p, ok := s.panes[m.PaneID]
    if !ok {
        tp.SendServer(ctx, protocol.MsgPipePaneResponse{
            PaneID: m.PaneID,
            Error:  fmt.Sprintf("pane %d not found", m.PaneID),
        })
        return
    }

    ch := make(chan []byte, 256)
    tapID := p.AddTap(ch)

    go func() {
        defer p.RemoveTap(tapID)
        for {
            select {
            case <-ctx.Done():
                return
            case chunk, ok := <-ch:
                if !ok {
                    tp.SendServer(context.Background(), protocol.MsgPipePaneResponse{
                        PaneID: m.PaneID,
                        Closed: true,
                    })
                    return
                }
                if !tp.SendServer(ctx, protocol.MsgPipePaneResponse{
                    PaneID: m.PaneID,
                    Data:   chunk,
                }) {
                    return
                }
            }
        }
    }()
}
```

**Verification — automated:**
- [ ] `make proto` completes without error
- [ ] `go test -v ./internal/protocol` passes
- [ ] `go test -v ./internal/server -run TestPipePane` passes
- [ ] `make quick` passes

---

## Phase 3: Root CLI Command `wideboi pipe-pane`

Implement the `runPipePane` subcommand in `cmd/wideboi/control.go` and wire into `cmd/wideboi/main.go`.

**Files:**
- Modify: `cmd/wideboi/main.go` — Add `pipe-pane` to `parseCLI` subcommands and `main()` dispatch.
- Modify: `cmd/wideboi/control.go` — Implement `runPipePane(cfg config.Config, args []string, stdout, stderr io.Writer) error`.
- Test: `cmd/wideboi/control_test.go` — Add unit tests for `pipe-pane` flag parsing, stdout streaming, file writing, `-a` append mode, and pane-id fallback.

**Key changes:**
In `cmd/wideboi/control.go`:
```go
func runPipePane(cfg config.Config, args []string, stdout, stderr io.Writer) error {
    fs := flag.NewFlagSet("pipe-pane", flag.ContinueOnError)
    fs.SetOutput(stderr)

    var output string
    var appendMode bool
    var session, socket string

    fs.StringVar(&output, "output", "", "write raw stream to file instead of stdout")
    fs.StringVar(&output, "o", "", "write raw stream to file instead of stdout")
    fs.BoolVar(&appendMode, "append", false, "append to output file instead of truncating")
    fs.BoolVar(&appendMode, "a", false, "append to output file instead of truncating")
    addTargetFlags(fs, &session, &socket)

    if err := fs.Parse(reorderFlags(args)); err != nil {
        if errors.Is(err, flag.ErrHelp) {
            return nil
        }
        return err
    }
    applySessionFlags(&cfg, session, socket)

    var callerID int
    if envID := os.Getenv("WIDEBOI_PANE_ID"); envID != "" {
        if id, err := strconv.Atoi(envID); err == nil {
            callerID = id
        }
    }

    targetID := callerID
    rest := fs.Args()
    if len(rest) > 0 {
        id, err := strconv.Atoi(rest[0])
        if err != nil {
            return fmt.Errorf("invalid pane id %q: %w", rest[0], err)
        }
        targetID = id
    } else if targetID <= 0 {
        return fmt.Errorf("usage: wideboi pipe-pane [flags] [pane-id] (pane-id required outside wideboi pane)")
    }

    var outWriter io.Writer = stdout
    if output != "" {
        flagVal := os.O_CREATE | os.O_WRONLY
        if appendMode {
            flagVal |= os.O_APPEND
        } else {
            flagVal |= os.O_TRUNC
        }
        f, err := os.OpenFile(output, flagVal, 0666)
        if err != nil {
            return fmt.Errorf("opening output file %q: %w", output, err)
        }
        defer f.Close()
        outWriter = f
    }

    // Connect, send MsgPipePaneRequest, loop reading MsgPipePaneResponse chunks until Closed or SIGINT
    // ...
}
```

**Verification — automated:**
- [ ] `go test -v ./cmd/wideboi -run TestPipePaneCLI` passes
- [ ] `make quick` passes

---

## Phase 4: Internal Command Registry Integration (`:pipe-pane`)

Register `pipe-pane` in `internal/commands/registry.go` with `--stop` and `-o <file>` options.

**Files:**
- Modify: `internal/commands/registry.go` — Register `pipe-pane` command.
- Test: `internal/commands/commands_test.go` — Unit tests for `:pipe-pane` in registry.

**Key changes:**
In `internal/commands/registry.go`:
```go
var (
    activePipeTapsMu sync.Mutex
    activePipeTaps   = make(map[int]context.CancelFunc)
)

r.Register(Command{
    Name:        "pipe-pane",
    Aliases:     []string{"pipe"},
    Description: "Pipe raw PTY output of a pane to a file or stream",
    Category:    "Panes",
    ArgsUsage:   "[pane-id] [-o <file>] [-a|--append] [--stop]",
    Run: func(ctx context.Context, inv Invocation) error {
        // Parse flags: -o, -a, --stop
        // Resolve targetID from args or inv.CallerPaneID
        // If --stop: cancel active tap
        // If -o <file>: launch background streaming tap
    },
})
```

**Verification — automated:**
- [ ] `go test -v ./internal/commands -run TestPipePaneCommand` passes
- [ ] `make quick` passes

---

## Phase 5: Agent Skill & Documentation Updates + Full Verification

Document `pipe-pane` in `docs/skills/wideboi-control/SKILL.md`, add e2e test, and run full test suites.

**Files:**
- Modify: `docs/skills/wideboi-control/SKILL.md` — Document `pipe-pane` for live streaming and fixture recording.
- Modify: `cmd/wideboi/control_e2e_test.go` — Add e2e test for `pipe-pane`.

**Verification — automated:**
- [ ] `make quick` passes
- [ ] `make check` passes
