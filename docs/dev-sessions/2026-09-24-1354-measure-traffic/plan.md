# Measure live pane traffic and rendering costs (#179) Implementation Plan

**Goal:** Make live pane traffic and rendering cost observable (`status --traffic`,
pprof, client benchmark, browser `?stats=1`), reproduce representative sessions
with `make traffic`, and record a decision in `docs/partial-pane-updates.md`.

**Approach:** Server keeps always-on per-client counters, and transports count
payload and `net.Conn` bytes. A new `MsgTrafficRequest`/`MsgTrafficStats` pair
carries them to `wideboi status --traffic`. Timing is gated by
`WIDEBOI_TRAFFIC_TIMING=1`. A Python harness drives a real server with pty clients
plus a Go WebSocket sink that also estimates streaming-deflate savings.

**Tech stack:** Go (protobuf via `make proto`, gorilla/websocket, runtime/pprof),
Python 3 on `scripts/ptylib.py`, TypeScript/Lit + vitest.

## Decisions made while planning (beyond spec.md)

- **Protocol version bumps to 3.** `internal/protocol/version.go` says to bump
  for any new message. That means running v2 servers refuse new-build clients
  with the existing clear handshake message, which is the documented norm.
  The web client's `wideboi.v2` subprotocol string becomes `wideboi.v3`.
- **A "client" in the report is a transport that has sent `MsgAttach`.**
  `status` / `status --traffic` / `kill-session` connections never attach, so
  they are excluded without special-casing the requester. Tests append to
  `s.transports` directly, so traffic entries are created lazily
  (`trafficLocked(tp)`), not in an add-transport helper.
- **Resyncs are counted as resync requests received** (`MsgPaneResync`). The
  full snapshot answering a request is also counted under `full`. This is the
  same number as "fulls sent because the client asked" without per-pane
  pending state.
- **Transport bytes come from one mechanism for both transports**: a
  `countingConn` wrapping the `net.Conn`. Socket: wrapped in
  `NewServerSocketConn`, so the hello handshake (before wrapping) is excluded.
  WebSocket: gorilla 1.5.3 hijacks via `w.(http.Hijacker)` and writes the
  101 response and every frame to the hijacked `netConn`
  (`gorilla/websocket@v1.5.3/server.go:175-256`). So the handler passes a
  `CountingResponseWriter` whose `Hijack` wraps the conn. The 101 response,
  pings and frame headers are included.
- **Departed clients** fold into a session `Departed` aggregate on removal
  (attached ones only), so a scenario's totals survive a client leaving.

---

## Phase 1: Server counters and `wideboi status --traffic`

End-to-end message-kind counting: the server counts full/row/shift/rows/resync/
failures per attached client. A new protocol message pair carries them, and
the CLI prints them. Byte columns read 0 until Phase 2.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto` — new messages; `traffic_request = 11` in `ClientMessage`, `traffic_stats = 6` in `ServerMessage`.
- Modify: `internal/protocol/messages.go` — Go structs.
- Modify: `internal/protocol/codec.go` — Marshal/Unmarshal cases for both.
- Modify: `internal/protocol/version.go` — `Version = 3`, doc line "3 adds MsgTrafficRequest/MsgTrafficStats".
- Modify: `web/src/client.ts:32` — `"wideboi.v3"`.
- Regenerate: `make proto` (`internal/protocol/wirepb/*.pb.go`, `web/src/gen/...`).
- Create: `internal/server/traffic.go` — per-client traffic state and report building.
- Modify: `internal/server/server.go` — `traffic` map + `departed` + `started` fields; count in `broadcastPaneUpdates` and resync/attach/request handling; fold on `removeTransportLocked`.
- Modify: `cmd/wideboi/main.go` — `--traffic` flag (status only), help text; `status` dispatch.
- Modify: `cmd/wideboi/status.go` — `runTrafficStatus`.
- Test: `internal/protocol/codec_test.go` (extend round-trip for the new messages); `internal/server/traffic_test.go`; extend `cmd/wideboi/status_test.go` with the `formatTraffic` test.

**Key changes:**

```proto
message MsgTrafficRequest {}

message TimingStat {
  uint64 count = 1;
  uint64 total_nanos = 2;
  uint64 max_nanos = 3;
}

message ClientTraffic {
  int32 client_id = 1;
  string transport = 2;        // "socket", "websocket", "inproc"
  int64 connected_millis = 3;
  uint64 full_updates = 4;
  uint64 row_patches = 5;
  uint64 shift_patches = 6;
  uint64 changed_rows = 7;
  uint64 resync_requests = 8;
  uint64 send_failures = 9;
  uint64 messages = 10;        // every message the transport encoded
  uint64 payload_bytes = 11;   // protobuf envelope bytes, all messages
  uint64 pane_payload_bytes = 12; // protobuf bytes of pane updates + patches
  uint64 wire_bytes = 13;      // bytes written to the net.Conn
  TimingStat encode = 14;
}

message MsgTrafficStats {
  int64 uptime_millis = 1;
  bool timing_enabled = 2;
  repeated ClientTraffic clients = 3;
  ClientTraffic departed = 4;  // sum over attached clients that have left
  TimingStat render = 5;
  TimingStat build_patch = 6;
}
```

```go
// messages.go
type MsgTrafficRequest struct{}
type TimingStat struct{ Count, TotalNanos, MaxNanos uint64 }
func (t *TimingStat) Add(d time.Duration)       // Count++, TotalNanos+=, MaxNanos=max
func (t *TimingStat) Merge(o TimingStat)
type ClientTraffic struct {
	ClientID        int
	Transport       string
	ConnectedMillis int64
	FullUpdates, RowPatches, ShiftPatches, ChangedRows uint64
	ResyncRequests, SendFailures                       uint64
	Messages, PayloadBytes, PanePayloadBytes, WireBytes uint64
	Encode                                             TimingStat
}
func (c *ClientTraffic) Merge(o ClientTraffic)   // sums counters, merges Encode; ID/Transport/Connected untouched
type MsgTrafficStats struct {
	UptimeMillis  int64
	TimingEnabled bool
	Clients       []ClientTraffic
	Departed      ClientTraffic
	Render, BuildPatch TimingStat
}
```

```go
// internal/server/traffic.go
type clientTraffic struct {
	id       int
	attached time.Time // zero until MsgAttach
	counts   protocol.ClientTraffic // server-side fields only
}

// trafficLocked returns tp's entry, creating it on first use. s.mu held.
func (s *Server) trafficLocked(tp transport.Transport) *clientTraffic

// recordSendLocked counts one pane delivery. s.mu held.
func (s *Server) recordSendLocked(tp transport.Transport, msg transport.ServerMessage, accepted bool) {
	t := s.trafficLocked(tp)
	if !accepted { t.counts.SendFailures++; return }
	switch m := msg.(type) {
	case protocol.MsgPaneUpdate:
		t.counts.FullUpdates++
	case protocol.MsgPanePatch:
		if m.ShiftRows != 0 { t.counts.ShiftPatches++ } else { t.counts.RowPatches++ }
		t.counts.ChangedRows += uint64(len(m.ChangedRows))
	}
}

// snapshotTraffic merges server counts with transport stats (Phase 2) for attached clients.
func (s *Server) trafficReportLocked() protocol.MsgTrafficStats

func transportKind(tp transport.Transport) string // type switch: *ServerSocketConn "socket", *WebSocketServerConn "websocket", default "inproc"
```

- `broadcastPaneUpdates`: `result` gains `message transport.ServerMessage`; in the existing locked bookkeeping loop, call `s.recordSendLocked(r.tp, r.message, r.accepted)` for every result whose client is still `present` (before the pane-current checks, so failures to a live client count).
- `handleClientMsg`: `MsgAttach` sets `trafficLocked(tp).attached = time.Now()` if zero; `MsgPaneResync` increments `ResyncRequests`; `MsgTrafficRequest` builds `trafficReportLocked()` under `s.mu` and sends it after unlocking (`tp.SendServer(ctx, report)`); no broadcast.
- `removeTransportLocked`: if the entry is attached, `s.departed.Merge(entry counts)`, then delete it.
- `NewServer` sets `started: time.Now()`. `trafficLocked` makes `s.traffic` on first use (test servers are struct literals), and `trafficReportLocked` reports `UptimeMillis` 0 when `started` is zero.
- `runTrafficStatus(cfg, jsonOut, w)`: dial and handshake like `runStatus`, send `MsgTrafficRequest`, loop reading `ServerSendChan()` and skipping anything that is not `MsgTrafficStats` (this connection also receives broadcasts) until the 2 s timeout. JSON: `json.Encoder` with indent on the struct, like `status --json`. Table via `formatTraffic(w, stats)`:

```
CLIENT  TRANSPORT  SECS  UPD/S  FULL  ROW  SHIFT  ROWS  RESYNC  FAIL  PAYLOAD  WIRE  B/UPD
```
  `UPD/S = (full+row+shift)/secs`, `B/UPD = pane_payload_bytes/(full+row+shift)`, a `departed` row if nonzero, and a session line `uptime Xs | ` followed by `render n=… avg=… max=… | build n=… avg=… max=…` or `timing off (WIDEBOI_TRAFFIC_TIMING=1)`.

**Tests (write first):**

```go
// internal/server/traffic_test.go
func TestTrafficCountsKindsPerAttachedClient(t *testing.T) {
	s, _ := serverWithStatuses(t, map[int]protocol.PaneStatus{1: protocol.StatusIdle})
	g := &cellGrid{statusGrid: newStatusGrid(protocol.StatusIdle)}
	g.set("a")
	s.panes[1].grid = g
	s.panes[1].cols, s.panes[1].rows = 4, 4
	// serverWithStatuses builds a Server literal: maps NewServer would make are nil.
	// MsgAttach writes clientSizes; trafficLocked must make s.traffic lazily itself.
	s.clientSizes = make(map[transport.Transport]protocol.MsgResize)
	ctx := context.Background()
	client := s.transports[0].(*transport.InProcChannel)
	s.handleClientMsg(ctx, client, protocol.MsgAttach{Cols: 4, Rows: 4})
	drainPaneUpdates(client)
	g.set("b"); s.broadcastPaneUpdates(ctx, false)                 // row patch, 1 row
	s.handleClientMsg(ctx, client, protocol.MsgPaneResync{PaneID: 1}) // full
	drainPaneUpdates(client)

	observer := transport.NewInProcChannel(64) // never attaches
	s.mu.Lock(); s.transports = append(s.transports, observer); s.mu.Unlock()
	s.handleClientMsg(ctx, observer, protocol.MsgTrafficRequest{})
	stats := takeTraffic(t, observer) // drains until MsgTrafficStats
	// want exactly one client; RowPatches==1, ChangedRows==1, ResyncRequests==1,
	// FullUpdates >= 2 (attach broadcast + resync), SendFailures==0, Transport=="inproc"
}
func TestTrafficCountsShiftPatchesAndFailures(t *testing.T) // scroll a cellGrid-like grid of 6 rows by one → ShiftPatches==1; fill the transport queue → SendFailures==1
func TestTrafficDepartedClientsFoldIntoSession(t *testing.T) // attach, send, dropClient → Clients empty, Departed.FullUpdates>0
```

`formatTraffic` test: a fixed `MsgTrafficStats` produces the expected header plus one row (golden string). Also a check that `timing off` appears when `TimingEnabled` is false.

**Verification — automated:**
- [x] New tests fail before implementation (`go test ./internal/server -run Traffic -count=1`), for the missing message/fields — **ok: server, protocol and cmd tests all failed to build on undefined `MsgTrafficStats`/`MsgTrafficRequest`/`TimingStat`/`ClientTraffic`; after implementing, removing the attached filter, the departed fold, or the failure count each fails a server test**
- [x] `make proto-check` passes after `make proto` — **ok, exit 0 with changes staged**
- [x] `go test ./internal/protocol -run TestCodecRoundTripsEveryField -count=1` covers the new messages — **ok, subtests `MsgTrafficRequest` and `MsgTrafficStats` pass; dropping `WireBytes` from the encoder fails `MsgTrafficStats`**
- [x] `make quick` passes — **ok**
- [x] `make check` passes (the version bump touches attach and the web handshake) — **ok, first run: race, vitest 22 tests, smoke 37 passed, attach 25 passed**

**Verification — manual:**
- [ ] `WIDEBOI_SOCK=/tmp/t179.sock ./bin/wideboi server &`, attach, type, then `WIDEBOI_SOCK=/tmp/t179.sock ./bin/wideboi status --traffic` shows one client with nonzero ROW and FULL

---

## Phase 2: Transport bytes and opt-in timing

Payload, pane-payload and wire bytes per client, plus encode/render/build
timing when `WIDEBOI_TRAFFIC_TIMING=1`.

**Files:**
- Create: `internal/transport/stats.go` — `Stats`, `StatsReporter`, `sendStats`, `countingConn`, `CountingResponseWriter`.
- Modify: `internal/transport/socket.go` — `ServerSocketConn` wraps its conn, records per message.
- Modify: `internal/transport/websocket.go` — `NewWebSocketServerConn(conn, bufSize, wire WireCounter)`; records per message.
- Modify: `internal/server/server.go` — `ListenWebSocket` wraps `w`; `SetTrafficTiming(bool)`; timing around `UpdateMessage` and `BuildPanePatch`; `trafficReportLocked` merges `StatsReporter`; transports get `EnableTiming` when timing is on (in `trafficLocked`, on entry creation).
- Modify: `cmd/wideboi/main.go` `runServer` — `if os.Getenv("WIDEBOI_TRAFFIC_TIMING") == "1" { srv.SetTrafficTiming(true) }`.
- Modify: other `NewWebSocketServerConn` call sites (tests) to pass `nil`.
- Test: `internal/transport/stats_test.go`; extend `internal/server/traffic_test.go`.

**Key changes:**

```go
// internal/transport/stats.go
type Stats struct {
	Messages, PayloadBytes, PanePayloadBytes, WireBytes uint64
	Encode protocol.TimingStat
}
// StatsReporter is implemented by the server-side socket and WebSocket conns.
type StatsReporter interface {
	TransportStats() Stats
	EnableTiming()
}
type WireCounter interface{ WireBytes() uint64 }

type sendStats struct {
	mu     sync.Mutex
	s      Stats
	timing atomic.Bool
	wire   WireCounter // nil: WireBytes stays 0
}
func (st *sendStats) EnableTiming() { st.timing.Store(true) }
// record is called by the write pump after a successful write.
func (st *sendStats) record(msg ServerMessage, payload int, encode time.Duration) {
	st.mu.Lock(); defer st.mu.Unlock()
	st.s.Messages++
	st.s.PayloadBytes += uint64(payload)
	switch msg.(type) { case protocol.MsgPaneUpdate, protocol.MsgPanePatch: st.s.PanePayloadBytes += uint64(payload) }
	if st.timing.Load() { st.s.Encode.Add(encode) }
}
func (st *sendStats) TransportStats() Stats { /* copy under mu; WireBytes = wire.WireBytes() if non-nil */ }

type countingConn struct {
	net.Conn
	written atomic.Uint64
}
func (c *countingConn) Write(p []byte) (int, error) { n, err := c.Conn.Write(p); c.written.Add(uint64(n)); return n, err }
func (c *countingConn) WireBytes() uint64 { return c.written.Load() }

// CountingResponseWriter counts bytes written to the hijacked connection,
// which is where gorilla/websocket writes the upgrade response and frames.
type CountingResponseWriter struct {
	http.ResponseWriter
	conn *countingConn
}
func NewCountingResponseWriter(w http.ResponseWriter) *CountingResponseWriter
func (w *CountingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok { return nil, nil, errors.New("transport: response writer cannot hijack") }
	c, brw, err := h.Hijack()
	if err != nil { return nil, nil, err }
	w.conn = &countingConn{Conn: c}
	return w.conn, brw, nil
}
func (w *CountingResponseWriter) WireBytes() uint64 { if w.conn == nil { return 0 }; return w.conn.WireBytes() }
```

- `ServerSocketConn` embeds `sendStats`. `NewServerSocketConn` does `cc := &countingConn{Conn: conn}`, stores `conn: cc`, `sendStats{wire: cc}`. `writeLoop` measures `MarshalServer` duration only `if sc.timing.Load()` (it otherwise passes 0) and calls `sc.record(msg, len(payload), d)` after `writeFrame` succeeds.
- `WebSocketServerConn` embeds `sendStats{wire: wire}` and records the same way after `WriteMessage` succeeds.
- `ListenWebSocket`: `cw := transport.NewCountingResponseWriter(w)`; `conn, err := upgrader.Upgrade(cw, r, nil)`; `transport.NewWebSocketServerConn(conn, 256, cw)`.
- Server timing: `s.timing bool` (set before Run, read under `s.mu` when snapshotting targets). In `broadcastPaneUpdates` record `time.Since` around `UpdateMessage` and `BuildPanePatch` into locals when timing is on. Add them into `s.renderTiming` / `s.buildTiming` in the locked bookkeeping section.
- `trafficReportLocked`: for each attached entry, `if r, ok := tp.(transport.StatsReporter); ok { st := r.TransportStats(); copy Messages/PayloadBytes/PanePayloadBytes/WireBytes/Encode }`. Departed folding does the same before delete.

**Tests (write first):**

```go
// internal/transport/stats_test.go
func TestServerSocketConnCountsPayloadAndWireBytes(t *testing.T) {
	a, b := net.Pipe()
	sc := NewServerSocketConn(a, 4); sc.RunPumps(ctx)
	// read side: loop readFrame(b) in a goroutine, collecting payload lengths
	sc.SendServer(ctx, protocol.MsgPaneCreated{PaneID: 1})
	sc.SendServer(ctx, samplePaneUpdate(4, 2)) // helper building a MsgPaneUpdate
	// wait until the reader saw 2 frames, then:
	st := sc.TransportStats()
	// Messages==2, PayloadBytes==sum(lens), PanePayloadBytes==len(update payload),
	// WireBytes==PayloadBytes+8
}
func TestWebSocketConnCountsFrameHeaders(t *testing.T) {
	// httptest server whose handler upgrades via NewCountingResponseWriter and
	// NewWebSocketServerConn(conn, 4, cw) and hands the conn to the test over a
	// channel. Sample before := TransportStats().WireBytes (> 0: the 101
	// response). Send MsgPaneCreated (payload n < 126 bytes → 2-byte unmasked
	// server header); client reads it with gorilla. Then:
	// WireBytes - before == n + 2, and PayloadBytes == n.
}
func TestEncodeTimingOnlyWhenEnabled(t *testing.T) // Encode.Count==0 before EnableTiming, ==1 after one more send
```

Server: extend `TestTrafficCountsKindsPerAttachedClient` with `s.SetTrafficTiming(true)` in a second test. Assert `Render.Count>0` and `BuildPatch.Count>0`, and assert both are zero with timing off.

**Verification — automated:**
- [x] New tests fail first (missing types), then pass — **ok: transport tests failed to build on undefined `Stats`/`StatsReporter`/`NewCountingResponseWriter`/`EnableTiming` and the 3-arg `NewWebSocketServerConn`; server test on undefined `SetTrafficTiming`. After implementing, making encode timing unconditional, forcing `timing := true` in `broadcastPaneUpdates`, or adding one stray byte to the socket wire count each fails the intended test**
- [x] `make quick` passes — **ok**
- [x] `go test -race -count=1 ./internal/transport ./internal/server` passes 4× (new atomics and locks on the write path) — **ok, 4/4 green (transport ~1.2s, server ~10.9s each)**
- [x] `make check` passes — **ok, first run: vitest 22 tests, smoke 37 passed, attach 25 passed**
- [x] Server test proves transport stats reach report and departed — **ok: `TestTrafficReportsSocketTransportStats` (real `ServerSocketConn` over `net.Pipe`) passes; dropping `withTransportStats` from `forgetTrafficLocked` fails it (departed payload=0 wire=0), from `trafficReportLocked` fails it (messages=0 bytes=0, encode count=0), and removing `EnableTiming` from `trafficLocked` fails it (encode count=0). `go test -race -count=1 ./internal/server -run Traffic` 4/4 green; `make quick` ok**

**Verification — manual:**
- [ ] With `WIDEBOI_TRAFFIC_TIMING=1`, `status --traffic` shows WIRE ≈ PAYLOAD + 4×messages for a socket client and a render/build line

---

## Phase 3: pprof profiles for live runs

`WIDEBOI_CPUPROFILE` / `WIDEBOI_MEMPROFILE` write profiles for server and
client on normal exit. Infrastructure scaffolding: TDD applies to the path
helper only.

**Files:**
- Create: `cmd/wideboi/profile.go`
- Modify: `cmd/wideboi/main.go` — `defer startProfiles("server")()` in `runServer`, `defer startProfiles("client")()` in `runClient`; mention both env vars in help text.
- Test: `cmd/wideboi/profile_test.go`

**Key changes:**

```go
// profilePath turns an env prefix into a per-process file:
// "/tmp/wb" -> "/tmp/wb.server.cpu.12345.pprof". Several clients attach
// at once, so the pid keeps their profiles apart; the kind (cpu|mem)
// keeps a shared prefix from writing both profiles to one file.
func profilePath(prefix, component, kind string, pid int) string {
	return fmt.Sprintf("%s.%s.%s.%d.pprof", prefix, component, kind, pid)
}

// startProfiles starts a CPU profile if WIDEBOI_CPUPROFILE is set and
// returns a stop func that also writes a heap profile if
// WIDEBOI_MEMPROFILE is set. Errors are logged, never fatal: profiling
// must not stop a session from starting.
func startProfiles(component string) func()
```
Memory profile: `runtime.GC()` then `pprof.Lookup("allocs").WriteTo(f, 0)`, so it includes cumulative allocations (`alloc_space`) and not just live heap. Signal teardown via `hostterm` re-raise skips defers. The harness stops servers with `kill-session` and detaches clients, so both exit normally. Document this in the help text line.

**Tests (write first):** `TestProfilePath` table: prefix/component/kind/pid → expected path.

*Review fix:* the path originally had no kind, so one prefix shared by both env vars made the heap profile truncate the CPU profile. It is now `<prefix>.<component>.<cpu|mem>.<pid>.pprof`; the help text also says profiles land after the process exits (after `kill-session` returns) and a signal-ended run leaves an empty CPU file.

**Verification — automated:**
- [x] `go test ./cmd/wideboi -run TestProfilePath -count=1` passes — failed first with `undefined: profilePath`, then `ok github.com/lmorchard/wideboi/cmd/wideboi 0.255s`
- [x] `make quick` passes — exit 0

**Verification — manual:**
- [ ] `WIDEBOI_CPUPROFILE=/tmp/wb WIDEBOI_MEMPROFILE=/tmp/wbm` session, then detach/kill-session, gives `.server.<pid>.pprof` and `.client.<pid>.pprof` files (now `.server.cpu.<pid>.pprof` etc. after the review fix); `go tool pprof -top` shows `broadcastPaneUpdates` / `Client.Draw` frames — *agent-verified, left for Les:* (a) `wideboi server` on a private socket + `kill-session` wrote `p3.server.<pid>.pprof` and `p3m.server.<pid>.pprof`, both readable by `go tool pprof -top`; (b) plain `wideboi` in a Python pty (ptylib), `seq 1 300000` in the pane, `C-b d`, then `kill-session` wrote all four files (`cpu|mem.client|server.<pid>.pprof`): the spawned server inherits the env. Client CPU shows `(*Client).Draw`. Server CPU (3.64s samples) is dominated by vt parsing/scrollback; `broadcastPaneUpdates` fell under pprof's drop threshold and shows in the server allocs profile instead (`Pane.UpdateMessage`, ~16% of alloc_space under `-focus=broadcast`)

---

## Phase 4: Terminal-client apply + draw benchmark

**Files:**
- Create: `internal/client/paint_bench_test.go`

**Key changes:**

```go
// BenchmarkClientApplyAndDraw measures what one delivered message costs the
// terminal client end to end: ApplyPanePatch (inside HandleServerMsg), the
// full-mirror rewrite in applyPaneUpdateLocked, and the next Draw.
func BenchmarkClientApplyAndDraw(b *testing.B) {
	for _, size := range []struct{ cols, rows int }{{80, 24}, {160, 48}} {
		for _, kind := range []string{"full", "row", "shift"} {
			b.Run(fmt.Sprintf("%s_%dx%d", kind, size.cols, size.rows), func(b *testing.B) {
				cli := NewClient(transport.NewInProcChannel(1024), size.cols+2, size.rows+2, "C-b")
				cli.SetLayoutMode(protocol.LayoutScroll)
				cli.HandleServerMsg(protocol.MsgLayoutSnapshot{Columns: []protocol.ColumnData{{PaneID: 1, Width: size.cols, Height: size.rows}}})
				base := benchFrame(size.cols, size.rows, 0) // styled text rows, generation 1
				cli.HandleServerMsg(base)
				scr := newFakeHostScreen(size.cols+2, size.rows+2)
				cli.Draw(scr)
				b.ReportAllocs(); b.ResetTimer()
				for i := 0; i < b.N; i++ {
					next := benchFrame(size.cols, size.rows, i+1) // gen i+2; content per kind
					msg := benchMessage(kind, base, next)        // full: next; row: BuildPanePatch with one row changed; shift: frame scrolled by one
					cli.HandleServerMsg(msg)
					cli.Draw(scr)
					base = next
				}
			})
		}
	}
}
```
*As built:* messages are prebuilt in a 128-entry ring before `ResetTimer` and the loop restamps `BaseGeneration`/`Generation` so each patch chains from the last accepted one; after the loop the benchmark fails if the client sent anything (a `MsgPaneResync`) or its generation is off.

`benchFrame`/`benchMessage` build frames so `protocol.BuildPanePatch` returns the intended kind (row: change only row 0; shift: drop row 0, append a new last row). They fail the benchmark with `b.Fatalf` if the built message isn't that kind. Use the `ColumnData` field names from `internal/protocol/messages.go`. Opt-out of TDD: benchmark only, no behaviour change. The kind guard is the self-check.

**Verification — automated:**
- [x] `go test ./internal/client -run '^$' -bench BenchmarkClientApplyAndDraw -benchmem -benchtime=200x` runs all six cases; record numbers in notes.md — all six ran, `PASS`; numbers in notes.md (e.g. full/row/shift 80x24: 165/176/178 µs/op). Guards mutation-checked: a stale `BaseGeneration` fails on the resync check, a two-row "row" frame fails on the kind check
- [x] `make quick` passes — exit 0

**Verification — manual:**
- [ ] none

---

## Phase 5: `scripts/wssink` WebSocket client

A minimal real WebSocket client: attach, apply patches, resync on mismatch,
count, and estimate streaming deflate. Prints one JSON object on SIGTERM/SIGINT
or after `-duration`.

**Files:**
- Create: `scripts/wssink/main.go` (package main; same module, so it may import `internal/protocol` and `internal/protocol/wirepb`)
- Create: `scripts/wssink/sink.go` — the testable core
- Test: `scripts/wssink/sink_test.go`

**Key changes:**

```go
// sink.go
type Summary struct {
	Messages, PaneUpdates, RowPatches, ShiftPatches, Resyncs uint64
	PayloadBytes, DeflateBytes uint64
	DeflateNanos, ApplyNanos uint64
}
type sink struct {
	frames map[int]protocol.MsgPaneUpdate
	zw     *flate.Writer // one stream for the connection: context takeover
	zbuf   countingWriter
	sum    Summary
}
// handle applies one binary payload and returns any message to send back.
func (s *sink) handle(payload []byte) (reply any, err error) {
	s.sum.Messages++; s.sum.PayloadBytes += uint64(len(payload))
	t := time.Now(); s.zw.Write(payload); s.zw.Flush(); s.sum.DeflateNanos += uint64(time.Since(t))
	s.sum.DeflateBytes = s.zbuf.n
	msg, err := protocol.UnmarshalServer(payload)
	switch m := msg.(type) {
	case protocol.MsgPaneUpdate: s.frames[m.PaneID] = m; s.sum.PaneUpdates++
	case protocol.MsgPanePatch:
		next, ok := protocol.ApplyPanePatch(s.frames[m.PaneID], m)
		if !ok { delete(s.frames, m.PaneID); s.sum.Resyncs++; return protocol.MsgPaneResync{PaneID: m.PaneID}, nil }
		s.frames[m.PaneID] = next
		if m.ShiftRows != 0 { s.sum.ShiftPatches++ } else { s.sum.RowPatches++ }
	}
	return nil, err
}
```
`flate.NewWriter(&s.zbuf, flate.DefaultCompression)` with `Flush` per message, like permessage-deflate with context takeover (RFC 7692 strips the trailing 4 bytes; subtract 4 per message from `DeflateBytes` and say so in a comment).
*Review fix:* gorilla/websocket v1.5.3 only implements permessage-deflate *without* context takeover, at level 1 (`compression.go:18,44-53`), so a DefaultCompression shared-history estimate was an optimistic bound. The sink now reports `DeflateNoCtxBytes/Nanos` (one BestSpeed writer `Reset` per message, flush, minus 4 — what gorilla would send with `EnableCompression`) alongside `DeflateBytes/Nanos` (shared history, now also BestSpeed). `ApplyNanos` is renamed `DecodeApplyNanos` (it includes `UnmarshalServer`); `MsgPaneClosed` drops the pane's frame; the sink sends a normal close frame before exiting.

`main.go` flags: `-url` (ws://host:port/ws), `-token`, `-cols`, `-rows`, `-duration` (0 = until signal). Dial with `websocket.Dialer{Subprotocols: []string{fmt.Sprintf("wideboi.v%d", protocol.Version), "wideboi-token." + base64url(token)}}`, send `MsgAttach{Cols, Rows}` via `protocol.MarshalClient`, read loop into `sink.handle`, write replies, print `json.Marshal(Summary)` on exit.

**Tests (write first):** `TestSinkAppliesPatchesAndRequestsResync`: feed a marshalled full update, a valid row patch, then a patch with a stale base. Expect counts {PaneUpdates:1, RowPatches:1, Resyncs:1}, a `MsgPaneResync` reply, and `DeflateBytes > 0 && DeflateBytes < PayloadBytes` for repeated similar frames.

**Verification — automated:**
- [x] sink test fails first, then passes — with a stub `handle`, both tests failed on their assertions (`stale patch reply = <nil>`, `DeflateBytes=0 PayloadBytes=0`); pass after implementation. Deflate split into its own `TestSinkEstimatesStreamingDeflate` (20 repeated frames) so the first test's exact counts hold
- [x] `make quick` passes (vet covers `scripts/wssink`; seam-check unaffected) — `go vet ./...` and `go test ./...` both list `scripts/wssink`; seam-check only scans `./internal/...`

**Verification — manual:**
- [ ] Against a server with `--websocket 127.0.0.1:7999 --websocket-token t`, `go run ./scripts/wssink -url ws://127.0.0.1:7999/ws -token t -duration 3s` prints a summary with PaneUpdates ≥ 1, and `status --traffic` lists a `websocket` client
  - Agent-verified on a private socket (port 63560): 3s sink printed `{"Messages":10,"PaneUpdates":4,"RowPatches":4,...,"PayloadBytes":53937,"DeflateBytes":873,...}`, exit 0; while a second sink ran, `status --traffic` showed `2 websocket ... PAYLOAD 26221 WIRE 26398` and a `departed` row whose PAYLOAD 53937 matched the first sink exactly; SIGINT printed a summary and exited; bad token exits 1 with `HTTP 401`. Left unticked for Les.

---

## Phase 6: `scripts/traffic.py` and `make traffic`

Reproducible scenarios against a real server. Measurement run, not a gate.

**Files:**
- Create: `scripts/traffic.py`
- Modify: `Makefile` — `traffic` target (`.PHONY`), not in `check`
- Modify: `.gitignore` — `/tmp/`

**Key changes:**

- Per scenario, a fresh private run dir (`ptylib.private_run_dir`) holding a generated config:
  ```toml
  websocket = "127.0.0.1:<free port>"
  websocket_token = "traffic"
  [[startup]]
  width = <pane cols>
  ```
  The server starts as `./bin/wideboi -c <cfg> server` with `WIDEBOI_SOCK`, `SHELL=/bin/sh`, `PS1="$ "`, `WIDEBOI_TRAFFIC_TIMING=1`, and `WIDEBOI_CPUPROFILE`/`WIDEBOI_MEMPROFILE=<outdir>/<scenario>` when `--profile` is given. Use the env-building shape of `attachcheck.bin_env`. Terminal clients use `spawn_in_pty([BIN, "-c", cfg, "attach"], cols, rows, True, env)` plus a `Drainer`. WS clients: `subprocess.Popen(["go", "run", "./scripts/wssink", ...])`, built once up front with `go build -o <rundir>/wssink ./scripts/wssink` to keep compile time out of the measurement.
- Wait for the first `$ ` via `settle_output`, then **reset the baseline**: query `status --traffic --json` before the workload and subtract it from the one after, so attach snapshots are reported separately from steady state (both are kept in the JSON).
- Scenarios (`--only` filter, `--seconds` default 10):
  - `typing`: 80×24 pane, 1 socket + 1 WS client; write `"the quick brown fox jumps over the lazy dog "` one byte every 1/15 s for `--seconds`.
  - `scroll`: 80×24, 1 socket + 1 WS; type `seq 1 200000\r`, wait for output to settle (ceiling 60 s).
  - `tui`: 80×24, 1 socket + 1 WS; generate a 5000-line file in the run dir, `vim -u NONE -N -n <file>\r`, then `\x06` (Ctrl-F) ×30 at 5/s, `j` ×150 at 30/s, `:q!\r`. Skip with a message if `shutil.which("vim")` is None.
  - `large`: 160×48 pane, 2 socket + 2 WS clients; typing as above.
  - Client pty size = pane size + the chrome the client reserves. Read the actual pane size from `status --json` (`Columns[0].Width/Height`) and report it rather than assuming it.
- Teardown: SIGTERM the sinks (collect their JSON stdout), detach pty clients with the prefix key `C-b d`, then `wideboi kill-session`, then `force_cleanup` as a backstop.
- Output: a table per scenario (per client: transport, updates/s, full/row/shift/resync, payload B/update, wire B/update, wire overhead %, sink deflate B and % saved, render/build/encode avg µs) plus `<outdir>/traffic-<timestamp>.json` with raw before/after stats, sink summaries, machine (`platform.platform()`, `platform.machine()`), and durations. `--outdir` default `tmp/traffic`.
- Makefile:
  ```make
  # traffic is a measurement run (#179), not a gate: minutes, machine-dependent.
  traffic: build
  	python3 scripts/traffic.py $(TRAFFIC_ARGS)
  ```

TDD opt-out: harness/scaffolding. Its self-check is that each scenario asserts
it observed traffic (≥1 update per client after the baseline, and for `scroll`
≥1 shift patch). Otherwise it exits nonzero with the scenario name, so a
broken harness can't report an empty table as a result.

*As built (deviations):*
- **Five scenarios, not four.** `scroll` (the `seq` burst) produced zero shift patches: when a pane's generation moves while it is being rendered or sent, `broadcastPaneUpdates` drops that client's baseline, so sustained output always goes out as fulls (BuildPatch n=0). The burst stays as planned, asserting ≥1 update. The ≥1-shift self-check moved to a new `scroll-paced` (one line every 1/15 s for `--seconds`).
- **Server counters come over the sink's WebSocket after the baseline, not the CLI.** Any handshaked connection that closes, including never-attached `status`/`status --traffic`, force-resends every pane in full to every client (`dropClient` → `broadcastLayout` → `broadcastPaneUpdates(ctx, true)`). Each CLI query put one full per client into the window. wssink now answers SIGUSR1 (print its running Summary) and SIGUSR2 (send `MsgTrafficRequest` on its own connection and print the reply, which it leaves uncounted). The harness subtracts the baseline reply from the reporting sink's (highest websocket ClientID, sinks attach one at a time) Messages/Payload/Wire.
- **Quiesce is 4 s of no updates (sink-polled)** on both sides of the workload: a pane's working→idle flip after `term.DefaultIdleTimeout` (3 s) is also a forced full. Before the baseline this keeps the startup flip out; after, it keeps the workload's own flip in. UPD/S is over the workload time, not the tail.
- The first-prompt wait looks for `$`, not `$ `: the renderer need not draw the trailing blank, and waiting on it timed out 2 of ~10 starts.
- **Review fixes (run 2 in notes.md).** UPD/S is over the active workload, ending at the first client's last pty output (`Drainer.last_read_at`), not after the settle's quiet: `scroll` reads 30.8/s (the 33 ms frame cap), not 21.4. Per-kind bytes come from wssink (`Summary.FullBytes`/`RowPatchBytes`/`ShiftPatchBytes`, tested) as FULL, B/FULL, PATCH, B/PATCH, checked to sum to the server's PanePayloadBytes; blended B/UPD columns dropped. Self-checks now prove the workload ran: typing/large ≥ half the keystrokes as updates, `scroll` sees `seq-done-42` (printed only by `seq ... && echo seq-done-$((6*7))`), `tui` sees `DEEPZONEQX` from line 300+ (only reachable by paging in vim) plus ≥1 shift patch; each mutation-checked alone (vim renamed, no Ctrl-F, `seqq`, a third of keystrokes, shift bytes dropped from the sum). wssink writes have a 5 s deadline; `kill_all` closes pty fds; detach waits for control mode's `d detach` instead of a fixed 0.4 s.

**Verification — automated:**
- [x] `make traffic TRAFFIC_ARGS="--seconds 3"` completes all ~~four~~ five scenarios and writes the JSON — **ok, rc=0; server and sink payload bytes agree exactly in every scenario; tables in notes.md (first full run, `--profile`, default 10 s, also rc=0, 22 pprof files)**
- [x] Deliberately break the harness (e.g. point wssink at a wrong port) and confirm it exits nonzero naming the scenario, then restore — **ok: wrong port → `traffic: typing: FAILED: wssink exited early (1)`, same for `scroll-paced`, rc=1. Also a no-op typing workload → `FAILED: client 1 (socket) got no pane updates` (and client 2, wssink 0), rc=1; before the 4 s quiesce this case wrongly passed on the startup idle-flip full. Both restored.**
- [x] `make quick` still passes — **ok**; `go test -race ./scripts/wssink` ok (new `TestSinkForgetsClosedPanes`, `TestSinkSetsTrafficRepliesAside`, both mutation-checked)

**Verification — manual:**
- [ ] Les runs `make traffic` once and the table reads sensibly

---

## Phase 7: Browser `?stats=1`

**Files:**
- Create: `web/src/stats.ts` — `RenderStats`
- Modify: `web/src/client.ts` — optional `stats` hook records bytes per message
- Modify: `web/src/renderer.ts` — time `handlePaneUpdate`, `handlePanePatch`, `draw`
- Modify: `web/src/wideboi-app.ts` — create `RenderStats` when `new URLSearchParams(location.search).get('stats') === '1'`, pass it to client and renderer, start the periodic report
- Test: `web/src/stats.test.ts`

**Key changes:**

```ts
// stats.ts — counts and timings only; never message contents.
export class RenderStats {
  messages = 0; bytes = 0; fulls = 0; patches = 0; resyncs = 0; draws = 0;
  private applyMs: number[] = []; private drawMs: number[] = [];
  recordMessage(bytes: number) { this.messages++; this.bytes += bytes; }
  recordApply(kind: 'full' | 'patch' | 'resync', ms: number) { /* count + push */ }
  recordDraw(ms: number) { this.draws++; this.drawMs.push(ms); }
  summary(elapsedMs: number): StatsSummary // rates per second, avg/p50/p95/max for apply and draw, bytes/message
  reset() // called after each report so windows don't overlap
}
export function formatSummary(s: StatsSummary): string
export function statsEnabled(search: string): boolean // "?stats=1"
```
- The app reports every 5 s with `console.info('[wideboi stats]', formatSummary(...))` and sets the text of a fixed-position `<pre class="stats-overlay">` rendered by `wideboi-app` when enabled.
- The renderer takes an optional `stats?: RenderStats` in its constructor. It wraps bodies with `performance.now()`. A `handlePanePatch` that returns false records `'resync'`.

**Tests (write first):** `stats.test.ts`: `statsEnabled('?stats=1')` true, `''` false. `summary` over known samples gives the expected avg/p50/p95/max and per-second rates. `reset` clears the windows.

**Verification — automated:**
- [x] `cd web && npx vitest run src/stats.test.ts` fails first, then passes — **ok: failed first on the missing `./stats` module, then 8/11 failed against a stub; now 11/11 pass (statsEnabled, nearest-rank `percentile` incl. rank clamp and empty window, summary avg/p50/p95/max + per-second rates, no-NaN empty window, reset, formatSummary). Added `renderer.test.ts` case: full/patch/resync/draw recorded with stats and `performance.now` never called without; recording rejected patches as `'patch'` or timing `handlePaneUpdate` unconditionally each fails it**
- [x] `make web-test` and `cd web && npm run lint` pass — **ok, vitest 34 tests; tsc clean**
- [x] `make quick` passes (web/dist rebuild) — **ok, exit 0; rebuilt bundle contains `[wideboi stats]`**

**Verification — manual:**
- [ ] Open the startup link with `&stats=1` / `?stats=1`, run `seq 1 200000` and see the overlay update with draw and apply timings; no content appears in the console output — *not run in a browser (agent can't drive one). Verified only that a private `--websocket` server serves `/?stats=1` and its JS asset contains `wideboi stats`, `stats-overlay` and `stats: collecting`*

---

## Phase 8: Run the measurements and record the decision

Doc-only (TDD opt-out).

**Files:**
- Modify: `docs/partial-pane-updates.md` — new "Live measurements (#179)" section
- Modify: `docs/dev-sessions/2026-09-24-1354-measure-traffic/notes.md`

**Steps:**
1. `make traffic TRAFFIC_ARGS="--profile"` (default 10 s scenarios). Keep the JSON under `tmp/traffic/` (not committed) and copy the tables into the doc.
2. `go tool pprof -top -nodecount=25` on the server and client CPU and alloc profiles for `scroll` and `large`. Record the top frames by flat/cum share, grouped into render, patch build, encode, write, client apply, and client draw/present.
3. Run the Phase 4 benchmark and existing benchmarks and record them.
4. Browser: Les or the agent opens `?stats=1` against a `make traffic`-style server running `scroll` and `large`. Record draw avg/p95 and apply cost. If it wasn't run, say so explicitly.
5. Write the section:
   - Command lines to reproduce, machine, duration.
   - Table per scenario and transport, with explicit **payload** and **wire** columns and the deflate estimate labelled as an estimate.
   - Patch/full/shift/resync mix and updates/s.
   - CPU/alloc findings.
   - **Decision:** name the dominant remaining cost. For each of sparse cell spans, transport compression, and incremental client painting, give a one-line verdict: *material → follow-up issue* or *not material → recorded, no change*.
6. Draft follow-up issue text for each "material" item in notes.md. **Ask Les before filing** (outward-facing). After filing, link them from the doc.

**Verification — automated:**
- [x] `make check` passes on the final tree — **first run: 1 smoke flake (focus switch moves the cursor; passed 4/4 standalone, no client cursor code changed); next two runs exit 0: vitest 38, smoke 37/37, attach 25/25**

**Verification — manual:**
- [ ] Les reviews the numbers and the decision
- [x] Follow-up issues filed (or explicitly none) and linked — **#202, #203, #204, #205, #206, all in Backlog, linked from docs/partial-pane-updates.md**
