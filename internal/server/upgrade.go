package server

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/ptyx"
	"github.com/lmorchard/wideboi/internal/server/term"
	"golang.org/x/sys/unix"
)

// inputDrainCeiling bounds how long execFn waits for each pane's queued
// input to reach its child before exec. The key-writer drains in
// microseconds; this only matters for a child that stopped reading.
const inputDrainCeiling = 500 * time.Millisecond

type UpgradeState struct {
	Cols            int                 `json:"cols"`
	Rows            int                 `json:"rows"`
	NextPaneID      int                 `json:"next_pane_id"`
	Shell           string              `json:"shell"`
	Cwd             string              `json:"cwd"`
	StatusPaneID    int                 `json:"status_pane_id"`
	Columns         []layout.Column     `json:"columns"`
	FocusPaneID     int                 `json:"focus_pane_id"`
	LastFocusID     int                 `json:"last_focus_pane_id"`
	IsCards         bool                `json:"is_cards"`
	Panes           map[int]UpgradePane `json:"panes"`
	OwnerPID        uint32              `json:"owner_pid,omitempty"`
	SizeOwnerPID    uint32              `json:"size_owner_pid,omitempty"`
	StartupLaunched bool                `json:"startup_launched,omitempty"`
	WebRunning      bool                `json:"web_running,omitempty"`
	WebAddr         string              `json:"web_addr,omitempty"`
	WebToken        string              `json:"web_token,omitempty"`
	WebTLSEnabled   bool                `json:"web_tls_enabled,omitempty"`
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

func (s *Server) rollbackUpgradeLocked(modifiedFDs []int) {
	for _, fd := range modifiedFDs {
		_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
	}
	s.upgrading = false
}

func (s *Server) buildUpgradeStateLocked(webStatus protocol.MsgWebServerControlResponse) (UpgradeState, []int, error) {
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

	if s.owner != nil {
		if cs := s.clients[s.owner]; cs != nil {
			state.OwnerPID = cs.peerPID
		}
	}
	if s.sizeOwner != nil {
		if cs := s.clients[s.sizeOwner]; cs != nil {
			state.SizeOwnerPID = cs.peerPID
		}
	}
	state.StartupLaunched = s.startupLaunched
	if webStatus.Running {
		state.WebRunning = true
		state.WebAddr = webStatus.Addr
		state.WebToken = webStatus.Token
		state.WebTLSEnabled = webStatus.TLSEnabled
	}

	var modifiedFDs []int
	for id, p := range s.panes {
		var gridSnap *term.GridSnapshot
		p.resizeMu.Lock()
		cols, rows := p.cols, p.rows
		if sn, ok := p.grid.(term.Snapshotter); ok {
			gridSnap = sn.ExportSnapshot()
		}
		p.resizeMu.Unlock()

		up := UpgradePane{
			ID:          id,
			Cols:        cols,
			Rows:        rows,
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
				return state, modifiedFDs, fmt.Errorf("clearing CLOEXEC on pty fd %d: %w", up.PtyFD, err)
			}
			modifiedFDs = append(modifiedFDs, up.PtyFD)
		}

		state.Panes[id] = up
	}

	return state, modifiedFDs, nil
}

// PrepareUpgrade freezes the server and returns an exec function.
// Pane snapshots and state serialization occur inside the returned exec function
// immediately prior to exec, eliminating output loss during client drain.
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

	execArgs := append([]string{absBin}, cleanExecArgs(os.Args[1:])...)

	return func() error {
		// Capture web server status outside s.mu to avoid lock-order inversion
		// (webServer methods take w.mu and then s.mu to disconnect clients).
		var webStatus protocol.MsgWebServerControlResponse
		s.mu.Lock()
		ws := s.webServer
		s.mu.Unlock()
		if ws != nil {
			webStatus = ws.Status()
		}

		s.mu.Lock()
		// Hand queued keys to the children before the snapshot, so any
		// output they cause has the best chance of being in it, and
		// before exec, which would take them with it. s.mu stays held
		// from here through exec so no handler can queue more; a
		// successful exec never returns to release it.
		for _, p := range s.panes {
			if !p.drainInput(inputDrainCeiling) {
				slog.Warn("upgrade: pane input did not drain before exec", "pane", p.id)
			}
		}
		state, modifiedFDs, err := s.buildUpgradeStateLocked(webStatus)
		if err != nil {
			s.rollbackUpgradeLocked(modifiedFDs)
			s.mu.Unlock()
			return err
		}

		f, err := os.CreateTemp("", "wideboi-upgrade-*.state")
		if err != nil {
			s.rollbackUpgradeLocked(modifiedFDs)
			s.mu.Unlock()
			return fmt.Errorf("creating state file: %w", err)
		}

		gw := gzip.NewWriter(f)
		if err := json.NewEncoder(gw).Encode(state); err != nil {
			_ = gw.Close()
			f.Close()
			_ = os.Remove(f.Name())
			s.rollbackUpgradeLocked(modifiedFDs)
			s.mu.Unlock()
			return fmt.Errorf("serializing state: %w", err)
		}
		if err := gw.Close(); err != nil {
			f.Close()
			_ = os.Remove(f.Name())
			s.rollbackUpgradeLocked(modifiedFDs)
			s.mu.Unlock()
			return fmt.Errorf("closing compressed state: %w", err)
		}
		f.Close()

		stateFilePath := f.Name()
		os.Setenv("WIDEBOI_RESTORE_STATE", stateFilePath)
		execSys := syscall.Exec
		if s.execSyscall != nil {
			execSys = s.execSyscall
		}
		err = execSys(absBin, execArgs, os.Environ())
		// If syscall.Exec returns, it failed. Roll back state:
		s.rollbackUpgradeLocked(modifiedFDs)
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

	var r io.Reader = f
	magic := make([]byte, 2)
	if n, err := f.ReadAt(magic, 0); err == nil && n == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gr, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("reading compressed state: %w", err)
		}
		defer gr.Close()
		r = gr
	}

	var state UpgradeState
	if err := json.NewDecoder(r).Decode(&state); err != nil {
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
	s.startupLaunched = state.StartupLaunched
	s.expectedOwnerPID = state.OwnerPID
	s.expectedSizeOwnerPID = state.SizeOwnerPID
	s.restoredWeb = RestoredWebState{
		Running:    state.WebRunning,
		Addr:       state.WebAddr,
		Token:      state.WebToken,
		TLSEnabled: state.WebTLSEnabled,
	}

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
			p.SetQueryTheme(s.queryTheme)
			p.SetOnBell(func() {
				s.onPaneBell(id)
			})

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
