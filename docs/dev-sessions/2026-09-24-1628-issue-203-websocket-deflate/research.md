# Research: Enable permessage-deflate for WebSocket clients

## 1. Upgrader and WebSocket Listener Configuration

- In `internal/server/server.go:1563-1570`:
  `Server.ListenWebSocket(ctx context.Context, mux *http.ServeMux, token string)` initializes `upgrader := &websocket.Upgrader{...}`:
  - `ReadBufferSize: 4096`, `WriteBufferSize: 4096`
  - `Subprotocols: []string{versionProtocol}` (e.g. `wideboi.v5`)
  - `CheckOrigin: webSocketOriginAllowed`
  - `EnableCompression` is currently omitted (defaults to `false`).

- In gorilla/websocket (`github.com/gorilla/websocket@v1.5.3/server.go:70-74, 163-173, 207-210`):
  - `Upgrader.EnableCompression bool` specifies whether the server should attempt to negotiate per-message compression (RFC 7692).
  - When `EnableCompression: true`, if the incoming request offers `Sec-WebSocket-Extensions: permessage-deflate`, gorilla sets `c.newCompressionWriter = compressNoContextTakeover` and `c.newDecompressionReader = decompressNoContextTakeover`.
  - In `conn.go:317-318`, `newConn` sets `enableWriteCompression: true` and `compressionLevel: defaultCompressionLevel` (`1`, i.e., `flate.BestSpeed`).
  - When `c.newCompressionWriter != nil && c.enableWriteCompression && isData(messageType)`, gorilla compresses the frame with `flate.BestSpeed` and sets the RSV1 bit.

## 2. Wire Counting and CountingResponseWriter

- In `internal/server/server.go:1594-1601`:
  - `cw := transport.NewCountingResponseWriter(w)`
  - `conn, err := upgrader.Upgrade(cw, r, nil)`
  - `sConn := transport.NewWebSocketServerConn(conn, 256, cw)`
- In `internal/transport/stats.go:103-131`:
  - `CountingResponseWriter.Hijack()` wraps the hijacked `net.Conn` in `countingConn`.
  - All bytes written by gorilla (including HTTP 101 response, WebSocket frame headers, and compressed payloads) pass through `countingConn.Write`, which updates `c.written`.
  - Therefore, `CountingResponseWriter` already measures compressed wire bytes as written to the TCP stream.

## 3. Scripts and Traffic Measurement Harness

- `scripts/wssink/main.go:53`:
  - `dialer := websocket.Dialer{Subprotocols: protocols, HandshakeTimeout: 5 * time.Second}`
  - Gorilla client dialer does not request `permessage-deflate` unless `EnableCompression: true`.
  - Setting `EnableCompression: true` on `wssink`'s dialer causes it to send `Sec-WebSocket-Extensions: permessage-deflate`, which gorilla server accepts and compresses.
  - In `scripts/wssink/sink.go:76-130`, `conn.ReadMessage()` in `readLoop` receives decompressed payloads transparently from gorilla client, preserving protobuf unmarshaling and payload estimation.
- In `scripts/traffic.py:501-515`:
  - `discount_reply(delta, reply_bytes)` subtracts the baseline `MsgTrafficRequest`'s reply payload and wire bytes.
  - In `internal/transport/websocket.go:80-99`: `writeLoop` writes outgoing binary messages.
  - If `MsgTrafficStats` is sent uncompressed via `wsConn.conn.EnableWriteCompression(false)` (or if `discount_reply` handles it), wire discount matches exact frame size.

## 4. Browser Client Behavior

- In standard browsers (Chromium, Firefox, Safari), `new WebSocket(url, protocols)` automatically includes `Sec-WebSocket-Extensions: permessage-deflate; client_max_window_bits` in the HTTP handshake.
- Browsers decompress frames transparently at the transport layer before triggering `onmessage`.
- `web/src/client.ts` expects raw binary `ArrayBuffer` in `event.data`, which remains unchanged.
- Browser tests in `web/tests/lifecycle.spec.js` and vitest mocks mock `WebSocket` or run against test servers without requiring manual deflate intervention.
