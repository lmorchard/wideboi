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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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
	case <-time.After(time.Second):
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
	case <-time.After(time.Second):
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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
	case <-time.After(time.Second):
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
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
	_ = healthyClient.SetReadDeadline(time.Now().Add(time.Second))
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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
	case <-time.After(time.Second):
		t.Fatal("oversized input did not close reader")
	}
}

// The WebSocket twin of TestServerWritePumpSkipsAnUnencodableMessage.
func TestWebSocketWritePumpSkipsAnUnencodableMessage(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var serverWSConn *transport.WebSocketServerConn
	connErr := make(chan error, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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
	_ = clientConn.SetReadDeadline(time.Now().Add(time.Second))
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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

	_ = clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := readServer(t, clientConn)
	gotUpdate, ok := got.(protocol.MsgPaneUpdate)
	if !ok {
		t.Fatalf("got %T, want MsgPaneUpdate", got)
	}
	if gotUpdate.PaneID != 1 || gotUpdate.Cols != 80 || gotUpdate.Rows != 24 {
		t.Fatalf("unexpected update fields: %+v", gotUpdate)
	}

	var wireWritten uint64
	for start := time.Now(); time.Since(start) < time.Second; time.Sleep(time.Millisecond) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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

	_ = clientConn.SetReadDeadline(time.Now().Add(time.Second))
	gotTraffic := readServer(t, clientConn)
	if _, ok := gotTraffic.(protocol.MsgTrafficStats); !ok {
		t.Fatalf("got %T, want MsgTrafficStats", gotTraffic)
	}

	var wireWritten uint64
	for start := time.Now(); time.Since(start) < time.Second; time.Sleep(time.Millisecond) {
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
