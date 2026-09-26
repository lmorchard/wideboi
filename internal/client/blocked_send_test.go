package client

import (
	"context"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

type blockingTransport struct {
	blockSend   chan struct{}
	enteredSend chan struct{}
}

func newBlockingTransport() *blockingTransport {
	return &blockingTransport{
		blockSend:   make(chan struct{}),
		enteredSend: make(chan struct{}, 1),
	}
}

func (b *blockingTransport) SendClient(ctx context.Context, msg transport.ClientMessage) bool {
	select {
	case b.enteredSend <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return false
	case <-b.blockSend:
		return true
	}
}

func (b *blockingTransport) SendServer(ctx context.Context, msg transport.ServerMessage) bool {
	return true
}

func (b *blockingTransport) ClientSendChan() <-chan transport.ClientMessage {
	return nil
}

func (b *blockingTransport) ServerSendChan() <-chan transport.ServerMessage {
	return nil
}

// Regression guard for Issue #266:
// Transport.SendClient must never be called while holding c.mu, otherwise
// a stalled transport writer freezes Client.Draw and the user's terminal UI.
func TestBlockedTransportDoesNotFreezeDraw(t *testing.T) {
	bt := newBlockingTransport()
	defer close(bt.blockSend)

	c := NewClient(bt, 80, 24, "")
	c.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 40, Height: 22},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 1. SendVerb blocks in transport.SendClient
	go func() {
		c.SendVerb(ctx, protocol.VerbCycleWidth)
	}()

	select {
	case <-bt.enteredSend:
	case <-time.After(1 * time.Second):
		t.Fatal("SendVerb did not call SendClient")
	}

	// 2. Draw should complete even while SendVerb is blocked inside SendClient
	drawDone := make(chan struct{})
	go func() {
		scr := newFakeHostScreen(80, 24)
		c.Draw(scr)
		close(drawDone)
	}()

	select {
	case <-drawDone:
		// Succeeded: Draw ran without deadlocking on c.mu!
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Draw froze while transport was blocked in SendVerb")
	}

	// 3. Test SendSplit also does not freeze Draw
	go func() {
		c.SendSplit(ctx, "echo hi", "", 1, false)
	}()

	select {
	case <-bt.enteredSend:
	case <-time.After(1 * time.Second):
		t.Fatal("SendSplit did not call SendClient")
	}

	drawDone2 := make(chan struct{})
	go func() {
		scr := newFakeHostScreen(80, 24)
		c.Draw(scr)
		close(drawDone2)
	}()

	select {
	case <-drawDone2:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Draw froze while transport was blocked in SendSplit")
	}
}
