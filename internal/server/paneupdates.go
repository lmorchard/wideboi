package server

import (
	"context"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// broadcastPaneUpdates sends each pane to every client that has not
// accepted its current view state. force sends every pane to every
// client: broadcastLayout needs that when the column set or pane sizes
// change, because a snapshot can prune a client's mirror or replace it
// with a blank one (client.go, the MsgLayoutSnapshot case). For status-
// or title-only snapshots where columns are unchanged, force is false.
func (s *Server) broadcastPaneUpdates(ctx context.Context, force bool) {
	s.paneSendMu.Lock()
	defer s.paneSendMu.Unlock()

	type clientTarget struct {
		tp            transport.Transport
		paneID        int
		pane          *Pane
		offset        int
		unread        bool
		underlyingGen uint64
		outputGen     uint64
		sbLen         int
		wireGen       uint64
		baseline      protocol.MsgPaneUpdate
		hasBase       bool
	}

	type renderKey struct {
		paneID int
		offset int
		unread bool
	}

	s.mu.Lock()
	tps := append([]transport.Transport{}, s.transports...)
	timing := s.timing

	var targets []clientTarget
	panesByID := make(map[int]*Pane, len(s.panes))
	neededRenders := make(map[renderKey]bool)

	for id, p := range s.panes {
		panesByID[id] = p
		underlyingGen := p.Generation()
		outputGen := p.OutputGen()
		sbLen := p.ScrollbackLen()

		for _, tp := range tps {
			cs := s.clientLocked(tp)
			lastWireGen, hasWireGen := cs.paneGens[id]
			lastOutputGen, hasOutputGen := cs.paneOutputGens[id]
			curOffset := p.ScrollOffset()
			if off, ok := cs.clientScrollOffsets[id]; ok {
				curOffset = off
			}
			lastOffset, hasOffset := cs.paneOffsets[id]
			curUnread := cs.clientUnreadOutput[id]
			lastUnread, hasUnread := cs.paneUnreads[id]
			lastSbLen := cs.paneSbLens[id]

			// Check if new output arrived while already scrolled up
			outputChanged := !hasOutputGen || outputGen != lastOutputGen
			if outputChanged && hasOffset && lastOffset > 0 && curOffset > 0 {
				curUnread = true
				cs.clientUnreadOutput[id] = true

				// Content pinning: if scrollback lengthened, increase offset to keep view anchored
				if sbLen > lastSbLen {
					diff := sbLen - lastSbLen
					curOffset += diff
					if curOffset > sbLen {
						curOffset = sbLen
					}
					cs.clientScrollOffsets[id] = curOffset
				}
			}

			offsetChanged := !hasOffset || curOffset != lastOffset
			unreadChanged := !hasUnread || curUnread != lastUnread

			scrollGen := cs.clientScrollGens[id]
			wireGen := underlyingGen + scrollGen

			dirty := force || !hasWireGen || (wireGen != lastWireGen) || offsetChanged || unreadChanged
			if !dirty {
				continue
			}

			baseline, hasBase := cs.paneFrames[id]
			hasBase = hasBase && hasWireGen && !force

			key := renderKey{paneID: id, offset: curOffset, unread: curUnread}
			neededRenders[key] = true

			targets = append(targets, clientTarget{
				tp:            tp,
				paneID:        id,
				pane:          p,
				offset:        curOffset,
				unread:        curUnread,
				underlyingGen: underlyingGen,
				outputGen:     outputGen,
				sbLen:         sbLen,
				wireGen:       wireGen,
				baseline:      baseline,
				hasBase:       hasBase,
			})
		}
	}
	s.mu.Unlock()

	if len(targets) == 0 {
		s.mu.Lock()
		s.cleanExitedPanesLocked()
		s.mu.Unlock()
		return
	}

	// Render distinct frames outside s.mu
	// Timing locals, folded in under s.mu below. With timing off the
	// clock is never read. Render time is the whole render call, which
	// includes waiting for a pending pane resize or the grid's write
	// lock, and counts calls that returned !ok: a high max may be a
	// wait, not rendering.
	var render, build protocol.TimingStat
	renderedFrames := make(map[renderKey]protocol.MsgPaneUpdate, len(neededRenders))
	for key := range neededRenders {
		p := panesByID[key.paneID]
		if p == nil {
			continue
		}
		var start time.Time
		if timing {
			start = time.Now()
		}
		frame, ok := p.UpdateMessageForOffset(key.offset, key.unread)
		if timing {
			render.Add(time.Since(start))
		}
		if ok {
			renderedFrames[key] = frame
		}
	}

	type result struct {
		tp            transport.Transport
		paneID        int
		pane          *Pane
		underlyingGen uint64
		outputGen     uint64
		sbLen         int
		wireGen       uint64
		offset        int
		unread        bool
		frame         protocol.MsgPaneUpdate
		message       transport.ServerMessage
		accepted      bool
	}

	var results []result
	for _, target := range targets {
		key := renderKey{paneID: target.paneID, offset: target.offset, unread: target.unread}
		frame, ok := renderedFrames[key]
		if !ok {
			continue
		}

		s.mu.Lock()
		current := s.panes[target.paneID] == target.pane
		s.mu.Unlock()
		if !current {
			continue
		}

		update := frame
		update.Generation = target.wireGen

		var message transport.ServerMessage = update
		if target.hasBase {
			var start time.Time
			if timing {
				start = time.Now()
			}
			patch, ok := protocol.BuildPanePatch(target.baseline, update)
			if timing {
				build.Add(time.Since(start))
			}
			if ok {
				message = patch
			}
		}
		accepted := target.tp.SendServer(ctx, message)
		results = append(results, result{
			tp:            target.tp,
			paneID:        target.paneID,
			pane:          target.pane,
			underlyingGen: target.underlyingGen,
			outputGen:     target.outputGen,
			sbLen:         target.sbLen,
			wireGen:       target.wireGen,
			offset:        target.offset,
			unread:        target.unread,
			frame:         update,
			message:       message,
			accepted:      accepted,
		})
	}

	// Update records under s.mu
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renderTiming.Merge(render)
	s.buildTiming.Merge(build)

	present := make(map[transport.Transport]bool, len(s.transports))
	for _, tp := range s.transports {
		present[tp] = true
	}

	for _, r := range results {
		// A client dropped mid-send must not be re-added, and a pane
		// that exited mid-send has nothing left to track. Its
		// deliveries from this tick are not counted either: its entry
		// has already folded into Departed, so traffic undercounts by
		// at most one tick per departure, which is accepted.
		if !present[r.tp] {
			continue
		}
		// Count before the pane check: a failure to a live client
		// counts even if the pane has since exited.
		s.recordSendLocked(r.tp, r.message, r.accepted)
		if s.panes[r.paneID] != r.pane {
			continue
		}
		cs := s.clients[r.tp]
		if cs == nil {
			continue
		}
		if !r.accepted {
			delete(cs.paneGens, r.paneID)
			delete(cs.paneFrames, r.paneID)
			delete(cs.paneUnderlyingGens, r.paneID)
			delete(cs.paneOutputGens, r.paneID)
			delete(cs.paneSbLens, r.paneID)
			continue
		}
		// Content can change while update was being rendered or sent.
		// Retain r.frame and r.wireGen as the baseline: the client accepted and
		// applied exactly this frame. On the next tick, wireGen != lastWireGen
		// will trigger a resend as a patch against this baseline.
		cs.paneGens[r.paneID] = r.wireGen
		cs.paneFrames[r.paneID] = r.frame
		cs.paneUnderlyingGens[r.paneID] = r.underlyingGen
		cs.paneOutputGens[r.paneID] = r.outputGen
		cs.paneSbLens[r.paneID] = r.sbLen
		cs.paneOffsets[r.paneID] = r.offset
		cs.paneUnreads[r.paneID] = r.unread
	}

	s.cleanExitedPanesLocked()
}

func (s *Server) cleanExitedPanesLocked() {
	for _, cs := range s.clients {
		for id := range cs.paneGens {
			if _, ok := s.panes[id]; !ok {
				delete(cs.paneGens, id)
			}
		}
		for id := range cs.paneFrames {
			if _, ok := s.panes[id]; !ok {
				delete(cs.paneFrames, id)
			}
		}
		for id := range cs.paneUnderlyingGens {
			if _, ok := s.panes[id]; !ok {
				delete(cs.paneUnderlyingGens, id)
			}
		}
		for id := range cs.paneOutputGens {
			if _, ok := s.panes[id]; !ok {
				delete(cs.paneOutputGens, id)
			}
		}
		for id := range cs.clientScrollOffsets {
			if _, ok := s.panes[id]; !ok {
				delete(cs.clientScrollOffsets, id)
			}
		}
		for id := range cs.paneOffsets {
			if _, ok := s.panes[id]; !ok {
				delete(cs.paneOffsets, id)
			}
		}
		for id := range cs.clientUnreadOutput {
			if _, ok := s.panes[id]; !ok {
				delete(cs.clientUnreadOutput, id)
			}
		}
		for id := range cs.paneUnreads {
			if _, ok := s.panes[id]; !ok {
				delete(cs.paneUnreads, id)
			}
		}
		for id := range cs.clientScrollGens {
			if _, ok := s.panes[id]; !ok {
				delete(cs.clientScrollGens, id)
			}
		}
		for id := range cs.paneSbLens {
			if _, ok := s.panes[id]; !ok {
				delete(cs.paneSbLens, id)
			}
		}
	}
}
