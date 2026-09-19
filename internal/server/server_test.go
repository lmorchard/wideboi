package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestServerLifecycleAndAttach(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	// Send Attach
	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})

	// Expect MsgLayoutSnapshot
	select {
	case msg := <-tp.ServerSend:
		snap, ok := msg.(protocol.MsgLayoutSnapshot)
		if !ok {
			t.Fatalf("expected MsgLayoutSnapshot, got %T", msg)
		}
		if len(snap.Placements) != 2 {
			t.Fatalf("expected 2 initial placements, got %d", len(snap.Placements))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for server MsgLayoutSnapshot")
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("server close error: %v", err)
	}
}

func TestServerVerbHandling(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})
	<-tp.ServerSend // Drain initial snapshot

	// Request new column
	tp.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbNewColumn})

	select {
	case msg := <-tp.ServerSend:
		snap, ok := msg.(protocol.MsgLayoutSnapshot)
		if !ok {
			t.Fatalf("expected MsgLayoutSnapshot, got %T", msg)
		}
		if len(snap.Placements) != 2 {
			t.Fatalf("expected 2 placements after NewColumn verb, got %d", len(snap.Placements))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for MsgLayoutSnapshot after VerbNewColumn")
	}

	_ = srv.Close()
}
