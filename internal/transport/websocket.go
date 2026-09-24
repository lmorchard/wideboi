package transport

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// WebSocketServerConn implements Transport for WebSocket client connections.
type WebSocketServerConn struct {
	connErr

	conn       *websocket.Conn
	ClientSend chan ClientMessage
	ServerSend chan ServerMessage
	closed     chan struct{}
	closeOnce  sync.Once

	mu sync.Mutex // serialize all websocket conn writes
}

func NewWebSocketServerConn(conn *websocket.Conn, bufSize int) *WebSocketServerConn {
	if bufSize <= 0 {
		bufSize = 256
	}
	return &WebSocketServerConn{
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

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-wsConn.ServerSend:
			if !ok {
				continue
			}

			payloadBytes, err := protocol.MarshalServer(msg)
			if err != nil {
				wsConn.set("encoding WebSocket payload", err)
				return
			}

			wsConn.mu.Lock()
			err = wsConn.conn.WriteMessage(websocket.BinaryMessage, payloadBytes)
			wsConn.mu.Unlock()

			if err != nil {
				wsConn.set(fmt.Sprintf("sending WebSocket message %T", msg), err)
				return
			}
		}
	}
}

func (wsConn *WebSocketServerConn) readLoop(ctx context.Context) {
	defer close(wsConn.ClientSend)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			messageType, payload, err := wsConn.conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					wsConn.set("reading websocket", err)
				}
				return
			}

			if messageType != websocket.BinaryMessage {
				slog.Warn("WebSocket received non-binary frame", "type", messageType)
				continue
			}
			msg, err := protocol.UnmarshalClient(payload)
			if err != nil {
				slog.Warn("WebSocket failed to unmarshal client message", "err", err)
				continue
			}

			select {
			case wsConn.ClientSend <- msg:
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

	select {
	case wsConn.ServerSend <- msg:
		return true
	case <-wsConn.closed:
		return false
	case <-ctx.Done():
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
	wsConn.closeOnce.Do(func() { close(wsConn.closed) })
	return wsConn.conn.Close()
}
