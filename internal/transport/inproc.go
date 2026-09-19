// Package transport provides channel and socket abstractions for client/server messaging.
package transport

import (
	"context"
)

// ClientMessage wraps any message sent from client to server.
type ClientMessage interface{}

// ServerMessage wraps any message sent from server to client.
type ServerMessage interface{}

// InProcChannel connects a client and server running in the same process.
type InProcChannel struct {
	ClientSend chan ClientMessage
	ServerSend chan ServerMessage
}

// NewInProcChannel returns a buffered in-process transport pair.
func NewInProcChannel(bufSize int) *InProcChannel {
	if bufSize <= 0 {
		bufSize = 128
	}
	return &InProcChannel{
		ClientSend: make(chan ClientMessage, bufSize),
		ServerSend: make(chan ServerMessage, bufSize),
	}
}

// SendClient sends a client message to the server, bounded by context.
func (ch *InProcChannel) SendClient(ctx context.Context, msg ClientMessage) bool {
	select {
	case ch.ClientSend <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

// SendServer sends a server message to the client, bounded by context.
func (ch *InProcChannel) SendServer(ctx context.Context, msg ServerMessage) bool {
	select {
	case ch.ServerSend <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}
