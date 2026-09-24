package transport

import (
	"context"
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

// connErr records the first non-clean reason a connection's pumps stopped.
// A framing or protobuf failure must reach the caller instead of looking like
// a normal detach.
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
// detaches closes its socket, which surfaces as EOF on our frame reader and
// as ErrClosed on any write that races it; neither is a fault.
func isCleanClose(err error) bool {
	// EPIPE and ECONNRESET belong here with EOF: they are what a write
	// in flight sees when the peer has already gone. A detaching client
	// closes its socket while the server's broadcast ticker is mid-frame
	// roughly every time, so treating them as faults would file an error
	// on every ordinary C-b d.
	return errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, context.Canceled)
}

var ErrSessionTaken = errors.New("session taken")

// SocketListener manages a Unix domain socket server.
type SocketListener struct {
	listener net.Listener
	path     string
	// lock is held for the listener's whole life; see NewSocketListener.
	lock   *os.File
	mu     sync.Mutex
	closed bool
}

// NewSocketListener binds a Unix domain socket at path.
//
// Ownership of path is an exclusive flock on path+".lock", held for as
// long as the server lives. The kernel drops it when the process dies,
// SIGKILL included, so a held lock always means a live server. The lock
// replaced a dial probe as the arbiter: the probe's probe/remove/listen
// window let two servers starting together both bind, the second
// unlinking the first's socket and leaving it running where no attach
// could reach it (#86). A probe survives only as a guard against a
// server too old to take the lock.
//
// The lock file is never deleted: unlinking it would let a waiter lock
// the old inode while a newcomer locks a fresh one. os.OpenFile sets
// O_CLOEXEC, so pane processes never inherit the lock and cannot keep
// the name taken after the server has gone.
func NewSocketListener(path string) (*SocketListener, error) {
	lock, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening lock for %s: %w", path, err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("a wideboi server is already listening at %s: %w", path, ErrSessionTaken)
		}
		return nil, fmt.Errorf("locking %s.lock: %w", path, err)
	}
	// The lock settles it among servers that take it. A server from a
	// binary older than the lock serves its socket without one, so a
	// socket that still answers is not a corpse, lock or no lock.
	if c, derr := net.Dial("unix", path); derr == nil {
		c.Close()
		lock.Close()
		return nil, fmt.Errorf("a wideboi server is already listening at %s: %w", path, ErrSessionTaken)
	}
	l, err := bindHeld(path)
	if err != nil {
		lock.Close()
		return nil, err
	}
	return &SocketListener{
		listener: l,
		path:     path,
		lock:     lock,
	}, nil
}

// bindHeld listens at path, whose lock the caller holds.
//
// A leftover socket has to be removed before Listen will bind. Holding
// the lock is what makes that safe: nobody else can be serving it, so a
// socket here is a corpse from a server that did not get to clean up
// after itself. Only a socket: WIDEBOI_SOCK lets a caller name any
// path, and unlinking whatever happens to be there would turn a typo
// into data loss.
func bindHeld(path string) (net.Listener, error) {
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
	return l, nil
}

// Path returns the socket file path.
func (sl *SocketListener) Path() string {
	return sl.path
}

// Accept waits for and returns the next connection to the listener.
func (sl *SocketListener) Accept() (net.Conn, error) {
	return sl.listener.Accept()
}

// Close closes the listener, removes the socket file and releases the lock.
func (sl *SocketListener) Close() error {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.closed {
		return nil
	}
	sl.closed = true
	err := sl.listener.Close()
	// Remove before unlocking: a successor that bound in between would
	// otherwise have its fresh socket unlinked by us.
	_ = os.Remove(sl.path)
	_ = sl.lock.Close()
	return err
}

// ServerSocketConn bridges a server-side net.Conn to ClientSend/ServerSend channels.
type ServerSocketConn struct {
	connErr
	conn       net.Conn
	ClientSend chan ClientMessage
	ServerSend chan ServerMessage
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
			payload, err := protocol.MarshalServer(msg)
			if err == nil {
				err = writeFrame(sc.conn, payload)
			}
			if err != nil {
				sc.set(fmt.Sprintf("encoding %T to client", msg), err)
				return
			}
		}
	}
}

func (sc *ServerSocketConn) readLoop(ctx context.Context) {
	defer close(sc.ClientSend)
	for {
		payload, err := readFrame(sc.conn)
		if err != nil {
			sc.set("reading from client", err)
			return
		}
		msg, err := protocol.UnmarshalClient(payload)
		if err != nil {
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
	cancel     context.CancelFunc
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
	}
}

// RunPumps starts background read and write loops for the connection.
func (cc *ClientSocketConn) RunPumps(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	cc.cancel = cancel
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
			payload, err := protocol.MarshalClient(msg)
			if err == nil {
				err = writeFrame(cc.conn, payload)
			}
			if err != nil {
				cc.set(fmt.Sprintf("encoding %T to server", msg), err)
				return
			}
		}
	}
}

func (cc *ClientSocketConn) readLoop(ctx context.Context) {
	defer close(cc.ServerSend)
	for {
		payload, err := readFrame(cc.conn)
		if err != nil {
			cc.set("reading from server", err)
			return
		}
		msg, err := protocol.UnmarshalServer(payload)
		if err != nil {
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

// Close closes the underlying network connection and stops the pumps.
func (cc *ClientSocketConn) Close() error {
	if cc.cancel != nil {
		cc.cancel()
	}
	return cc.conn.Close()
}
