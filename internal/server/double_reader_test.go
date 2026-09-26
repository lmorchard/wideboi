package server

import (
	"context"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// Regression test for Issue #266:
// A client admitted before Run() must not receive a second reader loop when Run() starts.
func TestAdmittedTransportDoesNotGetDoubleReaderOnRun(t *testing.T) {
	owner := transport.NewInProcChannel(32)
	s := NewServer(owner, "/bin/sh", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Simulate an early client connecting (e.g. via socket before Run)
	earlyClient := transport.NewInProcChannel(32)
	s.mu.Lock()
	s.transports = append(s.transports, earlyClient)
	s.startTransportLoopLocked(ctx, earlyClient)
	if !s.startedTransports[earlyClient] {
		t.Fatal("earlyClient not marked in startedTransports")
	}
	s.mu.Unlock()

	// Start Run in a background goroutine
	runErr := make(chan error, 1)
	go func() {
		runErr <- s.Run(ctx)
	}()

	// Give Run a moment to spin up and iterate initial transports
	time.Sleep(50 * time.Millisecond)

	s.mu.Lock()
	// Both owner and earlyClient should be in startedTransports, exactly once
	if !s.startedTransports[owner] {
		t.Errorf("owner not marked in startedTransports")
	}
	if !s.startedTransports[earlyClient] {
		t.Errorf("earlyClient not marked in startedTransports")
	}
	s.mu.Unlock()

	// Verify messages sent on earlyClient are processed in exact order
	// Attach early client
	earlyClient.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})

	select {
	case <-earlyClient.ServerSend:
		// Received layout snapshot
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for attach response on earlyClient")
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil && err != context.Canceled {
			t.Errorf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run to stop")
	}
}
