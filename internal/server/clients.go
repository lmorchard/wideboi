package server

import (
	"context"
	"io"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// clientState aggregates all state tracked per connected transport.
type clientState struct {
	attached                bool
	started                 bool
	remote                  bool
	size                    protocol.MsgResize
	peerPID                 uint32
	pendingPaneCreated      []int
	pendingCreationSnapshot bool

	paneGens            map[int]uint64
	paneFrames          map[int]protocol.MsgPaneUpdate
	paneUnderlyingGens  map[int]uint64
	paneOutputGens      map[int]uint64
	clientScrollOffsets map[int]int
	paneOffsets         map[int]int
	clientUnreadOutput  map[int]bool
	paneUnreads         map[int]bool
	clientScrollGens    map[int]uint64
	paneSbLens          map[int]int
}

// newClientState constructs an initialized clientState with allocated maps.
func newClientState() *clientState {
	return &clientState{
		paneGens:            make(map[int]uint64),
		paneFrames:          make(map[int]protocol.MsgPaneUpdate),
		paneUnderlyingGens:  make(map[int]uint64),
		paneOutputGens:      make(map[int]uint64),
		clientScrollOffsets: make(map[int]int),
		paneOffsets:         make(map[int]int),
		clientUnreadOutput:  make(map[int]bool),
		paneUnreads:         make(map[int]bool),
		clientScrollGens:    make(map[int]uint64),
		paneSbLens:          make(map[int]int),
	}
}

// clientLocked returns the clientState for tp, creating it if it does not exist.
// Must be called with s.mu held.
func (s *Server) clientLocked(tp transport.Transport) *clientState {
	if s.clients == nil {
		s.clients = make(map[transport.Transport]*clientState)
	}
	cs := s.clients[tp]
	if cs == nil {
		cs = newClientState()
		s.clients[tp] = cs
	}
	return cs
}

// isAttachedLocked reports whether tp has sent MsgAttach.
func (s *Server) isAttachedLocked(tp transport.Transport) bool {
	if s.clients == nil || tp == nil {
		return false
	}
	cs := s.clients[tp]
	return cs != nil && cs.attached
}

// attachedCountLocked returns the number of currently attached clients.
func (s *Server) attachedCountLocked() int {
	count := 0
	for _, cs := range s.clients {
		if cs.attached {
			count++
		}
	}
	return count
}

func (s *Server) startTransportLoopLocked(ctx context.Context, tp transport.Transport) {
	if s.startedTransports == nil {
		s.startedTransports = make(map[transport.Transport]bool)
	}
	if s.startedTransports[tp] {
		return
	}
	s.startedTransports[tp] = true
	cs := s.clientLocked(tp)
	cs.started = true
	go s.handleClientConnLoop(ctx, tp)
}

// IsRemoteTransport reports whether tp is marked as a remote transport.
func (s *Server) IsRemoteTransport(tp transport.Transport) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remoteTransports != nil && s.remoteTransports[tp]
}

// SetRemoteTransport marks or unmarks tp as a remote transport.
func (s *Server) SetRemoteTransport(tp transport.Transport, remote bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remoteTransports == nil {
		s.remoteTransports = make(map[transport.Transport]bool)
	}
	if remote {
		s.remoteTransports[tp] = true
	} else {
		delete(s.remoteTransports, tp)
	}
	s.clientLocked(tp).remote = remote
}

// removeTransportLocked removes tp from the broadcast set and deletes its per-client state.
func (s *Server) removeTransportLocked(tp transport.Transport) {
	out := make([]transport.Transport, 0, len(s.transports))
	for _, t := range s.transports {
		if t != tp {
			out = append(out, t)
		}
	}
	s.transports = out
	delete(s.clients, tp)
	delete(s.startedTransports, tp)
	delete(s.remoteTransports, tp)
	s.forgetTrafficLocked(tp)
	if s.sizeOwner == tp {
		s.sizeOwner = nil
	}
}

// dropClient removes tp from the broadcast set and closes it. It reports
// whether tp was the owner, and clears ownership under the same lock.
func (s *Server) dropClient(ctx context.Context, tp transport.Transport) (wasOwner bool) {
	s.mu.Lock()
	attached := s.isAttachedLocked(tp)
	s.removeTransportLocked(tp)
	if tp == s.owner {
		s.owner = nil
		wasOwner = true
	}
	s.mu.Unlock()
	// Broadcast layout outside the lock to push the resized panes to remaining clients,
	// but only if the disconnected peer had attached (not a status query or probe).
	if attached {
		s.broadcastLayout(ctx)
	}
	// Close outside s.mu. Close can block, and
	// Issue #43 records holding s.mu across a
	// blocking call as the shape behind the server that
	// cannot be shut down. Close is not on the Transport
	// interface -- only the socket implementations have
	// it, InProcChannel does not -- so this is a type
	// assertion rather than a call. Without it every
	// detach leaked an fd and the socket's reader
	// goroutine on a server built to outlive its clients.
	if cl, ok := tp.(io.Closer); ok {
		_ = cl.Close()
	}
	return wasOwner
}
