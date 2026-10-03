package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

type msgEffects struct {
	needBroadcast       bool
	needPaneBroadcast   bool
	sendLayout          bool
	sendMetadata        bool
	sendMacros          bool
	sendConfig          bool
	needMacrosBroadcast bool
	resyncPaneID        int
	createdPaneID       int
	focusTargetID       int
	closeServer         bool
	closedPaneID        int
	paneClosed          <-chan struct{}

	trafficReport *protocol.MsgTrafficStats
	historyPane   *Pane
	splitResp     *protocol.MsgSplitResponse
	sendResp      *protocol.MsgSendInputResponse
	captureResp   *protocol.MsgCaptureResponse
	closeResp     *protocol.MsgClosePaneResponse
	renameResp    *protocol.MsgRenamePaneResponse
	dumpResp      *protocol.MsgDumpPaneResponse
	pipeResp      *protocol.MsgPipePaneResponse
	waitResp      *protocol.MsgWaitResponse
	webReq        *protocol.MsgWebServerControlRequest

	detachClient   bool
	shutdownServer bool
	upgradeExecFn  func() error
	upgradeError   string
}

func (s *Server) handleClientConnLoop(ctx context.Context, tp transport.Transport) {
	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	for {
		select {
		case <-connCtx.Done():
			return
		case msg, ok := <-tp.ClientSendChan():
			if !ok {
				connCancel()
				if s.dropClient(ctx, tp) {
					s.ownerLeftWithoutDetaching()
				}
				return
			}
			if s.rejectRemoteClientMsg(connCtx, tp, msg) {
				continue
			}
			if s.handleClientMsg(connCtx, tp, msg) {
				return
			}
		}
	}
}

// ownerLeftWithoutDetaching handles the owner's connection ending with
// no MsgDetach before it: its terminal hung up without a word reaching
// us, or it was killed outright. dropClient has already given up
// ownership, so a kept session is ownerless exactly as after a detach.
func (s *Server) ownerLeftWithoutDetaching() {
	s.mu.Lock()
	keep, lost := s.keepOnOwnerLoss, s.onOwnerLost
	s.mu.Unlock()
	if !keep {
		slog.Info("owning client left without detaching; ending the session")
		_ = s.CloseFor(ReasonOwnerLeft)
		return
	}
	slog.Info("owning client left without detaching; keeping the session")
	if lost != nil {
		lost()
	}
}

// rejectRemoteClientMsg checks whether msg is a session-control message
// restricted to local peers. If tp is remote and msg is restricted, it emits
// a warning, sends an error response if the message supports one, and returns true.
func (s *Server) rejectRemoteClientMsg(ctx context.Context, tp transport.Transport, msg transport.ClientMessage) bool {
	if !s.IsRemoteTransport(tp) {
		return false
	}
	pid := s.peerPID(tp)
	switch msg.(type) {
	case protocol.MsgShutdown, *protocol.MsgShutdown:
		slog.Warn("refusing MsgShutdown from remote peer", "peerPID", pid)
		return true
	case protocol.MsgUpgradeRequest, *protocol.MsgUpgradeRequest:
		slog.Warn("refusing MsgUpgradeRequest from remote peer", "peerPID", pid)
		tp.SendServer(ctx, protocol.MsgUpgradeResponse{Error: "upgrade is restricted to local peers"})
		return true
	case protocol.MsgWebServerControlRequest, *protocol.MsgWebServerControlRequest:
		slog.Warn("refusing MsgWebServerControlRequest from remote peer", "peerPID", pid)
		tp.SendServer(ctx, protocol.MsgWebServerControlResponse{Error: "web server control is restricted to local peers"})
		return true
	}
	return false
}

// handleClientMsg dispatches msg to the appropriate handler and applies
// resulting effects outside s.mu. It returns true if the connection loop
// should terminate (detach, shutdown, upgrade).
func (s *Server) handleClientMsg(ctx context.Context, tp transport.Transport, msg transport.ClientMessage) (done bool) {
	if m, ok := msg.(protocol.MsgUpgradeRequest); ok {
		return s.applyEffects(ctx, tp, s.handleUpgradeRequest(tp, m))
	}

	s.mu.Lock()
	if s.upgrading {
		// Keystrokes for a pty pane still reach the child: the pty
		// survives exec (CLOEXEC is cleared) and execFn drains queued
		// keys before exec. Everything else mutates state that is being
		// serialized, or is re-sent by the client on reconnect (MsgAttach
		// carries its size).
		in, ok := msg.(protocol.MsgInput)
		if !ok || !s.isPtyPaneLocked(in.PaneID) {
			s.mu.Unlock()
			return false
		}
	}

	var eff msgEffects
	switch m := msg.(type) {
	case protocol.MsgShutdown:
		eff = s.handleShutdownLocked(tp, m)
	case protocol.MsgDetach:
		eff = s.handleDetachLocked(tp, m)
	case protocol.MsgSplitRequest:
		eff = s.handleSplitRequestLocked(tp, m)
	case protocol.MsgSendInputRequest:
		eff = s.handleSendInputRequestLocked(tp, m)
	case protocol.MsgCaptureRequest:
		eff = s.handleCaptureRequestLocked(tp, m)
	case protocol.MsgClosePaneRequest:
		eff = s.handleClosePaneRequestLocked(tp, m)
	case protocol.MsgRenamePaneRequest:
		eff = s.handleRenamePaneRequestLocked(tp, m)
	case protocol.MsgDumpPaneRequest:
		eff = s.handleDumpPaneRequestLocked(tp, m)
	case protocol.MsgPipePaneRequest:
		eff = s.handlePipePaneRequestLocked(ctx, tp, m)
	case protocol.MsgWaitRequest:
		eff = s.handleWaitRequestLocked(tp, m)
	case protocol.MsgPaneResync:
		eff = s.handlePaneResyncLocked(tp, m)
	case protocol.MsgStatusRequest:
		eff = s.handleStatusRequestLocked(tp, m)
	case protocol.MsgTrafficRequest:
		eff = s.handleTrafficRequestLocked(tp, m)
	case protocol.MsgHistoryRequest:
		eff = s.handleHistoryRequestLocked(tp, m)
	case protocol.MsgAttach:
		eff = s.handleAttachLocked(tp, m)
	case protocol.MsgResize:
		eff = s.handleResizeLocked(tp, m)
	case protocol.MsgSetPaneWidth:
		eff = s.handleSetPaneWidthLocked(tp, m)
	case protocol.MsgVerb:
		eff = s.handleVerbLocked(tp, m)
	case protocol.MsgInput:
		eff = s.handleInputLocked(tp, m)
	case protocol.MsgMouse:
		eff = s.handleMouseLocked(tp, m)
	case protocol.MsgScroll:
		eff = s.handleScrollLocked(tp, m)
	case protocol.MsgSaveMacros:
		eff = s.handleSaveMacrosLocked(tp, m)
	case protocol.MsgWebServerControlRequest:
		eff = s.handleWebServerControlRequestLocked(tp, m)
	}

	if tp != nil && eff.createdPaneID != 0 {
		cs := s.clientLocked(tp)
		cs.pendingPaneCreated = append(cs.pendingPaneCreated, eff.createdPaneID)
		cs.pendingCreationSnapshot = true
	}
	s.mu.Unlock()

	return s.applyEffects(ctx, tp, eff)
}

func (s *Server) handleShutdownLocked(tp transport.Transport, m protocol.MsgShutdown) msgEffects {
	return msgEffects{shutdownServer: true}
}

func (s *Server) handleDetachLocked(tp transport.Transport, m protocol.MsgDetach) msgEffects {
	return msgEffects{detachClient: true}
}

func (s *Server) handleUpgradeRequest(tp transport.Transport, m protocol.MsgUpgradeRequest) msgEffects {
	execFn, err := s.PrepareUpgrade(m.BinPath)
	if err != nil {
		return msgEffects{upgradeError: err.Error()}
	}
	return msgEffects{upgradeExecFn: execFn}
}

func (s *Server) handleSplitRequestLocked(tp transport.Transport, m protocol.MsgSplitRequest) msgEffects {
	var eff msgEffects
	if m.AfterPaneID > 0 {
		if _, ok := s.panes[m.AfterPaneID]; !ok {
			eff.splitResp = &protocol.MsgSplitResponse{Error: fmt.Sprintf("pane %d not found", m.AfterPaneID)}
			return eff
		}
	}
	spec := StartupPane{Command: m.Command, Dir: m.Cwd, Keep: m.Keep}
	p, err := s.spawnPaneWithSpecLocked(spec, m.AfterPaneID)
	if err != nil {
		eff.splitResp = &protocol.MsgSplitResponse{Error: err.Error()}
	} else {
		eff.splitResp = &protocol.MsgSplitResponse{PaneID: p.ID()}
		eff.createdPaneID = p.ID()
		eff.focusTargetID = p.ID()
		eff.needBroadcast = true
		// A server that split auto-spawned never sees an attach,
		// which is otherwise what marks startup done; without this
		// its clean exit would skip auto-cleanup.
		s.startupComplete = true
	}
	return eff
}

func (s *Server) handleSendInputRequestLocked(tp transport.Transport, m protocol.MsgSendInputRequest) msgEffects {
	var eff msgEffects
	p, ok := s.panes[m.PaneID]
	if !ok {
		eff.sendResp = &protocol.MsgSendInputResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d not found", m.PaneID)}
	} else if _, exited := p.ExitStatus(); exited {
		eff.sendResp = &protocol.MsgSendInputResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d has exited", m.PaneID)}
	} else {
		if _, err := p.Write(m.Data); err != nil {
			eff.sendResp = &protocol.MsgSendInputResponse{PaneID: m.PaneID, Error: err.Error()}
		} else {
			eff.sendResp = &protocol.MsgSendInputResponse{PaneID: m.PaneID}
		}
	}
	return eff
}

func (s *Server) handleCaptureRequestLocked(tp transport.Transport, m protocol.MsgCaptureRequest) msgEffects {
	var eff msgEffects
	p, ok := s.panes[m.PaneID]
	if !ok {
		eff.captureResp = &protocol.MsgCaptureResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d not found", m.PaneID)}
	} else {
		text := p.CaptureText(m.Scrollback, m.Lines)
		eff.captureResp = &protocol.MsgCaptureResponse{PaneID: m.PaneID, Text: text}
	}
	return eff
}

func (s *Server) handleDumpPaneRequestLocked(tp transport.Transport, m protocol.MsgDumpPaneRequest) msgEffects {
	var eff msgEffects
	p, ok := s.panes[m.PaneID]
	if !ok {
		eff.dumpResp = &protocol.MsgDumpPaneResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d not found", m.PaneID)}
		return eff
	}
	if m.CountOnly {
		_, total := p.DumpText(m.Scrollback, 0, 0, 0, false)
		eff.dumpResp = &protocol.MsgDumpPaneResponse{
			PaneID:     m.PaneID,
			TotalLines: total,
		}
		return eff
	}
	text, total := p.DumpText(m.Scrollback, m.Offset, m.Limit, m.TailLines, m.ANSI)
	eff.dumpResp = &protocol.MsgDumpPaneResponse{
		PaneID:     m.PaneID,
		Text:       text,
		TotalLines: total,
		Offset:     m.Offset,
		Lines:      m.Limit,
	}
	return eff
}

func (s *Server) handlePipePaneRequestLocked(ctx context.Context, tp transport.Transport, m protocol.MsgPipePaneRequest) msgEffects {
	var eff msgEffects
	p, ok := s.panes[m.PaneID]
	if !ok {
		eff.pipeResp = &protocol.MsgPipePaneResponse{
			PaneID: m.PaneID,
			Error:  fmt.Sprintf("pane %d not found", m.PaneID),
		}
		return eff
	}

	ch := make(chan []byte, 256)
	tapID, ok := p.AddTap(ch)
	if !ok {
		eff.pipeResp = &protocol.MsgPipePaneResponse{
			PaneID: m.PaneID,
			Closed: true,
		}
		return eff
	}

	go func() {
		defer p.RemoveTap(tapID)
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-ch:
				if !ok {
					if tp != nil {
						tp.SendServer(context.Background(), protocol.MsgPipePaneResponse{
							PaneID: m.PaneID,
							Closed: true,
						})
					}
					return
				}
				if tp == nil || !tp.SendServer(ctx, protocol.MsgPipePaneResponse{
					PaneID: m.PaneID,
					Data:   chunk,
				}) {
					return
				}
			}
		}
	}()

	return eff
}

func (s *Server) handleClosePaneRequestLocked(tp transport.Transport, m protocol.MsgClosePaneRequest) msgEffects {
	var eff msgEffects
	if _, ok := s.panes[m.PaneID]; !ok {
		eff.closeResp = &protocol.MsgClosePaneResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d not found", m.PaneID)}
	} else {
		eff.closeServer, eff.paneClosed = s.removePaneLocked(m.PaneID)
		eff.closedPaneID = m.PaneID
		eff.needBroadcast = true
		eff.closeResp = &protocol.MsgClosePaneResponse{PaneID: m.PaneID}
	}
	return eff
}

func (s *Server) handleRenamePaneRequestLocked(tp transport.Transport, m protocol.MsgRenamePaneRequest) msgEffects {
	var eff msgEffects
	p, ok := s.panes[m.PaneID]
	if !ok {
		eff.renameResp = &protocol.MsgRenamePaneResponse{
			PaneID: m.PaneID,
			Error:  fmt.Sprintf("pane %d not found", m.PaneID),
		}
		return eff
	}
	if m.Clear || m.Title == "" {
		p.ClearCustomTitle()
	} else {
		p.SetCustomTitle(m.Title)
	}
	s.updateDashboardLocked()
	eff.needBroadcast = true
	eff.renameResp = &protocol.MsgRenamePaneResponse{
		PaneID: m.PaneID,
	}
	return eff
}

func (s *Server) totalWaitersLocked() int {
	total := 0
	for _, ws := range s.waiters {
		total += len(ws)
	}
	return total
}

func (s *Server) handleWaitRequestLocked(tp transport.Transport, m protocol.MsgWaitRequest) msgEffects {
	var eff msgEffects
	p, ok := s.panes[m.PaneID]
	if !ok {
		eff.waitResp = &protocol.MsgWaitResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d not found", m.PaneID)}
		return eff
	}
	if code, exited := p.ExitStatus(); exited {
		eff.waitResp = &protocol.MsgWaitResponse{PaneID: m.PaneID, ExitCode: code}
		return eff
	}
	if s.waiters == nil {
		s.waiters = make(map[int][]transport.Transport)
	}
	for _, existing := range s.waiters[m.PaneID] {
		if existing == tp {
			return eff
		}
	}
	if len(s.waiters[m.PaneID]) >= maxWaitersPerPane || s.totalWaitersLocked() >= maxTotalWaiters {
		eff.waitResp = &protocol.MsgWaitResponse{
			PaneID: m.PaneID,
			Error:  "wait registration limit exceeded",
		}
		return eff
	}
	s.waiters[m.PaneID] = append(s.waiters[m.PaneID], tp)
	return eff
}

func (s *Server) handlePaneResyncLocked(tp transport.Transport, m protocol.MsgPaneResync) msgEffects {
	s.trafficLocked(tp).counts.ResyncRequests++
	return msgEffects{resyncPaneID: m.PaneID}
}

func (s *Server) handleStatusRequestLocked(tp transport.Transport, m protocol.MsgStatusRequest) msgEffects {
	return msgEffects{sendLayout: true, sendMetadata: true}
}

func (s *Server) handleTrafficRequestLocked(tp transport.Transport, m protocol.MsgTrafficRequest) msgEffects {
	report := s.trafficReportLocked()
	return msgEffects{trafficReport: &report}
}

func (s *Server) handleHistoryRequestLocked(tp transport.Transport, m protocol.MsgHistoryRequest) msgEffects {
	var eff msgEffects
	if tp != nil {
		eff.historyPane = s.panes[m.PaneID]
	}
	return eff
}

func (s *Server) handleAttachLocked(tp transport.Transport, m protocol.MsgAttach) msgEffects {
	s.startupComplete = true
	s.markAttachedLocked(tp)
	cs := s.clientLocked(tp)
	if validGeometry(m.Cols, m.Rows) {
		cs.size = protocol.MsgResize{Cols: m.Cols, Rows: m.Rows}
		if s.sizeOwner == nil && (s.rows == 0 || s.attachedCountLocked() == 1) {
			s.sizeOwner = tp
			s.cols = m.Cols
			s.rows = m.Rows
		}
	}
	if len(s.panes) == 0 {
		if len(s.startup) == 0 {
			_, _ = s.spawnPaneLocked(0)
			_, _ = s.spawnPaneLocked(0)
			s.strip.FocusLeft()
		} else if !s.startupLaunched {
			s.startupLaunched = true
			firstID := 0
			var firstInteractiveID int
			for _, spec := range s.startup {
				var p *Pane
				var err error
				if spec.IsDashboard() {
					if s.statusPaneID != 0 {
						slog.Warn("ignoring duplicate dashboard pane in startup configuration")
						continue
					}
					p, err = s.spawnDashboardPaneWithWidthLocked(spec.Width, 0)
				} else {
					p, err = s.spawnPaneWithSpecLocked(spec, 0)
					if err == nil && firstInteractiveID == 0 {
						firstInteractiveID = p.ID()
					}
				}
				if err != nil {
					slog.Error("starting configured pane", "command", spec.Command, "err", err)
					continue
				}
				if spec.Pinned {
					s.strip.PinColumn(p.ID())
				}
				if firstID == 0 {
					firstID = p.ID()
				}
			}
			if firstInteractiveID != 0 {
				s.strip.FocusPaneID(firstInteractiveID)
			} else if firstID != 0 {
				s.strip.FocusPaneID(firstID)
			}
		}
	}
	s.resizePanesLocked()
	return msgEffects{
		needBroadcast: true,
		sendMetadata:  true,
		sendMacros:    true,
		sendConfig:    true,
	}
}

func (s *Server) handleResizeLocked(tp transport.Transport, m protocol.MsgResize) msgEffects {
	var eff msgEffects
	if validGeometry(m.Cols, m.Rows) {
		cs := s.clientLocked(tp)
		cs.size = protocol.MsgResize{Cols: m.Cols, Rows: m.Rows}
		if s.sizeOwner == nil && s.rows == 0 {
			s.sizeOwner = tp
			s.cols = m.Cols
			s.rows = m.Rows
			s.resizePanesLocked()
			eff.needBroadcast = true
		} else {
			if s.sizeOwner == nil && s.attachedCountLocked() <= 1 && len(s.transports) <= 1 {
				s.sizeOwner = tp
			}
			if s.sizeOwner == tp {
				oldCols, oldRows := s.cols, s.rows
				s.cols, s.rows = m.Cols, m.Rows
				if s.cols != oldCols || s.rows != oldRows {
					s.resizePanesLocked()
					eff.needBroadcast = true
				}
			}
		}
	}
	return eff
}

func (s *Server) handleSetPaneWidthLocked(tp transport.Transport, m protocol.MsgSetPaneWidth) msgEffects {
	var eff msgEffects
	if (tp == s.sizeOwner || !s.isAttachedLocked(tp)) && m.Width >= layout.MinColumnWidth && m.Width <= layout.MaxColumnWidth {
		if old, ok := s.strip.ColumnWidth(m.PaneID); ok && old != m.Width {
			s.strip.SetColumnWidth(m.PaneID, m.Width)
			s.resizePanesLocked()
			eff.needBroadcast = true
		}
	}
	return eff
}

func (s *Server) handleVerbLocked(tp transport.Transport, m protocol.MsgVerb) msgEffects {
	var eff msgEffects
	switch m.Verb {
	case protocol.VerbNewColumn:
		if p, err := s.spawnPaneLocked(m.PaneID); err == nil {
			eff.createdPaneID = p.ID()
		}
		s.resizePanesLocked()
	case protocol.VerbCycleWidth:
		if tp == s.sizeOwner {
			s.strip.CycleWidth(m.PaneID)
			s.resizePanesLocked()
			eff.needBroadcast = true
		}
	case protocol.VerbGrowWidth:
		if tp == s.sizeOwner {
			s.strip.GrowWidth(m.PaneID, 10)
			s.resizePanesLocked()
			eff.needBroadcast = true
		}
	case protocol.VerbShrinkWidth:
		if tp == s.sizeOwner {
			s.strip.ShrinkWidth(m.PaneID, 10)
			s.resizePanesLocked()
			eff.needBroadcast = true
		}
	case protocol.VerbMoveLeft:
		s.strip.MoveLeft(m.PaneID)
	case protocol.VerbMoveRight:
		// Neither move calls resizePanesLocked: order is presentation,
		// and a column's width goes wherever the column goes.
		s.strip.MoveRight(m.PaneID)
	case protocol.VerbKillPane:
		if _, ok := s.panes[m.PaneID]; ok {
			eff.closeServer, eff.paneClosed = s.removePaneLocked(m.PaneID)
			eff.closedPaneID = m.PaneID
			s.resizePanesLocked()
		}
	case protocol.VerbToggleStatus:
		if s.statusPaneID != 0 {
			eff.focusTargetID = s.statusPaneID
		} else {
			if p, err := s.spawnDashboardPaneLocked(m.PaneID); err == nil {
				eff.createdPaneID = p.ID()
				eff.focusTargetID = p.ID()
			}
			s.resizePanesLocked()
			s.updateDashboardLocked()
		}
	case protocol.VerbClaimSize:
		valid := true
		for id, width := range m.Widths {
			if width < layout.MinColumnWidth || width > layout.MaxColumnWidth {
				valid = false
				break
			}
			if _, ok := s.strip.ColumnWidth(id); !ok {
				valid = false
				break
			}
		}
		if !valid {
			break
		}
		s.sizeOwner = tp
		for id, width := range m.Widths {
			s.strip.SetColumnWidth(id, width)
		}
		cs := s.clientLocked(tp)
		if validGeometry(cs.size.Cols, cs.size.Rows) {
			oldCols, oldRows := s.cols, s.rows
			s.cols, s.rows = cs.size.Cols, cs.size.Rows
			if s.cols != oldCols || s.rows != oldRows || len(m.Widths) > 0 {
				s.resizePanesLocked()
			}
		} else if len(m.Widths) > 0 {
			s.resizePanesLocked()
		}
	case protocol.VerbTogglePin:
		s.strip.TogglePinColumn(m.PaneID)
		s.resizePanesLocked()
	case protocol.VerbPinPane:
		s.strip.PinColumn(m.PaneID)
		s.resizePanesLocked()
	case protocol.VerbUnpinPane:
		s.strip.UnpinColumn(m.PaneID)
		s.resizePanesLocked()
	case protocol.VerbToggleCards:
		// Reserved: layout is the client's (#92).
	}

	// Every other verb changes the strip. The reserved one changes
	// nothing, and a snapshot costs a forced resend of every pane to
	// every client, so it must not broadcast.
	if m.Verb != protocol.VerbToggleCards && (m.Verb != protocol.VerbToggleStatus || eff.createdPaneID != 0) {
		eff.needBroadcast = true
	}
	return eff
}

// isPtyPaneLocked reports whether id is a pane backed by a pty.
func (s *Server) isPtyPaneLocked(id int) bool {
	p, ok := s.panes[id]
	return ok && p.pty != nil && id != s.statusPaneID
}

func (s *Server) handleInputLocked(tp transport.Transport, m protocol.MsgInput) msgEffects {
	var eff msgEffects
	if m.PaneID == s.statusPaneID && s.dashboard != nil {
		targetID, handled := s.dashboard.HandleInput(m)
		if handled {
			if targetID > 0 {
				eff.focusTargetID = targetID
			}
			s.updateDashboardLocked()
			eff.needPaneBroadcast = true
		}
		return eff
	}
	if p, ok := s.panes[m.PaneID]; ok {
		if tp != nil {
			cs := s.clientLocked(tp)
			if cs.clientScrollOffsets[m.PaneID] > 0 {
				cs.clientScrollOffsets[m.PaneID] = 0
				delete(cs.clientUnreadOutput, m.PaneID)
				cs.clientScrollGens[m.PaneID]++
				eff.needPaneBroadcast = true
			}
		}
		if len(m.Data) > 0 {
			data := m.Data
			if m.Paste && p.grid != nil && p.grid.BracketedPaste() {
				data = append([]byte("\x1b[200~"), append(data, []byte("\x1b[201~")...)...)
			}
			p.SendBytes(data)
		} else if !m.Key.IsZero() {
			p.SendKey(m.Key.Decode())
		}
	}
	return eff
}

func (s *Server) handleMouseLocked(tp transport.Transport, m protocol.MsgMouse) msgEffects {
	var eff msgEffects
	if m.PaneID == s.statusPaneID && s.dashboard != nil {
		targetID, handled := s.dashboard.HandleMouse(m.Decode())
		if handled {
			if targetID > 0 {
				eff.focusTargetID = targetID
			}
			s.updateDashboardLocked()
			eff.needPaneBroadcast = true
		}
		return eff
	}
	if p, ok := s.panes[m.PaneID]; ok {
		p.SendMouse(m.Decode())
	}
	return eff
}

func (s *Server) handleScrollLocked(tp transport.Transport, m protocol.MsgScroll) msgEffects {
	var eff msgEffects
	p, ok := s.panes[m.PaneID]
	if !ok || tp == nil {
		return eff
	}
	cs := s.clientLocked(tp)
	cur := cs.clientScrollOffsets[m.PaneID]
	newOffset := cur + m.Delta
	if m.SetAbsolute {
		newOffset = m.Offset
	}
	maxOffset := p.ScrollbackLen()
	if m.SetAbsolute && m.AnchorHistory {
		newOffset += maxOffset - m.HistoryLen
		// This request already accounts for growth since the snapshot;
		// the next broadcast must not pin that growth a second time.
		cs.paneSbLens[m.PaneID] = maxOffset
	}
	if newOffset < 0 {
		newOffset = 0
	}
	if newOffset > maxOffset {
		newOffset = maxOffset
	}
	if newOffset != cur {
		cs.clientScrollOffsets[m.PaneID] = newOffset
		if newOffset == 0 {
			delete(cs.clientUnreadOutput, m.PaneID)
		}
		cs.clientScrollGens[m.PaneID]++
		eff.needPaneBroadcast = true
	}
	return eff
}

func (s *Server) handleSaveMacrosLocked(tp transport.Transport, m protocol.MsgSaveMacros) msgEffects {
	if !validMacros(m.Macros) {
		slog.Warn("rejecting invalid or oversized macro save", "count", len(m.Macros))
		return msgEffects{}
	}
	s.macros = append([]protocol.Macro(nil), m.Macros...)
	macrosCopy := append([]protocol.Macro(nil), m.Macros...)
	s.saveMacrosMu.Lock()
	if !s.saveMacrosStop {
		s.ensureSaveMacrosWorkerLocked()
		if len(s.saveMacrosQ) >= maxMacroQueue {
			s.saveMacrosQ[len(s.saveMacrosQ)-1] = macrosCopy
		} else {
			s.saveMacrosQ = append(s.saveMacrosQ, macrosCopy)
		}
		s.saveMacrosCond.Signal()
	}
	s.saveMacrosMu.Unlock()
	return msgEffects{needMacrosBroadcast: true}
}

func (s *Server) handleWebServerControlRequestLocked(tp transport.Transport, m protocol.MsgWebServerControlRequest) msgEffects {
	webReq := m
	return msgEffects{webReq: &webReq}
}

func (s *Server) applyEffects(ctx context.Context, tp transport.Transport, eff msgEffects) (done bool) {
	if eff.detachClient {
		s.dropClient(ctx, tp)
		return true
	}
	if eff.shutdownServer {
		pid := s.peerPID(tp)
		_ = s.CloseFor(ReasonShutdownRequest,
			"requesterPID", pid, "requester", processAncestry(int(pid)))
		return true
	}
	if eff.upgradeExecFn != nil {
		if tp != nil {
			tp.SendServer(ctx, protocol.MsgUpgradeResponse{})
			if d, ok := tp.(interface{ Drain(time.Duration) bool }); ok {
				d.Drain(500 * time.Millisecond)
			}
			if c, ok := tp.(io.Closer); ok {
				_ = c.Close()
			}
		}
		if err := eff.upgradeExecFn(); err != nil {
			slog.Error("exec failed during upgrade", "err", err)
			if tp != nil {
				s.dropClient(ctx, tp)
			}
		}
		return true
	}
	if eff.upgradeError != "" {
		if tp != nil {
			tp.SendServer(ctx, protocol.MsgUpgradeResponse{Error: eff.upgradeError})
		}
		return false
	}

	if tp != nil {
		if eff.focusTargetID > 0 {
			tp.SendServer(ctx, protocol.MsgFocusPane{PaneID: eff.focusTargetID})
		}
		if eff.splitResp != nil {
			tp.SendServer(ctx, *eff.splitResp)
		}
		if eff.sendResp != nil {
			tp.SendServer(ctx, *eff.sendResp)
		}
		if eff.captureResp != nil {
			tp.SendServer(ctx, *eff.captureResp)
		}
		if eff.closeResp != nil {
			tp.SendServer(ctx, *eff.closeResp)
		}
		if eff.renameResp != nil {
			tp.SendServer(ctx, *eff.renameResp)
		}
		if eff.dumpResp != nil {
			tp.SendServer(ctx, *eff.dumpResp)
		}
		if eff.pipeResp != nil {
			tp.SendServer(ctx, *eff.pipeResp)
		}
		if eff.waitResp != nil {
			tp.SendServer(ctx, *eff.waitResp)
		}
		if eff.webReq != nil {
			var resp protocol.MsgWebServerControlResponse
			switch eff.webReq.Action {
			case protocol.WebServerActionStatus:
				resp = s.WebServerStatus()
			case protocol.WebServerActionStart:
				resp, _ = s.StartWebServer(ctx, *eff.webReq)
			case protocol.WebServerActionStop:
				resp, _ = s.StopWebServer()
			default:
				resp = protocol.MsgWebServerControlResponse{
					Error: fmt.Sprintf("unrecognized web server action: %d", eff.webReq.Action),
				}
			}
			tp.SendServer(ctx, resp)
		}
	}

	if eff.closeServer {
		go func() {
			// Give the response a moment to flush over the socket before tearing down
			time.Sleep(50 * time.Millisecond)
			// And let the last pane's waiters hear its exit before Close
			// hangs up their connections.
			if eff.paneClosed != nil {
				<-eff.paneClosed
			}
			_ = s.CloseFor(ReasonLastPaneClosed, "paneID", eff.closedPaneID)
		}()
	}

	if eff.historyPane != nil && tp != nil {
		tp.SendServer(ctx, eff.historyPane.HistoryRows())
	}
	if eff.trafficReport != nil && tp != nil {
		tp.SendServer(ctx, *eff.trafficReport)
	}

	if eff.resyncPaneID > 0 && tp != nil {
		s.paneSendMu.Lock()
		s.mu.Lock()
		if cs := s.clients[tp]; cs != nil {
			delete(cs.paneGens, eff.resyncPaneID)
			delete(cs.paneFrames, eff.resyncPaneID)
			delete(cs.paneUnderlyingGens, eff.resyncPaneID)
		}
		s.mu.Unlock()
		s.paneSendMu.Unlock()
		s.broadcastPaneUpdates(ctx, false)
	}

	if eff.needBroadcast {
		s.broadcastLayout(ctx)
	}
	if eff.needPaneBroadcast {
		go s.broadcastPaneUpdates(ctx, false)
	}
	if eff.sendLayout && tp != nil {
		s.sendLayoutTo(ctx, tp)
	}
	if eff.sendMetadata && tp != nil {
		s.sendPaneMetadataTo(ctx, tp)
	}
	if eff.sendMacros && tp != nil {
		s.sendMacrosTo(ctx, tp)
	}
	if eff.sendConfig && tp != nil {
		s.sendConfigTo(ctx, tp)
	}
	if eff.needMacrosBroadcast {
		go s.broadcastMacros(ctx)
	}
	return false
}
