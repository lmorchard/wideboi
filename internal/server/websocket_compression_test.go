package server_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
)

func TestListenWebSocketCompressionNegotiation(t *testing.T) {
	s := server.NewServer(nil, "/bin/sh", "")
	mux := http.NewServeMux()
	s.ListenWebSocket(context.Background(), mux, "")

	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	wsURL.Scheme = "ws"
	wsURL.Path = "/ws"

	t.Run("client requests compression", func(t *testing.T) {
		dialer := websocket.Dialer{
			HandshakeTimeout:  2 * time.Second,
			EnableCompression: true,
			Subprotocols:      []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
		}

		conn, resp, err := dialer.Dial(wsURL.String(), nil)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer conn.Close()

		exts := resp.Header.Get("Sec-WebSocket-Extensions")
		if !strings.Contains(exts, "permessage-deflate") {
			t.Fatalf("Sec-WebSocket-Extensions header %q does not contain permessage-deflate", exts)
		}
	})

	t.Run("client does not request compression", func(t *testing.T) {
		dialer := websocket.Dialer{
			HandshakeTimeout:  2 * time.Second,
			EnableCompression: false,
			Subprotocols:      []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
		}

		conn, resp, err := dialer.Dial(wsURL.String(), nil)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer conn.Close()

		exts := resp.Header.Get("Sec-WebSocket-Extensions")
		if strings.Contains(exts, "permessage-deflate") {
			t.Fatalf("Sec-WebSocket-Extensions header %q unexpectedly contains permessage-deflate", exts)
		}
	})
}
