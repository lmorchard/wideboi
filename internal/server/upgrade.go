package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/server/ptyx"
	"github.com/lmorchard/wideboi/internal/server/term"
	"golang.org/x/sys/unix"
)

type UpgradeState struct {
	Cols         int                 `json:"cols"`
	Rows         int                 `json:"rows"`
	NextPaneID   int                 `json:"next_pane_id"`
	Shell        string              `json:"shell"`
	Cwd          string              `json:"cwd"`
	StatusPaneID int                 `json:"status_pane_id"`
	Columns      []layout.Column     `json:"columns"`
	FocusPaneID  int                 `json:"focus_pane_id"`
	LastFocusID  int                 `json:"last_focus_pane_id"`
	IsCards      bool                `json:"is_cards"`
	Panes        map[int]UpgradePane `json:"panes"`
}

type UpgradePane struct {
	ID          int                `json:"id"`
	Cols        int                `json:"cols"`
	Rows        int                `json:"rows"`
	HasPTY      bool               `json:"has_pty"`
	PtyFD       int                `json:"pty_fd"`
	Pid         int                `json:"pid"`
	Keep        bool               `json:"keep"`
	IsDashboard bool               `json:"is_dashboard"`
	Exited      bool               `json:"exited"`
	ExitCode    int                `json:"exit_code"`
	GridSnap    *term.GridSnapshot `json:"grid_snap,omitempty"`
}

// cleanExecArgs removes --owner-fd flags and ensures "server" is present in arguments.
func cleanExecArgs(args []string) []string {
	var out []string
	hasServer := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "server" {
			hasServer = true
		}
		if a == "-owner-fd" || a == "--owner-fd" {
			i++ // skip flag and value
			continue
		}
		if strings.HasPrefix(a, "-owner-fd=") || strings.HasPrefix(a, "--owner-fd=") {
			continue
		}
		out = append(out, a)
	}
	if !hasServer {
		out = append([]string{"server"}, out...)
	}
	return out
}

// PrepareUpgrade freezes the server, serializes all state to a temporary file,
// clears close-on-exec on all PTY master file descriptors, and returns an exec function.
func (s *Server) PrepareUpgrade(binPath string) (func() error, error) {
	absBin, err := filepath.Abs(binPath)
	if err != nil {
		return nil, fmt.Errorf("resolving binary path: %w", err)
	}
	fi, err := os.Stat(absBin)
	if err != nil {
		return nil, fmt.Errorf("binary not found: %w", err)
	}
	if fi.Mode()&0111 == 0 {
		return nil, fmt.Errorf("binary %s is not executable", absBin)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Quiesce the server: mark as upgrading so no concurrent mutations occur
	s.upgrading = true

	state := UpgradeState{
		Cols:         s.cols,
		Rows:         s.rows,
		NextPaneID:   s.nextPaneID,
		Shell:        s.shell,
		Cwd:          s.cwd,
		StatusPaneID: s.statusPaneID,
		Panes:        make(map[int]UpgradePane),
	}

	if s.strip != nil {
		state.Columns = s.strip.Columns()
		state.FocusPaneID = s.strip.FocusedPaneID()
		state.LastFocusID = s.strip.LastFocusPaneID()
		_, isCards := s.strip.Strategy().(layout.CardStrategy)
		state.IsCards = isCards
	}

	var modifiedFDs []int
	rollback := func() {
		for _, fd := range modifiedFDs {
			_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
		}
		s.upgrading = false
	}

	for id, p := range s.panes {
		var gridSnap *term.GridSnapshot
		p.resizeMu.Lock()
		if sn, ok := p.grid.(term.Snapshotter); ok {
			gridSnap = sn.ExportSnapshot()
		}
		p.resizeMu.Unlock()

		up := UpgradePane{
			ID:          id,
			Cols:        p.cols,
			Rows:        p.rows,
			Keep:        p.keep,
			IsDashboard: p.isDashboard || id == s.statusPaneID,
			GridSnap:    gridSnap,
		}

		if p.pty != nil && p.pty.Master != nil {
			up.HasPTY = true
			up.PtyFD = int(p.pty.Master.Fd())
			up.Pid = p.pty.PID()
			if code, reaped := p.pty.ExitCode(); reaped {
				up.Exited = true
				up.ExitCode = code
			}

			// Clear close-on-exec so the file descriptor survives the syscall.Exec
			if _, err := unix.FcntlInt(uintptr(up.PtyFD), unix.F_SETFD, 0); err != nil {
				rollback()
				return nil, fmt.Errorf("clearing CLOEXEC on pty fd %d: %w", up.PtyFD, err)
			}
			modifiedFDs = append(modifiedFDs, up.PtyFD)
		}

		state.Panes[id] = up
	}

	f, err := os.CreateTemp("", "wideboi-upgrade-*.json")
	if err != nil {
		rollback()
		return nil, fmt.Errorf("creating state file: %w", err)
	}

	if err := json.NewEncoder(f).Encode(state); err != nil {
		f.Close()
		_ = os.Remove(f.Name())
		rollback()
		return nil, fmt.Errorf("serializing state: %w", err)
	}
	f.Close()

	execArgs := append([]string{absBin}, cleanExecArgs(os.Args[1:])...)
	stateFilePath := f.Name()

	return func() error {
		os.Setenv("WIDEBOI_RESTORE_STATE", stateFilePath)
		err := syscall.Exec(absBin, execArgs, os.Environ())
		// If syscall.Exec returns, it failed. Roll back state:
		s.mu.Lock()
		rollback()
		s.mu.Unlock()
		_ = os.Remove(stateFilePath)
		_ = os.Unsetenv("WIDEBOI_RESTORE_STATE")
		return err
	}, nil
}

// RestoreState restores panes, terminal contents, scrollback, and layout if WIDEBOI_RESTORE_STATE is set.
func RestoreState(s *Server) error {
	path := os.Getenv("WIDEBOI_RESTORE_STATE")
	if path == "" {
		return nil
	}
	defer os.Remove(path)
	defer os.Unsetenv("WIDEBOI_RESTORE_STATE")

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var state UpgradeState
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cols = state.Cols
	s.rows = state.Rows
	s.nextPaneID = state.NextPaneID
	s.shell = state.Shell
	s.cwd = state.Cwd
	s.statusPaneID = state.StatusPaneID

	if state.IsCards {
		s.strip.SetStrategy(layout.CardStrategy{})
	}

	s.panes = make(map[int]*Pane)
	for id, up := range state.Panes {
		var p *Pane

		if up.IsDashboard || id == state.StatusPaneID {
			p = NewCustomPane(id, term.NewVT(up.Cols, up.Rows), up.Cols, up.Rows)
			p.isDashboard = true
			if up.GridSnap != nil {
				if sn, ok := p.grid.(term.Snapshotter); ok {
					sn.RestoreSnapshot(up.GridSnap)
				}
			}
			s.panes[id] = p
			s.statusPaneID = id
			s.dashboard = NewDashboard()
		} else {
			var ptyPane *ptyx.Pane
			if up.HasPTY && up.Pid != 0 {
				var err error
				ptyPane, err = ptyx.Adopt(up.Pid, up.PtyFD, fmt.Sprintf("pty-%d", id), up.Exited, up.ExitCode)
				if err != nil {
					return fmt.Errorf("adopt pane %d (pid %d): %w", id, up.Pid, err)
				}
			}

			p = AdoptPane(id, ptyPane, term.NewVT(up.Cols, up.Rows), up.Cols, up.Rows, up.Keep)
			if up.GridSnap != nil {
				if sn, ok := p.grid.(term.Snapshotter); ok {
					sn.RestoreSnapshot(up.GridSnap)
				}
			}
			s.panes[id] = p

			if p.pty != nil {
				if up.Keep {
					drained := make(chan struct{})
					p.Start(func() { close(drained) })
					go s.watchKeptPane(id, p, drained)
				} else {
					p.Start(func() {
						s.onPaneExit(id)
					})
				}
			}
		}
	}

	for _, col := range state.Columns {
		s.strip.AddColumn(col.PaneID, col.Width, col.Height, 0)
	}

	// Restore previous focus history before final focus
	if state.LastFocusID != 0 && state.LastFocusID != state.FocusPaneID {
		s.strip.FocusPaneID(state.LastFocusID)
	}
	if state.FocusPaneID != 0 {
		s.strip.FocusPaneID(state.FocusPaneID)
	}

	s.startupComplete = true
	return nil
}
