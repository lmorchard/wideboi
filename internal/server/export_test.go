package server

import (
	"context"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
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

// HandleClientMsgForTest dispatches a client message for testing.
func (s *Server) HandleClientMsgForTest(ctx context.Context, tp transport.Transport, msg transport.ClientMessage) bool {
	return s.handleClientMsg(ctx, tp, msg)
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

// SpawnPane adds a new pane and column to the layout strip.
func (s *Server) SpawnPane() (int, error) {
	s.mu.Lock()
	p, err := s.spawnPaneLocked(0)
	if err != nil {
		s.mu.Unlock()
		return 0, err
	}
	s.mu.Unlock()
	s.broadcastLayout(context.Background())
	return p.ID(), nil
}

// PaneSize reports pane id's current logical dimensions, as last set by
// resizePanesLocked. ok is false if id is unknown.
func (s *Server) PaneSize(id int) (cols, rows int, ok bool) {
	s.mu.Lock()
	p, ok := s.panes[id]
	s.mu.Unlock()
	if !ok {
		return 0, 0, false
	}
	cols, rows = p.Size()
	return cols, rows, true
}

// UpdateMessage constructs a protocol.MsgPaneUpdate for wire transport at the
// current scroll offset, with unread output marked false.
func (p *Pane) UpdateMessage() (protocol.MsgPaneUpdate, bool) {
	return p.UpdateMessageForOffset(p.ScrollOffset(), false)
}

// PaneGrid returns the term.Grid for the given pane id, or nil.
func (s *Server) PaneGrid(id int) term.Grid {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.panes[id]; ok {
		return p.grid
	}
	return nil
}
