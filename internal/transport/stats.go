package transport

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
)

// Stats is what a server-side connection has written since it opened,
// for `wideboi status --traffic` (#179). Counts and sizes only.
type Stats struct {
	Messages         uint64 // messages encoded and written
	PayloadBytes     uint64 // protobuf envelope bytes, all messages
	PanePayloadBytes uint64 // the part of PayloadBytes that was pane updates and patches
	WireBytes        uint64 // bytes written to the net.Conn, framing included
	Encode           protocol.TimingStat
}

// StatsReporter is implemented by the server-side socket and WebSocket
// conns.
type StatsReporter interface {
	TransportStats() Stats
	// EnableTiming starts timing MarshalServer. Off by default, so the
	// write pump does not read the clock per message unless asked.
	EnableTiming()
}

// WireCounter reports bytes written to a connection.
type WireCounter interface{ WireBytes() uint64 }

// sendStats is embedded by the server-side conns and updated by their
// write pumps.
type sendStats struct {
	mu     sync.Mutex
	s      Stats
	timing atomic.Bool
	wire   WireCounter // nil: WireBytes stays 0
}

func (st *sendStats) EnableTiming() { st.timing.Store(true) }

// record is called by the write pump after a successful write. encode
// is ignored unless timing is on.
func (st *sendStats) record(msg ServerMessage, payload int, encode time.Duration) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.s.Messages++
	st.s.PayloadBytes += uint64(payload)
	switch msg.(type) {
	case protocol.MsgPaneUpdate, protocol.MsgPanePatch:
		st.s.PanePayloadBytes += uint64(payload)
	}
	if st.timing.Load() {
		st.s.Encode.Add(encode)
	}
}

// marshal encodes msg, timing it only when timing is on.
func (st *sendStats) marshal(msg ServerMessage) ([]byte, time.Duration, error) {
	if !st.timing.Load() {
		payload, err := protocol.MarshalServer(msg)
		return payload, 0, err
	}
	start := time.Now()
	payload, err := protocol.MarshalServer(msg)
	return payload, time.Since(start), err
}

func (st *sendStats) TransportStats() Stats {
	st.mu.Lock()
	s := st.s
	st.mu.Unlock()
	if st.wire != nil {
		s.WireBytes = st.wire.WireBytes()
	}
	return s
}

// countingConn counts every byte written to the connection.
type countingConn struct {
	net.Conn
	written atomic.Uint64
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.written.Add(uint64(n))
	return n, err
}

func (c *countingConn) WireBytes() uint64 { return c.written.Load() }

// CountingResponseWriter counts bytes written to the hijacked connection,
// which is where gorilla/websocket writes the upgrade response and
// every frame (gorilla/websocket@v1.5.3 server.go, Upgrade). So the
// count includes the 101 response, pings and frame headers.
type CountingResponseWriter struct {
	http.ResponseWriter
	conn *countingConn
}

func NewCountingResponseWriter(w http.ResponseWriter) *CountingResponseWriter {
	return &CountingResponseWriter{ResponseWriter: w}
}

func (w *CountingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("transport: response writer cannot hijack")
	}
	c, brw, err := h.Hijack()
	if err != nil {
		return nil, nil, err
	}
	w.conn = &countingConn{Conn: c}
	return w.conn, brw, nil
}

// WireBytes is 0 until the connection has been hijacked.
func (w *CountingResponseWriter) WireBytes() uint64 {
	if w.conn == nil {
		return 0
	}
	return w.conn.WireBytes()
}
