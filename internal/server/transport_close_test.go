package server

// White-box (package server): drives handleClientConnLoop and inspects
// s.transports directly. Same justification as pane_wedge_test.go.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/transport"
)

// closableTransport is a Transport that also implements io.Closer,
// which the socket transports do and InProcChannel does not. Close is
// not part of the Transport interface, so the server can only reach it
// through a type assertion -- which is exactly the thing under test.
type closableTransport struct {
	*transport.InProcChannel
	closed atomic.Bool
}

func (t *closableTransport) Close() error {
	t.closed.Store(true)
	return nil
}

// A detaching client's connection was dropped from s.transports and
// never closed, leaking an fd and the socket's reader goroutine on a
// server meant to outlive its clients. Close is called only from tests
// today, which is how it went unnoticed.
func TestDroppedTransportIsClosed(t *testing.T) {
	tp := &closableTransport{InProcChannel: transport.NewInProcChannel(8)}
	s := &Server{
		strip:      layout.NewStrip(),
		panes:      make(map[int]*Pane),
		stopCh:     make(chan struct{}),
		transports: []transport.Transport{tp},
	}

	// Closing the client channel is what a dropped connection looks
	// like from the server's side: the receive reports !ok.
	close(tp.ClientSend)

	done := make(chan struct{})
	go func() {
		s.handleClientConnLoop(context.Background(), tp)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleClientConnLoop did not return after its client channel closed")
	}

	if !tp.closed.Load() {
		t.Error("dropped transport was not closed; it leaks an fd and a reader goroutine per detach")
	}

	s.mu.Lock()
	remaining := len(s.transports)
	s.mu.Unlock()
	if remaining != 0 {
		t.Errorf("s.transports still holds %d entries, want 0", remaining)
	}
}

// A transport that is not an io.Closer -- InProcChannel, which is what
// the in-process binary uses -- must still be removed, and must not
// panic on the type assertion.
func TestDroppedNonClosableTransportIsStillRemoved(t *testing.T) {
	tp := transport.NewInProcChannel(8)
	s := &Server{
		strip:      layout.NewStrip(),
		panes:      make(map[int]*Pane),
		stopCh:     make(chan struct{}),
		transports: []transport.Transport{tp},
	}

	close(tp.ClientSend)

	done := make(chan struct{})
	go func() {
		s.handleClientConnLoop(context.Background(), tp)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleClientConnLoop did not return after its client channel closed")
	}

	s.mu.Lock()
	remaining := len(s.transports)
	s.mu.Unlock()
	if remaining != 0 {
		t.Errorf("s.transports still holds %d entries, want 0", remaining)
	}
}
