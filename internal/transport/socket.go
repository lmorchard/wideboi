package transport

import (
	"context"
	"encoding/gob"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/lmorchard/wideboi/internal/protocol"
)

func init() {
	gob.Register(protocol.ColumnData{})
	gob.Register(protocol.CellData{})
	gob.Register(protocol.LineData{})
	gob.Register(protocol.MsgPaneUpdate{})
	gob.Register(protocol.MsgAttach{})
	gob.Register(protocol.MsgVerb{})
	gob.Register(protocol.MsgInput{})
	gob.Register(protocol.MsgResize{})
	gob.Register(protocol.MsgScroll{})
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
func NewSocketListener(path string) (*SocketListener, error) {
	_ = os.Remove(path) // Clean up stale socket file if present
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
	conn       net.Conn
	ClientSend chan ClientMessage
	ServerSend chan ServerMessage
	encoder    *gob.Encoder
	decoder    *gob.Decoder
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
	}
}

// RunPumps starts background read and write loops for the connection.
func (sc *ServerSocketConn) RunPumps(ctx context.Context) {
	go sc.writeLoop(ctx)
	go sc.readLoop(ctx)
}

func (sc *ServerSocketConn) writeLoop(ctx context.Context) {
	defer sc.conn.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-sc.ServerSend:
			if !ok {
				return
			}
			if err := sc.encoder.Encode(&msg); err != nil {
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
			return
		}
		select {
		case sc.ClientSend <- msg:
		case <-ctx.Done():
			return
		}
	}
}

// SendServer sends a server message over the socket connection.
func (sc *ServerSocketConn) SendServer(ctx context.Context, msg ServerMessage) bool {
	select {
	case sc.ServerSend <- msg:
		return true
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

// Close closes the underlying network connection.
func (sc *ServerSocketConn) Close() error {
	return sc.conn.Close()
}

// ClientSocketConn bridges a client-side net.Conn to ClientSend/ServerSend channels.
type ClientSocketConn struct {
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

// SocketConn is an alias for ServerSocketConn for backwards compatibility.
type SocketConn = ServerSocketConn

// NewSocketConn is an alias for NewServerSocketConn.
func NewSocketConn(conn net.Conn, bufSize int) *SocketConn {
	return NewServerSocketConn(conn, bufSize)
}
