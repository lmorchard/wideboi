// Package desktop bridges the browser client in a desktop window to local
// sessions. A CLI-started session only has a Unix socket, so the desktop
// app supplies a loopback WebSocket without changing that session.
package desktop

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// Gateway accepts browser connections for named local sessions. Token is a
// random secret held by the desktop process, not a session server token.
type Gateway struct {
	Token string
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ws" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	name := r.URL.Query().Get("session")
	if !config.ValidSessionName(name) {
		http.Error(w, "invalid session", http.StatusBadRequest)
		return
	}
	if !hasToken(r, g.Token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	version := fmt.Sprintf("wideboi.v%d", protocol.Version)
	offered := false
	for _, p := range websocket.Subprotocols(r) {
		if p == version {
			offered = true
			break
		}
	}
	if !offered {
		http.Error(w, "unsupported wideboi protocol version", http.StatusUpgradeRequired)
		return
	}

	conn, err := net.DialTimeout("unix", config.SessionSocketPath(name), 2*time.Second)
	if err != nil {
		http.Error(w, "session unavailable", http.StatusBadGateway)
		return
	}
	defer conn.Close()
	if _, err := transport.Handshake(conn); err != nil {
		http.Error(w, "session protocol mismatch", http.StatusBadGateway)
		return
	}

	upgrader := websocket.Upgrader{
		Subprotocols: []string{version},
		CheckOrigin:  sameOrigin,
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	browser := transport.NewWebSocketServerConn(ws, 256, nil)
	socket := transport.NewClientSocketConn(conn, 256)
	browser.RunPumps(ctx)
	socket.RunPumps(ctx)
	defer browser.Close()
	defer socket.Close()

	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		for msg := range browser.ClientSendChan() {
			if !socket.SendClient(ctx, msg) {
				return
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for msg := range socket.ServerSendChan() {
			if !browser.SendServer(ctx, msg) {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func hasToken(r *http.Request, want string) bool {
	if want == "" {
		return false
	}
	for _, p := range websocket.Subprotocols(r) {
		const prefix = "wideboi-token."
		if len(p) <= len(prefix) || p[:len(prefix)] != prefix {
			continue
		}
		got, err := base64.RawURLEncoding.DecodeString(p[len(prefix):])
		if err == nil && subtle.ConstantTimeCompare(got, []byte(want)) == 1 {
			return true
		}
	}
	return false
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "http" && u.Host == r.Host &&
		u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}
