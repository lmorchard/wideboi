package server

import (
	"context"
	"encoding/base64"
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

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
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
	transports      []transport.Transport
	stopCh          chan struct{}
	closeOnce       sync.Once

	// lastStatuses and lastTitles are the per-pane glyph and title
	// sets as of the last layout broadcast, so the frame loop can
	// tell when either has changed.
	lastStatuses map[int]protocol.PaneStatus
	lastTitles   map[int]string
	// Creation notices are retried until accepted, followed by a snapshot.
	pendingPaneCreated      map[transport.Transport][]int
	pendingCreationSnapshot map[transport.Transport]bool
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

	// clientSizes records the last known window dimensions of each connected
	// client. The server session size is the minimum among all clients,
	// preventing a large client from cropping a smaller one.
	clientSizes map[transport.Transport]protocol.MsgResize

	// paneSendMu serializes broadcastPaneUpdates. The Run loop, every
	// client's message loop (via broadcastLayout) and onPaneExit all
	// broadcast, and two overlapping rounds could deliver an older
	// render last while recording the newer generation, leaving that
	// client stale with nothing left to trigger a resend. Taken before
	// s.mu, never while holding it.
	paneSendMu sync.Mutex

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

	// closeGrace is handed to every pane this server spawns. Zero means
	// the CloseGrace default; see Pane.graceOrDefault. Only a test sets
	// it, via SetCloseGrace in export_test.go.
	closeGrace time.Duration
}

// StartupPane is a pane created on the first attach to a new session.
type StartupPane struct {
	Command string
	Width   int
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
		stopCh:                  make(chan struct{}),
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
			sConn := transport.NewServerSocketConn(conn, 256)
			sConn.RunPumps(ctx)

			s.mu.Lock()
			if s.stoppingLocked() {
				// Accepted just as Close shut the listener. Close
				// has taken, or is about to take, its snapshot of
				// the transports, so this one would never be hung up.
				s.mu.Unlock()
				_ = sConn.Close()
				continue
			}
			s.transports = append(s.transports, sConn)
			s.mu.Unlock()

			go s.handleClientConnLoop(ctx, sConn)
		}
	}()
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
	s.removeTransportLocked(tp)
	if tp == s.owner {
		s.owner = nil
		wasOwner = true
	}
	s.mu.Unlock()
	// Broadcast layout outside the lock to push the resized panes to remaining clients
	s.broadcastLayout(ctx)
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
	delete(s.clientSizes, tp)
	delete(s.pendingPaneCreated, tp)
	delete(s.pendingCreationSnapshot, tp)
	s.recomputeSessionSizeLocked()
	s.resizePanesLocked()
}

// Run executes the main server event loop, processing client messages and polling descendants.
func (s *Server) Run(ctx context.Context) error {
	s.mu.Lock()
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
			// broadcastLayout already ends with a pane-update
			// broadcast, so only send one separately when it did
			// not fire.
			if !s.broadcastLayoutIfStatusChanged(ctx) {
				s.broadcastPaneUpdates(ctx, false)
			}
		}
	}
}

func (s *Server) handleClientMsg(ctx context.Context, tp transport.Transport, msg transport.ClientMessage) {
	s.mu.Lock()
	needBroadcast := false
	resyncPaneID := 0
	createdPaneID := 0

	switch m := msg.(type) {
	case protocol.MsgPaneResync:
		resyncPaneID = m.PaneID
	case protocol.MsgStatusRequest:
		needBroadcast = true

	case protocol.MsgAttach:
		if m.Cols > 0 && m.Rows > 0 {
			s.clientSizes[tp] = protocol.MsgResize{Cols: m.Cols, Rows: m.Rows}
			s.recomputeSessionSizeLocked()
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

	case protocol.MsgResize:
		if m.Cols > 0 && m.Rows > 0 {
			s.clientSizes[tp] = protocol.MsgResize{Cols: m.Cols, Rows: m.Rows}
			s.recomputeSessionSizeLocked()
		}
		s.resizePanesLocked()
		needBroadcast = true

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
			if m.PaneID > 0 {
				s.removePaneLocked(m.PaneID)
				s.resizePanesLocked()
			}
		case protocol.VerbToggleCards:
			// Reserved: layout is the client's (#92). An older client
			// may still send it; there is nothing to do.
		}
		// Every other verb changes the strip. The reserved one changes
		// nothing, and a snapshot costs a forced resend of every pane to
		// every client, so it must not broadcast.
		if m.Verb != protocol.VerbToggleCards {
			needBroadcast = true
		}

	case protocol.MsgInput:
		if p, ok := s.panes[m.PaneID]; ok {
			if len(m.Data) > 0 {
				_, _ = p.Write(m.Data)
			} else if !m.Key.IsZero() {
				p.SendKey(m.Key.Decode())
			}
		}

	case protocol.MsgMouse:
		if p, ok := s.panes[m.PaneID]; ok {
			p.SendMouse(m.Decode())
		}

	case protocol.MsgScroll:
		if p, ok := s.panes[m.PaneID]; ok {
			p.SetScrollOffset(p.ScrollOffset() + m.Delta)
		}
	}
	if tp != nil && createdPaneID != 0 {
		s.pendingPaneCreated[tp] = append(s.pendingPaneCreated[tp], createdPaneID)
		s.pendingCreationSnapshot[tp] = true
	}

	s.mu.Unlock()
	if resyncPaneID > 0 {
		// Take the broadcast lane before invalidating the baseline. An
		// in-flight patch may finish first, but the full resend follows it.
		s.paneSendMu.Lock()
		s.mu.Lock()
		delete(s.paneGens[tp], resyncPaneID)
		delete(s.paneFrames[tp], resyncPaneID)
		s.mu.Unlock()
		s.paneSendMu.Unlock()
		s.broadcastPaneUpdates(ctx, false)
	}

	if needBroadcast {
		s.broadcastLayout(ctx)
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
	p, err := NewPane(id, argv, paneCols, paneRows, s.cwd)
	if err != nil {
		return nil, err
	}
	p.closeGrace = s.closeGrace

	s.panes[id] = p
	s.strip.AddColumn(id, paneCols, paneRows, afterPaneID)

	p.Start(func() {
		// Called when PTY reader hits EOF
		s.onPaneExit(id)
	})

	return p, nil
}

func (s *Server) onPaneExit(id int) {
	s.mu.Lock()
	_, ok := s.panes[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	s.strip.KillPane(id)
	p := s.panes[id]
	delete(s.panes, id)
	s.resizePanesLocked()
	s.mu.Unlock()

	s.broadcastLayout(context.Background())
	_ = p.Close()
}

func (s *Server) removePaneLocked(id int) {
	p, ok := s.panes[id]
	if !ok {
		return
	}
	s.strip.KillPane(id)
	delete(s.panes, id)
	go p.Close()
}

// recomputeSessionSizeLocked updates s.cols and s.rows to the minimum dimensions
// among all connected clients.
func (s *Server) recomputeSessionSizeLocked() {
	if len(s.clientSizes) == 0 {
		return
	}
	minCols, minRows := 0, 0
	first := true
	for _, sz := range s.clientSizes {
		if first {
			minCols, minRows = sz.Cols, sz.Rows
			first = false
		} else {
			if sz.Cols < minCols {
				minCols = sz.Cols
			}
			if sz.Rows < minRows {
				minRows = sz.Rows
			}
		}
	}
	s.cols, s.rows = minCols, minRows
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
	statuses := s.statusGlyphsLocked()
	titles := s.paneTitlesLocked()
	snapshot := protocol.MsgLayoutSnapshot{
		Columns:      layout.ToColumnData(s.strip.Columns()),
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
		s.mu.Unlock()
	}

	s.broadcastPaneUpdates(ctx, true)
}

// broadcastPaneUpdates sends each pane to every client that has not
// accepted its current generation. force sends every pane to every
// client: broadcastLayout needs that, because a snapshot can prune a
// client's mirror or replace it with a blank one (client.go, the
// MsgLayoutSnapshot case), so an unchanged pane must still be resent
// after one.
func (s *Server) broadcastPaneUpdates(ctx context.Context, force bool) {
	s.paneSendMu.Lock()
	defer s.paneSendMu.Unlock()

	type target struct {
		tp       transport.Transport
		baseline protocol.MsgPaneUpdate
		hasBase  bool
	}
	type outgoing struct {
		pane   *Pane
		update protocol.MsgPaneUpdate
		ready  bool
		gen    uint64
		to     []target
	}
	s.mu.Lock()
	tps := append([]transport.Transport{}, s.transports...)
	var out []outgoing
	for id, p := range s.panes {
		// Read before rendering. A write landing in between leaves
		// the recorded generation behind the content, so the next
		// tick resends: one update too many, never one too few.
		gen := p.Generation()
		var to []target
		for _, tp := range tps {
			last, ok := s.paneGens[tp][id]
			if force || !ok || last != gen {
				baseline, hasBase := s.paneFrames[tp][id]
				to = append(to, target{tp: tp, baseline: baseline, hasBase: hasBase && ok && !force})
			}
		}
		if len(to) > 0 {
			out = append(out, outgoing{pane: p, gen: gen, to: to})
		}
	}
	s.mu.Unlock()

	// Rendering copies the entire grid and can wait for a pane resize.
	// Neither operation may hold the server mutex: focus, input, close,
	// and other panes must remain responsive during that work.
	for i := range out {
		update, ok := out[i].pane.UpdateMessage()
		if ok {
			update.Generation = out[i].gen
			out[i].update = update
			out[i].ready = true
		}
	}

	type result struct {
		tp       transport.Transport
		id       int
		pane     *Pane
		gen      uint64
		frame    protocol.MsgPaneUpdate
		accepted bool
	}
	var results []result
	for _, o := range out {
		if !o.ready {
			continue
		}
		s.mu.Lock()
		current := s.panes[o.update.PaneID] == o.pane
		s.mu.Unlock()
		if !current {
			continue
		}
		for _, target := range o.to {
			var message transport.ServerMessage = o.update
			if target.hasBase {
				if patch, ok := protocol.BuildPanePatch(target.baseline, o.update); ok {
					message = patch
				}
			}
			results = append(results, result{target.tp, o.update.PaneID, o.pane, o.gen, o.update, target.tp.SendServer(ctx, message)})
		}
	}

	// Runs even when nothing was sent, so records for exited panes go
	// when the last pane does.
	s.mu.Lock()
	defer s.mu.Unlock()
	present := make(map[transport.Transport]bool, len(s.transports))
	for _, tp := range s.transports {
		present[tp] = true
	}
	if s.paneGens == nil {
		s.paneGens = make(map[transport.Transport]map[int]uint64)
	}
	if s.paneFrames == nil {
		s.paneFrames = make(map[transport.Transport]map[int]protocol.MsgPaneUpdate)
	}
	for _, r := range results {
		// A client dropped mid-send must not be re-added, and a pane
		// that exited mid-send has nothing left to track.
		if !present[r.tp] {
			continue
		}
		if s.panes[r.id] != r.pane {
			continue
		}
		if !r.accepted {
			// Forget rather than leave alone: a forced resend goes to
			// clients whose record may already equal gen, and leaving
			// that in place would mean no later tick retries it.
			delete(s.paneGens[r.tp], r.id)
			delete(s.paneFrames[r.tp], r.id)
			continue
		}
		// Content can change while the update is being rendered or sent.
		// Leave this client behind so the next tick sends the new state.
		if r.pane.Generation() != r.gen {
			delete(s.paneGens[r.tp], r.id)
			delete(s.paneFrames[r.tp], r.id)
			continue
		}
		m := s.paneGens[r.tp]
		if m == nil {
			m = make(map[int]uint64)
			s.paneGens[r.tp] = m
		}
		m[r.id] = r.gen
		frames := s.paneFrames[r.tp]
		if frames == nil {
			frames = make(map[int]protocol.MsgPaneUpdate)
			s.paneFrames[r.tp] = frames
		}
		frames[r.id] = r.frame
	}
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

		for _, p := range panesToClose {
			wg.Add(1)
			go func(p *Pane) {
				defer wg.Done()
				if err := p.Close(); err != nil {
					errMu.Lock()
					errs = append(errs, fmt.Errorf("pane %d: %w", p.ID(), err))
					errMu.Unlock()
				}
			}(p)
		}
		wg.Wait()

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
	upgrader := &websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		Subprotocols:    []string{"wideboi"},
		CheckOrigin:     webSocketOriginAllowed,
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

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Debug("websocket upgrade failed", "err", err)
			return
		}

		sConn := transport.NewWebSocketServerConn(conn, 256)
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
