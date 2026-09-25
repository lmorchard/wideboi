package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

// Server manages multiplexer layout, PTY sessions, and client protocol messages.
type Server struct {
	mu              sync.Mutex
	strip           *layout.Strip
	panes           map[int]*Pane
	nextPaneID      int
	cols            int
	rows            int
	shell           string
	cwd             string
	startup         []StartupPane
	startupLaunched bool
	startupComplete bool
	transports      []transport.Transport
	stopCh          chan struct{}
	closeOnce       sync.Once

	statusPaneID int
	dashboard    *Dashboard

	// waiters holds the transports blocked in `wideboi wait` on each
	// pane, answered when that pane's process exits or it is closed.
	waiters map[int][]transport.Transport

	// lastStatuses and lastTitles are the per-pane glyph and title
	// sets as of the last layout broadcast, so the frame loop can
	// tell when either has changed. lastColumns tracks the last broadcast
	// column set and dimensions to determine whether mirrors were affected.
	lastStatuses map[int]protocol.PaneStatus
	lastTitles   map[int]string
	lastColumns  []protocol.ColumnData
	lastCWD      map[int]string
	lastUserVars map[int]map[string]string
	// Creation notices are retried until accepted, followed by a snapshot.
	pendingPaneCreated      map[transport.Transport][]int
	pendingCreationSnapshot map[transport.Transport]bool
	// attachedTransports tracks which transports have sent MsgAttach.
	// Only attached clients trigger a layout broadcast on disconnect.
	attachedTransports map[transport.Transport]bool
	// layoutSendMu orders each client's creation notices before the snapshot
	// that includes them, even when broadcasts run concurrently.
	layoutSendMu sync.Mutex

	// paneGens records, per client, the grid generation each pane was
	// at in the last update that client accepted. The frame tick sends a
	// pane only to clients whose record is missing or behind, so a new
	// client gets everything and a dropped update is retried for the
	// client that missed it. See broadcastPaneUpdates.
	paneGens map[transport.Transport]map[int]uint64
	// paneFrames is the last accepted full state per client and pane. A
	// client's next patch is calculated only from its own baseline.
	paneFrames map[transport.Transport]map[int]protocol.MsgPaneUpdate

	// paneUnderlyingGens records the underlying term.Grid generation
	// observed during the last update sent to that client for that pane.
	paneUnderlyingGens map[transport.Transport]map[int]uint64
	// paneOutputGens records the term.Grid.OutputGen observed during the last
	// update, distinguishing new child output from resizes.
	paneOutputGens map[transport.Transport]map[int]uint64
	// clientScrollOffsets tracks view scroll offset per client, per pane.
	clientScrollOffsets map[transport.Transport]map[int]int
	// paneOffsets is the last scroll offset successfully delivered to that client.
	paneOffsets map[transport.Transport]map[int]int
	// clientUnreadOutput is true when new terminal output arrived while scrolled up.
	clientUnreadOutput map[transport.Transport]map[int]bool
	// paneUnreads is the last unread output flag delivered to that client.
	paneUnreads map[transport.Transport]map[int]bool
	// clientScrollGens tracks wire generation increments caused by client scroll actions.
	clientScrollGens map[transport.Transport]map[int]uint64
	// paneSbLens tracks the scrollback length when last updated to keep content pinned.
	paneSbLens map[transport.Transport]map[int]int

	// clientSizes records the last known window dimensions of each connected client.
	clientSizes map[transport.Transport]protocol.MsgResize

	// sizeOwner is the client whose viewport dimensions currently define
	// the session PTY rows and columns. Initially set by the first client
	// to attach. Viewers connect without altering session geometry. Any
	// client can claim size ownership with VerbClaimSize (#184).
	sizeOwner transport.Transport

	// paneSendMu serializes broadcastPaneUpdates. The Run loop, every
	// client's message loop (via broadcastLayout) and onPaneExit all
	// broadcast, and two overlapping rounds could deliver an older
	// render last while recording the newer generation, leaving that
	// client stale with nothing left to trigger a resend. Taken before
	// s.mu, never while holding it.
	paneSendMu sync.Mutex
	// metaSendMu serializes broadcastMetadataIfChanged and sendPaneMetadataTo
	// so concurrent status queries and ticker broadcasts deliver in order.
	metaSendMu sync.Mutex

	// owner is the connection of the client that launched this session,
	// or nil when the session is ownerless: started as `wideboi
	// server`, or given up by a detach. Once nil it stays nil;
	// reattaching does not re-own, because nobody's terminal is tied to
	// the session any more.
	owner transport.Transport

	// listener is the socket ListenSocket accepts on, if any. Close
	// shuts it first, so nothing can attach to a session that is
	// already being reaped.
	listener *transport.SocketListener

	// traffic holds per-connection delivery counts; see traffic.go.
	// Entries are made on first use, so a Server literal works too.
	// departed sums attached clients that have left, and started is
	// when NewServer ran, for uptime. All under s.mu.
	traffic      map[transport.Transport]*clientTraffic
	nextClientID int
	departed     protocol.ClientTraffic
	started      time.Time
	// timing turns on render, patch-build and encode timing; see
	// SetTrafficTiming. renderTiming and buildTiming accumulate it.
	timing       bool
	renderTiming protocol.TimingStat
	buildTiming  protocol.TimingStat

	// closeGrace is handed to every pane this server spawns. Zero means
	// the CloseGrace default; see Pane.graceOrDefault. Only a test sets
	// it, via SetCloseGrace in export_test.go.
	closeGrace time.Duration
}

// StartupPane is a pane created on the first attach to a new session.
type StartupPane struct {
	Command string
	Dir     string
	Width   int
	// Keep retains the pane after its process exits; see watchKeptPane.
	Keep bool
}

// SetStartupPanes configures the initial columns in their display order.
func (s *Server) SetStartupPanes(panes []StartupPane) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startup = append([]StartupPane(nil), panes...)
}

// websocketProtocolToken reads the browser's token-bearing subprotocol offer.
// The server deliberately does not select it as the negotiated subprotocol.
func websocketProtocolToken(r *http.Request) string {
	const prefix = "wideboi-token."
	for _, protocol := range websocket.Subprotocols(r) {
		if strings.HasPrefix(protocol, prefix) {
			decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(protocol, prefix))
			if err == nil {
				return string(decoded)
			}
		}
	}
	return ""
}

// SetOwner marks tp -- already passed to NewServer -- as the owning
// client's connection. If it closes without a MsgDetach first, the
// session ends: that is how an owner killed by SIGKILL, which runs no
// code at all, still takes its panes with it.
func (s *Server) SetOwner(tp transport.Transport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.owner = tp
}

// SetTrafficTiming turns on render, patch-build and encode timing for
// `wideboi status --traffic` (#179). Off by default: the hot paths
// then never read the clock. Call it before Run; connections already
// counted keep encode timing off.
func (s *Server) SetTrafficTiming(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timing = on
}

// SetWidthPresets configures the sequence of presets used by CycleWidth.
func (s *Server) SetWidthPresets(presets []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.strip.SetWidthPresets(presets)
}

// NewServer initializes a Server instance connected via transport.
func NewServer(tp transport.Transport, shell, cwd string) *Server {
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	srv := &Server{
		strip:                   layout.NewStrip(),
		panes:                   make(map[int]*Pane),
		shell:                   shell,
		cwd:                     cwd,
		transports:              make([]transport.Transport, 0),
		clientSizes:             make(map[transport.Transport]protocol.MsgResize),
		pendingPaneCreated:      make(map[transport.Transport][]int),
		pendingCreationSnapshot: make(map[transport.Transport]bool),
		attachedTransports:      make(map[transport.Transport]bool),
		stopCh:                  make(chan struct{}),
		started:                 time.Now(),
	}
	if tp != nil {
		srv.transports = append(srv.transports, tp)
	}
	return srv
}

// ListenSocket starts accepting socket connections on sl.
func (s *Server) ListenSocket(ctx context.Context, sl *transport.SocketListener) {
	s.mu.Lock()
	s.listener = sl
	s.mu.Unlock()
	go func() {
		for {
			conn, err := sl.Accept()
			if err != nil {
				return
			}
			go s.admitSocketConn(ctx, conn)
		}
	}()
}

// admitSocketConn registers conn as a client once it has shown it
// speaks this build's protocol. A peer that fails the handshake is
// hung up on before it is a client at all, so it cannot resize panes,
// receive broadcasts, or end the session on the way out (#174). It runs
// off the accept loop: a peer that never says hello costs its own
// goroutine the handshake ceiling, not everyone else's attach.
func (s *Server) admitSocketConn(ctx context.Context, conn net.Conn) {
	if peer, err := transport.Handshake(conn); err != nil {
		_ = conn.Close()
		// `wideboi ls`, `cleanup` and the listener's liveness probe all
		// dial and hang up at once; that is not worth a warning.
		if errors.Is(err, io.EOF) {
			slog.Debug("socket peer hung up before its hello")
		} else {
			slog.Warn("refusing socket client", "peerPID", peer.PID, "err", err)
		}
		return
	}
	sConn := transport.NewServerSocketConn(conn, 256)
	sConn.RunPumps(ctx)

	s.mu.Lock()
	if s.stoppingLocked() {
		// Accepted just as Close shut the listener. Close
		// has taken, or is about to take, its snapshot of
		// the transports, so this one would never be hung up.
		s.mu.Unlock()
		_ = sConn.Close()
		return
	}
	s.transports = append(s.transports, sConn)
	s.mu.Unlock()

	go s.handleClientConnLoop(ctx, sConn)
}

func (s *Server) handleClientConnLoop(ctx context.Context, tp transport.Transport) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-tp.ClientSendChan():
			if !ok {
				if s.dropClient(ctx, tp) {
					slog.Info("owning client left without detaching; ending the session")
					_ = s.Close()
				}
				return
			}
			if _, ok := msg.(protocol.MsgShutdown); ok {
				// Close hangs up on every transport, this one
				// included, and only after reaping. Called here,
				// not under s.mu, for the same reason dropClient
				// closes outside it (#43).
				_ = s.Close()
				return
			}
			if _, ok := msg.(protocol.MsgDetach); ok {
				// An owner detaching gives up ownership, not the
				// session. Returning here means the EOF that follows
				// is never read as an owner leaving: this goroutine is
				// the only reader of this connection, and it stops.
				s.dropClient(ctx, tp)
				return
			}
			s.handleClientMsg(ctx, tp, msg)
		}
	}
}

// dropClient removes tp from the broadcast set and closes it. It reports
// whether tp was the owner, and clears ownership under the same lock.
func (s *Server) dropClient(ctx context.Context, tp transport.Transport) (wasOwner bool) {
	s.mu.Lock()
	attached := s.attachedTransports != nil && s.attachedTransports[tp]
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

func (s *Server) removeTransportLocked(tp transport.Transport) {
	out := make([]transport.Transport, 0, len(s.transports))
	for _, t := range s.transports {
		if t != tp {
			out = append(out, t)
		}
	}
	s.transports = out
	delete(s.paneGens, tp)
	delete(s.paneFrames, tp)
	delete(s.paneUnderlyingGens, tp)
	delete(s.paneOutputGens, tp)
	delete(s.clientScrollOffsets, tp)
	delete(s.paneOffsets, tp)
	delete(s.clientUnreadOutput, tp)
	delete(s.paneUnreads, tp)
	delete(s.clientScrollGens, tp)
	delete(s.paneSbLens, tp)
	delete(s.clientSizes, tp)
	delete(s.pendingPaneCreated, tp)
	delete(s.pendingCreationSnapshot, tp)
	delete(s.attachedTransports, tp)
	s.forgetTrafficLocked(tp)
	if s.sizeOwner == tp {
		s.sizeOwner = nil
	}
}

// Run executes the main server event loop, processing client messages and polling descendants.
func (s *Server) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.owner == nil {
		s.startupComplete = true
	}
	initialTransports := append([]transport.Transport{}, s.transports...)
	s.mu.Unlock()

	for _, tp := range initialTransports {
		go s.handleClientConnLoop(ctx, tp)
	}

	frameTicker := time.NewTicker(33 * time.Millisecond)
	defer frameTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return s.Close()
		case <-s.stopCh:
			return nil
		case <-frameTicker.C:
			s.broadcastMetadataIfChanged(ctx)
			// broadcastLayout already ends with a pane-update
			// broadcast, so only send one separately when it did
			// not fire.
			if !s.broadcastLayoutIfStatusChanged(ctx) {
				s.broadcastPaneUpdates(ctx, false)
			}
		}
	}
}

// StartupComplete reports whether the server finished initial startup.
// For a server spawned by an owner, this means the owner successfully attached.
// For an unowned server, startup completes once it enters the main event loop.
func (s *Server) StartupComplete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startupComplete
}

func (s *Server) handleClientMsg(ctx context.Context, tp transport.Transport, msg transport.ClientMessage) {
	s.mu.Lock()
	needBroadcast := false
	needPaneBroadcast := false
	sendMetadata := false
	resyncPaneID := 0
	createdPaneID := 0
	focusTargetID := 0
	closeServer := false
	var trafficReport *protocol.MsgTrafficStats
	var historyPane *Pane
	var splitResp *protocol.MsgSplitResponse
	var sendResp *protocol.MsgSendInputResponse
	var captureResp *protocol.MsgCaptureResponse
	var closeResp *protocol.MsgClosePaneResponse
	var waitResp *protocol.MsgWaitResponse
	// paneClosed closes once a pane removed here has been hung up and its
	// waiters answered; a server closing with it waits for that first.
	var paneClosed <-chan struct{}

	switch m := msg.(type) {
	case protocol.MsgSplitRequest:
		if m.AfterPaneID > 0 {
			if _, ok := s.panes[m.AfterPaneID]; !ok {
				splitResp = &protocol.MsgSplitResponse{Error: fmt.Sprintf("pane %d not found", m.AfterPaneID)}
				break
			}
		}
		spec := StartupPane{Command: m.Command, Dir: m.Cwd, Keep: m.Keep}
		p, err := s.spawnPaneWithSpecLocked(spec, m.AfterPaneID)
		if err != nil {
			splitResp = &protocol.MsgSplitResponse{Error: err.Error()}
		} else {
			splitResp = &protocol.MsgSplitResponse{PaneID: p.ID()}
			needBroadcast = true
			// A server that split auto-spawned never sees an attach,
			// which is otherwise what marks startup done; without this
			// its clean exit would skip auto-cleanup.
			s.startupComplete = true
		}
	case protocol.MsgSendInputRequest:
		p, ok := s.panes[m.PaneID]
		if !ok {
			sendResp = &protocol.MsgSendInputResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d not found", m.PaneID)}
		} else if _, exited := p.ExitStatus(); exited {
			sendResp = &protocol.MsgSendInputResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d has exited", m.PaneID)}
		} else {
			if _, err := p.Write(m.Data); err != nil {
				sendResp = &protocol.MsgSendInputResponse{PaneID: m.PaneID, Error: err.Error()}
			} else {
				sendResp = &protocol.MsgSendInputResponse{PaneID: m.PaneID}
			}
		}
	case protocol.MsgCaptureRequest:
		p, ok := s.panes[m.PaneID]
		if !ok {
			captureResp = &protocol.MsgCaptureResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d not found", m.PaneID)}
		} else {
			text := p.CaptureText(m.Scrollback, m.Lines)
			captureResp = &protocol.MsgCaptureResponse{PaneID: m.PaneID, Text: text}
		}
	case protocol.MsgClosePaneRequest:
		if _, ok := s.panes[m.PaneID]; !ok {
			closeResp = &protocol.MsgClosePaneResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d not found", m.PaneID)}
		} else {
			closeServer, paneClosed = s.removePaneLocked(m.PaneID)
			needBroadcast = true
			closeResp = &protocol.MsgClosePaneResponse{PaneID: m.PaneID}
		}
	case protocol.MsgWaitRequest:
		// Checked and registered under s.mu, which every exit path takes
		// before collecting waiters: watchKeptPane marks the exit before
		// notifyWaiters locks, and an unkept pane leaves s.panes under
		// the lock before finishWaiters runs. So a waiter either sees
		// the exit here or is registered in time to hear it.
		p, ok := s.panes[m.PaneID]
		if !ok {
			waitResp = &protocol.MsgWaitResponse{PaneID: m.PaneID, Error: fmt.Sprintf("pane %d not found", m.PaneID)}
			break
		}
		if code, exited := p.ExitStatus(); exited {
			waitResp = &protocol.MsgWaitResponse{PaneID: m.PaneID, ExitCode: code}
			break
		}
		if s.waiters == nil {
			s.waiters = make(map[int][]transport.Transport)
		}
		s.waiters[m.PaneID] = append(s.waiters[m.PaneID], tp)
	case protocol.MsgPaneResync:
		resyncPaneID = m.PaneID
		s.trafficLocked(tp).counts.ResyncRequests++
	case protocol.MsgStatusRequest:
		needBroadcast = true
		sendMetadata = true
	case protocol.MsgTrafficRequest:
		report := s.trafficReportLocked()
		trafficReport = &report
	case protocol.MsgHistoryRequest:
		if tp != nil {
			historyPane = s.panes[m.PaneID]
		}

	case protocol.MsgAttach:
		s.startupComplete = true
		s.markAttachedLocked(tp)
		if s.attachedTransports == nil {
			s.attachedTransports = make(map[transport.Transport]bool)
		}
		if tp != nil {
			s.attachedTransports[tp] = true
		}
		if m.Cols > 0 && m.Rows > 0 {
			if s.clientSizes == nil {
				s.clientSizes = make(map[transport.Transport]protocol.MsgResize)
			}
			s.clientSizes[tp] = protocol.MsgResize{Cols: m.Cols, Rows: m.Rows}
			if s.sizeOwner == nil && s.rows == 0 {
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
				for _, spec := range s.startup {
					p, err := s.spawnPaneWithSpecLocked(spec, 0)
					if err != nil {
						slog.Error("starting configured pane", "command", spec.Command, "err", err)
						continue
					}
					if firstID == 0 {
						firstID = p.ID()
					}
				}
				if firstID != 0 {
					s.strip.FocusPaneID(firstID)
				}
			}
		}
		s.resizePanesLocked()
		needBroadcast = true
		sendMetadata = true

	case protocol.MsgResize:
		if m.Cols > 0 && m.Rows > 0 {
			if s.clientSizes == nil {
				s.clientSizes = make(map[transport.Transport]protocol.MsgResize)
			}
			s.clientSizes[tp] = protocol.MsgResize{Cols: m.Cols, Rows: m.Rows}
			if s.sizeOwner == nil && s.rows == 0 {
				s.sizeOwner = tp
				s.cols = m.Cols
				s.rows = m.Rows
				s.resizePanesLocked()
				needBroadcast = true
			} else {
				if s.sizeOwner == nil && len(s.attachedTransports) == 0 && len(s.transports) <= 1 {
					s.sizeOwner = tp
				}
				if s.sizeOwner == tp {
					oldCols, oldRows := s.cols, s.rows
					s.cols, s.rows = m.Cols, m.Rows
					if s.cols != oldCols || s.rows != oldRows {
						s.resizePanesLocked()
						needBroadcast = true
					}
				}
			}
		}

	case protocol.MsgVerb:
		switch m.Verb {
		case protocol.VerbNewColumn:
			if p, err := s.spawnPaneLocked(m.PaneID); err == nil {
				createdPaneID = p.ID()
			}
			s.resizePanesLocked()
		case protocol.VerbCycleWidth:
			s.strip.CycleWidth(m.PaneID)
			s.resizePanesLocked()
		case protocol.VerbGrowWidth:
			s.strip.GrowWidth(m.PaneID, 10)
			s.resizePanesLocked()
		case protocol.VerbShrinkWidth:
			s.strip.ShrinkWidth(m.PaneID, 10)
			s.resizePanesLocked()
		case protocol.VerbMoveLeft:
			s.strip.MoveLeft(m.PaneID)
		case protocol.VerbMoveRight:
			// Neither move calls resizePanesLocked, for the same
			// reason a layout toggle never did: order is presentation,
			// and a column's width goes wherever the column goes.
			s.strip.MoveRight(m.PaneID)
		case protocol.VerbKillPane:
			if _, ok := s.panes[m.PaneID]; ok {
				closeServer, paneClosed = s.removePaneLocked(m.PaneID)
				s.resizePanesLocked()
			}
		case protocol.VerbToggleStatus:
			if s.statusPaneID != 0 {
				focusTargetID = s.statusPaneID
			} else {
				if p, err := s.spawnDashboardPaneLocked(m.PaneID); err == nil {
					createdPaneID = p.ID()
					focusTargetID = p.ID()
				}
				s.resizePanesLocked()
				s.updateDashboardLocked()
			}
		case protocol.VerbClaimSize:
			s.sizeOwner = tp
			if sz, ok := s.clientSizes[tp]; ok && sz.Cols > 0 && sz.Rows > 0 {
				oldCols, oldRows := s.cols, s.rows
				s.cols, s.rows = sz.Cols, sz.Rows
				if s.cols != oldCols || s.rows != oldRows {
					s.resizePanesLocked()
				}
			}
		case protocol.VerbToggleCards:
			// Reserved: layout is the client's (#92). An older client
			// may still send it; there is nothing to do.
		}
		// Every other verb changes the strip. The reserved one changes
		// nothing, and a snapshot costs a forced resend of every pane to
		// every client, so it must not broadcast.
		if m.Verb != protocol.VerbToggleCards && (m.Verb != protocol.VerbToggleStatus || createdPaneID != 0) {
			needBroadcast = true
		}

	case protocol.MsgInput:
		if m.PaneID == s.statusPaneID && s.dashboard != nil {
			var key uv.KeyEvent
			if !m.Key.IsZero() {
				key = m.Key.Decode()
			} else if len(m.Data) > 0 {
				if len(m.Data) == 1 && (m.Data[0] == '\r' || m.Data[0] == '\n') {
					key = uv.KeyPressEvent{Code: 13}
				} else if len(m.Data) == 1 && m.Data[0] == 'j' {
					key = uv.KeyPressEvent{Code: 'j'}
				} else if len(m.Data) == 1 && m.Data[0] == 'k' {
					key = uv.KeyPressEvent{Code: 'k'}
				}
			}
			if key != nil {
				targetID, handled := s.dashboard.HandleKey(key)
				if handled {
					if targetID > 0 {
						focusTargetID = targetID
					}
					s.updateDashboardLocked()
					needPaneBroadcast = true
				}
			}
			break
		}
		if p, ok := s.panes[m.PaneID]; ok {
			if tp != nil && s.clientScrollOffsets != nil && s.clientScrollOffsets[tp] != nil && s.clientScrollOffsets[tp][m.PaneID] > 0 {
				s.clientScrollOffsets[tp][m.PaneID] = 0
				if s.clientUnreadOutput != nil && s.clientUnreadOutput[tp] != nil {
					delete(s.clientUnreadOutput[tp], m.PaneID)
				}
				if s.clientScrollGens == nil {
					s.clientScrollGens = make(map[transport.Transport]map[int]uint64)
				}
				if s.clientScrollGens[tp] == nil {
					s.clientScrollGens[tp] = make(map[int]uint64)
				}
				s.clientScrollGens[tp][m.PaneID]++
				needPaneBroadcast = true
			}
			if len(m.Data) > 0 {
				_, _ = p.Write(m.Data)
			} else if !m.Key.IsZero() {
				p.SendKey(m.Key.Decode())
			}
		}

	case protocol.MsgMouse:
		if m.PaneID == s.statusPaneID && s.dashboard != nil {
			targetID, handled := s.dashboard.HandleMouse(m.Decode())
			if handled {
				if targetID > 0 {
					focusTargetID = targetID
				}
				s.updateDashboardLocked()
				needPaneBroadcast = true
			}
			break
		}
		if p, ok := s.panes[m.PaneID]; ok {
			p.SendMouse(m.Decode())
		}

	case protocol.MsgScroll:
		if p, ok := s.panes[m.PaneID]; ok && tp != nil {
			if s.clientScrollOffsets == nil {
				s.clientScrollOffsets = make(map[transport.Transport]map[int]int)
			}
			offsets := s.clientScrollOffsets[tp]
			if offsets == nil {
				offsets = make(map[int]int)
				s.clientScrollOffsets[tp] = offsets
			}
			cur := offsets[m.PaneID]
			newOffset := cur + m.Delta
			if m.SetAbsolute {
				newOffset = m.Offset
			}
			maxOffset := p.ScrollbackLen()
			if m.SetAbsolute && m.AnchorHistory {
				newOffset += maxOffset - m.HistoryLen
				// This request already accounts for growth since the snapshot;
				// the next broadcast must not pin that growth a second time.
				if s.paneSbLens == nil {
					s.paneSbLens = make(map[transport.Transport]map[int]int)
				}
				if s.paneSbLens[tp] == nil {
					s.paneSbLens[tp] = make(map[int]int)
				}
				s.paneSbLens[tp][m.PaneID] = maxOffset
			}
			if newOffset < 0 {
				newOffset = 0
			}
			if newOffset > maxOffset {
				newOffset = maxOffset
			}
			if newOffset != cur {
				offsets[m.PaneID] = newOffset
				if newOffset == 0 && s.clientUnreadOutput != nil && s.clientUnreadOutput[tp] != nil {
					delete(s.clientUnreadOutput[tp], m.PaneID)
				}
				if s.clientScrollGens == nil {
					s.clientScrollGens = make(map[transport.Transport]map[int]uint64)
				}
				if s.clientScrollGens[tp] == nil {
					s.clientScrollGens[tp] = make(map[int]uint64)
				}
				s.clientScrollGens[tp][m.PaneID]++
				needPaneBroadcast = true
			}
		}
	}
	if tp != nil && createdPaneID != 0 {
		s.pendingPaneCreated[tp] = append(s.pendingPaneCreated[tp], createdPaneID)
		s.pendingCreationSnapshot[tp] = true
	}

	s.mu.Unlock()
	if tp != nil {
		if focusTargetID > 0 {
			tp.SendServer(ctx, protocol.MsgFocusPane{PaneID: focusTargetID})
		}
		if splitResp != nil {
			tp.SendServer(ctx, *splitResp)
		}
		if sendResp != nil {
			tp.SendServer(ctx, *sendResp)
		}
		if captureResp != nil {
			tp.SendServer(ctx, *captureResp)
		}
		if closeResp != nil {
			tp.SendServer(ctx, *closeResp)
		}
		if waitResp != nil {
			tp.SendServer(ctx, *waitResp)
		}
	}
	if closeServer {
		go func() {
			// Give the response a moment to flush over the socket before tearing down
			time.Sleep(50 * time.Millisecond)
			// And let the last pane's waiters hear its exit before Close
			// hangs up their connections.
			if paneClosed != nil {
				<-paneClosed
			}
			_ = s.Close()
		}()
	}
	if historyPane != nil {
		tp.SendServer(ctx, historyPane.HistoryRows())
	}
	if trafficReport != nil {
		// Only the requester gets it. Sent outside s.mu: a socket
		// SendServer can block on a full queue.
		tp.SendServer(ctx, *trafficReport)
	}
	if resyncPaneID > 0 {
		// Take the broadcast lane before invalidating the baseline. An
		// in-flight patch may finish first, but the full resend follows it.
		s.paneSendMu.Lock()
		s.mu.Lock()
		delete(s.paneGens[tp], resyncPaneID)
		delete(s.paneFrames[tp], resyncPaneID)
		delete(s.paneUnderlyingGens[tp], resyncPaneID)
		s.mu.Unlock()
		s.paneSendMu.Unlock()
		s.broadcastPaneUpdates(ctx, false)
	}

	if needBroadcast {
		s.broadcastLayout(ctx)
	}
	if needPaneBroadcast {
		go s.broadcastPaneUpdates(ctx, false)
	}
	if sendMetadata && tp != nil {
		s.sendPaneMetadataTo(ctx, tp)
	}
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

func (s *Server) spawnPaneLocked(afterPaneID int) (*Pane, error) {
	return s.spawnPaneWithSpecLocked(StartupPane{}, afterPaneID)
}

func (s *Server) spawnPaneWithSpecLocked(spec StartupPane, afterPaneID int) (*Pane, error) {
	// Close reaps the panes it snapshotted; one spawned after that --
	// an attach or a new column during the reap -- would be nobody's
	// to reap.
	if s.stoppingLocked() {
		return nil, fmt.Errorf("server is shutting down")
	}
	s.nextPaneID++
	id := s.nextPaneID
	presets := s.strip.WidthPresets()
	paneCols := presets[len(presets)-1]
	if paneCols > s.cols && s.cols > 0 {
		paneCols = s.cols
	}
	if spec.Width > 0 {
		paneCols = spec.Width
	}
	paneRows := max(s.rows-2, 20)

	argv := []string{s.shell}
	if spec.Command != "" {
		argv = []string{s.shell, "-c", spec.Command}
	}
	cwd := s.cwd
	if spec.Dir != "" {
		cwd = spec.Dir
	}
	p, err := NewPane(id, argv, paneCols, paneRows, cwd)
	if err != nil {
		return nil, err
	}
	p.closeGrace = s.closeGrace

	s.panes[id] = p
	s.strip.AddColumn(id, paneCols, paneRows, afterPaneID)

	if spec.Keep {
		// A kept pane ends when its child is reaped, not at pty EOF: a
		// background job can hold the pty open past the exit, and the
		// exit code only exists after the reap. EOF still matters, as
		// the point where the child's last output is on screen.
		drained := make(chan struct{})
		p.Start(func() { close(drained) })
		go s.watchKeptPane(id, p, drained)
	} else {
		p.Start(func() {
			// Called when PTY reader hits EOF
			s.onPaneExit(id)
		})
	}

	return p, nil
}

// keptDrainCeiling bounds how long a reaped kept pane waits for its pty
// reader to reach EOF before reporting the exit. EOF normally follows
// the reap within a read; it never comes while a background job still
// holds the pty, and the exit must not wait on that job.
const keptDrainCeiling = time.Second

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
	s.notifyWaiters(id, code, "")
	s.broadcastLayout(context.Background())
}

// waiterSendCeiling bounds each wait answer, so a waiter whose connection
// has stalled cannot hold up the others.
const waiterSendCeiling = time.Second

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

// waiterDrainCeiling bounds the wait for a wait answer to reach the wire
// before the session hangs up the waiter's connection.
const waiterDrainCeiling = 500 * time.Millisecond

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
	if code, ok := p.reapedExitCode(); ok {
		return s.notifyWaiters(id, code, "")
	}
	return s.notifyWaiters(id, 0, fmt.Sprintf("pane %d closed before its process exited", id))
}

func (s *Server) onPaneExit(id int) {
	s.mu.Lock()
	_, ok := s.panes[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	if id == s.statusPaneID {
		s.statusPaneID = 0
		s.dashboard = nil
	}
	s.strip.KillPane(id)
	p := s.panes[id]
	delete(s.panes, id)
	delete(s.lastCWD, id)
	delete(s.lastUserVars, id)
	s.resizePanesLocked()
	s.updateDashboardLocked()

	hasTerminalPanes := false
	for pid, pane := range s.panes {
		if pid != s.statusPaneID && !pane.isDashboard {
			hasTerminalPanes = true
			break
		}
	}
	shouldClose := !hasTerminalPanes
	s.mu.Unlock()

	s.broadcastLayout(context.Background())
	_ = p.Close()
	// Before any s.Close below, so a waiter hears the code before its
	// connection goes.
	answered := s.finishWaiters(id, p)

	if shouldClose {
		drainAnswered(answered)
		_ = s.Close()
	}
}

// removePaneLocked takes pane id out of the session and closes it in the
// background. last reports that no terminal panes remain; closed closes
// once the pane is hung up and its waiters answered.
func (s *Server) removePaneLocked(id int) (last bool, closed <-chan struct{}) {
	p, ok := s.panes[id]
	if !ok {
		return false, nil
	}
	if id == s.statusPaneID {
		s.statusPaneID = 0
		s.dashboard = nil
	}
	s.strip.KillPane(id)
	delete(s.panes, id)
	delete(s.lastCWD, id)
	delete(s.lastUserVars, id)
	s.updateDashboardLocked()

	hasTerminalPanes := false
	for pid, pane := range s.panes {
		if pid != s.statusPaneID && !pane.isDashboard {
			hasTerminalPanes = true
			break
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = p.Close()
		answered := s.finishWaiters(id, p)
		if !hasTerminalPanes {
			// The session closes behind this; see paneClosed.
			drainAnswered(answered)
		}
	}()
	return !hasTerminalPanes, done
}

func (s *Server) spawnDashboardPaneLocked(afterPaneID int) (*Pane, error) {
	if s.stoppingLocked() {
		return nil, fmt.Errorf("server is shutting down")
	}
	s.nextPaneID++
	id := s.nextPaneID
	presets := s.strip.WidthPresets()
	paneCols := presets[len(presets)-1]
	if paneCols > s.cols && s.cols > 0 {
		paneCols = s.cols
	}
	paneRows := max(s.rows-2, 20)

	grid := term.NewVT(paneCols, paneRows)
	_, _ = grid.Write([]byte("\x1b]0;Dashboard\x07"))

	p := NewCustomPane(id, grid, paneCols, paneRows)
	p.isDashboard = true
	s.statusPaneID = id
	s.dashboard = NewDashboard()

	s.panes[id] = p
	s.strip.AddColumn(id, paneCols, paneRows, afterPaneID)

	p.Start(func() {
		s.onPaneExit(id)
	})

	return p, nil
}

func (s *Server) updateDashboardLocked() {
	if s.statusPaneID == 0 || s.dashboard == nil {
		return
	}
	p, ok := s.panes[s.statusPaneID]
	if !ok {
		return
	}
	cols := s.strip.Columns()
	var infos []PaneInfo
	glyphs := s.statusGlyphsLocked()
	titles := s.paneTitlesLocked()
	focusedID := s.strip.FocusedPaneID()
	for _, c := range cols {
		if c.PaneID == s.statusPaneID {
			continue
		}
		infos = append(infos, PaneInfo{
			ID:      c.PaneID,
			Status:  glyphs[c.PaneID],
			Title:   titles[c.PaneID],
			CWD:     s.lastCWD[c.PaneID],
			Width:   c.Width,
			Height:  c.Height,
			Focused: c.PaneID == focusedID,
		})
	}
	dbCols, dbRows := p.Size()
	data := s.dashboard.Render(infos, dbCols, dbRows)
	_, _ = p.grid.Write(data)
}

// resizePanesLocked pushes each pane's current column width and available
// height down to its emulator and child. Call it after anything that
// changes geometry: an attach, a host resize, a new column, a width cycle,
// or a pane closing.
//
// Width comes from the column's own logical width (layout.Strip's
// ColumnWidth), never from a Placement's Dst: Dst is what survives
// clipping against the viewport and against sibling columns, and the
// layout spec's invariant 4 is that "a pane's logical width equals its
// column width, independent of what is visible." A column scrolled
// partly (or entirely) off-screen keeps its full width, so its child gets
// no SIGWINCH and never learns it was occluded.
//
// Height comes from layout.AvailHeight(s.rows), the same uniform formula
// ComputePlacements uses internally -- NOT from Placement.Dst.Dy(). A
// column scrolled fully off-screen has no Placement at all (ComputePlacements
// drops it once its Dst is empty), so iterating Placements silently skips
// it: it would keep whatever height it had at
// spawn forever, mismatched against every visible pane, corrected only if
// it happens to scroll back into view before anything else touches it.
// Iterating s.strip.PaneIDs() instead of ComputePlacements's output covers
// every pane in the strip, visible or not.
//
// Called with s.mu held, per the *Locked convention, but does not hold it
// across the resize calls themselves: Pane.Resize -> Grid.Resize can block
// writing into the emulator's own unbuffered pipe, which only the pane's
// pty-writer pump goroutine drains -- and that goroutine can itself be
// blocked in Master.Write against a child that has stopped reading its
// stdin. Blocking here while holding s.mu would park every other goroutine
// that needs s.mu on one wedged child, srv.Close() among them. So the
// geometry is read and the lock released before any pane is actually
// touched, following PaneSize's precedent, and the lock is reacquired
// before returning so the caller's held lock is honored on exit.
//
// Be precise about what that buys, because it is less than it looks.
// It stops THIS call from being the thing that holds s.mu forever. It
// does NOT rescue the Run loop, which still blocks inside the wedged
// Resize; only other goroutines gain from the release.
//
// That release also means two resizePanesLocked calls can now overlap --
// this one and, e.g., the one onPaneExit runs for a different pane's
// death -- and both can reach the very same surviving pane's Resize
// concurrently. Pane.Resize and Pane.Close serialize against each other
// and against themselves via Pane's own resizeMu for exactly this reason;
// see pane.go.
//
// Errors are recorded on the pane itself (surfaced the next time it
// closes) rather than logged: log.Printf writes to stderr, which corrupts
// the user's screen while the alt screen is active. One child failing
// TIOCSWINSZ must not stop the others from being resized.
//
// s.rows <= 0 returns immediately: layout.AvailHeight(0) is
// max(-1, 1) == 1, a positive number, so it would otherwise resize every
// pane to a 1-row grid and SIGWINCH every child -- Pane.Resize's own
// non-positive guard is on cols/rows individually and does not catch a
// viewport that simply hasn't been set yet (e.g. a verb arriving before
// the first MsgAttach). ComputePlacements used to give this for free by
// returning nil for a non-positive viewport; iterating PaneIDs directly
// does not.
func (s *Server) resizePanesLocked() {
	if s.rows <= 0 {
		return
	}

	type resizeJob struct {
		pane *Pane
		w, h int
	}

	h := layout.AvailHeight(s.rows)
	s.strip.SetAllColumnHeights(h)
	var jobs []resizeJob
	for _, id := range s.strip.PaneIDs() {
		p, ok := s.panes[id]
		if !ok {
			continue
		}
		w, ok := s.strip.ColumnWidth(id)
		if !ok {
			continue
		}
		jobs = append(jobs, resizeJob{pane: p, w: w, h: h})
	}
	if len(jobs) == 0 {
		return
	}

	s.mu.Unlock()
	defer s.mu.Lock()

	for _, j := range jobs {
		if err := j.pane.Resize(j.w, j.h); err != nil {
			j.pane.recordFailure(fmt.Errorf("resize to %dx%d: %w", j.w, j.h, err))
		}
	}
}

// smartJumpTargetLocked picks the pane most worth jumping to, or 0 when
// nothing wants attention. s.mu must be held.
//
// Priority rather than first match: OSC 133's A and B both mean
// NeedsInput, and a shell sits at a prompt almost all the time, so
// "first pane with an interesting status" would land on whichever idle
// shell the map happened to yield first -- and map order is not stable
// between runs, so the same screen could send you somewhere different
// each press. A failed command outranks a finished one, which outranks
// a prompt. Working and Idle are never targets: a busy pane does not
// want you and an empty one has nothing to say. Ties break on the
// lowest pane ID so repeated presses are deterministic.

// statusGlyphsLocked renders the current per-pane status glyphs.
// s.mu must be held.
func (s *Server) statusGlyphsLocked() map[int]protocol.PaneStatus {
	out := make(map[int]protocol.PaneStatus, len(s.panes))
	for id, p := range s.panes {
		out[id] = p.Status()
	}
	return out
}

// paneTitlesLocked collects each pane's terminal title.
// s.mu must be held.
func (s *Server) paneTitlesLocked() map[int]string {
	out := make(map[int]string, len(s.panes))
	for id, p := range s.panes {
		out[id] = p.Title()
	}
	return out
}

func sameStatusMap(a, b map[int]protocol.PaneStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// sameStringMap reports whether two pane-keyed string maps agree.
func sameStringMap(a, b map[int]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sameUserVars(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// sameColumnSetAndSizes reports whether two column slices have the same
// set of pane IDs with identical width and height. Order changes (like
// moving columns left/right) do not change the set or sizes.
func sameColumnSetAndSizes(a, b []protocol.ColumnData) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[int]protocol.ColumnData, len(a))
	for _, col := range a {
		m[col.PaneID] = col
	}
	for _, col := range b {
		other, ok := m[col.PaneID]
		if !ok || other.Width != col.Width || other.Height != col.Height {
			return false
		}
	}
	return true
}

func (s *Server) broadcastMetadataIfChanged(ctx context.Context) bool {
	s.metaSendMu.Lock()
	defer s.metaSendMu.Unlock()

	s.mu.Lock()
	type metaUpdate struct {
		id   int
		cwd  string
		vars map[string]string
	}
	var updates []metaUpdate
	for id, p := range s.panes {
		cwd := p.CWD()
		vars := p.UserVars()
		lastCwd, okCwd := s.lastCWD[id]
		lastVars, okVars := s.lastUserVars[id]
		if !okCwd || cwd != lastCwd || !okVars || !sameUserVars(vars, lastVars) {
			updates = append(updates, metaUpdate{id: id, cwd: cwd, vars: vars})
		}
	}
	for id := range s.lastCWD {
		if _, ok := s.panes[id]; !ok {
			delete(s.lastCWD, id)
		}
	}
	for id := range s.lastUserVars {
		if _, ok := s.panes[id]; !ok {
			delete(s.lastUserVars, id)
		}
	}
	if len(updates) == 0 {
		s.mu.Unlock()
		return false
	}
	s.updateDashboardLocked()
	tps := append([]transport.Transport{}, s.transports...)
	s.mu.Unlock()

	for _, u := range updates {
		msg := protocol.MsgPaneMetadata{
			PaneID:   u.id,
			CWD:      u.cwd,
			UserVars: u.vars,
		}
		delivered := true
		for _, tp := range tps {
			if !tp.SendServer(ctx, msg) {
				delivered = false
			}
		}
		if delivered {
			s.mu.Lock()
			if s.lastCWD == nil {
				s.lastCWD = make(map[int]string)
			}
			if s.lastUserVars == nil {
				s.lastUserVars = make(map[int]map[string]string)
			}
			s.lastCWD[u.id] = u.cwd
			s.lastUserVars[u.id] = u.vars
			s.mu.Unlock()
		}
	}
	return true
}

func (s *Server) sendPaneMetadataTo(ctx context.Context, tp transport.Transport) {
	s.metaSendMu.Lock()
	defer s.metaSendMu.Unlock()

	s.mu.Lock()
	var msgs []protocol.MsgPaneMetadata
	for id, p := range s.panes {
		code, exited := p.ExitStatus()
		msgs = append(msgs, protocol.MsgPaneMetadata{
			PaneID:   id,
			CWD:      p.CWD(),
			UserVars: p.UserVars(),
			Exited:   exited,
			ExitCode: code,
		})
	}
	s.mu.Unlock()

	for _, msg := range msgs {
		tp.SendServer(ctx, msg)
	}
}

// broadcastLayoutIfStatusChanged pushes a layout snapshot when any
// pane's status glyph differs from the last one sent, and reports
// whether it did.
//
// PaneStatuses rides on MsgLayoutSnapshot, which is otherwise only
// sent for verbs, spawns and kills. Without this, a status change
// driven by OSC 133 would sit invisible until the user happened to
// press a verb key -- which is how the glyphs stayed unobservable even
// after the handler itself was fixed. Change detection keeps an idle
// session quiet: the frame ticker runs at 33ms, but nothing is sent
// unless the glyph set actually moved.
func (s *Server) broadcastLayoutIfStatusChanged(ctx context.Context) bool {
	s.mu.Lock()
	changed := !sameStatusMap(s.statusGlyphsLocked(), s.lastStatuses) ||
		!sameStringMap(s.paneTitlesLocked(), s.lastTitles) ||
		len(s.pendingCreationSnapshot) > 0
	s.mu.Unlock()

	if !changed {
		return false
	}
	// Call outside s.mu: broadcastLayout takes it itself. It is also
	// what marks the glyph set delivered -- deliberately not done
	// here. A status broadcast is edge-triggered: if this snapshot is
	// dropped (SendServer returns false on a full buffer) and we had
	// already recorded the set as sent, the client would stay stale
	// until some later, unrelated status change. Leaving lastStatuses
	// untouched on a failed send makes the next tick retry. Pane
	// updates have the same problem and solve it per client instead;
	// see paneGens.
	s.broadcastLayout(ctx)
	return true
}

func (s *Server) broadcastLayout(ctx context.Context) {
	s.layoutSendMu.Lock()
	defer s.layoutSendMu.Unlock()

	s.mu.Lock()
	s.updateDashboardLocked()
	statuses := s.statusGlyphsLocked()
	titles := s.paneTitlesLocked()
	cols := layout.ToColumnData(s.strip.Columns())
	columnsChanged := !sameColumnSetAndSizes(cols, s.lastColumns)
	snapshot := protocol.MsgLayoutSnapshot{
		Columns:      cols,
		PaneStatuses: statuses,
		PaneTitles:   titles,
	}
	tps := append([]transport.Transport{}, s.transports...)
	pending := make(map[transport.Transport][]int, len(s.pendingPaneCreated))
	for tp, ids := range s.pendingPaneCreated {
		pending[tp] = append([]int(nil), ids...)
	}
	s.mu.Unlock()

	// Delivered means *every* attached client accepted it, not any
	// one of them.
	//
	// A status or title broadcast is edge-triggered, so a client
	// whose buffer was full when it fired would never see that change
	// again -- two clients of one session would disagree about what
	// the panes are doing, with nothing to retry. With no clients at
	// all there is nobody to be stale, so that counts as delivered
	// rather than retrying forever.
	delivered := true
	for _, tp := range tps {
		sent := 0
		for _, id := range pending[tp] {
			if !tp.SendServer(ctx, protocol.MsgPaneCreated{PaneID: id}) {
				break
			}
			sent++
		}
		if sent > 0 {
			s.mu.Lock()
			if len(s.pendingPaneCreated[tp]) >= sent {
				s.pendingPaneCreated[tp] = s.pendingPaneCreated[tp][sent:]
				if len(s.pendingPaneCreated[tp]) == 0 {
					delete(s.pendingPaneCreated, tp)
				}
			}
			s.mu.Unlock()
		}
		if sent != len(pending[tp]) || !tp.SendServer(ctx, snapshot) {
			delivered = false
			continue
		}
		s.mu.Lock()
		if len(s.pendingPaneCreated[tp]) == 0 {
			delete(s.pendingCreationSnapshot, tp)
		}
		s.mu.Unlock()
	}

	// Mark the glyph set clean only once it actually went somewhere.
	// This is also what keeps a verb-triggered broadcast from making
	// the next frame tick send a redundant status snapshot.
	if delivered {
		s.mu.Lock()
		s.lastStatuses = statuses
		s.lastTitles = titles
		s.lastColumns = cols
		s.mu.Unlock()
	}

	s.broadcastPaneUpdates(ctx, columnsChanged)
}

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
	if s.paneGens == nil {
		s.paneGens = make(map[transport.Transport]map[int]uint64)
	}
	if s.paneFrames == nil {
		s.paneFrames = make(map[transport.Transport]map[int]protocol.MsgPaneUpdate)
	}
	if s.paneUnderlyingGens == nil {
		s.paneUnderlyingGens = make(map[transport.Transport]map[int]uint64)
	}
	if s.paneOutputGens == nil {
		s.paneOutputGens = make(map[transport.Transport]map[int]uint64)
	}
	if s.clientScrollOffsets == nil {
		s.clientScrollOffsets = make(map[transport.Transport]map[int]int)
	}
	if s.paneOffsets == nil {
		s.paneOffsets = make(map[transport.Transport]map[int]int)
	}
	if s.clientUnreadOutput == nil {
		s.clientUnreadOutput = make(map[transport.Transport]map[int]bool)
	}
	if s.paneUnreads == nil {
		s.paneUnreads = make(map[transport.Transport]map[int]bool)
	}
	if s.clientScrollGens == nil {
		s.clientScrollGens = make(map[transport.Transport]map[int]uint64)
	}
	if s.paneSbLens == nil {
		s.paneSbLens = make(map[transport.Transport]map[int]int)
	}

	var targets []clientTarget
	panesByID := make(map[int]*Pane, len(s.panes))
	neededRenders := make(map[renderKey]bool)

	for id, p := range s.panes {
		panesByID[id] = p
		underlyingGen := p.Generation()
		outputGen := p.OutputGen()
		sbLen := p.ScrollbackLen()

		for _, tp := range tps {
			lastWireGen, hasWireGen := s.paneGens[tp][id]
			lastOutputGen, hasOutputGen := s.paneOutputGens[tp][id]
			curOffset := p.ScrollOffset()
			if m, ok := s.clientScrollOffsets[tp]; ok {
				if off, ok := m[id]; ok {
					curOffset = off
				}
			}
			lastOffset, hasOffset := s.paneOffsets[tp][id]
			curUnread := s.clientUnreadOutput[tp][id]
			lastUnread, hasUnread := s.paneUnreads[tp][id]
			lastSbLen := s.paneSbLens[tp][id]

			// Check if new output arrived while already scrolled up
			outputChanged := !hasOutputGen || outputGen != lastOutputGen
			if outputChanged && hasOffset && lastOffset > 0 && curOffset > 0 {
				curUnread = true
				if s.clientUnreadOutput[tp] == nil {
					s.clientUnreadOutput[tp] = make(map[int]bool)
				}
				s.clientUnreadOutput[tp][id] = true

				// Content pinning: if scrollback lengthened, increase offset to keep view anchored
				if sbLen > lastSbLen {
					diff := sbLen - lastSbLen
					curOffset += diff
					if curOffset > sbLen {
						curOffset = sbLen
					}
					if s.clientScrollOffsets[tp] == nil {
						s.clientScrollOffsets[tp] = make(map[int]int)
					}
					s.clientScrollOffsets[tp][id] = curOffset
				}
			}

			offsetChanged := !hasOffset || curOffset != lastOffset
			unreadChanged := !hasUnread || curUnread != lastUnread

			scrollGen := uint64(0)
			if m, ok := s.clientScrollGens[tp]; ok {
				scrollGen = m[id]
			}
			wireGen := underlyingGen + scrollGen

			dirty := force || !hasWireGen || (wireGen != lastWireGen) || offsetChanged || unreadChanged
			if !dirty {
				continue
			}

			baseline, hasBase := s.paneFrames[tp][id]
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
		if !r.accepted {
			delete(s.paneGens[r.tp], r.paneID)
			delete(s.paneFrames[r.tp], r.paneID)
			delete(s.paneUnderlyingGens[r.tp], r.paneID)
			delete(s.paneOutputGens[r.tp], r.paneID)
			delete(s.paneSbLens[r.tp], r.paneID)
			continue
		}
		// Content can change while update was being rendered or sent.
		// Retain r.frame and r.wireGen as the baseline: the client accepted and
		// applied exactly this frame. On the next tick, wireGen != lastWireGen
		// will trigger a resend as a patch against this baseline.

		if s.paneGens[r.tp] == nil {
			s.paneGens[r.tp] = make(map[int]uint64)
		}
		s.paneGens[r.tp][r.paneID] = r.wireGen

		if s.paneFrames[r.tp] == nil {
			s.paneFrames[r.tp] = make(map[int]protocol.MsgPaneUpdate)
		}
		s.paneFrames[r.tp][r.paneID] = r.frame

		if s.paneUnderlyingGens[r.tp] == nil {
			s.paneUnderlyingGens[r.tp] = make(map[int]uint64)
		}
		s.paneUnderlyingGens[r.tp][r.paneID] = r.underlyingGen

		if s.paneOutputGens[r.tp] == nil {
			s.paneOutputGens[r.tp] = make(map[int]uint64)
		}
		s.paneOutputGens[r.tp][r.paneID] = r.outputGen

		if s.paneSbLens[r.tp] == nil {
			s.paneSbLens[r.tp] = make(map[int]int)
		}
		s.paneSbLens[r.tp][r.paneID] = r.sbLen

		if s.paneOffsets[r.tp] == nil {
			s.paneOffsets[r.tp] = make(map[int]int)
		}
		s.paneOffsets[r.tp][r.paneID] = r.offset

		if s.paneUnreads[r.tp] == nil {
			s.paneUnreads[r.tp] = make(map[int]bool)
		}
		s.paneUnreads[r.tp][r.paneID] = r.unread
	}

	s.cleanExitedPanesLocked()
}

func (s *Server) cleanExitedPanesLocked() {
	for _, m := range s.paneGens {
		for id := range m {
			if _, ok := s.panes[id]; !ok {
				delete(m, id)
			}
		}
	}
	for _, m := range s.paneFrames {
		for id := range m {
			if _, ok := s.panes[id]; !ok {
				delete(m, id)
			}
		}
	}
	for _, m := range s.paneUnderlyingGens {
		for id := range m {
			if _, ok := s.panes[id]; !ok {
				delete(m, id)
			}
		}
	}
	for _, m := range s.paneOutputGens {
		for id := range m {
			if _, ok := s.panes[id]; !ok {
				delete(m, id)
			}
		}
	}
	for _, m := range s.clientScrollOffsets {
		for id := range m {
			if _, ok := s.panes[id]; !ok {
				delete(m, id)
			}
		}
	}
	for _, m := range s.paneOffsets {
		for id := range m {
			if _, ok := s.panes[id]; !ok {
				delete(m, id)
			}
		}
	}
	for _, m := range s.clientUnreadOutput {
		for id := range m {
			if _, ok := s.panes[id]; !ok {
				delete(m, id)
			}
		}
	}
	for _, m := range s.paneUnreads {
		for id := range m {
			if _, ok := s.panes[id]; !ok {
				delete(m, id)
			}
		}
	}
	for _, m := range s.clientScrollGens {
		for id := range m {
			if _, ok := s.panes[id]; !ok {
				delete(m, id)
			}
		}
	}
	for _, m := range s.paneSbLens {
		for id := range m {
			if _, ok := s.panes[id]; !ok {
				delete(m, id)
			}
		}
	}
}

// PaneSize reports pane id's current logical dimensions, as last set by
// resizePanesLocked. Exposed for observability and tests, which otherwise
// have no way to see across the package boundary that a resize landed.
//
// s.mu guards the map lookup only, released before Pane.Size: Size
// takes p.resizeMu internally, and Resize can
// hold that lock for an unbounded time (see Pane.Close's doc comment).
// Holding s.mu across the call would park it, and with it the Run loop
// and srv.Close(), on the same wedge this pattern exists to avoid
// elsewhere.
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

// stoppingLocked reports whether Close has begun. s.mu must be held; it
// is what orders this against Close's snapshot of panes and transports.
func (s *Server) stoppingLocked() bool {
	select {
	case <-s.stopCh:
		return true
	default:
		return false
	}
}

// Close hangs up all panes concurrently, and then hangs up on every
// client.
func (s *Server) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		close(s.stopCh)
		sl := s.listener
		s.mu.Unlock()
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
		s.paneGens = nil
		s.paneFrames = nil
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

// ListenWebSocket starts accepting WebSocket connections via the provided http.ServeMux.
func (s *Server) ListenWebSocket(ctx context.Context, mux *http.ServeMux, token string) {
	versionProtocol := fmt.Sprintf("wideboi.v%d", protocol.Version)
	upgrader := &websocket.Upgrader{
		ReadBufferSize:    4096,
		WriteBufferSize:   4096,
		Subprotocols:      []string{versionProtocol},
		CheckOrigin:       webSocketOriginAllowed,
		EnableCompression: true,
	}

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			reqToken := r.URL.Query().Get("token")
			if reqToken != token && websocketProtocolToken(r) != token {
				slog.Warn("websocket connection rejected: invalid token", "remote", r.RemoteAddr)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		versionOffered := false
		for _, offered := range websocket.Subprotocols(r) {
			if offered == versionProtocol {
				versionOffered = true
				break
			}
		}
		if !versionOffered {
			http.Error(w, "Unsupported wideboi protocol version", http.StatusUpgradeRequired)
			return
		}

		// gorilla writes the 101 response and every frame to the
		// hijacked conn, so counting that conn counts the wire.
		cw := transport.NewCountingResponseWriter(w)
		conn, err := upgrader.Upgrade(cw, r, nil)
		if err != nil {
			slog.Debug("websocket upgrade failed", "err", err)
			return
		}

		sConn := transport.NewWebSocketServerConn(conn, 256, cw)
		sConn.RunPumps(ctx)

		s.mu.Lock()
		if s.stoppingLocked() {
			s.mu.Unlock()
			_ = sConn.Close()
			return
		}
		s.transports = append(s.transports, sConn)
		s.mu.Unlock()

		go s.handleClientConnLoop(ctx, sConn)
	})
}

// webSocketOriginAllowed permits same-host browser connections and the local
// Vite development server. The development exception must never apply to a
// remotely addressed WebSocket endpoint.
func webSocketOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // Non-browser clients still need the token.
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	requestHost, _, err := net.SplitHostPort(r.Host)
	if err != nil || (requestHost != "localhost" && requestHost != "127.0.0.1" && requestHost != "::1") {
		return false
	}
	return u.Scheme == "http" && (u.Host == "localhost:5173" || u.Host == "127.0.0.1:5173" || u.Host == "[::1]:5173")
}
