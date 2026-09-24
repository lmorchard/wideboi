package server

import (
	"context"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestUnchangedClientResizeDoesNotBroadcast(t *testing.T) {
	tp := transport.NewInProcChannel(8)
	s := NewServer(tp, "/bin/sh", "")
	s.clientSizes[tp] = protocol.MsgResize{Cols: 80, Rows: 24}
	s.cols, s.rows = 80, 24

	s.handleClientMsg(context.Background(), tp, protocol.MsgResize{Cols: 80, Rows: 24})
	if got := len(tp.ServerSend); got != 0 {
		t.Fatalf("unchanged resize broadcast %d messages", got)
	}
}
