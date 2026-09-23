package transport

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"log/slog"
	"sync"

	"github.com/gorilla/websocket"
)

// WebSocketServerConn implements Transport for WebSocket client connections.
type WebSocketServerConn struct {
	conn        *websocket.Conn
	ClientSend  chan ClientMessage
	ServerSend  chan ServerMessage
	closed      chan struct{}
	closeOnce   sync.Once
	
	mu sync.Mutex // serialize all websocket conn access (gorilla is not thread-safe)
}

func NewWebSocketServerConn(conn *websocket.Conn, bufSize int) *WebSocketServerConn {
	if bufSize <= 0 {
		bufSize = 256
	}
	return &WebSocketServerConn{
		conn:        conn,
		ClientSend:  make(chan ClientMessage, bufSize),
		ServerSend:  make(chan ServerMessage, bufSize),
		closed:      make(chan struct{}),
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

			err := wsConn.sendGob(&msg)
			if err != nil {
				wsConn.set(fmt.Sprintf("encoding WebSocket server message to client %T", msg), fmt.Errorf("%w: gob %v", transportErr, err))
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
			mType, msgBytes, err := wsConn.conn.ReadMessage() // returns []byte directly (uses internal buffer)
			if err != nil {
				continue
			}

			if mType != websocket.BinaryMessage || len(msgBytes) == 0 {
				continue
			}

			var msg ServerMessage
			rErr := gob.NewDecoder(bytes.NewReader(msgBytes)).Decode(&msg)
			if rErr != nil && i > 10 {
				wsConn.set("decoding WebSocket client message", fmt.Errorf("%w: gob %v", decode_err, rErr))
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

// sendGob writes a gob-encoded message to the websocket connection.
func (wsConn *WebSocketServerConn) sendGob(v interface{}) error {
	frame, err := wsConn.conn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}

	err = gob.NewEncoder(frame).Encode(v)
	if err != nil {
		frame.Close()
		return err
	}

	err = frame.Close()
	if err != nil {
		slog.Debug("websocket frame close error", "err", err)
	}

	return nil
}

func (wsConn *WebSocketServerConn) SendClient(ctx context.Context, msg ClientMessage) bool {
	select {
	case <-wsConn.closed:
		return false
	default:
	}

	wsConn.mu.Lock()
	defer wsConn.mu.Unlock()
	select {
	case <-wsConn.closed:
		return false
	default:
	}
	select {
	case wsConn.ServerSend <- msg:
		return true
	case <-ctx.Done():
		return false
	case <-wsConn.closed:
		return false
	}
}

func (wsConn *WebSocketServerConn) SendServer(ctx context.Context, msg ServerMessage) bool {
	// No-op on the server side; messages are broadcast instead of sent per-channel
	return true
}

func (wsConn *WebSocketServerConn) ClientSendChan() <-chan ClientMessage {
	return wsConn.ClientSend
}

func (wsConn *WebSocketServerConn) ServerSendChan() <-chan ServerMessage {
	// This method doesn't apply on the server-side websocket conn, but we return a closed channel for interface consistency.
	return make(chan ServerMessage, 0)
}

func (wsConn *WebSocketServerConn) Close() error {
	wsConn.closeOnce.Do(func() { close(wsConn.closed) })
	if err := wsConn.conn.Close(); err != nil {
		slog.Debug("websocket conn close", "err", err)
	}
	return nil
}

var transportErr = fmt.Errorf("transport send error")
var decode_err = fmt.Errorf("gob decode error")

func (wsConn *WebSocketServerConn) set(where string, err error) {
	wsConn.setErr(fmt.Sprintf("%s: %w", where, err))
}

func (wsConn *WebSocketServerConn) setErr(s string) {
	if wsConn.closed != nil { return } // already closed
	// store for later logging if needed
}
