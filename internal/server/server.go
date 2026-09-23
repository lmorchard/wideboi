package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

// Server manages multiplexer layout, PTY sessions, and client protocol messages.
type Server struct {
	mu         sync.Mutex
	strip      *layout.Strip
	panes      map[int]*Pane
	nextPaneID int
	cols       int
	rows       int
	shell      string
	cwd        string
	transports []transport.Transport
	escapees   map[int]struct{} // Tracked descendant PIDs for teardown
	stopCh     chan struct{}
	closeOnce  sync.Once

	// lastStatuses and lastTitles are the per-pane glyph and title
	// sets as of the last layout broadcast, so the frame loop can
	// tell when either has changed.
	lastStatuses map[int]string
	lastTitles   map[int]string

	// layout is the session's strategy mode, shared with every client.
	layout protocol.LayoutMode

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

// SetLayout installs the session's layout mode.
//
// A method rather than a NewServer parameter: the zero value is
// already the scrolling strip, so only a caller that wants cards has
// to say so, and the eight existing NewServer call sites stay put.
func (s *Server) SetLayout(mode protocol.LayoutMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.layout = mode
	layout.ApplyMode(s.strip, mode)
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
		strip:      layout.NewStrip(),
		panes:      make(map[int]*Pane),
		shell:      shell,
		cwd:        cwd,
		transports: make([]transport.Transport, 0),
		escapees:   make(map[int]struct{}),
		stopCh:     make(chan struct{}),
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
				if s.dropClient(tp) {
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
				s.dropClient(tp)
				return
			}
			s.handleClientMsg(ctx, msg)
		}
	}
}

// dropClient removes tp from the broadcast set and closes it. It reports
// whether tp was the owner, and clears ownership under the same lock.
func (s *Server) dropClient(tp transport.Transport) (wasOwner bool) {
	s.mu.Lock()
	s.removeTransportLocked(tp)
	if tp == s.owner {
		s.owner = nil
		wasOwner = true
	}
	s.mu.Unlock()
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
}

// Run executes the main server event loop, processing client messages and polling descendants.
func (s *Server) Run(ctx context.Context) error {
	s.mu.Lock()
	initialTransports := append([]transport.Transport{}, s.transports...)
	s.mu.Unlock()

	for _, tp := range initialTransports {
		go s.handleClientConnLoop(ctx, tp)
	}

	ticker := time.NewTicker(1000 * time.Millisecond)
	defer ticker.Stop()

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
				s.broadcastPaneUpdates(ctx)
			}
		case <-ticker.C:
			s.pollDescendants()
		}
	}
}

func (s *Server) handleClientMsg(ctx context.Context, msg transport.ClientMessage) {
	s.mu.Lock()
	needBroadcast := false

	switch m := msg.(type) {
	case protocol.MsgAttach:
		if m.Cols > 0 && m.Rows > 0 {
			s.cols, s.rows = m.Cols, m.Rows
		}
		if len(s.panes) == 0 {
			_, _ = s.spawnPaneLocked()
			_, _ = s.spawnPaneLocked()
			s.strip.FocusLeft()
		}
		s.resizePanesLocked()
		needBroadcast = true

	case protocol.MsgResize:
		if m.Cols > 0 && m.Rows > 0 {
			s.cols, s.rows = m.Cols, m.Rows
		}
		s.resizePanesLocked()
		needBroadcast = true

	case protocol.MsgVerb:
		switch m.Verb {
		case protocol.VerbFocusLeft:
			s.strip.FocusLeft()
		case protocol.VerbFocusRight:
			s.strip.FocusRight()
		case protocol.VerbNewColumn:
			_, _ = s.spawnPaneLocked()
			s.resizePanesLocked()
		case protocol.VerbCycleWidth:
			s.strip.CycleWidth()
			s.resizePanesLocked()
		case protocol.VerbGrowWidth:
			s.strip.GrowWidth(10)
			s.resizePanesLocked()
		case protocol.VerbShrinkWidth:
			s.strip.ShrinkWidth(10)
			s.resizePanesLocked()
		case protocol.VerbKillPane:
			focusedID := s.strip.FocusedPaneID()
			if focusedID > 0 {
				s.removePaneLocked(focusedID)
				s.resizePanesLocked()
			}
		case protocol.VerbSmartJump:
			if id := s.smartJumpTargetLocked(); id > 0 {
				s.strip.FocusPaneID(id)
			}
		case protocol.VerbToggleCards:
			if s.layout == protocol.LayoutCards {
				s.layout = protocol.LayoutScroll
			} else {
				s.layout = protocol.LayoutCards
			}
			layout.ApplyMode(s.strip, s.layout)
			// Deliberately no resizePanesLocked: a pane's logical
			// width is its column's width regardless of what is
			// visible, so changing presentation must not resize
			// anything. That is the no-shrink premise.
		}
		needBroadcast = true

	case protocol.MsgFocusPane:
		// Guarded: the pane may have closed between the client's draw
		// and the click.
		if _, ok := s.panes[m.PaneID]; ok {
			s.strip.FocusPaneID(m.PaneID)
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

	s.mu.Unlock()

	if needBroadcast {
		s.broadcastLayout(ctx)
	}
}

// SpawnPane adds a new pane and column to the layout strip.
func (s *Server) SpawnPane() (int, error) {
	s.mu.Lock()

	p, err := s.spawnPaneLocked()
	if err != nil {
		s.mu.Unlock()
		return 0, err
	}
	s.mu.Unlock()

	s.broadcastLayout(context.Background())
	return p.ID(), nil
}

func (s *Server) spawnPaneLocked() (*Pane, error) {
	// Close reaps the panes it snapshotted; one spawned after that --
	// an attach or a new column during the reap -- would be nobody's
	// to reap.
	if s.stoppingLocked() {
		return nil, fmt.Errorf("server is shutting down")
	}
	s.nextPaneID++
	id := s.nextPaneID
	paneCols := max((s.cols-1)/2, 40)
	if paneCols > s.cols && s.cols > 0 {
		paneCols = s.cols
	}
	paneRows := max(s.rows-2, 20)

	p, err := NewPane(id, []string{s.shell}, paneCols, paneRows, s.cwd)
	if err != nil {
		return nil, err
	}
	p.closeGrace = s.closeGrace

	s.panes[id] = p
	s.strip.AddColumn(id, paneCols, paneRows)

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
// Resize; only other goroutines gain from the release. And it does not
// make srv.Close() unblockable in general: broadcastLayoutLocked runs on
// the very next line still under s.mu, and transport.SendServer is a
// buffered channel send that blocks once the client stops draining
// ServerSend and the 256-deep buffer fills, bounded only by a context
// main cancels AFTER guard.Stop() -- which is where srv.Close() itself
// lives. Reachable path: a child stops reading stdin -> the pty-writer
// parks -> the reply pipe fills -> vtGrid.Write parks holding se.mu ->
// the main loop parks in Draw -> ServerSend stops being drained -> the
// Run loop blocks in SendServer holding s.mu -> srv.Close() waits
// forever. Hard to reach and pre-existing. Hoisting SendServer out of
// s.mu would be a behaviour change, so the residual is recorded in
// issue #38 alongside the parked bounded-write item, which is
// its real fix.
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
func (s *Server) smartJumpTargetLocked() int {
	rank := func(st term.PaneStatus) int {
		switch st {
		case term.StatusFailed:
			return 3
		case term.StatusDone:
			return 2
		case term.StatusNeedsInput:
			return 1
		default:
			return 0
		}
	}

	bestID, bestRank := 0, 0
	for id, p := range s.panes {
		r := rank(p.Status())
		switch {
		case r == 0:
		case r > bestRank:
			bestID, bestRank = id, r
		case r == bestRank && id < bestID:
			bestID = id
		}
	}
	return bestID
}

// statusGlyphsLocked renders the current per-pane status glyphs.
// s.mu must be held.
func (s *Server) statusGlyphsLocked() map[int]string {
	out := make(map[int]string, len(s.panes))
	for id, p := range s.panes {
		out[id] = p.Status().Glyph()
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
	changed := !sameStringMap(s.statusGlyphsLocked(), s.lastStatuses) ||
		!sameStringMap(s.paneTitlesLocked(), s.lastTitles)
	s.mu.Unlock()

	if !changed {
		return false
	}
	// Call outside s.mu: broadcastLayout takes it itself. It is also
	// what marks the glyph set delivered -- deliberately not done
	// here. A status broadcast is edge-triggered, so unlike the 33ms
	// pane updates it does not self-heal: if this snapshot is dropped
	// (SendServer returns false on a full buffer) and we had already
	// recorded the set as sent, the client would stay stale until some
	// later, unrelated status change. Leaving lastStatuses untouched
	// on a failed send makes the next tick retry.
	s.broadcastLayout(ctx)
	return true
}

func (s *Server) broadcastLayout(ctx context.Context) {
	s.mu.Lock()
	placements := s.strip.ComputePlacements(s.cols, s.rows)
	statuses := s.statusGlyphsLocked()
	titles := s.paneTitlesLocked()
	snapshot := protocol.MsgLayoutSnapshot{
		Columns:      layout.ToColumnData(s.strip.Columns()),
		Placements:   layout.ToProtocol(placements),
		FocusPaneID:  s.strip.FocusedPaneID(),
		PaneStatuses: statuses,
		PaneTitles:   titles,
		Layout:       s.layout,
	}
	tps := append([]transport.Transport{}, s.transports...)
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
		if !tp.SendServer(ctx, snapshot) {
			delivered = false
		}
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

	s.broadcastPaneUpdates(ctx)
}

func (s *Server) broadcastPaneUpdates(ctx context.Context) {
	s.mu.Lock()
	updates := make([]protocol.MsgPaneUpdate, 0, len(s.panes))
	for _, p := range s.panes {
		updates = append(updates, p.UpdateMessage())
	}
	tps := append([]transport.Transport{}, s.transports...)
	s.mu.Unlock()

	for _, update := range updates {
		for _, tp := range tps {
			tp.SendServer(ctx, update)
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
// elsewhere. It removes one way to hold s.mu forever, not every way --
// see resizePanesLocked for the SendServer-under-s.mu residual that
// survives this discipline.
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

// pollDescendantsLocked walks ps to maintain a list of active descendant PIDs.
func (s *Server) pollDescendantsLocked() {
	pids := make([]int, 0, len(s.panes))
	for _, p := range s.panes {
		if pid := p.pty.PID(); pid > 0 {
			pids = append(pids, pid)
		}
	}
	if len(pids) == 0 {
		return
	}

	out, err := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		return
	}

	kids := make(map[int][]int)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 == nil && err2 == nil {
			kids[ppid] = append(kids[ppid], pid)
		}
	}

	stack := make([]int, 0, len(pids))
	for _, root := range pids {
		stack = append(stack, kids[root]...)
	}

	seen := make(map[int]struct{})
	for len(stack) > 0 {
		curr := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, ok := seen[curr]; ok {
			continue
		}
		seen[curr] = struct{}{}
		s.escapees[curr] = struct{}{}
		stack = append(stack, kids[curr]...)
	}
}

func (s *Server) pollDescendants() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pollDescendantsLocked()
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

// Close terminates all panes concurrently, cleans up escapee processes,
// and then hangs up on every client.
func (s *Server) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		close(s.stopCh)
		sl := s.listener
		s.mu.Unlock()
		// Stop answering first. The reap below takes seconds, and a
		// `wideboi` that dialled in during it would attach to a
		// session about to hang up on it; with the socket gone, it
		// starts a fresh one instead.
		if sl != nil {
			_ = sl.Close()
		}

		s.mu.Lock()
		panesToClose := make([]*Pane, 0, len(s.panes))
		for _, p := range s.panes {
			panesToClose = append(panesToClose, p)
		}
		s.panes = make(map[int]*Pane)
		// No final pollDescendantsLocked here: each pane's Close walks
		// its own live tree (ptyx.Kill's snapshot), so a poll now
		// would find nothing that walk misses. s.escapees is for what
		// the background poll saw before it left the tree (#83).
		escapees := make([]int, 0, len(s.escapees))
		for pid := range s.escapees {
			escapees = append(escapees, pid)
		}
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

		for _, pid := range escapees {
			_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
		}

		// Hang up on every client last. For a client waiting on a
		// shutdown, the closed connection is the only acknowledgement
		// it gets, so it must not arrive until the reaping above has
		// finished.
		s.mu.Lock()
		tps := s.transports
		s.transports = nil
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
