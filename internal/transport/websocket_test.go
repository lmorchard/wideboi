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
	snapMsg := protocol.MsgLayoutSnapshot{FocusPaneID: 42}
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
	if !strings.Contains(string(res.Payload), `"FocusPaneID":42`) {
		t.Errorf("got payload %q, want it to contain FocusPaneID:42", string(res.Payload))
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
