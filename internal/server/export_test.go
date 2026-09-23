package server

import "time"

// SetCloseGrace shortens the hangup grace for panes this server spawns.
//
// Deliberately in export_test.go, which is compiled only into the test
// binary: nothing but a test would ever turn this knob, so it should not
// be product API. The tests that assert the grace contract itself live in
// internal/server/ptyx and scripts/ptycheck.py and do not use this.
func (s *Server) SetCloseGrace(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeGrace = d
}
