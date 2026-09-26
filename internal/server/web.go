package server

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// WebTokenPath returns the filesystem path for the session's web token file.
func WebTokenPath(socket string) string {
	return strings.TrimSuffix(socket, ".sock") + ".web-token"
}

// WriteWebToken writes token to <socket>.web-token with permissions 0600 atomically.
func WriteWebToken(socket, token string) error {
	path := WebTokenPath(socket)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".web-token-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(token + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// WebServerConfig contains startup settings for the session's web server.
type WebServerConfig struct {
	SocketPath string
	ListenAddr string
	Token      string
	TLSEnabled bool
	TLSCert    string
	TLSKey     string
	AssetFS    http.FileSystem
}

type webServerManager struct {
	mu sync.Mutex

	socketPath string
	addr       string
	token      string
	tlsEnabled bool
	tlsCert    string
	tlsKey     string
	assetFS    http.FileSystem
	closed     bool

	running     bool
	boundAddr   string
	url         string
	warning     string
	httpSrv     *http.Server
	listener    net.Listener
	isGenerated bool
}

func newWebServerManager(cfg WebServerConfig) *webServerManager {
	return &webServerManager{
		socketPath: cfg.SocketPath,
		addr:       cfg.ListenAddr,
		token:      cfg.Token,
		tlsEnabled: cfg.TLSEnabled,
		tlsCert:    cfg.TLSCert,
		tlsKey:     cfg.TLSKey,
		assetFS:    cfg.AssetFS,
	}
}

// InitWebServer initializes the web server manager on Server.
func (s *Server) InitWebServer(cfg WebServerConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webServer = newWebServerManager(cfg)
}

// StartWebServer starts or updates the session's web server.
func (s *Server) StartWebServer(ctx context.Context, req protocol.MsgWebServerControlRequest) (protocol.MsgWebServerControlResponse, error) {
	w := s.getOrCreateWebManager()
	return w.Start(ctx, s, req)
}

// StopWebServer stops the session's web server and disconnects web clients.
func (s *Server) StopWebServer() (protocol.MsgWebServerControlResponse, error) {
	w := s.getOrCreateWebManager()
	return w.Stop(s)
}

// WebServerStatus returns the current web server state.
func (s *Server) WebServerStatus() protocol.MsgWebServerControlResponse {
	w := s.getOrCreateWebManager()
	return w.Status()
}

func (s *Server) getOrCreateWebManager() *webServerManager {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.webServer == nil {
		s.webServer = newWebServerManager(WebServerConfig{
			TLSEnabled: true,
		})
	}
	return s.webServer
}

// disconnectWebSocketClients closes all connected websocket transports.
func (s *Server) disconnectWebSocketClients() {
	s.mu.Lock()
	var toClose []io.Closer
	for _, tp := range s.transports {
		if transportKind(tp) == "websocket" {
			if cl, ok := tp.(io.Closer); ok {
				toClose = append(toClose, cl)
			}
		}
	}
	s.mu.Unlock()

	for _, cl := range toClose {
		_ = cl.Close()
	}
}

func sameTCPAddr(a, b string) bool {
	if a == b {
		return true
	}
	_, portA, errA := net.SplitHostPort(a)
	_, portB, errB := net.SplitHostPort(b)
	if errA == nil && errB == nil && portA == portB && portA != "0" {
		return true
	}
	return false
}

func (w *webServerManager) Start(ctx context.Context, s *Server, req protocol.MsgWebServerControlRequest) (protocol.MsgWebServerControlResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed || (s != nil && s.isStopping()) {
		err := errors.New("server is shutting down")
		return protocol.MsgWebServerControlResponse{Error: err.Error()}, err
	}

	if req.EnableTLS && req.DisableTLS {
		err := errors.New("cannot specify both enable and disable TLS")
		return protocol.MsgWebServerControlResponse{Error: err.Error()}, err
	}

	targetTLS := w.tlsEnabled
	if req.EnableTLS {
		targetTLS = true
	} else if req.DisableTLS {
		targetTLS = false
	}

	targetAddr := req.Addr
	if targetAddr == "" {
		if w.running && w.boundAddr != "" {
			targetAddr = w.boundAddr
		} else {
			targetAddr = w.addr
		}
	}
	if targetAddr == "" {
		targetAddr = "127.0.0.1:0"
	}

	tlsChanged := (targetTLS != w.tlsEnabled)
	addrChanged := (req.Addr != "" && req.Addr != w.addr && req.Addr != w.boundAddr)

	if w.running {
		if !addrChanged && !req.RotateToken && !tlsChanged && (req.Token == "" || req.Token == w.token) {
			return w.statusLocked(), nil
		}
	}

	token := req.Token
	isGenerated := false
	if token != "" {
		isGenerated = false
	} else if req.RotateToken || w.token == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			errStr := fmt.Sprintf("generate token: %v", err)
			return protocol.MsgWebServerControlResponse{Error: errStr}, err
		}
		token = hex.EncodeToString(b)
		isGenerated = true
	} else {
		token = w.token
		isGenerated = w.isGenerated
	}

	reusingAddr := w.running && (req.Addr == "" || targetAddr == w.boundAddr || targetAddr == w.addr || sameTCPAddr(targetAddr, w.boundAddr))

	if reusingAddr {
		if w.httpSrv != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = w.httpSrv.Shutdown(shutdownCtx)
			cancel()
			w.httpSrv = nil
		}
		if w.listener != nil {
			_ = w.listener.Close()
			w.listener = nil
		}
		if (req.RotateToken || tlsChanged) && s != nil {
			s.disconnectWebSocketClients()
		}
	}

	l, err := net.Listen("tcp", targetAddr)
	if err != nil {
		if reusingAddr {
			w.running = false
			w.boundAddr = ""
			w.url = ""
			w.warning = ""
		}
		errStr := fmt.Sprintf("cannot listen on %s: %v", targetAddr, err)
		return protocol.MsgWebServerControlResponse{Error: errStr}, err
	}

	if targetTLS {
		tlsConfig, err := transport.LoadOrGenerateTLSConfig(w.tlsCert, w.tlsKey, targetAddr)
		if err != nil {
			_ = l.Close()
			if reusingAddr {
				w.running = false
				w.boundAddr = ""
				w.url = ""
				w.warning = ""
			}
			errStr := fmt.Sprintf("configure tls: %v", err)
			return protocol.MsgWebServerControlResponse{Error: errStr}, err
		}
		l = tls.NewListener(l, tlsConfig)
	}

	mux := http.NewServeMux()
	if s != nil {
		s.ListenWebSocket(context.Background(), mux, token)
	}

	if w.assetFS != nil {
		mux.Handle("/", http.FileServer(w.assetFS))
	}

	httpSrv := &http.Server{Handler: mux}

	if w.socketPath != "" {
		if err := WriteWebToken(w.socketPath, token); err != nil {
			_ = l.Close()
			if reusingAddr {
				w.running = false
				w.boundAddr = ""
				w.url = ""
				w.warning = ""
			}
			errStr := fmt.Sprintf("save web token: %v", err)
			return protocol.MsgWebServerControlResponse{Error: errStr}, err
		}
	}

	boundAddr := l.Addr().String()
	host := boundAddr
	if tcpAddr, ok := l.Addr().(*net.TCPAddr); ok {
		if tcpAddr.IP.IsLoopback() || tcpAddr.IP.IsUnspecified() {
			host = fmt.Sprintf("127.0.0.1:%d", tcpAddr.Port)
		}
	}

	scheme := "https"
	if !targetTLS {
		scheme = "http"
	}
	webURL := fmt.Sprintf("%s://%s/#token=%s", scheme, host, url.QueryEscape(token))

	if w.running && !reusingAddr {
		if w.httpSrv != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = w.httpSrv.Shutdown(shutdownCtx)
			cancel()
		}
		if w.listener != nil {
			_ = w.listener.Close()
		}
		if (req.RotateToken || tlsChanged) && s != nil {
			s.disconnectWebSocketClients()
		}
	}

	w.running = true
	w.addr = targetAddr
	w.boundAddr = boundAddr
	w.token = token
	w.isGenerated = isGenerated
	w.tlsEnabled = targetTLS
	w.url = webURL
	w.httpSrv = httpSrv
	w.listener = l

	slog.Info("websocket server listening", "addr", boundAddr, "token", "***REDACTED***", "tls", targetTLS)
	w.warning = ExposureWarning(l.Addr(), targetTLS)
	if w.warning != "" {
		slog.Warn("web client exposed beyond loopback over unencrypted HTTP/WS", "addr", boundAddr)
	}

	go func() {
		if err := httpSrv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("websocket server failed", "err", err)
		}
	}()

	return w.statusLocked(), nil
}

func (w *webServerManager) Stop(s *Server) (protocol.MsgWebServerControlResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stopLocked(s)
}

func (w *webServerManager) stopLocked(s *Server) (protocol.MsgWebServerControlResponse, error) {
	if !w.running {
		return w.statusLocked(), nil
	}

	if w.httpSrv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = w.httpSrv.Shutdown(shutdownCtx)
		cancel()
		w.httpSrv = nil
	}
	if w.listener != nil {
		_ = w.listener.Close()
		w.listener = nil
	}
	if w.socketPath != "" {
		_ = os.Remove(WebTokenPath(w.socketPath))
	}

	w.running = false
	w.boundAddr = ""
	w.url = ""
	w.warning = ""

	if s != nil {
		s.disconnectWebSocketClients()
	}

	return w.statusLocked(), nil
}

func (w *webServerManager) Status() protocol.MsgWebServerControlResponse {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.statusLocked()
}

func (w *webServerManager) statusLocked() protocol.MsgWebServerControlResponse {
	return protocol.MsgWebServerControlResponse{
		Running:    w.running,
		Addr:       w.boundAddr,
		URL:        w.url,
		TLSEnabled: w.tlsEnabled,
		Token:      w.token,
		Warning:    w.warning,
	}
}

func (w *webServerManager) Close(s *Server) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	_, _ = w.stopLocked(s)
}

// ExposureWarning returns a warning message if the web client is exposed beyond
// loopback without TLS, or an empty string otherwise.
func ExposureWarning(addr net.Addr, tlsEnabled bool) string {
	if tlsEnabled {
		return ""
	}
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok || tcpAddr.IP.IsLoopback() {
		return ""
	}
	return "wideboi: WARNING: web client is exposed beyond loopback over unencrypted HTTP/WS; bind to loopback behind an HTTPS reverse proxy for remote access"
}

// websocketProtocolToken reads the browser's token-bearing subprotocol offer.
// The server deliberately does not select it as the negotiated subprotocol.
func websocketProtocolToken(r *http.Request) string {
	return transport.WebSocketTokenFromSubprotocols(websocket.Subprotocols(r))
}

// ListenWebSocket starts accepting WebSocket connections via the provided http.ServeMux.
func (s *Server) ListenWebSocket(ctx context.Context, mux *http.ServeMux, token string) {
	versionProtocol := transport.WebSocketSubprotocol()
	upgrader := &websocket.Upgrader{
		ReadBufferSize:    4096,
		WriteBufferSize:   4096,
		Subprotocols:      []string{versionProtocol},
		CheckOrigin:       webSocketOriginAllowed,
		EnableCompression: true,
	}

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			reqToken := r.URL.Query().Get("token")
			if reqToken != token && websocketProtocolToken(r) != token {
				slog.Warn("websocket connection rejected: invalid token", "remote", r.RemoteAddr)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		versionOffered := false
		for _, offered := range websocket.Subprotocols(r) {
			if offered == versionProtocol {
				versionOffered = true
				break
			}
		}
		if !versionOffered {
			http.Error(w, "Unsupported wideboi protocol version", http.StatusUpgradeRequired)
			return
		}

		// gorilla writes the 101 response and every frame to the
		// hijacked conn, so counting that conn counts the wire.
		cw := transport.NewCountingResponseWriter(w)
		conn, err := upgrader.Upgrade(cw, r, nil)
		if err != nil {
			slog.Debug("websocket upgrade failed", "err", err)
			return
		}

		sConn := transport.NewWebSocketServerConn(conn, 256, cw)
		sConn.RunPumps(ctx)

		s.mu.Lock()
		if s.stoppingLocked() {
			s.mu.Unlock()
			_ = sConn.Close()
			return
		}
		s.transports = append(s.transports, sConn)
		if s.remoteTransports == nil {
			s.remoteTransports = make(map[transport.Transport]bool)
		}
		s.remoteTransports[sConn] = true
		s.clientLocked(sConn).remote = true
		s.startTransportLoopLocked(ctx, sConn)
		s.mu.Unlock()
	})
}

// webSocketOriginAllowed permits same-host browser connections and the local
// Vite development server. The development exception must never apply to a
// remotely addressed WebSocket endpoint.
func webSocketOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // Non-browser clients still need the token.
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	requestHost, _, err := net.SplitHostPort(r.Host)
	if err != nil || (requestHost != "localhost" && requestHost != "127.0.0.1" && requestHost != "::1") {
		return false
	}
	return u.Scheme == "http" && (u.Host == "localhost:5173" || u.Host == "127.0.0.1:5173" || u.Host == "[::1]:5173")
}
