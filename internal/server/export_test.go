package server

import (
	"context"
	"time"

	"github.com/lmorchard/wideboi/internal/transport"
)

// SetCloseGrace shortens the hangup grace for panes this server spawns.
//
// Deliberately in export_test.go, which is compiled only into the test
// binary: nothing but a test would ever turn this knob, so it should not
// be product API. The tests that assert the grace contract itself live in
// internal/server/ptyx and scripts/ptycheck.py and do not use this.
func (s *Server) SetCloseGrace(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeGrace = d
}

// AddClientForTest registers an additional client transport and runs its loop.
func (s *Server) AddClientForTest(ctx context.Context, tp transport.Transport) {
	s.mu.Lock()
	s.transports = append(s.transports, tp)
	s.mu.Unlock()
	go s.handleClientConnLoop(ctx, tp)
}

// SessionDimensions reports the server's logical cols and rows.
func (s *Server) SessionDimensions() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}

// SizeOwner reports the current size-owning transport.
func (s *Server) SizeOwner() transport.Transport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sizeOwner
}
