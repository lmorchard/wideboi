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

func (w *webServerManager) Start(ctx context.Context, s *Server, req protocol.MsgWebServerControlRequest) (protocol.MsgWebServerControlResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed || (s != nil && s.isStopping()) {
		err := errors.New("server is shutting down")
		return protocol.MsgWebServerControlResponse{Error: err.Error()}, err
	}

	// If already running: if no address change and no rotate token, return current status
	addr := req.Addr
	if addr == "" {
		addr = w.addr
	}
	if addr == "" {
		addr = "127.0.0.1:0"
	}

	disableTLS := req.DisableTLS
	tlsEnabled := w.tlsEnabled
	if disableTLS {
		tlsEnabled = false
	}

	if w.running {
		if (req.Addr == "" || req.Addr == w.boundAddr || req.Addr == w.addr) && !req.RotateToken && (!req.DisableTLS || !w.tlsEnabled) {
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

	l, err := net.Listen("tcp", addr)
	if err != nil {
		errStr := fmt.Sprintf("cannot listen on %s: %v", addr, err)
		return protocol.MsgWebServerControlResponse{Error: errStr}, err
	}

	if tlsEnabled {
		tlsConfig, err := transport.LoadOrGenerateTLSConfig(w.tlsCert, w.tlsKey, addr)
		if err != nil {
			_ = l.Close()
			errStr := fmt.Sprintf("configure tls: %v", err)
			return protocol.MsgWebServerControlResponse{Error: errStr}, err
		}
		l = tls.NewListener(l, tlsConfig)
	}

	mux := http.NewServeMux()
	s.ListenWebSocket(context.Background(), mux, token)

	if w.assetFS != nil {
		mux.Handle("/", http.FileServer(w.assetFS))
	}

	httpSrv := &http.Server{Handler: mux}

	if w.socketPath != "" {
		if err := WriteWebToken(w.socketPath, token); err != nil {
			_ = l.Close()
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
	if !tlsEnabled {
		scheme = "http"
	}
	webURL := fmt.Sprintf("%s://%s/#token=%s", scheme, host, url.QueryEscape(token))

	// All setup for new server succeeded. Tear down old listener if replacing a running server.
	if w.running {
		if w.httpSrv != nil {
			_ = w.httpSrv.Shutdown(context.Background())
		}
		if w.listener != nil {
			_ = w.listener.Close()
		}
		if req.RotateToken && s != nil {
			s.disconnectWebSocketClients()
		}
	}

	w.running = true
	w.addr = addr
	w.boundAddr = boundAddr
	w.token = token
	w.isGenerated = isGenerated
	w.tlsEnabled = tlsEnabled
	w.url = webURL
	w.httpSrv = httpSrv
	w.listener = l

	slog.Info("websocket server listening", "addr", boundAddr, "token", "***REDACTED***", "tls", tlsEnabled)
	warnIfWebClientExposed(l.Addr(), tlsEnabled)

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
		_ = w.httpSrv.Shutdown(context.Background())
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
	}
}

func (w *webServerManager) Close(s *Server) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	_, _ = w.stopLocked(s)
}

func warnIfWebClientExposed(addr net.Addr, tlsEnabled bool) {
	if tlsEnabled {
		return
	}
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok || tcpAddr.IP.IsLoopback() {
		return
	}
	slog.Warn("web client exposed beyond loopback over unencrypted HTTP/WS", "addr", addr.String())
}
