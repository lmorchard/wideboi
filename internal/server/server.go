package server

import (
	"context"
	"fmt"
	"image"
	"log"
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

// resizePanesLocked pushes each pane's current placement size down to its
// emulator and child. Call it after anything that changes geometry: a host
// resize, a new column, a width cycle, a pane closing.
//
// Errors are collected rather than returned: one child failing TIOCSWINSZ
// must not stop the others from being resized.
func (s *Server) resizePanesLocked() {
	for _, pl := range s.strip.ComputePlacements(s.cols, s.rows) {
		p, ok := s.panes[pl.PaneID]
		if !ok {
			continue
		}
		w, h := pl.Dst.Dx(), pl.Dst.Dy()
		if err := p.Resize(w, h); err != nil {
			log.Printf("wideboi: resize pane %d to %dx%d: %v", pl.PaneID, w, h, err)
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
func (s *Server) PaneSize(id int) (cols, rows int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.panes[id]
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
func (s *Server) CursorInfo(id int) (image.Point, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.panes[id]; ok && p != nil {
		return p.CursorPosition(), p.CursorVisible()
	}
	return image.Point{}, false
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
