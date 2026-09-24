package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lmorchard/wideboi/internal/server"
)

func TestListenWebSocketAuth(t *testing.T) {
	cases := []struct {
		name            string
		expectedToken   string
		requestQuery    string
		requestProtocol string
		browserProtocol bool
		wantSuccess     bool
	}{
		{name: "no token expected, no token provided", expectedToken: "", requestQuery: "", wantSuccess: true},
		{name: "no token expected, random token provided", expectedToken: "", requestQuery: "token=xyz", wantSuccess: true},
		{name: "token expected, matching token provided", expectedToken: "secret123", requestQuery: "token=secret123", wantSuccess: true},
		{name: "generated token via browser protocol", expectedToken: "secret123", requestProtocol: "wideboi-token.c2VjcmV0MTIz", wantSuccess: true},
		{name: "browser selects fixed protocol", expectedToken: "secret123", requestProtocol: "wideboi-token.c2VjcmV0MTIz", browserProtocol: true, wantSuccess: true},
		{name: "configured token via browser protocol", expectedToken: "configured!", requestProtocol: "wideboi-token.Y29uZmlndXJlZCE", wantSuccess: true},
		{name: "wrong browser protocol token", expectedToken: "secret123", requestProtocol: "wideboi-token.d3Jvbmc", wantSuccess: false},
		{name: "token expected, no token provided", expectedToken: "secret123", requestQuery: "", wantSuccess: false},
		{name: "token expected, mismatching token provided", expectedToken: "secret123", requestQuery: "token=wrong", wantSuccess: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := server.NewServer(nil, "/bin/sh", "")
			mux := http.NewServeMux()
			s.ListenWebSocket(context.Background(), mux, tc.expectedToken)

			ts := httptest.NewServer(mux)
			defer ts.Close()

			wsURL, err := url.Parse(ts.URL)
			if err != nil {
				t.Fatalf("parse url: %v", err)
			}
			wsURL.Scheme = "ws"
			wsURL.Path = "/ws"
			wsURL.RawQuery = tc.requestQuery

			dialer := websocket.Dialer{
				HandshakeTimeout: 2 * time.Second,
			}
			if tc.requestProtocol != "" {
				dialer.Subprotocols = []string{tc.requestProtocol}
			}
			if tc.browserProtocol {
				dialer.Subprotocols = []string{"wideboi", tc.requestProtocol}
			}
			conn, resp, err := dialer.Dial(wsURL.String(), nil)

			if tc.wantSuccess {
				if err != nil {
					t.Fatalf("expected successful connection, got error: %v (resp: %+v)", err, resp)
				}
				if tc.browserProtocol && conn.Subprotocol() != "wideboi" {
					t.Fatalf("selected subprotocol = %q, want wideboi", conn.Subprotocol())
				}
				conn.Close()
			} else {
				if err == nil {
					conn.Close()
					t.Fatalf("expected connection to fail due to auth, but it succeeded")
				}
				if resp == nil {
					t.Fatalf("expected HTTP response, got nil (err: %v)", err)
				}
				if resp.StatusCode != http.StatusUnauthorized {
					t.Errorf("expected status %d Unauthorized, got %d", http.StatusUnauthorized, resp.StatusCode)
				}
			}
		})
	}
}
