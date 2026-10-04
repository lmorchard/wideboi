package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// keptDrainCeiling bounds how long a reaped kept pane waits for its pty
// reader to reach EOF before reporting the exit. EOF normally follows
// the reap within a read; it never comes while a background job still
// holds the pty, and the exit must not wait on that job.
const keptDrainCeiling = 3 * time.Second

// waiterSendCeiling bounds each wait answer, so a waiter whose connection
// has stalled cannot hold up the others.
const waiterSendCeiling = time.Second

// waiterDrainCeiling bounds the wait for a wait answer to reach the wire
// before the session hangs up the waiter's connection.
const waiterDrainCeiling = 500 * time.Millisecond

// watchKeptPane records a kept pane's exit and leaves the pane in place,
// screen intact, until something closes it.
//
// The exit is reported only once the child is reaped *and* the reader
// has drained the pty (or keptDrainCeiling passed). The reap can beat
// the reader to the child's final bytes, and a waiter that captures as
// soon as it hears the exit must see them.
func (s *Server) watchKeptPane(id int, p *Pane, drained <-chan struct{}) {
	select {
	case <-p.pty.Done():
	case <-p.closed:
		return
	}
	select {
	case <-drained:
	case <-time.After(keptDrainCeiling):
	case <-p.closed:
		return
	}
	code, _ := p.pty.ExitCode()
	p.markExited(code)
	s.mu.Lock()
	readySt := s.checkStatusWaitersLocked(id)
	s.mu.Unlock()
	sendReadyStatusWaiters(readySt)
	s.notifyWaiters(id, code, "")
	s.broadcastLayout(context.Background())
}

// notifyWaiters answers everyone waiting on pane id, once, and returns
// the transports it answered.
func (s *Server) notifyWaiters(id, code int, errMsg string) []transport.Transport {
	s.mu.Lock()
	tps := s.waiters[id]
	delete(s.waiters, id)
	s.mu.Unlock()
	resp := protocol.MsgWaitResponse{PaneID: id, ExitCode: code, Error: errMsg}
	for _, tp := range tps {
		ctx, cancel := context.WithTimeout(context.Background(), waiterSendCeiling)
		tp.SendServer(ctx, resp)
		cancel()
	}
	return tps
}

// drainAnswered lets wait answers just queued on tps reach the wire. A
// transport's Close drops whatever is still queued, and these answers
// are followed closely by the session hanging up.
func drainAnswered(tps []transport.Transport) {
	for _, tp := range tps {
		if d, ok := tp.(interface{ Drain(time.Duration) bool }); ok {
			d.Drain(waiterDrainCeiling)
		}
	}
}

// finishWaiters answers waiters on a pane that has been closed. Close
// hung it up and waited for the reap, so the code is normally there; a
// child that outlived the grace has none to give.
func (s *Server) finishWaiters(id int, p *Pane) []transport.Transport {
	code, reaped := p.reapedExitCode()

	s.mu.Lock()
	ows := s.outputWaiters[id]
	delete(s.outputWaiters, id)
	sws := s.statusWaiters[id]
	delete(s.statusWaiters, id)
	s.mu.Unlock()

	for _, w := range ows {
		ctx, cancel := context.WithTimeout(context.Background(), waiterSendCeiling)
		w.tp.SendServer(ctx, protocol.MsgWaitOutputResponse{
			PaneID: id,
			Error:  fmt.Sprintf("pane %d closed before matching output appeared", id),
		})
		cancel()
	}

	var exitStatus protocol.PaneStatus
	if reaped {
		if code == 0 {
			exitStatus = protocol.StatusDone
		} else {
			exitStatus = protocol.StatusFailed
		}
	}

	for _, w := range sws {
		ctx, cancel := context.WithTimeout(context.Background(), waiterSendCeiling)
		if reaped && slices.Contains(w.until, exitStatus) {
			w.tp.SendServer(ctx, protocol.MsgWaitStatusResponse{
				PaneID: id,
				Status: exitStatus,
			})
		} else {
			w.tp.SendServer(ctx, protocol.MsgWaitStatusResponse{
				PaneID: id,
				Error:  fmt.Sprintf("pane %d closed before reaching target status", id),
			})
		}
		cancel()
	}

	if reaped {
		return s.notifyWaiters(id, code, "")
	}
	return s.notifyWaiters(id, 0, fmt.Sprintf("pane %d closed before its process exited", id))
}

// hasTerminalPanesLocked reports whether any non-dashboard terminal panes remain.
func (s *Server) hasTerminalPanesLocked() bool {
	for pid, pane := range s.panes {
		if pid != s.statusPaneID && !pane.isDashboard {
			return true
		}
	}
	return false
}

// detachPaneLocked removes pane id from server data structures, updating strip
// and dashboard. It returns the removed pane and whether no terminal panes remain.
func (s *Server) detachPaneLocked(id int) (*Pane, bool) {
	p, ok := s.panes[id]
	if !ok {
		return nil, false
	}
	if id == s.statusPaneID {
		s.statusPaneID = 0
		s.dashboard = nil
	}
	s.strip.KillPane(id)
	delete(s.panes, id)
	delete(s.lastCWD, id)
	delete(s.lastUserVars, id)
	if s.unseenDone != nil {
		delete(s.unseenDone, id)
	}
	s.updateDashboardLocked()
	return p, !s.hasTerminalPanesLocked()
}

func (s *Server) onPaneExit(id int) {
	s.mu.Lock()
	p, shouldClose := s.detachPaneLocked(id)
	if p == nil {
		s.mu.Unlock()
		return
	}
	s.resizePanesLocked()
	s.mu.Unlock()

	s.broadcastLayout(context.Background())
	_ = p.Close()
	// After Close, which waits (up to the grace) for the reap.
	pid, status := p.ExitSummary()
	slog.Info("pane ended", "paneID", id, "panePID", pid, "status", status, "how", "exited")
	// Before any s.Close below, so a waiter hears the code before its
	// connection goes.
	answered := s.finishWaiters(id, p)

	if shouldClose {
		drainAnswered(answered)
		_ = s.CloseFor(ReasonLastPaneExited, "paneID", id, "panePID", pid, "status", status)
	}
}

// removePaneLocked takes pane id out of the session and closes it in the
// background. last reports that no terminal panes remain; closed closes
// once the pane is hung up and its waiters answered.
func (s *Server) removePaneLocked(id int) (last bool, closed <-chan struct{}) {
	p, last := s.detachPaneLocked(id)
	if p == nil {
		return false, nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = p.Close()
		pid, status := p.ExitSummary()
		slog.Info("pane ended", "paneID", id, "panePID", pid, "status", status, "how", "closed")
		answered := s.finishWaiters(id, p)
		if last {
			// The session closes behind this; see paneClosed.
			drainAnswered(answered)
		}
	}()
	return last, done
}

// stoppingLocked reports whether Close has begun. s.mu must be held; it
// is what orders this against Close's snapshot of panes and transports.
func (s *Server) stoppingLocked() bool {
	if s.upgrading {
		return true
	}
	select {
	case <-s.stopCh:
		return true
	default:
		return false
	}
}

func (s *Server) isStopping() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stoppingLocked()
}

// Close hangs up all panes concurrently, and then hangs up on every client.
func (s *Server) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		close(s.stopCh)
		sl := s.listener
		ws := s.webServer
		s.mu.Unlock()

		s.saveMacrosMu.Lock()
		s.saveMacrosStop = true
		if s.saveMacrosCond != nil {
			s.saveMacrosCond.Broadcast()
		}
		done := s.saveMacrosDone
		s.saveMacrosMu.Unlock()
		if done != nil {
			<-done
		}

		if ws != nil {
			ws.Close(nil)
		}
		// Stop answering first. The hangup below can take up to the
		// pane grace, and a `wideboi` that dialled in during it would
		// attach to a session about to hang up on it; with the socket
		// gone, it starts a fresh one instead.
		if sl != nil {
			_ = sl.Close()
		}

		s.mu.Lock()
		panesToClose := make([]*Pane, 0, len(s.panes))
		for _, p := range s.panes {
			panesToClose = append(panesToClose, p)
		}
		s.panes = make(map[int]*Pane)
		s.mu.Unlock()

		var wg sync.WaitGroup
		var errMu sync.Mutex
		var errs []error
		var answered []transport.Transport

		for _, p := range panesToClose {
			wg.Add(1)
			go func(p *Pane) {
				defer wg.Done()
				if err := p.Close(); err != nil {
					errMu.Lock()
					errs = append(errs, fmt.Errorf("pane %d: %w", p.ID(), err))
					errMu.Unlock()
				}
				// Before the transports close below, so `wideboi wait`
				// hears the pane's end rather than a dropped connection.
				tps := s.finishWaiters(p.ID(), p)
				errMu.Lock()
				answered = append(answered, tps...)
				errMu.Unlock()
			}(p)
		}
		wg.Wait()
		drainAnswered(answered)

		// Hang up on every client last. For a client waiting on a
		// shutdown, the closed connection is the only acknowledgement
		// it gets, so it must not arrive until the panes above have
		// been hung up.
		s.mu.Lock()
		tps := s.transports
		s.transports = nil
		s.clients = nil
		s.owner = nil
		s.mu.Unlock()
		for _, tp := range tps {
			if cl, ok := tp.(io.Closer); ok {
				_ = cl.Close()
			}
		}

		if len(errs) > 0 {
			closeErr = fmt.Errorf("server close: %v", errs)
		}
	})
	return closeErr
}
