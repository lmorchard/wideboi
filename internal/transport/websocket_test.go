package transport_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// wsAssertionTimeout is a generous ceiling for test assertions (channel closure,
// message receipt, read deadlines) to prevent false failures under heavy CI load (#368).
// Successful tests finish promptly upon delivery without waiting out the full duration.
const wsAssertionTimeout = 5 * time.Second

// wsPumpTimeout is the safety ceiling for test pump contexts. It is kept strictly
// longer than wsAssertionTimeout so that an assertion failure fires before the context
// expires and closes the transport pumps underneath the test.
const wsPumpTimeout = 15 * time.Second

// sendClient writes msg the way the browser does: one binary frame
// holding a protobuf ClientMessage.
func sendClient(t *testing.T, conn *websocket.Conn, msg any) {
	t.Helper()
	payload, err := protocol.MarshalClient(msg)
	if err != nil {
		t.Fatalf("encode %T: %v", msg, err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("write %T: %v", msg, err)
	}
}

// readServer reads one frame, requires it to be binary, and decodes it.
func readServer(t *testing.T, conn *websocket.Conn) any {
	t.Helper()
	kind, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if kind != websocket.BinaryMessage {
		t.Fatalf("frame kind %d, want binary", kind)
	}
	msg, err := protocol.UnmarshalServer(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return msg
}

func TestWebSocketRoundTrip(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var serverWSConn *transport.WebSocketServerConn
	connErr := make(chan error, 1)

	ctx, cancel := context.WithTimeout(context.Background(), wsPumpTimeout)
	defer cancel()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			connErr <- err
			return
		}
		serverWSConn = transport.NewWebSocketServerConn(c, 16, nil)
		serverWSConn.RunPumps(ctx)
		connErr <- nil
	}))
	defer s.Close()

	u := "ws" + strings.TrimPrefix(s.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer clientConn.Close()

	if err := <-connErr; err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}

	// 1. Client -> Server
	sendClient(t, clientConn, protocol.MsgAttach{Cols: 80, Rows: 24})

	select {
	case msg := <-serverWSConn.ClientSendChan():
		got, ok := msg.(protocol.MsgAttach)
		if !ok || got.Cols != 80 || got.Rows != 24 {
			t.Fatalf("got server msg %+v, want MsgAttach {80, 24}", msg)
		}
	case <-time.After(wsAssertionTimeout):
		t.Fatal("timeout waiting for client message on server")
	}

	// 2. Server -> Client
	snapMsg := protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{{PaneID: 1, Width: 80, Height: 24}},
	}
	if !serverWSConn.SendServer(ctx, snapMsg) {
		t.Fatal("server SendServer failed")
	}

	if got, ok := readServer(t, clientConn).(protocol.MsgLayoutSnapshot); !ok || len(got.Columns) != 1 || got.Columns[0].PaneID != 1 {
		t.Errorf("got %#v, want the one-column snapshot", got)
	}

	sendClient(t, clientConn, protocol.MsgPaneResync{PaneID: 7})
	select {
	case msg := <-serverWSConn.ClientSendChan():
		if msg != (protocol.MsgPaneResync{PaneID: 7}) {
			t.Fatalf("resync decoded as %#v", msg)
		}
	case <-time.After(wsAssertionTimeout):
		t.Fatal("timeout waiting for resync request")
	}
	if !serverWSConn.SendServer(ctx, protocol.MsgPanePatch{PaneID: 7, Cols: 2, Rows: 4, BaseGeneration: 1, Generation: 2}) {
		t.Fatal("server patch send failed")
	}
	if got, ok := readServer(t, clientConn).(protocol.MsgPanePatch); !ok || got.BaseGeneration != 1 || got.Generation != 2 {
		t.Fatalf("patch decoded as %#v", got)
	}

	serverWSConn.Close()
}

func TestWebSocketCloseBehavior(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var serverWSConn *transport.WebSocketServerConn
	connErr := make(chan error, 1)

	ctx, cancel := context.WithTimeout(context.Background(), wsPumpTimeout)
	defer cancel()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			connErr <- err
			return
		}
		serverWSConn = transport.NewWebSocketServerConn(c, 1, nil)
		serverWSConn.RunPumps(ctx)
		connErr <- nil
	}))
	defer s.Close()

	u := "ws" + strings.TrimPrefix(s.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	if err := <-connErr; err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}

	// Close client connection immediately
	clientConn.Close()

	// Server should notice the close and close its ClientSend channel
	select {
	case _, ok := <-serverWSConn.ClientSendChan():
		if ok {
			t.Error("expected ClientSendChan to be closed on client disconnect")
		}
	case <-time.After(wsAssertionTimeout):
		t.Fatal("timeout waiting for server to notice disconnect")
	}
}

func TestWebSocketSlowPeerDoesNotBlockAnotherPeer(t *testing.T) {
	upgrader := websocket.Upgrader{}
	conns := make(chan *transport.WebSocketServerConn, 2)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			conns <- transport.NewWebSocketServerConn(c, 1, nil)
		}
	}))
	defer s.Close()
	u := "ws" + strings.TrimPrefix(s.URL, "http")
	slowClient, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer slowClient.Close()
	slow := <-conns
	healthyClient, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer healthyClient.Close()
	healthy := <-conns
	ctx, cancel := context.WithTimeout(context.Background(), wsPumpTimeout)
	defer cancel()
	healthy.RunPumps(ctx)
	defer healthy.Close()
	msg := protocol.MsgLayoutSnapshot{Columns: []protocol.ColumnData{{PaneID: 42, Width: 40, Height: 20}}}
	if !slow.SendServer(ctx, msg) {
		t.Fatal("first queued send failed")
	}
	if slow.SendServer(ctx, msg) {
		t.Fatal("full queue should disconnect slow peer")
	}
	if !healthy.SendServer(ctx, msg) {
		t.Fatal("healthy peer was blocked")
	}
	_ = healthyClient.SetReadDeadline(time.Now().Add(wsAssertionTimeout))
	if got, ok := readServer(t, healthyClient).(protocol.MsgLayoutSnapshot); !ok {
		t.Fatalf("got %#v", got)
	}
}

// A frame the server cannot decode -- text, or binary that is not a
// ClientMessage -- is logged and skipped. The connection stays up and the
// next valid message still arrives.
func TestWebSocketSkipsUndecodableFrames(t *testing.T) {
	upgrader := websocket.Upgrader{}
	conns := make(chan *transport.WebSocketServerConn, 1)
	ctx, cancel := context.WithTimeout(context.Background(), wsPumpTimeout)
	defer cancel()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			serverConn := transport.NewWebSocketServerConn(c, 4, nil)
			serverConn.RunPumps(ctx)
			conns <- serverConn
		}
	}))
	defer s.Close()
	u := "ws" + strings.TrimPrefix(s.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	serverConn := <-conns
	defer serverConn.Close()

	if err := client.WriteMessage(websocket.TextMessage, []byte(`{"t":"MsgAttach","p":{"Cols":1,"Rows":1}}`)); err != nil {
		t.Fatal(err)
	}
	if err := client.WriteMessage(websocket.BinaryMessage, []byte{0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	sendClient(t, client, protocol.MsgAttach{Cols: 80, Rows: 24})

	select {
	case msg, ok := <-serverConn.ClientSendChan():
		if !ok {
			t.Fatal("connection closed on an undecodable frame")
		}
		if msg != (protocol.MsgAttach{Cols: 80, Rows: 24}) {
			t.Fatalf("first delivered message %#v, want the valid attach", msg)
		}
	case <-ctx.Done():
		t.Fatal("valid message never arrived")
	}
	if err := serverConn.Err(); err != nil {
		t.Fatalf("skipped frames recorded a connection error: %v", err)
	}
}

func TestWebSocketRejectsOversizedInput(t *testing.T) {
	upgrader := websocket.Upgrader{}
	conns := make(chan *transport.WebSocketServerConn, 1)
	ctx, cancel := context.WithTimeout(context.Background(), wsPumpTimeout)
	defer cancel()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			serverConn := transport.NewWebSocketServerConn(c, 1, nil)
			serverConn.RunPumps(ctx)
			conns <- serverConn
		}
	}))
	defer s.Close()
	u := "ws" + strings.TrimPrefix(s.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	serverConn := <-conns
	// The server may close the connection before the client finishes writing.
	_ = client.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", (1<<20)+1)))
	select {
	case _, ok := <-serverConn.ClientSendChan():
		if ok {
			t.Fatal("oversized input reached server")
		}
	case <-time.After(wsAssertionTimeout):
		t.Fatal("oversized input did not close reader")
	}
}

func TestWebSocketRejectsDecompressedOversizedInput(t *testing.T) {
	upgrader := websocket.Upgrader{
		EnableCompression: true,
	}
	conns := make(chan *transport.WebSocketServerConn, 1)
	ctx, cancel := context.WithTimeout(context.Background(), wsPumpTimeout)
	defer cancel()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			serverConn := transport.NewWebSocketServerConn(c, 1, nil)
			serverConn.RunPumps(ctx)
			conns <- serverConn
		}
	}))
	defer s.Close()

	u := "ws" + strings.TrimPrefix(s.URL, "http")
	dialer := websocket.Dialer{
		EnableCompression: true,
	}
	// Verify permessage-deflate was negotiated
	ws, resp, err := dialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if !strings.Contains(resp.Header.Get("Sec-Websocket-Extensions"), "permessage-deflate") {
		t.Fatalf("expected permessage-deflate extension negotiated, got %q", resp.Header.Get("Sec-Websocket-Extensions"))
	}
	serverConn := <-conns

	// Create a 2 MiB payload of repeated data. Highly compressible (<10KB on wire).
	hugeMsg := protocol.MsgInput{Data: []byte(strings.Repeat("A", 2<<20))}
	payload, err := protocol.MarshalClient(hugeMsg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	ws.EnableWriteCompression(true)
	_ = ws.WriteMessage(websocket.BinaryMessage, payload)

	select {
	case msg, ok := <-serverConn.ClientSendChan():
		if ok {
			t.Fatalf("decompressed oversized input reached server: %T", msg)
		}
	case <-time.After(wsAssertionTimeout):
		t.Fatal("decompressed oversized input did not close reader")
	}

	// Verify client observes CloseMessageTooBig
	_ = ws.SetReadDeadline(time.Now().Add(wsAssertionTimeout))
	_, _, err = ws.ReadMessage()
	if err == nil {
		t.Fatal("expected read error on client after oversized input, got nil")
	}
	closeErr, ok := err.(*websocket.CloseError)
	if !ok {
		t.Fatalf("expected *websocket.CloseError, got %T (%v)", err, err)
	}
	if closeErr.Code != websocket.CloseMessageTooBig {
		t.Fatalf("got close code %d, want %d (CloseMessageTooBig)", closeErr.Code, websocket.CloseMessageTooBig)
	}
}

func makeMarshaledMsgOfSize(t *testing.T, targetLen int) []byte {
	t.Helper()
	dataLen := targetLen - 10
	for {
		msg := protocol.MsgInput{Data: make([]byte, dataLen)}
		payload, err := protocol.MarshalClient(msg)
		if err != nil {
			t.Fatal(err)
		}
		if len(payload) == targetLen {
			return payload
		}
		if len(payload) < targetLen {
			dataLen++
		} else {
			dataLen--
		}
	}
}

func TestWebSocketBoundaryPayloads(t *testing.T) {
	for _, compress := range []bool{false, true} {
		name := "uncompressed"
		if compress {
			name = "compressed"
		}
		t.Run(name, func(t *testing.T) {
			upgrader := websocket.Upgrader{
				EnableCompression: compress,
			}
			conns := make(chan *transport.WebSocketServerConn, 1)
			ctx, cancel := context.WithTimeout(context.Background(), wsPumpTimeout)
			defer cancel()

			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := upgrader.Upgrade(w, r, nil)
				if err == nil {
					serverConn := transport.NewWebSocketServerConn(c, 16, nil)
					serverConn.RunPumps(ctx)
					conns <- serverConn
				}
			}))
			defer s.Close()

			u := "ws" + strings.TrimPrefix(s.URL, "http")
			dialer := websocket.Dialer{
				EnableCompression: compress,
			}
			client, resp, err := dialer.Dial(u, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if compress && !strings.Contains(resp.Header.Get("Sec-Websocket-Extensions"), "permessage-deflate") {
				t.Fatalf("expected permessage-deflate negotiated, got %q", resp.Header.Get("Sec-Websocket-Extensions"))
			}
			serverConn := <-conns

			// 1. Valid small message arrives
			smallMsg := protocol.MsgInput{Data: []byte("valid-message")}
			sendClient(t, client, smallMsg)
			select {
			case msg, ok := <-serverConn.ClientSendChan():
				if !ok {
					t.Fatal("channel closed prematurely")
				}
				got, ok := msg.(protocol.MsgInput)
				if !ok || string(got.Data) != "valid-message" {
					t.Fatalf("unexpected message: %#v", msg)
				}
			case <-time.After(wsAssertionTimeout):
				t.Fatal("timed out waiting for small message")
			}

			// 2. Exact 1 MiB boundary payload is delivered
			exactPayload := makeMarshaledMsgOfSize(t, 1<<20)
			if len(exactPayload) != 1<<20 {
				t.Fatalf("exact payload len %d != %d", len(exactPayload), 1<<20)
			}
			if compress {
				client.EnableWriteCompression(true)
			}
			if err := client.WriteMessage(websocket.BinaryMessage, exactPayload); err != nil {
				t.Fatalf("write exact payload: %v", err)
			}
			select {
			case msg, ok := <-serverConn.ClientSendChan():
				if !ok {
					t.Fatal("channel closed on exact 1 MiB boundary")
				}
				if _, ok := msg.(protocol.MsgInput); !ok {
					t.Fatalf("unexpected message type on boundary: %T", msg)
				}
			case <-time.After(wsAssertionTimeout):
				t.Fatal("timed out waiting for 1 MiB boundary message")
			}

			// 3. Payload of (1 MiB + 1) is rejected with CloseMessageTooBig
			overPayload := makeMarshaledMsgOfSize(t, (1<<20)+1)
			if len(overPayload) != (1<<20)+1 {
				t.Fatalf("over payload len %d != %d", len(overPayload), (1<<20)+1)
			}
			_ = client.WriteMessage(websocket.BinaryMessage, overPayload)
			select {
			case msg, ok := <-serverConn.ClientSendChan():
				if ok {
					t.Fatalf("oversized 1 MiB + 1 payload reached server: %T", msg)
				}
			case <-time.After(wsAssertionTimeout):
				t.Fatal("oversized 1 MiB + 1 payload did not close reader")
			}

			_ = client.SetReadDeadline(time.Now().Add(wsAssertionTimeout))
			_, _, err = client.ReadMessage()
			if err == nil {
				t.Fatal("expected read error on client after oversized input, got nil")
			}
			closeErr, ok := err.(*websocket.CloseError)
			if !ok {
				t.Fatalf("expected *websocket.CloseError, got %T (%v)", err, err)
			}
			if closeErr.Code != websocket.CloseMessageTooBig {
				t.Fatalf("got close code %d, want %d (CloseMessageTooBig)", closeErr.Code, websocket.CloseMessageTooBig)
			}
		})
	}
}

func TestWebSocketFragmentedMessage(t *testing.T) {
	upgrader := websocket.Upgrader{}
	conns := make(chan *transport.WebSocketServerConn, 1)
	ctx, cancel := context.WithTimeout(context.Background(), wsPumpTimeout)
	defer cancel()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			serverConn := transport.NewWebSocketServerConn(c, 1, nil)
			serverConn.RunPumps(ctx)
			conns <- serverConn
		}
	}))
	defer s.Close()

	u := "ws" + strings.TrimPrefix(s.URL, "http")
	// Using a small WriteBufferSize forces Gorilla to split a message across multiple frames.
	dialer := websocket.Dialer{
		WriteBufferSize: 512,
	}
	client, _, err := dialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	serverConn := <-conns

	// 2048 bytes of valid client message
	chunk := strings.Repeat("hello world wideboi ", 100)
	msg := protocol.MsgInput{Data: []byte(chunk)}
	payload, err := protocol.MarshalClient(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(payload) <= 512 {
		t.Fatalf("payload size %d not large enough to fragment with 512-byte buffer", len(payload))
	}

	// Writing payload via NextWriter with small write buffer sends multiple frames
	w, err := client.NextWriter(websocket.BinaryMessage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case received, ok := <-serverConn.ClientSendChan():
		if !ok {
			t.Fatal("channel closed unexpectedly")
		}
		got, ok := received.(protocol.MsgInput)
		if !ok {
			t.Fatalf("got %T, want MsgInput", received)
		}
		if string(got.Data) != chunk {
			t.Fatalf("fragmented payload content mismatch: len got %d, want %d", len(got.Data), len(chunk))
		}
	case <-time.After(wsAssertionTimeout):
		t.Fatal("timed out waiting for fragmented message")
	}
}

func TestWebSocketFragmentedOversizedInput(t *testing.T) {
	upgrader := websocket.Upgrader{}
	conns := make(chan *transport.WebSocketServerConn, 1)
	ctx, cancel := context.WithTimeout(context.Background(), wsPumpTimeout)
	defer cancel()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			serverConn := transport.NewWebSocketServerConn(c, 1, nil)
			serverConn.RunPumps(ctx)
			conns <- serverConn
		}
	}))
	defer s.Close()

	u := "ws" + strings.TrimPrefix(s.URL, "http")
	dialer := websocket.Dialer{
		WriteBufferSize: 1024,
	}
	client, _, err := dialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	serverConn := <-conns

	// Stream >1 MiB across multiple fragments
	w, err := client.NextWriter(websocket.BinaryMessage)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 64*1024)
	total := 0
	for total < (1<<20)+4096 {
		n, err := w.Write(chunk)
		if err != nil {
			break
		}
		total += n
	}
	_ = w.Close()

	select {
	case msg, ok := <-serverConn.ClientSendChan():
		if ok {
			t.Fatalf("oversized fragmented message reached server: %T", msg)
		}
	case <-time.After(wsAssertionTimeout):
		t.Fatal("oversized fragmented input did not close reader")
	}
}

// The WebSocket twin of TestServerWritePumpSkipsAnUnencodableMessage.
func TestWebSocketWritePumpSkipsAnUnencodableMessage(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var serverWSConn *transport.WebSocketServerConn
	connErr := make(chan error, 1)

	ctx, cancel := context.WithTimeout(context.Background(), wsPumpTimeout)
	defer cancel()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			connErr <- err
			return
		}
		serverWSConn = transport.NewWebSocketServerConn(c, 16, nil)
		serverWSConn.RunPumps(ctx)
		connErr <- nil
	}))
	defer s.Close()

	u := "ws" + strings.TrimPrefix(s.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer clientConn.Close()
	if err := <-connErr; err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	defer serverWSConn.Close()

	serverWSConn.SendServer(ctx, struct{}{})
	serverWSConn.SendServer(ctx, protocol.MsgPaneClosed{PaneID: 9})
	_ = clientConn.SetReadDeadline(time.Now().Add(wsAssertionTimeout))
	if got := readServer(t, clientConn); got != (protocol.MsgPaneClosed{PaneID: 9}) {
		t.Fatalf("got %#v, want the MsgPaneClosed sent after the unencodable message", got)
	}
}

func TestWebSocketCompressionRoundTripAndWireBytes(t *testing.T) {
	upgrader := websocket.Upgrader{
		EnableCompression: true,
	}
	var serverWSConn *transport.WebSocketServerConn
	var cw *transport.CountingResponseWriter
	connErr := make(chan error, 1)

	ctx, cancel := context.WithTimeout(context.Background(), wsPumpTimeout)
	defer cancel()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cw = transport.NewCountingResponseWriter(w)
		c, err := upgrader.Upgrade(cw, r, nil)
		if err != nil {
			connErr <- err
			return
		}
		serverWSConn = transport.NewWebSocketServerConn(c, 16, cw)
		serverWSConn.RunPumps(ctx)
		connErr <- nil
	}))
	defer s.Close()

	u := "ws" + strings.TrimPrefix(s.URL, "http")
	dialer := websocket.Dialer{
		EnableCompression: true,
	}
	clientConn, _, err := dialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer clientConn.Close()
	if err := <-connErr; err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	defer serverWSConn.Close()

	// Build a typical 80x24 blank pane snapshot
	lines := make([]protocol.LineData, 24)
	for i := range lines {
		lines[i] = make(protocol.LineData, 80)
		for j := range lines[i] {
			lines[i][j] = protocol.CellData{Content: " ", Width: 1}
		}
	}
	update := protocol.MsgPaneUpdate{
		PaneID:     1,
		Generation: 1,
		Cols:       80,
		Rows:       24,
		Lines:      lines,
	}

	payload, err := protocol.MarshalServer(update)
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}
	rawPayloadSize := len(payload)
	if rawPayloadSize < 10000 {
		t.Fatalf("expected raw payload size > 10KB, got %d", rawPayloadSize)
	}

	wireBefore := cw.WireBytes()
	serverWSConn.SendServer(ctx, update)

	_ = clientConn.SetReadDeadline(time.Now().Add(wsAssertionTimeout))
	got := readServer(t, clientConn)
	gotUpdate, ok := got.(protocol.MsgPaneUpdate)
	if !ok {
		t.Fatalf("got %T, want MsgPaneUpdate", got)
	}
	if gotUpdate.PaneID != 1 || gotUpdate.Cols != 80 || gotUpdate.Rows != 24 {
		t.Fatalf("unexpected update fields: %+v", gotUpdate)
	}

	var wireWritten uint64
	for start := time.Now(); time.Since(start) < wsAssertionTimeout; time.Sleep(time.Millisecond) {
		if n := cw.WireBytes() - wireBefore; n > 0 {
			wireWritten = n
			break
		}
	}

	// Wire bytes for the compressed frame should be a fraction of the raw payload
	if wireWritten >= uint64(rawPayloadSize)/4 {
		t.Fatalf("wire bytes %d not compressed significantly compared to raw payload %d", wireWritten, rawPayloadSize)
	}
}

func TestWebSocketTrafficStatsSentUncompressed(t *testing.T) {
	upgrader := websocket.Upgrader{
		EnableCompression: true,
	}
	var serverWSConn *transport.WebSocketServerConn
	var cw *transport.CountingResponseWriter
	connErr := make(chan error, 1)

	ctx, cancel := context.WithTimeout(context.Background(), wsPumpTimeout)
	defer cancel()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cw = transport.NewCountingResponseWriter(w)
		c, err := upgrader.Upgrade(cw, r, nil)
		if err != nil {
			connErr <- err
			return
		}
		serverWSConn = transport.NewWebSocketServerConn(c, 16, cw)
		serverWSConn.RunPumps(ctx)
		connErr <- nil
	}))
	defer s.Close()

	u := "ws" + strings.TrimPrefix(s.URL, "http")
	dialer := websocket.Dialer{
		EnableCompression: true,
	}
	clientConn, _, err := dialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer clientConn.Close()
	if err := <-connErr; err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	defer serverWSConn.Close()

	trafficStats := protocol.MsgTrafficStats{
		Clients: []protocol.ClientTraffic{{ClientID: 1, Messages: 10}},
	}
	trafficPayload, err := protocol.MarshalServer(trafficStats)
	if err != nil {
		t.Fatalf("marshal traffic stats: %v", err)
	}

	wireBefore := cw.WireBytes()
	serverWSConn.SendServer(ctx, trafficStats)

	_ = clientConn.SetReadDeadline(time.Now().Add(wsAssertionTimeout))
	gotTraffic := readServer(t, clientConn)
	if _, ok := gotTraffic.(protocol.MsgTrafficStats); !ok {
		t.Fatalf("got %T, want MsgTrafficStats", gotTraffic)
	}

	var wireWritten uint64
	for start := time.Now(); time.Since(start) < wsAssertionTimeout; time.Sleep(time.Millisecond) {
		if n := cw.WireBytes() - wireBefore; n > 0 {
			wireWritten = n
			break
		}
	}

	// Uncompressed frame header for small payload is 2 bytes (unmasked from server) or 4 bytes
	headerLen := uint64(2)
	if len(trafficPayload) >= 126 {
		headerLen = 4
	}
	expectedWire := uint64(len(trafficPayload)) + headerLen
	if wireWritten != expectedWire {
		t.Fatalf("traffic wire bytes = %d, want exact uncompressed wire bytes = %d (payload %d + header %d)",
			wireWritten, expectedWire, len(trafficPayload), headerLen)
	}
}
