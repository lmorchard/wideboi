# Enable permessage-deflate for WebSocket clients Implementation Plan

**Goal:** Enable RFC 7692 `permessage-deflate` on the WebSocket upgrader in `Server.ListenWebSocket` so web browsers and WebSocket clients receive compressed protobuf frames, drastically cutting snapshot byte consumption.

**Approach:** Set `EnableCompression: true` on `websocket.Upgrader` in `Server.ListenWebSocket`. Enable `EnableCompression: true` on `scripts/wssink`'s dialer so the traffic measurement harness exercises compression. Measure wire bytes with `make traffic` and document findings in `docs/partial-pane-updates.md`.

**Tech stack:** Go, `github.com/gorilla/websocket`, WebSocket permessage-deflate (RFC 7692), Python (`scripts/traffic.py`), TypeScript (`web/`).

---

## Phase 1: Enable compression on WebSocket upgrader with unit tests

Enable `EnableCompression: true` on the `websocket.Upgrader` in `internal/server/server.go`. Add unit tests in `internal/server` and `internal/transport` verifying that clients requesting `permessage-deflate` negotiate compression, that frames are compressed on the wire, that `CountingResponseWriter` records the compressed wire bytes, and that clients not requesting compression continue to receive uncompressed frames.

**Files:**
- Modify: `internal/server/server.go` — add `EnableCompression: true` to `upgrader` in `ListenWebSocket`
- Create: `internal/server/websocket_compression_test.go` — test permessage-deflate negotiation and compressed snapshot delivery
- Modify: `internal/transport/websocket_test.go` — add test verifying compressed round-trip and wire counting

**Key changes:**
In `internal/server/server.go`:
```go
	upgrader := &websocket.Upgrader{
		ReadBufferSize:    4096,
		WriteBufferSize:   4096,
		Subprotocols:      []string{versionProtocol},
		CheckOrigin:       webSocketOriginAllowed,
		EnableCompression: true,
	}
```

In `internal/server/websocket_compression_test.go`:
```go
func TestListenWebSocketCompressionNegotiation(t *testing.T) {
	// Test that a dialer with EnableCompression: true receives
	// Sec-WebSocket-Extensions: permessage-deflate, and that
	// a large repetitive snapshot produces wire bytes significantly
	// smaller than raw protobuf payload.
}
```

**Verification — automated:**
- [x] `go test -v ./internal/server -run TestListenWebSocketCompression` passes — **2/2 subtests passed (0.20s)**
- [x] `go test -v ./internal/transport -run TestWebSocketCompression` passes — **TestWebSocketCompressionRoundTripAndWireBytes passed (0.22s), wire bytes < 1/4 of payload**
- [x] `make quick` passes — **vet, seam-check, Go test suite, and 37 web tests passed**

**Verification — manual:**
- [x] Inspect HTTP response headers during WebSocket upgrade to confirm `Sec-WebSocket-Extensions: permessage-deflate; client_no_context_takeover; server_no_context_takeover` — **confirmed via TestListenWebSocketCompressionNegotiation**

---

## Phase 2: Enable compression in wssink, run traffic harness, and record measurements

Enable `EnableCompression: true` on the dialer in `scripts/wssink/main.go` so `make traffic` scenarios negotiate compression. Run `make traffic` to verify that WebSocket wire bytes drop by roughly the estimated amount (84–98% on snapshot-heavy scenarios). Record the new wire bytes, overhead, and server CPU/timing measurements in `docs/partial-pane-updates.md`.

**Files:**
- Modify: `scripts/wssink/main.go` — set `EnableCompression: true` in `websocket.Dialer`
- Modify: `docs/partial-pane-updates.md` — record measured wire bytes, compression savings, and CPU timings

**Key changes:**
In `scripts/wssink/main.go`:
```go
	dialer := websocket.Dialer{
		Subprotocols:      protocols,
		HandshakeTimeout:  5 * time.Second,
		EnableCompression: true,
	}
```

**Verification — automated:**
- [x] `go test ./scripts/wssink/...` passes — **5/5 passed (0.17s)**
- [x] `make traffic TRAFFIC_ARGS="--only typing --seconds 3"` passes and shows compressed wire bytes — **passed, WebSocket wire 4,292 B (-91.0% vs payload)**
- [x] `make traffic TRAFFIC_ARGS="--seconds 3"` passes all scenarios — **all 5 scenarios passed, wire dropped 91.0–97.9%**
- [x] Full `make traffic` passes and logs results — **all 5 10s scenarios passed, recorded in docs/partial-pane-updates.md and tmp/traffic/traffic-20260924-163656.json**

**Verification — manual:**
- [x] Verify that WebSocket `WIRE B` in `make traffic` drops from payload size to roughly `DEFLATE AS-IS B` — **verified: typing 15,460 B vs 15,016 B est (-84.0%); scroll 16,915 B vs 16,665 B est (-97.9%); scroll-paced 15,597 B vs 15,277 B est (-91.8%); tui 39,543 B vs 38,863 B est (-93.6%); large 18,092 B vs 17,598 B est (-92.6%)**

---

## Phase 3: Web client verification and full project checks

Verify that the browser client and web tests continue to work flawlessly. Run full `make check` (unit tests, race detector, lint, formatting, seam checks, pty verify-exit, smoke tests, attach-check, web tests, browser acceptance tests).

**Files:**
- Update: `docs/dev-sessions/2026-09-24-1628-issue-203-websocket-deflate/notes.md`

**Verification — automated:**
- [x] `make fmt-check` passes — **clean**
- [x] `make lint` passes — **clean**
- [x] `make seam-check` passes — **clean**
- [x] `make test` passes — **all Go unit tests passed**
- [x] `make web-test` passes — **37/37 vitest tests passed**
- [x] `make web-accept` passes — **4/4 Playwright browser tests passed**
- [x] `make race` passes — **all packages passed with -race -count=1**
- [x] `make verify-exit` passes — **all 6 signal/size combos passed**
- [x] `make smoke` passes — **37/37 smoke tests + golden snapshot passed**
- [x] `make attach-check` passes — **25/25 multi-process tests passed**
- [x] `make check` passes — **entire parallel gate green**

**Verification — manual:**
- [x] Launch `bin/wideboi --web` and verify browser connection at `http://127.0.0.1:8080/` connects, types, and renders without error — **verified via Playwright browser acceptance test suite (web-accept)**
