package transport

import (
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/lmorchard/wideboi/internal/protocol"
)

// connErr records the first non-clean reason a connection's pumps
// stopped, so a caller can tell a protocol failure from a detach.
//
// Before this existed, all four pump loops did `if err != nil { return
// }`. A gob encode error -- the kind raised by an interface field with
// no registered concrete type -- was therefore indistinguishable from
// the user pressing C-b d: the socket closed, the peer saw EOF, and
// `wideboi attach` exited with status 0 and no message. That is how a
// defect that killed every session within two seconds of attaching
// survived to a third fix attempt. A pump that gives up must say why.
type connErr struct {
	err atomic.Value // error
}

func (c *connErr) set(where string, err error) {
	if err == nil || isCleanClose(err) {
		return
	}
	wrapped := fmt.Errorf("%s: %w", where, err)
	c.err.CompareAndSwap(nil, wrapped)
	slog.Error("transport pump failed", "where", where, "err", err)
}

// Err returns the first protocol-level failure seen on this connection,
// or nil if it only ever saw a clean shutdown.
func (c *connErr) Err() error {
	if v := c.err.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// isCleanClose reports whether err is an ordinary end-of-connection
// rather than something worth telling the user about. A peer that
// detaches closes its socket, which surfaces as EOF on our decoder and
// as ErrClosed on any write that races it; neither is a fault.
func isCleanClose(err error) bool {
	// EPIPE and ECONNRESET belong here with EOF: they are what a write
	// in flight sees when the peer has already gone. A detaching client
	// closes its socket while the server's broadcast ticker is mid-frame
	// roughly every time, so treating them as faults would file an error
	// on every ordinary C-b d.
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, context.Canceled)
}

func init() {
	gob.Register(protocol.ColumnData{})
	gob.Register(protocol.CellData{})
	gob.Register(protocol.LineData{})
	gob.Register(protocol.MsgPaneUpdate{})
	gob.Register(protocol.MsgAttach{})
	gob.Register(protocol.MsgVerb{})
	gob.Register(protocol.MsgFocusPane{})
	gob.Register(protocol.MsgMouse{})
	gob.Register(protocol.MsgInput{})
	gob.Register(protocol.MsgResize{})
	gob.Register(protocol.MsgScroll{})
	gob.Register(protocol.MsgShutdown{})
	gob.Register(protocol.MsgDetach{})
	gob.Register(protocol.MsgLayoutSnapshot{})
	gob.Register(protocol.MsgPaneClosed{})
}

// SocketListener manages a Unix domain socket server.
type SocketListener struct {
	listener net.Listener
	path     string
	mu       sync.Mutex
	closed   bool
}

// NewSocketListener binds a Unix domain socket at path.
//
// A leftover socket file has to be removed before Listen will bind, but
// removing it unconditionally is how you steal the address from a
// server that is still running: the old process keeps its listening fd
// and its clients, the new one binds a fresh inode at the same path,
// and every subsequent `wideboi attach` reaches the new server while
// the old one's panes keep running invisibly. So probe first. A
// successful dial means somebody is home, and that is an error, not a
// file to delete.
func NewSocketListener(path string) (*SocketListener, error) {
	if c, derr := net.Dial("unix", path); derr == nil {
		c.Close()
		return nil, fmt.Errorf("a wideboi server is already listening at %s", path)
	}
	// Nothing answered, so a socket here is a corpse from a server that
	// did not get to clean up after itself. Only a socket: WIDEBOI_SOCK
	// lets a caller name any path, and unlinking whatever happens to be
	// there would turn a typo into data loss.
	if fi, serr := os.Lstat(path); serr == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing to remove %s: it exists and is not a socket", path)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("removing stale socket %s: %w", path, err)
		}
	} else if !errors.Is(serr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspecting %s: %w", path, serr)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen unix socket %s: %w", path, err)
	}
	return &SocketListener{
		listener: l,
		path:     path,
	}, nil
}

// Path returns the socket file path.
func (sl *SocketListener) Path() string {
	return sl.path
}

// Accept waits for and returns the next connection to the listener.
func (sl *SocketListener) Accept() (net.Conn, error) {
	return sl.listener.Accept()
}

// Close closes the listener and removes the socket file.
func (sl *SocketListener) Close() error {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.closed {
		return nil
	}
	sl.closed = true
	err := sl.listener.Close()
	_ = os.Remove(sl.path)
	return err
}

// ServerSocketConn bridges a server-side net.Conn to ClientSend/ServerSend channels.
type ServerSocketConn struct {
	connErr
	conn       net.Conn
	ClientSend chan ClientMessage
	ServerSend chan ServerMessage
	encoder    *gob.Encoder
	decoder    *gob.Decoder
	// closed is shut by Close, so a SendServer blocked on a full
	// queue is released rather than waiting on a write pump that has
	// stopped draining.
	closed    chan struct{}
	closeOnce sync.Once
}

// NewServerSocketConn wraps a server-side net.Conn with buffered channels.
func NewServerSocketConn(conn net.Conn, bufSize int) *ServerSocketConn {
	if bufSize <= 0 {
		bufSize = 128
	}
	return &ServerSocketConn{
		conn:       conn,
		ClientSend: make(chan ClientMessage, bufSize),
		ServerSend: make(chan ServerMessage, bufSize),
		encoder:    gob.NewEncoder(conn),
		decoder:    gob.NewDecoder(conn),
		closed:     make(chan struct{}),
	}
}

// RunPumps starts background read and write loops for the connection.
func (sc *ServerSocketConn) RunPumps(ctx context.Context) {
	go sc.writeLoop(ctx)
	go sc.readLoop(ctx)
}

func (sc *ServerSocketConn) writeLoop(ctx context.Context) {
	// Close, not conn.Close: once this pump is gone nothing drains
	// ServerSend, so anyone blocked sending to it must be let go.
	defer sc.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-sc.ServerSend:
			if !ok {
				return
			}
			if err := sc.encoder.Encode(&msg); err != nil {
				sc.set(fmt.Sprintf("encoding %T to client", msg), err)
				return
			}
		}
	}
}

func (sc *ServerSocketConn) readLoop(ctx context.Context) {
	defer close(sc.ClientSend)
	for {
		var msg ClientMessage
		if err := sc.decoder.Decode(&msg); err != nil {
			sc.set("decoding from client", err)
			return
		}
		select {
		case sc.ClientSend <- msg:
		case <-ctx.Done():
			return
		}
	}
}

// SendServer sends a server message over the socket connection. It
// gives up once the connection is closed: a client that stopped reading
// must not be able to park the server's broadcast loop -- and with it a
// server that has otherwise finished shutting down -- forever.
func (sc *ServerSocketConn) SendServer(ctx context.Context, msg ServerMessage) bool {
	select {
	case <-sc.closed:
		return false
	default:
	}
	select {
	case sc.ServerSend <- msg:
		return true
	case <-sc.closed:
		return false
	case <-ctx.Done():
		return false
	}
}

// SendClient is a no-op for server-side socket conn.
func (sc *ServerSocketConn) SendClient(ctx context.Context, msg ClientMessage) bool {
	return false
}

// ClientSendChan returns the channel where client messages arrive.
func (sc *ServerSocketConn) ClientSendChan() <-chan ClientMessage {
	return sc.ClientSend
}

// ServerSendChan returns the channel where server messages are queued.
func (sc *ServerSocketConn) ServerSendChan() <-chan ServerMessage {
	return sc.ServerSend
}

// Close closes the underlying network connection and releases any
// SendServer blocked on it.
func (sc *ServerSocketConn) Close() error {
	sc.closeOnce.Do(func() { close(sc.closed) })
	return sc.conn.Close()
}

// ClientSocketConn bridges a client-side net.Conn to ClientSend/ServerSend channels.
type ClientSocketConn struct {
	connErr
	conn       net.Conn
	ClientSend chan ClientMessage
	ServerSend chan ServerMessage
	encoder    *gob.Encoder
	decoder    *gob.Decoder
}

// NewClientSocketConn wraps a client-side net.Conn with buffered channels.
func NewClientSocketConn(conn net.Conn, bufSize int) *ClientSocketConn {
	if bufSize <= 0 {
		bufSize = 128
	}
	return &ClientSocketConn{
		conn:       conn,
		ClientSend: make(chan ClientMessage, bufSize),
		ServerSend: make(chan ServerMessage, bufSize),
		encoder:    gob.NewEncoder(conn),
		decoder:    gob.NewDecoder(conn),
	}
}

// RunPumps starts background read and write loops for the connection.
func (cc *ClientSocketConn) RunPumps(ctx context.Context) {
	go cc.writeLoop(ctx)
	go cc.readLoop(ctx)
}

func (cc *ClientSocketConn) writeLoop(ctx context.Context) {
	defer cc.conn.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-cc.ClientSend:
			if !ok {
				return
			}
			if err := cc.encoder.Encode(&msg); err != nil {
				cc.set(fmt.Sprintf("encoding %T to server", msg), err)
				return
			}
		}
	}
}

func (cc *ClientSocketConn) readLoop(ctx context.Context) {
	defer close(cc.ServerSend)
	for {
		var msg ServerMessage
		if err := cc.decoder.Decode(&msg); err != nil {
			cc.set("decoding from server", err)
			return
		}
		select {
		case cc.ServerSend <- msg:
		case <-ctx.Done():
			return
		}
	}
}

// SendClient sends a client message over the socket connection.
func (cc *ClientSocketConn) SendClient(ctx context.Context, msg ClientMessage) bool {
	select {
	case cc.ClientSend <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

// SendServer is a no-op for client-side socket conn.
func (cc *ClientSocketConn) SendServer(ctx context.Context, msg ServerMessage) bool {
	return false
}

// ClientSendChan returns the channel where client messages are queued.
func (cc *ClientSocketConn) ClientSendChan() <-chan ClientMessage {
	return cc.ClientSend
}

// ServerSendChan returns the channel where server messages arrive.
func (cc *ClientSocketConn) ServerSendChan() <-chan ServerMessage {
	return cc.ServerSend
}

// Close closes the underlying network connection.
func (cc *ClientSocketConn) Close() error {
	return cc.conn.Close()
}
