package server

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lmorchard/wideboi/internal/transport"
)

// ancestryDepth bounds the walk up from a requester; enough to reach
// the agent or terminal that started it.
const ancestryDepth = 8

// processAncestry names pid and its ancestors by executable, nearest
// first ("wideboi<-zsh<-claude"), for a log line saying who asked for
// something.
//
// Names only: command lines here carry secrets (--websocket-token).
// And logging only -- nothing may ever act on this.
func processAncestry(pid int) string {
	if pid <= 0 {
		return "unknown"
	}
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,comm=").Output()
	if err != nil {
		return "unknown: " + err.Error()
	}
	table := parsePsTable(string(out))
	var names []string
	for i := 0; i < ancestryDepth && pid > 0; i++ {
		row, ok := table[pid]
		if !ok {
			break
		}
		names = append(names, filepath.Base(row.comm))
		if pid == 1 {
			break
		}
		pid = row.ppid
	}
	if len(names) == 0 {
		return "unknown"
	}
	return strings.Join(names, "<-")
}

type psRow struct {
	ppid int
	comm string
}

// parsePsTable reads "pid ppid comm" rows. comm is the rest of the line
// and may contain spaces. A row whose pid or ppid is not an integer, or
// that has no comm, is dropped rather than guessed at: shifted fields
// once turned a kill path into a mass kill (docs/LESSONS.md).
func parsePsTable(out string) map[int]psRow {
	table := make(map[int]psRow)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		// Everything after the second field, spaces and all.
		rest := strings.TrimSpace(line)
		rest = strings.TrimSpace(rest[len(fields[0]):])
		rest = strings.TrimSpace(rest[len(fields[1]):])
		table[pid] = psRow{ppid: ppid, comm: rest}
	}
	return table
}

// SetPeerPID records the pid tp's peer gave in its hello. The server
// learns it for socket clients itself; this is for the owner, whose
// handshake happens before the server exists.
func (s *Server) SetPeerPID(tp transport.Transport, pid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setPeerPIDLocked(tp, pid)
}

func (s *Server) setPeerPIDLocked(tp transport.Transport, pid uint32) {
	if s.peerPIDs == nil {
		s.peerPIDs = make(map[transport.Transport]uint32)
	}
	s.peerPIDs[tp] = pid
}

// peerPID is tp's peer's pid, 0 if unknown.
func (s *Server) peerPID(tp transport.Transport) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peerPIDs[tp]
}
