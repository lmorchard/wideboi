package server

import "log/slog"

// Why a session ended. Recorded by CloseFor and reported by CloseReason,
// for the exits log: a server that ended without saying why is what made
// an abrupt exit undiagnosable.
const (
	ReasonOwnerLeft        = "owner-left"
	ReasonShutdownRequest  = "shutdown-request"
	ReasonLastPaneClosed   = "last-pane-closed"
	ReasonLastPaneExited   = "last-pane-exited"
	ReasonContextCancelled = "context-cancelled"
	ReasonSignal           = "signal"
	ReasonUnspecified      = "unspecified"
)

type closeReason struct {
	reason string
	attrs  []any
}

// CloseFor records why the session is ending, then closes it. The first
// reason wins: Close is idempotent, and later triggers are echoes of the
// one that started it.
//
// The reason has its own lock rather than s.mu: callers of Close must
// not hold s.mu (#43), and this runs on the way there.
func (s *Server) CloseFor(reason string, attrs ...any) error {
	s.reasonMu.Lock()
	if s.reason == nil {
		s.reason = &closeReason{reason: reason, attrs: attrs}
		slog.Info("server closing", append([]any{"reason", reason}, attrs...)...)
	}
	s.reasonMu.Unlock()
	return s.Close()
}

// CloseReason reports why the server closed, and the details recorded
// with it: ReasonUnspecified if it was closed without one, by a direct
// Close, or has not closed.
func (s *Server) CloseReason() (string, []any) {
	s.reasonMu.Lock()
	defer s.reasonMu.Unlock()
	if s.reason == nil {
		return ReasonUnspecified, nil
	}
	return s.reason.reason, s.reason.attrs
}
