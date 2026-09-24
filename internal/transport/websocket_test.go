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
		serverWSConn = transport.NewWebSocketServerConn(c, 16)
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

	// 1. Test Client -> Server (JSON Envelope)
	env := transport.WSEnvelope{
		Type:    "MsgAttach",
		Payload: []byte(`{"Cols":80,"Rows":24}`),
	}
	if err := clientConn.WriteJSON(env); err != nil {
		t.Fatalf("client write JSON failed: %v", err)
	}

	select {
	case msg := <-serverWSConn.ClientSendChan():
		got, ok := msg.(protocol.MsgAttach)
		if !ok || got.Cols != 80 || got.Rows != 24 {
			t.Fatalf("got server msg %+v, want MsgAttach {80, 24}", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for client message on server")
	}

	// 2. Test Server -> Client (Go struct -> JSON Envelope)
	snapMsg := protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{{PaneID: 1, Width: 80, Height: 24}},
	}
	if !serverWSConn.SendServer(ctx, snapMsg) {
		t.Fatal("server SendServer failed")
	}

	var res transport.WSEnvelope
	if err := clientConn.ReadJSON(&res); err != nil {
		t.Fatalf("client read JSON failed: %v", err)
	}
	if res.Type != "MsgLayoutSnapshot" {
		t.Errorf("got type %q, want MsgLayoutSnapshot", res.Type)
	}

	if err := clientConn.WriteJSON(transport.WSEnvelope{Type: "MsgPaneResync", Payload: []byte(`{"PaneID":7}`)}); err != nil {
		t.Fatalf("client resync write failed: %v", err)
	}
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
	if err := clientConn.ReadJSON(&res); err != nil {
		t.Fatalf("client patch read failed: %v", err)
	}
	if res.Type != "MsgPanePatch" || !strings.Contains(string(res.Payload), `"BaseGeneration":1`) {
		t.Fatalf("patch envelope = %#v", res)
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
		serverWSConn = transport.NewWebSocketServerConn(c, 1)
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
			conns <- transport.NewWebSocketServerConn(c, 1)
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
	var got transport.WSEnvelope
	if err := healthyClient.ReadJSON(&got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "MsgLayoutSnapshot" {
		t.Fatalf("got %q", got.Type)
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
			serverConn := transport.NewWebSocketServerConn(c, 1)
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
