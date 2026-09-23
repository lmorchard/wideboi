package transport

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"

	"google.golang.org/protobuf/proto"

	"github.com/lmorchard/wideboi/internal/protocol"
)

// connErr records the first non-clean reason a connection's pumps
// stopped, so a caller can tell a protocol failure from a detach.
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

func isCleanClose(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, context.Canceled)
}

// SocketListener manages a Unix domain socket server.
type SocketListener struct {
	listener net.Listener
	path     string
	mu       sync.Mutex
	closed   bool
}

func NewSocketListener(path string) (*SocketListener, error) {
	if c, derr := net.Dial("unix", path); derr == nil {
		c.Close()
		return nil, fmt.Errorf("a wideboi server is already listening at %s", path)
	}
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

func (sl *SocketListener) Path() string {
	return sl.path
}

func (sl *SocketListener) Accept() (net.Conn, error) {
	return sl.listener.Accept()
}

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

func writeProto(conn net.Conn, msg proto.Message) error {
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	var length uint32 = uint32(len(b))
	if err := binary.Write(conn, binary.LittleEndian, length); err != nil {
		return err
	}
	_, err = conn.Write(b)
	return err
}

func readProto(conn net.Conn, msg proto.Message) error {
	var length uint32
	if err := binary.Read(conn, binary.LittleEndian, &length); err != nil {
		return err
	}
	if length > 10*1024*1024 { // 10MB sanity limit
		return fmt.Errorf("message too large: %d bytes", length)
	}
	b := make([]byte, length)
	if _, err := io.ReadFull(conn, b); err != nil {
		return err
	}
	return proto.Unmarshal(b, msg)
}

// ServerSocketConn bridges a server-side net.Conn to ClientSend/ServerSend channels.
type ServerSocketConn struct {
	connErr
	conn       net.Conn
	ClientSend chan ClientMessage
	ServerSend chan ServerMessage
	closed     chan struct{}
	closeOnce  sync.Once
}

func NewServerSocketConn(conn net.Conn, bufSize int) *ServerSocketConn {
	if bufSize <= 0 {
		bufSize = 128
	}
	return &ServerSocketConn{
		conn:       conn,
		ClientSend: make(chan ClientMessage, bufSize),
		ServerSend: make(chan ServerMessage, bufSize),
		closed:     make(chan struct{}),
	}
}

func (sc *ServerSocketConn) RunPumps(ctx context.Context) {
	go sc.writeLoop(ctx)
	go sc.readLoop(ctx)
}

func (sc *ServerSocketConn) writeLoop(ctx context.Context) {
	defer sc.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-sc.ServerSend:
			if !ok {
				return
			}
			if err := writeProto(sc.conn, msg); err != nil {
				sc.set(fmt.Sprintf("encoding %T to client", msg), err)
				return
			}
		}
	}
}

func (sc *ServerSocketConn) readLoop(ctx context.Context) {
	defer close(sc.ClientSend)
	for {
		msg := &protocol.ClientEnvelope{}
		if err := readProto(sc.conn, msg); err != nil {
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

func (sc *ServerSocketConn) SendClient(ctx context.Context, msg ClientMessage) bool {
	return false
}

func (sc *ServerSocketConn) ClientSendChan() <-chan ClientMessage {
	return sc.ClientSend
}

func (sc *ServerSocketConn) ServerSendChan() <-chan ServerMessage {
	return sc.ServerSend
}

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
}

func NewClientSocketConn(conn net.Conn, bufSize int) *ClientSocketConn {
	if bufSize <= 0 {
		bufSize = 128
	}
	return &ClientSocketConn{
		conn:       conn,
		ClientSend: make(chan ClientMessage, bufSize),
		ServerSend: make(chan ServerMessage, bufSize),
	}
}

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
			if err := writeProto(cc.conn, msg); err != nil {
				cc.set(fmt.Sprintf("encoding %T to server", msg), err)
				return
			}
		}
	}
}

func (cc *ClientSocketConn) readLoop(ctx context.Context) {
	defer close(cc.ServerSend)
	for {
		msg := &protocol.ServerEnvelope{}
		if err := readProto(cc.conn, msg); err != nil {
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

func (cc *ClientSocketConn) SendClient(ctx context.Context, msg ClientMessage) bool {
	select {
	case cc.ClientSend <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

func (cc *ClientSocketConn) SendServer(ctx context.Context, msg ServerMessage) bool {
	return false
}

func (cc *ClientSocketConn) ClientSendChan() <-chan ClientMessage {
	return cc.ClientSend
}

func (cc *ClientSocketConn) ServerSendChan() <-chan ServerMessage {
	return cc.ServerSend
}

func (cc *ClientSocketConn) Close() error {
	return cc.conn.Close()
}
