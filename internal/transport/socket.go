package transport

import (
	"context"
	"encoding/gob"
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/lmorchard/wideboi/internal/protocol"
)

func init() {
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

// SocketConn bridges a net.Conn to ClientSend/ServerSend channels.
type SocketConn struct {
	conn       net.Conn
	ClientSend chan ClientMessage
	ServerSend chan ServerMessage
	encoder    *gob.Encoder
	decoder    *gob.Decoder
}

// NewSocketConn wraps a net.Conn with buffered channels and gob encoding.
func NewSocketConn(conn net.Conn, bufSize int) *SocketConn {
	if bufSize <= 0 {
		bufSize = 128
	}
	return &SocketConn{
		conn:       conn,
		ClientSend: make(chan ClientMessage, bufSize),
		ServerSend: make(chan ServerMessage, bufSize),
		encoder:    gob.NewEncoder(conn),
		decoder:    gob.NewDecoder(conn),
	}
}

// RunPumps starts background read and write loops for the connection.
func (sc *SocketConn) RunPumps(ctx context.Context) {
	go sc.writeLoop(ctx)
	go sc.readLoop(ctx)
}

func (sc *SocketConn) writeLoop(ctx context.Context) {
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

func (sc *SocketConn) readLoop(ctx context.Context) {
	defer close(sc.ClientSend)
	for {
		var msg ClientMessage
		if err := sc.decoder.Decode(&msg); err != nil {
			if err != io.EOF {
				// connection closed or error
			}
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
func (sc *SocketConn) SendServer(ctx context.Context, msg ServerMessage) bool {
	select {
	case sc.ServerSend <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

// Close closes the underlying network connection.
func (sc *SocketConn) Close() error {
	return sc.conn.Close()
}
