package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

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

	// reasonMu guards reason; see CloseFor.
	reasonMu  sync.Mutex
	reason    *closeReason
	upgrading bool

	expectedOwnerPID     uint32
	expectedSizeOwnerPID uint32
	restoredWeb          RestoredWebState

	execSyscall func(bin string, args []string, env []string) error

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

	// clients tracks per-connected-transport state (sizes, pending notices, gens, baselines, scroll).
	clients map[transport.Transport]*clientState
	// layoutSendMu orders each client's creation notices before the snapshot
	// that includes them, even when broadcasts run concurrently.
	layoutSendMu sync.Mutex

	// resizeGeneration orders snapshots that release s.mu while applying
	// geometry. An older request must not overwrite a newer resize.
	resizeGeneration uint64

	// sizeOwner is the client whose viewport dimensions currently define
	// the session PTY rows and columns. Initially set by the first client
	// to attach. Viewers connect without altering session geometry. Any
	// client can claim size ownership with VerbClaimSize (#184).
	sizeOwner transport.Transport

	webServer *webServerManager

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

	macros         []protocol.Macro
	onSaveMacros   func([]protocol.Macro)
	saveMacrosMu   sync.Mutex
	saveMacrosCond *sync.Cond
	saveMacrosQ    [][]protocol.Macro
	saveMacrosStop bool
	saveMacrosDone chan struct{}

	bindings []protocol.KeyBinding

	// startedTransports tracks which transports have had handleClientConnLoop
	// started, preventing double-reader races when clients connect before Run().
	startedTransports map[transport.Transport]bool

	// remoteTransports tracks which transports are remote (such as WebSocket
	// connections), restricting access to privileged session-control messages.
	remoteTransports map[transport.Transport]bool
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

// SetMacros configures the initial input macros for the server.
func (s *Server) SetMacros(macros []protocol.Macro) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.macros = append([]protocol.Macro(nil), macros...)
}

// SetOnSaveMacros configures a callback invoked when a client saves macros.
// Saves are serialized on a single background worker to prevent out-of-order writes.
func (s *Server) SetOnSaveMacros(fn func([]protocol.Macro)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSaveMacros = fn
	s.saveMacrosMu.Lock()
	if !s.saveMacrosStop && fn != nil {
		s.ensureSaveMacrosWorkerLocked()
	}
	s.saveMacrosMu.Unlock()
}

func (s *Server) ensureSaveMacrosWorkerLocked() {
	if s.saveMacrosCond == nil {
		s.saveMacrosCond = sync.NewCond(&s.saveMacrosMu)
		s.saveMacrosDone = make(chan struct{})
		go s.runSaveMacrosWorker()
	}
}

func (s *Server) runSaveMacrosWorker() {
	defer close(s.saveMacrosDone)
	for {
		s.saveMacrosMu.Lock()
		for len(s.saveMacrosQ) == 0 && !s.saveMacrosStop {
			s.saveMacrosCond.Wait()
		}
		if len(s.saveMacrosQ) == 0 && s.saveMacrosStop {
			s.saveMacrosMu.Unlock()
			return
		}
		macros := s.saveMacrosQ[0]
		s.saveMacrosQ = s.saveMacrosQ[1:]
		s.saveMacrosMu.Unlock()

		s.mu.Lock()
		fn := s.onSaveMacros
		s.mu.Unlock()

		if fn != nil {
			fn(macros)
		}
	}
}

// Macros returns a copy of the current server macros.
func (s *Server) Macros() []protocol.Macro {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]protocol.Macro, len(s.macros))
	copy(out, s.macros)
	return out
}

// SetBindings configures the key bindings for the server.
func (s *Server) SetBindings(bindings []protocol.KeyBinding) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings = append([]protocol.KeyBinding(nil), bindings...)
}

// Bindings returns a copy of the current key bindings.
func (s *Server) Bindings() []protocol.KeyBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]protocol.KeyBinding, len(s.bindings))
	copy(out, s.bindings)
	return out
}

func (s *Server) sendConfigTo(ctx context.Context, tp transport.Transport) {
	s.mu.Lock()
	presets := s.strip.WidthPresets()
	bindings := make([]protocol.KeyBinding, len(s.bindings))
	copy(bindings, s.bindings)
	s.mu.Unlock()
	_ = tp.SendServer(ctx, protocol.MsgConfigSnapshot{
		WidthPresets:   presets,
		MinColumnWidth: layout.MinColumnWidth,
		MaxColumnWidth: layout.MaxColumnWidth,
		Bindings:       bindings,
	})
}

func (s *Server) sendMacrosTo(ctx context.Context, tp transport.Transport) {
	s.mu.Lock()
	macros := make([]protocol.Macro, len(s.macros))
	copy(macros, s.macros)
	s.mu.Unlock()
	_ = tp.SendServer(ctx, protocol.MsgMacrosSnapshot{Macros: macros})
}

func (s *Server) broadcastMacros(ctx context.Context) {
	s.mu.Lock()
	macros := make([]protocol.Macro, len(s.macros))
	copy(macros, s.macros)
	tps := append([]transport.Transport{}, s.transports...)
	s.mu.Unlock()

	msg := protocol.MsgMacrosSnapshot{Macros: macros}
	for _, tp := range tps {
		_ = tp.SendServer(ctx, msg)
	}
}

// RestoredWebState holds the web server configuration and active status
// restored from an in-place upgrade state file.
type RestoredWebState struct {
	Running    bool
	Addr       string
	Token      string
	TLSEnabled bool
}

// RestoredWebState returns any web server state restored from an in-place upgrade.
func (s *Server) RestoredWebState() RestoredWebState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restoredWeb
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
		strip:             layout.NewStrip(),
		panes:             make(map[int]*Pane),
		shell:             shell,
		cwd:               cwd,
		transports:        make([]transport.Transport, 0),
		clients:           make(map[transport.Transport]*clientState),
		startedTransports: make(map[transport.Transport]bool),
		remoteTransports:  make(map[transport.Transport]bool),
		stopCh:            make(chan struct{}),
		started:           time.Now(),
	}
	srv.saveMacrosMu.Lock()
	srv.ensureSaveMacrosWorkerLocked()
	srv.saveMacrosMu.Unlock()
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
	peer, err := transport.Handshake(conn)
	if err != nil {
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
	s.setPeerPIDLocked(sConn, peer.PID)
	s.startTransportLoopLocked(ctx, sConn)
	s.mu.Unlock()
}

// Run executes the main server event loop, processing client messages and polling descendants.
func (s *Server) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.owner == nil {
		s.startupComplete = true
	}
	for _, tp := range s.transports {
		s.startTransportLoopLocked(ctx, tp)
	}
	s.mu.Unlock()

	frameTicker := time.NewTicker(33 * time.Millisecond)
	defer frameTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return s.CloseFor(ReasonContextCancelled)
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
	p.keep = spec.Keep

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
type resizeJob struct {
	pane *Pane
	w, h int
}

func (s *Server) prepareResizePanesLocked() ([]resizeJob, uint64) {
	if s.rows <= 0 {
		return nil, 0
	}
	s.resizeGeneration++
	generation := s.resizeGeneration
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
	return jobs, generation
}

func (s *Server) applyResizeJobs(jobs []resizeJob, generation uint64) {
	for _, j := range jobs {
		if err := j.pane.ResizeOrdered(j.w, j.h, generation); err != nil {
			j.pane.recordFailure(fmt.Errorf("resize to %dx%d: %w", j.w, j.h, err))
		}
	}
}

// resizePanesLocked prepares resize jobs, releases s.mu to execute TIOCSWINSZ
// on each child, and re-acquires s.mu. For atomic handlers, prefer preparing
// jobs with prepareResizePanesLocked and applying them with applyResizeJobs
// after releasing s.mu.
func (s *Server) resizePanesLocked() {
	jobs, gen := s.prepareResizePanesLocked()
	if len(jobs) == 0 {
		return
	}

	s.mu.Unlock()
	defer s.mu.Lock()

	s.applyResizeJobs(jobs, gen)
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
	hasPendingCreation := false
	for _, cs := range s.clients {
		if cs.pendingCreationSnapshot {
			hasPendingCreation = true
			break
		}
	}
	changed := !sameStatusMap(s.statusGlyphsLocked(), s.lastStatuses) ||
		!sameStringMap(s.paneTitlesLocked(), s.lastTitles) ||
		hasPendingCreation
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
		SessionCWD:   s.cwd,
	}
	tps := append([]transport.Transport{}, s.transports...)
	pending := make(map[transport.Transport][]int)
	for tp, cs := range s.clients {
		if len(cs.pendingPaneCreated) > 0 {
			pending[tp] = append([]int(nil), cs.pendingPaneCreated...)
		}
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
			if cs := s.clients[tp]; cs != nil {
				if len(cs.pendingPaneCreated) >= sent {
					cs.pendingPaneCreated = cs.pendingPaneCreated[sent:]
				}
			}
			s.mu.Unlock()
		}
		if sent != len(pending[tp]) || !tp.SendServer(ctx, snapshot) {
			delivered = false
			continue
		}
		s.mu.Lock()
		if cs := s.clients[tp]; cs != nil {
			if len(cs.pendingPaneCreated) == 0 {
				cs.pendingCreationSnapshot = false
			}
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
