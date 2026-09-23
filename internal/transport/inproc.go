// Package transport provides channel and socket abstractions for client/server messaging.
package transport

import (
	"context"

	"github.com/lmorchard/wideboi/internal/protocol"
)

// ClientMessage wraps any message sent from client to server.
type ClientMessage = *protocol.ClientEnvelope

// ServerMessage wraps any message sent from server to client.
type ServerMessage = *protocol.ServerEnvelope

// Transport represents a bi-directional communication channel between client and server.
type Transport interface {
	SendClient(ctx context.Context, msg ClientMessage) bool
	SendServer(ctx context.Context, msg ServerMessage) bool
	ClientSendChan() <-chan ClientMessage
	ServerSendChan() <-chan ServerMessage
}

// InProcChannel connects a client and server running in the same process.
type InProcChannel struct {
	ClientSend chan ClientMessage
	ServerSend chan ServerMessage
}

// ClientSendChan returns the channel where client messages arrive.
func (ch *InProcChannel) ClientSendChan() <-chan ClientMessage {
	return ch.ClientSend
}

// ServerSendChan returns the channel where server messages arrive.
func (ch *InProcChannel) ServerSendChan() <-chan ServerMessage {
	return ch.ServerSend
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

// SendServer sends a server message to the client, non-blocking if channel is full.
func (ch *InProcChannel) SendServer(ctx context.Context, msg ServerMessage) bool {
	select {
	case ch.ServerSend <- msg:
		return true
	default:
		return false
	}
}
