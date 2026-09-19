package server

import (
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"strconv"
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
	mu         sync.Mutex
	strip      *layout.Strip
	panes      map[int]*Pane
	nextPaneID int
	cols       int
	rows       int
	shell      string
	cwd        string
	transport  *transport.InProcChannel
	escapees   map[int]struct{} // Tracked descendant PIDs for teardown
	stopCh     chan struct{}
	closeOnce  sync.Once
}

// NewServer initializes a Server instance connected via transport.
func NewServer(tp *transport.InProcChannel, shell, cwd string) *Server {
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return &Server{
		strip:     layout.NewStrip(),
		panes:     make(map[int]*Pane),
		shell:     shell,
		cwd:       cwd,
		transport: tp,
		escapees:  make(map[int]struct{}),
		stopCh:    make(chan struct{}),
	}
}

// Run executes the main server event loop, processing client messages and polling descendants.
func (s *Server) Run(ctx context.Context) error {
	ticker := time.NewTicker(1000 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return s.Close()
		case <-s.stopCh:
			return nil
		case msg, ok := <-s.transport.ClientSend:
			if !ok {
				return s.Close()
			}
			s.handleClientMsg(ctx, msg)
		case <-ticker.C:
			s.pollDescendants()
		}
	}
}

func (s *Server) handleClientMsg(ctx context.Context, msg transport.ClientMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch m := msg.(type) {
	case protocol.MsgAttach:
		s.cols, s.rows = m.Cols, m.Rows
		if len(s.panes) == 0 {
			_, _ = s.spawnPaneLocked()
			_, _ = s.spawnPaneLocked()
			s.strip.FocusLeft()
		}
		s.resizePanesLocked()
		s.broadcastLayoutLocked(ctx)

	case protocol.MsgResize:
		s.cols, s.rows = m.Cols, m.Rows
		s.resizePanesLocked()
		s.broadcastLayoutLocked(ctx)

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
		case protocol.VerbKillPane:
			focusedID := s.strip.FocusedPaneID()
			if focusedID > 0 {
				s.removePaneLocked(focusedID)
				s.resizePanesLocked()
			}
		case protocol.VerbSmartJump:
			for id, p := range s.panes {
				st := p.Status()
				if st == term.StatusNeedsInput || st == term.StatusFailed {
					s.strip.FocusPaneID(id)
					break
				}
			}
		}
		s.broadcastLayoutLocked(ctx)

	case protocol.MsgInput:
		if p, ok := s.panes[m.PaneID]; ok {
			if len(m.Data) > 0 {
				_, _ = p.Write(m.Data)
			} else {
				p.SendKey(m.Key)
			}
		}

	case protocol.MsgScroll:
		if p, ok := s.panes[m.PaneID]; ok {
			p.SetScrollOffset(p.ScrollOffset() + m.Delta)
		}
	}
}

// SpawnPane adds a new pane and column to the layout strip.
func (s *Server) SpawnPane() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.spawnPaneLocked()
	if err != nil {
		return 0, err
	}
	s.broadcastLayoutLocked(context.Background())
	return p.ID(), nil
}

func (s *Server) spawnPaneLocked() (*Pane, error) {
	s.nextPaneID++
	id := s.nextPaneID
	paneCols := max((s.cols-1)/2, 40)
	if paneCols > s.cols && s.cols > 0 {
		paneCols = s.cols
	}
	paneRows := max(s.rows-1, 20)

	p, err := NewPane(id, []string{s.shell}, paneCols, paneRows, s.cwd)
	if err != nil {
		return nil, err
	}

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
	s.broadcastLayoutLocked(context.Background())
	s.mu.Unlock()

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
// that needs s.mu on one wedged child -- notably srv.Close() from the
// client goroutine, i.e. the user could no longer quit. It does NOT rescue
// the Run loop itself, which still blocks inside the wedged Resize call;
// only other goroutines gain anything from the lock being released. So the
// geometry is read and the lock released before any pane is actually
// touched, following DrawPane's precedent, and the lock is reacquired
// before returning so the caller's held lock is honored on exit.
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

func (s *Server) broadcastLayoutLocked(ctx context.Context) {
	placements := s.strip.ComputePlacements(s.cols, s.rows)
	statuses := make(map[int]string)
	for id, p := range s.panes {
		statuses[id] = p.Status().Glyph()
	}
	snapshot := protocol.MsgLayoutSnapshot{
		Placements:   layout.ToProtocol(placements),
		FocusPaneID:  s.strip.FocusedPaneID(),
		PaneStatuses: statuses,
	}
	s.transport.SendServer(ctx, snapshot)
}

// PaneSize reports pane id's current logical dimensions, as last set by
// resizePanesLocked. Exposed for observability and tests, which otherwise
// have no way to see across the package boundary that a resize landed.
//
// s.mu guards the map lookup only, released before Pane.Size, matching
// DrawPane's precedent: Size takes p.resizeMu internally, and Resize can
// hold that lock for an unbounded time (see Pane.Close's doc comment).
// Holding s.mu across the call would park it, and with it the Run loop and
// srv.Close(), on the same wedge this pattern exists to avoid elsewhere.
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

// DrawPane draws pane id's cell buffer onto dst within area.
func (s *Server) DrawPane(id int, dst uv.Screen, area image.Rectangle) {
	s.mu.Lock()
	p, ok := s.panes[id]
	s.mu.Unlock()
	if ok && p != nil {
		p.Draw(dst, area)
	}
}

// CursorInfo returns the cursor position and visibility for pane id.
//
// s.mu guards the map lookup only, released before CursorPosition and
// CursorVisible, matching PaneSize and DrawPane's precedent: both reach
// SafeEmulator's se.mu.RLock, which a blocked Emulator.Write can hold
// against a child that has stopped reading its stdin. Holding s.mu across
// the call would park it, and with it the Run loop and srv.Close(), on
// the same wedge this pattern exists to avoid elsewhere.
func (s *Server) CursorInfo(id int) (image.Point, bool) {
	s.mu.Lock()
	p, ok := s.panes[id]
	s.mu.Unlock()
	if !ok || p == nil {
		return image.Point{}, false
	}
	return p.CursorPosition(), p.CursorVisible()
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

// Close terminates all panes concurrently and cleans up escapee processes.
func (s *Server) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		close(s.stopCh)

		s.mu.Lock()
		panesToClose := make([]*Pane, 0, len(s.panes))
		for _, p := range s.panes {
			panesToClose = append(panesToClose, p)
		}
		s.panes = make(map[int]*Pane)
		s.pollDescendantsLocked()
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

		if len(errs) > 0 {
			closeErr = fmt.Errorf("server close: %v", errs)
		}
	})
	return closeErr
}
