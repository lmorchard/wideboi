package desktop

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestGatewayConnectsBrowserToExistingUnixSession(t *testing.T) {
	if err := os.MkdirAll(config.SessionDir(), 0700); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("dt-%d-%d", os.Getpid(), time.Now().UnixNano()%1000000)
	path := config.SessionSocketPath(name)
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close(); os.Remove(path) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverReady := make(chan *transport.ServerSocketConn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		if _, err := transport.Handshake(conn); err != nil {
			conn.Close()
			return
		}
		server := transport.NewServerSocketConn(conn, 8)
		server.RunPumps(ctx)
		serverReady <- server
	}()

	httpServer := httptest.NewServer(&Gateway{Token: "local-secret"})
	defer httpServer.Close()
	version := fmt.Sprintf("wideboi.v%d", protocol.Version)
	token := "wideboi-token." + base64.RawURLEncoding.EncodeToString([]byte("local-secret"))
	dialer := websocket.Dialer{Subprotocols: []string{version, token}}
	header := http.Header{"Origin": []string{httpServer.URL}}
	ws, response, err := dialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws?session="+name, header)
	if err != nil {
		t.Fatalf("websocket: %v (response %v)", err, response)
	}
	defer ws.Close()
	if ws.Subprotocol() != version {
		t.Fatalf("subprotocol = %q, want %q", ws.Subprotocol(), version)
	}
	var session *transport.ServerSocketConn
	select {
	case session = <-serverReady:
	case <-time.After(2 * time.Second):
		t.Fatal("gateway did not handshake with session")
	}
	defer session.Close()

	attach, err := protocol.MarshalClient(protocol.MsgAttach{Cols: 90, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, attach); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-session.ClientSendChan():
		got, ok := msg.(protocol.MsgAttach)
		if !ok || got.Cols != 90 || got.Rows != 30 {
			t.Fatalf("session received %#v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session did not receive browser attach")
	}

	if !session.SendServer(ctx, protocol.MsgLayoutSnapshot{}) {
		t.Fatal("could not send snapshot")
	}
	if err := ws.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	kind, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if kind != websocket.BinaryMessage {
		t.Fatalf("frame kind = %d", kind)
	}
	if msg, err := protocol.UnmarshalServer(data); err != nil {
		t.Fatal(err)
	} else if _, ok := msg.(protocol.MsgLayoutSnapshot); !ok {
		t.Fatalf("browser received %#v", msg)
	}
}

func TestGatewayRejectsMissingToken(t *testing.T) {
	s := httptest.NewServer(&Gateway{Token: "local-secret"})
	defer s.Close()
	version := fmt.Sprintf("wideboi.v%d", protocol.Version)
	dialer := websocket.Dialer{Subprotocols: []string{version}}
	_, response, err := dialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/ws?session=default",
		http.Header{"Origin": []string{s.URL}})
	if err == nil {
		t.Fatal("connection without token succeeded")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("response = %v, want 401", response)
	}
}
