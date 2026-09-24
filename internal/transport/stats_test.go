package transport

// Internal: the socket test reads frames with readFrame, the same
// framing the client uses.

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// samplePaneUpdate is a pane full of one styled glyph, big enough that
// its payload is clearly a pane's and not an envelope's.
func samplePaneUpdate(cols, rows int) protocol.MsgPaneUpdate {
	lines := make([]protocol.LineData, rows)
	for y := range lines {
		lines[y] = make(protocol.LineData, cols)
		for x := range lines[y] {
			lines[y][x] = protocol.CellData{Content: "x", Width: 1}
		}
	}
	return protocol.MsgPaneUpdate{PaneID: 1, Generation: 1, Cols: cols, Rows: rows, Lines: lines, CursorVisible: true}
}

func marshalLen(t *testing.T, msg ServerMessage) uint64 {
	t.Helper()
	payload, err := protocol.MarshalServer(msg)
	if err != nil {
		t.Fatalf("encode %T: %v", msg, err)
	}
	return uint64(len(payload))
}

// waitStats polls until the write pump has recorded want messages. The
// pump records after its write returns, so a reader can see a frame
// before its count lands.
func waitStats(t *testing.T, r StatsReporter, want uint64) Stats {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		st := r.TransportStats()
		if st.Messages >= want {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorded %d messages, want %d", st.Messages, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// socketPair returns a pumping ServerSocketConn and a goroutine-drained
// peer, which net.Pipe needs: its writes block until read.
func socketPair(t *testing.T) (*ServerSocketConn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	a, b := net.Pipe()
	sc := NewServerSocketConn(a, 4)
	sc.RunPumps(ctx)
	go func() {
		for {
			if _, err := readFrame(b); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		sc.Close()
		b.Close()
	})
	return sc, ctx
}

func TestServerSocketConnCountsPayloadAndWireBytes(t *testing.T) {
	sc, ctx := socketPair(t)
	created := protocol.MsgPaneCreated{PaneID: 1}
	update := samplePaneUpdate(4, 2)
	sc.SendServer(ctx, created)
	sc.SendServer(ctx, update)

	st := waitStats(t, sc, 2)
	createdLen, updateLen := marshalLen(t, created), marshalLen(t, update)
	if st.Messages != 2 || st.PayloadBytes != createdLen+updateLen {
		t.Errorf("messages=%d payload=%d, want 2 and %d", st.Messages, st.PayloadBytes, createdLen+updateLen)
	}
	if st.PanePayloadBytes != updateLen {
		t.Errorf("pane payload=%d, want only the update's %d", st.PanePayloadBytes, updateLen)
	}
	// Each frame is a four-byte length prefix plus its payload.
	if st.WireBytes != st.PayloadBytes+8 {
		t.Errorf("wire=%d, want payload %d + 8", st.WireBytes, st.PayloadBytes)
	}
}

func TestEncodeTimingOnlyWhenEnabled(t *testing.T) {
	sc, ctx := socketPair(t)
	sc.SendServer(ctx, protocol.MsgPaneCreated{PaneID: 1})
	if st := waitStats(t, sc, 1); st.Encode.Count != 0 {
		t.Errorf("encode timed %d messages with timing off", st.Encode.Count)
	}
	sc.EnableTiming()
	sc.SendServer(ctx, protocol.MsgPaneCreated{PaneID: 2})
	if st := waitStats(t, sc, 2); st.Encode.Count != 1 {
		t.Errorf("encode count=%d after enabling, want 1", st.Encode.Count)
	}
}

func TestWebSocketConnCountsFrameHeaders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conns := make(chan *WebSocketServerConn, 1)
	upgrader := websocket.Upgrader{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cw := NewCountingResponseWriter(w)
		c, err := upgrader.Upgrade(cw, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			close(conns)
			return
		}
		conn := NewWebSocketServerConn(c, 4, cw)
		conn.RunPumps(ctx)
		conns <- conn
	}))
	defer s.Close()

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	conn := <-conns
	if conn == nil {
		t.Fatal("no server conn")
	}
	defer conn.Close()

	before := conn.TransportStats().WireBytes
	if before == 0 {
		t.Fatal("the 101 response was not counted")
	}
	msg := protocol.MsgPaneCreated{PaneID: 1}
	n := marshalLen(t, msg)
	if n >= 126 {
		t.Fatalf("payload %d needs an extended length header; pick a smaller message", n)
	}
	conn.SendServer(ctx, msg)
	if _, _, err := client.ReadMessage(); err != nil {
		t.Fatalf("read: %v", err)
	}
	st := waitStats(t, conn, 1)
	// An unmasked server frame under 126 bytes has a two-byte header.
	if st.WireBytes-before != n+2 || st.PayloadBytes != n {
		t.Errorf("wire delta=%d payload=%d, want %d and %d", st.WireBytes-before, st.PayloadBytes, n+2, n)
	}
}
