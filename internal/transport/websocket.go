package transport

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lmorchard/wideboi/internal/protocol"
)

const (
	webSocketReadLimit    = 1 << 20
	webSocketWriteTimeout = 10 * time.Second
	webSocketPongTimeout  = 90 * time.Second
	webSocketPingInterval = 30 * time.Second
)

// WebSocketServerConn implements Transport for WebSocket client connections.
type WebSocketServerConn struct {
	connErr
	sendStats

	conn       *websocket.Conn
	ClientSend chan ClientMessage
	ServerSend chan ServerMessage
	closed     chan struct{}
	closeOnce  sync.Once

	mu sync.Mutex // serialize all websocket conn writes
}

// NewWebSocketServerConn wraps an upgraded conn. wire counts the bytes
// written to it, normally the CountingResponseWriter the upgrade was
// given; nil leaves WireBytes at 0.
func NewWebSocketServerConn(conn *websocket.Conn, bufSize int, wire WireCounter) *WebSocketServerConn {
	if bufSize <= 0 {
		bufSize = 256
	}
	return &WebSocketServerConn{
		sendStats:  sendStats{wire: wire},
		conn:       conn,
		ClientSend: make(chan ClientMessage, bufSize),
		ServerSend: make(chan ServerMessage, bufSize),
		closed:     make(chan struct{}),
	}
}

func (wsConn *WebSocketServerConn) RunPumps(ctx context.Context) {
	go wsConn.writeLoop(ctx)
	go wsConn.readLoop(ctx)
}

func (wsConn *WebSocketServerConn) writeLoop(ctx context.Context) {
	defer wsConn.Close()
	ping := time.NewTicker(webSocketPingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-wsConn.closed:
			return
		case <-ping.C:
			wsConn.mu.Lock()
			_ = wsConn.conn.SetWriteDeadline(time.Now().Add(webSocketWriteTimeout))
			err := wsConn.conn.WriteMessage(websocket.PingMessage, nil)
			wsConn.mu.Unlock()
			if err != nil {
				wsConn.set("pinging websocket", err)
				return
			}
		case msg, ok := <-wsConn.ServerSend:
			if !ok {
				return
			}

			// One binary frame per protobuf ServerMessage; WebSocket
			// does the framing the Unix socket needs a length prefix for.
			payload, encode, err := wsConn.marshal(msg)
			if err != nil {
				// See ServerSocketConn.writeLoop: skip it, keep the peer (#175).
				slog.Error("dropping unencodable message", "type", fmt.Sprintf("%T", msg), "err", err)
				continue
			}

			wsConn.mu.Lock()
			_ = wsConn.conn.SetWriteDeadline(time.Now().Add(webSocketWriteTimeout))
			err = wsConn.conn.WriteMessage(websocket.BinaryMessage, payload)
			wsConn.mu.Unlock()

			if err != nil {
				wsConn.set(fmt.Sprintf("sending WebSocket message %T", msg), err)
				return
			}
			wsConn.record(msg, len(payload), encode)
		}
	}
}

func (wsConn *WebSocketServerConn) readLoop(ctx context.Context) {
	defer close(wsConn.ClientSend)
	defer wsConn.Close()
	wsConn.conn.SetReadLimit(webSocketReadLimit)
	_ = wsConn.conn.SetReadDeadline(time.Now().Add(webSocketPongTimeout))
	wsConn.conn.SetPongHandler(func(string) error {
		return wsConn.conn.SetReadDeadline(time.Now().Add(webSocketPongTimeout))
	})

	for {
		select {
		case <-ctx.Done():
			return
		default:
			kind, payload, err := wsConn.conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					wsConn.set("reading websocket", err)
				}
				return
			}

			// A frame we cannot decode is skipped, not fatal: one bad
			// message should not drop a browser session.
			if kind != websocket.BinaryMessage {
				slog.Warn("WebSocket received non-binary frame", "type", kind)
				continue
			}
			msg, err := protocol.UnmarshalClient(payload)
			if err != nil {
				slog.Warn("WebSocket failed to decode client message", "err", err)
				continue
			}

			select {
			case wsConn.ClientSend <- msg:
			case <-wsConn.closed:
				return
			case <-ctx.Done():
				return
			}
		}
	}
}

func (wsConn *WebSocketServerConn) SendClient(ctx context.Context, msg ClientMessage) bool {
	return false
}

func (wsConn *WebSocketServerConn) SendServer(ctx context.Context, msg ServerMessage) bool {
	select {
	case <-wsConn.closed:
		return false
	default:
	}

	// A full queue means this peer cannot keep up. Closing only this
	// connection keeps broadcasts and shutdown responsive for everyone else.
	select {
	case wsConn.ServerSend <- msg:
		return true
	case <-wsConn.closed:
		return false
	case <-ctx.Done():
		return false
	default:
		_ = wsConn.Close()
		return false
	}
}

func (wsConn *WebSocketServerConn) ClientSendChan() <-chan ClientMessage {
	return wsConn.ClientSend
}

func (wsConn *WebSocketServerConn) ServerSendChan() <-chan ServerMessage {
	return wsConn.ServerSend
}

func (wsConn *WebSocketServerConn) Close() error {
	var err error
	wsConn.closeOnce.Do(func() {
		close(wsConn.closed)
		err = wsConn.conn.Close()
	})
	return err
}
