package server

import (
	"sort"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// clientTraffic is one connection's delivery counts for `wideboi status
// --traffic` (#179). Every connection that receives a pane delivery gets
// one, but only those that have sent MsgAttach are reported: status,
// kill-session and the traffic request itself never attach, so they are
// left out without special-casing the requester.
type clientTraffic struct {
	id       int       // assigned on attach, in attach order
	attached time.Time // zero until MsgAttach
	counts   protocol.ClientTraffic
}

// trafficLocked returns tp's entry, creating it on first use, which is
// also when tp's encode timing is turned on if timing is. s.mu held.
//
// Encode timing therefore starts with the entry, not the connection:
// messages written before it (anything sent ahead of the first attach
// or pane delivery) are counted but not timed, so
// Encode.Count < Messages is expected. Averages divide by Count.
func (s *Server) trafficLocked(tp transport.Transport) *clientTraffic {
	if s.traffic == nil {
		s.traffic = make(map[transport.Transport]*clientTraffic)
	}
	t := s.traffic[tp]
	if t == nil {
		t = &clientTraffic{}
		s.traffic[tp] = t
		if r, ok := tp.(transport.StatsReporter); ok && s.timing {
			r.EnableTiming()
		}
	}
	return t
}

// markAttachedLocked makes tp a reported client. A repeated attach keeps
// the first one's id and time. s.mu held.
func (s *Server) markAttachedLocked(tp transport.Transport) {
	t := s.trafficLocked(tp)
	if t.attached.IsZero() {
		s.nextClientID++
		t.id = s.nextClientID
		t.attached = time.Now()
	}
}

// recordSendLocked counts one pane delivery. s.mu held.
func (s *Server) recordSendLocked(tp transport.Transport, msg transport.ServerMessage, accepted bool) {
	t := s.trafficLocked(tp)
	if !accepted {
		t.counts.SendFailures++
		return
	}
	switch m := msg.(type) {
	case protocol.MsgPaneUpdate:
		t.counts.FullUpdates++
	case protocol.MsgPanePatch:
		if m.ShiftRows != 0 {
			t.counts.ShiftPatches++
		} else {
			t.counts.RowPatches++
		}
		t.counts.ChangedRows += uint64(len(m.ChangedRows))
	}
}

// forgetTrafficLocked drops tp's entry, folding it into the departed
// aggregate if it was a reported client, so a session's totals survive
// a client leaving. s.mu held.
func (s *Server) forgetTrafficLocked(tp transport.Transport) {
	if t := s.traffic[tp]; t != nil && !t.attached.IsZero() {
		s.departed.Merge(withTransportStats(tp, t.counts))
	}
	delete(s.traffic, tp)
}

// trafficReportLocked builds the reply to MsgTrafficRequest from the
// attached clients' entries. s.mu held.
func (s *Server) trafficReportLocked() protocol.MsgTrafficStats {
	now := time.Now()
	var report protocol.MsgTrafficStats
	if !s.started.IsZero() {
		report.UptimeMillis = now.Sub(s.started).Milliseconds()
	}
	for tp, t := range s.traffic {
		if t.attached.IsZero() {
			continue
		}
		c := withTransportStats(tp, t.counts)
		c.ClientID = t.id
		c.Transport = transportKind(tp)
		c.ConnectedMillis = now.Sub(t.attached).Milliseconds()
		report.Clients = append(report.Clients, c)
	}
	sort.Slice(report.Clients, func(i, j int) bool { return report.Clients[i].ClientID < report.Clients[j].ClientID })
	report.Departed = s.departed
	report.TimingEnabled = s.timing
	report.Render = s.renderTiming
	report.BuildPatch = s.buildTiming
	return report
}

// withTransportStats fills c's byte and encode fields from tp, if tp
// counts them. The in-process channel does not: it encodes nothing.
func withTransportStats(tp transport.Transport, c protocol.ClientTraffic) protocol.ClientTraffic {
	if r, ok := tp.(transport.StatsReporter); ok {
		st := r.TransportStats()
		c.Messages = st.Messages
		c.PayloadBytes = st.PayloadBytes
		c.PanePayloadBytes = st.PanePayloadBytes
		c.WireBytes = st.WireBytes
		c.Encode = st.Encode
	}
	return c
}

// transportKind names tp's transport for the report.
func transportKind(tp transport.Transport) string {
	switch tp.(type) {
	case *transport.ServerSocketConn:
		return "socket"
	case *transport.WebSocketServerConn:
		return "websocket"
	default:
		return "inproc"
	}
}
