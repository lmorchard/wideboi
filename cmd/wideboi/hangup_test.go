package main

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// The ceiling bounds the whole hang-up, the send included. hangUp runs
// inside the signal guard's teardown, before the terminal is restored,
// so a send that could block forever would leave the terminal in the
// alt screen and the process deaf to the signal it caught.
func TestHangUpCeilingCoversTheSend(t *testing.T) {
	ours, theirs := net.Pipe()
	defer ours.Close()
	defer theirs.Close()
	// No pumps: nothing drains ClientSend, so once it is full a send
	// can only block.
	cc := transport.NewClientSocketConn(ours, 1)
	cc.SendClient(context.Background(), protocol.MsgResize{Cols: 1, Rows: 1})

	done := make(chan bool, 1)
	go func() { done <- hangUp(context.Background(), cc, protocol.MsgShutdown{}, 100*time.Millisecond) }()
	select {
	case ok := <-done:
		if ok {
			t.Error("hangUp reported an acknowledgement that never came")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hangUp blocked past its ceiling on a send that could not complete")
	}
}
