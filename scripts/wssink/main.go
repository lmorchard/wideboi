// Command wssink is a minimal WebSocket client for traffic measurement. It
// attaches to a wideboi server, mirrors panes by applying patches (asking
// for a resync when one does not apply), counts what arrives, estimates
// permessage-deflate, and prints one JSON Summary on exit.
//
// For scripts/traffic.py, two signals print a JSON line without exiting:
// SIGUSR1 the running Summary (so a baseline can be subtracted), and
// SIGUSR2 the server's `status --traffic` report, asked for over this
// connection ({"Traffic": ..., "ReplyBytes": n}). Asking here rather than
// with the CLI matters: a CLI connection leaving makes the server resend
// every pane in full to every client, which would land in the window
// being measured.
//
//	go run ./scripts/wssink -url ws://127.0.0.1:7999/ws -token t -duration 10s
package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lmorchard/wideboi/internal/protocol"
)

func main() {
	url := flag.String("url", "ws://127.0.0.1:7999/ws", "WebSocket endpoint")
	token := flag.String("token", "", "WebSocket token (sent as a subprotocol)")
	cols := flag.Int("cols", 120, "viewport columns to attach with")
	rows := flag.Int("rows", 40, "viewport rows to attach with")
	duration := flag.Duration("duration", 0, "how long to run; 0 runs until SIGINT/SIGTERM")
	insecure := flag.Bool("insecure", false, "skip TLS certificate verification (for self-signed servers)")
	flag.Parse()

	// Register before dialing so an early signal still yields a summary.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	// SIGUSR1's default action is to terminate, so catch it from the start.
	usr1 := make(chan os.Signal, 1)
	signal.Notify(usr1, syscall.SIGUSR1)
	usr2 := make(chan os.Signal, 1)
	signal.Notify(usr2, syscall.SIGUSR2)

	protocols := []string{fmt.Sprintf("wideboi.v%d", protocol.Version)}
	if *token != "" {
		protocols = append(protocols, "wideboi-token."+base64.RawURLEncoding.EncodeToString([]byte(*token)))
	}
	var tlsConfig *tls.Config
	if *insecure {
		tlsConfig = &tls.Config{InsecureSkipVerify: true}
	}
	dialer := websocket.Dialer{
		Subprotocols:      protocols,
		HandshakeTimeout:  5 * time.Second,
		EnableCompression: true,
		TLSClientConfig:   tlsConfig,
	}
	conn, resp, err := dialer.Dial(*url, nil)
	if err != nil {
		if resp != nil {
			fatalf("dial %s: %v (HTTP %s)", *url, err, resp.Status)
		}
		fatalf("dial %s: %v", *url, err)
	}
	attach, err := protocol.MarshalClient(protocol.MsgAttach{Cols: *cols, Rows: *rows})
	if err != nil {
		fatalf("marshal attach: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, attach); err != nil {
		fatalf("send attach: %v", err)
	}

	s := newSink()
	s.onTraffic = func(ts protocol.MsgTrafficStats, n int) {
		printLine(struct {
			Traffic    protocol.MsgTrafficStats
			ReplyBytes int
		}{ts, n})
	}
	w := &writer{conn: conn}
	done := make(chan error, 1)
	go func() { done <- readLoop(conn, w, s) }()

	var timeout <-chan time.Time
	if *duration > 0 {
		timeout = time.After(*duration)
	}
	var readErr error
wait:
	for {
		select {
		case <-usr1:
			printLine(s.summary())
		case <-usr2:
			if err := w.send(protocol.MsgTrafficRequest{}); err != nil {
				fmt.Fprintf(os.Stderr, "wssink: traffic request: %v\n", err)
			}
		case <-sigs:
			break wait
		case <-timeout:
			break wait
		case readErr = <-done:
			done = nil
			break wait
		}
	}
	if done != nil {
		// Say goodbye properly so the server logs a normal closure rather
		// than a dropped connection. WriteControl is safe alongside the
		// reader's WriteMessage. Closing the conn then unblocks
		// ReadMessage; wait so the summary is read only after the reader
		// stops touching it.
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second))
		conn.Close()
		<-done
	} else {
		conn.Close()
	}
	if readErr != nil {
		fmt.Fprintf(os.Stderr, "wssink: connection ended: %v\n", readErr)
	}

	printLine(s.summary())
}

var printMu sync.Mutex

// printLine writes v as one JSON line. The reader goroutine prints traffic
// replies while main prints summaries, so lines are serialized. The last
// line printed is the final Summary.
func printLine(v any) {
	out, err := json.Marshal(v)
	if err != nil {
		fatalf("marshal %T: %v", v, err)
	}
	printMu.Lock()
	defer printMu.Unlock()
	fmt.Println(string(out))
}

// writeTimeout bounds each data write, so a server that stops reading
// cannot wedge main in a SIGUSR2 traffic request (or the reader in a
// resync) and leave the harness waiting on a reply that never comes.
const writeTimeout = 5 * time.Second

// writer serializes data messages: gorilla allows one concurrent writer,
// and both the reader (resync requests) and main (traffic requests) send.
type writer struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *writer) send(msg any) error {
	b, err := protocol.MarshalClient(msg)
	if err != nil {
		return fmt.Errorf("marshal %T: %w", msg, err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	return w.conn.WriteMessage(websocket.BinaryMessage, b)
}

// readLoop feeds every binary message to the sink and writes its replies.
func readLoop(conn *websocket.Conn, w *writer, s *sink) error {
	for {
		kind, payload, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		reply, err := s.handle(payload)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wssink: %v\n", err)
			continue
		}
		if reply == nil {
			continue
		}
		if err := w.send(reply); err != nil {
			return err
		}
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "wssink: "+format+"\n", args...)
	os.Exit(1)
}
