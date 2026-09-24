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

func TestListenWebSocketOrigins(t *testing.T) {
	cases := []struct {
		name, host, origin string
		wantAccepted       bool
	}{
		{name: "no origin", wantAccepted: true},
		{name: "same local host", origin: "http://127.0.0.1:8080", host: "127.0.0.1:8080", wantAccepted: true},
		{name: "same remote host through TLS proxy", origin: "https://wideboi.example.com", host: "wideboi.example.com", wantAccepted: true},
		{name: "local Vite development", origin: "http://localhost:5173", host: "127.0.0.1:8080", wantAccepted: true},
		{name: "foreign site", origin: "https://evil.example.com", host: "wideboi.example.com"},
		{name: "Vite exception on remote host", origin: "http://localhost:5173", host: "wideboi.example.com"},
		{name: "old 8080 exception", origin: "http://localhost:8080", host: "wideboi.example.com"},
		{name: "old 8081 exception", origin: "http://127.0.0.1:8081", host: "wideboi.example.com"},
		{name: "invalid scheme", origin: "file://wideboi.example.com", host: "wideboi.example.com"},
		{name: "origin with path", origin: "https://wideboi.example.com/path", host: "wideboi.example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := server.NewServer(nil, "/bin/sh", "")
			mux := http.NewServeMux()
			s.ListenWebSocket(context.Background(), mux, "secret")
			ts := httptest.NewServer(mux)
			defer ts.Close()
			wsURL := "ws" + ts.URL[len("http"):] + "/ws?token=secret"
			headers := http.Header{}
			if tc.host != "" {
				headers.Set("Host", tc.host)
			}
			if tc.origin != "" {
				headers.Set("Origin", tc.origin)
			}
			dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
			conn, resp, err := dialer.Dial(wsURL, headers)
			if err == nil {
				conn.Close()
			}
			if tc.wantAccepted && err != nil {
				t.Fatalf("expected accepted origin: %v", err)
			}
			if !tc.wantAccepted && (err == nil || resp == nil || resp.StatusCode != http.StatusForbidden) {
				t.Fatalf("expected forbidden origin; err=%v response=%v", err, resp)
			}
		})
	}
}
