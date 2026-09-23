package transport

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

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

	mu sync.Mutex // serialize all websocket conn access (gorilla is not thread-safe)
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
	defer func() {
		if err := wsConn.conn.Close(); err != nil {
			slog.Debug("websocket conn close", "err", err)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-wsConn.ServerSend:
			if !ok {
				continue
			}

			err := wsConn.sendProto(msg)
			if err != nil {
				wsConn.set(fmt.Sprintf("encoding WebSocket server message to client %T", msg), fmt.Errorf("proto: %v", err))
				return
			}
		}
	}
}

func (wsConn *WebSocketServerConn) readLoop(ctx context.Context) {
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return
		default:
			mType, msgBytes, err := wsConn.conn.ReadMessage()
			if err != nil {
				wsConn.set("reading websocket", err)
				return
			}

			if mType != websocket.BinaryMessage || len(msgBytes) == 0 {
				continue
			}

			msg := &protocol.ClientEnvelope{}
			rErr := proto.Unmarshal(msgBytes, msg)
			if rErr != nil && i > 10 {
				wsConn.set("decoding WebSocket client message", fmt.Errorf("proto %v", rErr))
				return
			} else if rErr != nil {
				continue // skip decode errors; next frame might be OK
			}

			select {
			case wsConn.ClientSend <- msg:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (wsConn *WebSocketServerConn) sendProto(v proto.Message) error {
	b, err := proto.Marshal(v)
	if err != nil {
		return err
	}

	wsConn.mu.Lock()
	err = wsConn.conn.WriteMessage(websocket.BinaryMessage, b)
	wsConn.mu.Unlock()

	return err
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
