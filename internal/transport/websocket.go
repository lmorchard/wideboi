package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
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

// WSEnvelope represents the JSON payload format sent over WebSockets.
type WSEnvelope struct {
	Type    string          `json:"t"`
	Payload json.RawMessage `json:"p"`
}

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

			payloadBytes, err := json.Marshal(msg)
			if err != nil {
				wsConn.set("encoding WebSocket payload", err)
				return
			}

			typeName := reflect.TypeOf(msg).Name()
			if typeName == "" && reflect.TypeOf(msg).Kind() == reflect.Ptr {
				typeName = reflect.TypeOf(msg).Elem().Name()
			}

			env := WSEnvelope{
				Type:    typeName,
				Payload: payloadBytes,
			}

			wsConn.mu.Lock()
			_ = wsConn.conn.SetWriteDeadline(time.Now().Add(webSocketWriteTimeout))
			err = wsConn.conn.WriteJSON(env)
			wsConn.mu.Unlock()

			if err != nil {
				wsConn.set(fmt.Sprintf("sending WebSocket message %T", msg), err)
				return
			}
		}
	}
}

var clientTypes = map[string]func() any{
	"MsgAttach":   func() any { return &protocol.MsgAttach{} },
	"MsgVerb":     func() any { return &protocol.MsgVerb{} },
	"MsgMouse":    func() any { return &protocol.MsgMouse{} },
	"MsgInput":    func() any { return &protocol.MsgInput{} },
	"MsgResize":   func() any { return &protocol.MsgResize{} },
	"MsgScroll":   func() any { return &protocol.MsgScroll{} },
	"MsgDetach":   func() any { return &protocol.MsgDetach{} },
	"MsgShutdown": func() any { return &protocol.MsgShutdown{} },
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
			var env WSEnvelope
			err := wsConn.conn.ReadJSON(&env)
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					wsConn.set("reading websocket", err)
				}
				return
			}

			factory, ok := clientTypes[env.Type]
			if !ok {
				slog.Warn("WebSocket received unknown message type", "type", env.Type)
				continue
			}

			ptr := factory()
			if err := json.Unmarshal(env.Payload, ptr); err != nil {
				slog.Warn("WebSocket failed to unmarshal message payload", "type", env.Type, "err", err)
				continue
			}

			msg := reflect.ValueOf(ptr).Elem().Interface()

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
