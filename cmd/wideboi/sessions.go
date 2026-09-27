package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// listSessions names the live sessions in dir, sorted: the *.sock files
// with valid session names that answer a dial. A socket nobody answers
// is a dead server's leftover. It is skipped, not removed; the next
// server to bind that name reclaims it under the lock.
func listSessions(dir string) ([]string, error) {
	socks, err := filepath.Glob(filepath.Join(dir, "*.sock"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, sock := range socks { // Glob sorts
		name := strings.TrimSuffix(filepath.Base(sock), ".sock")
		if !config.ValidSessionName(name) { // only what -L can address
			continue
		}
		conn, err := net.DialTimeout("unix", sock, time.Second)
		if err != nil {
			continue
		}
		conn.Close()
		names = append(names, name)
	}
	return names, nil
}

// sessionInfo is what `wideboi ls` reports about one live session.
type sessionInfo struct {
	clients int
	web     *protocol.MsgWebServerControlResponse
}

// querySession asks the server at sock how many clients are attached
// and whether its web server is up. The same exchange as runStatus: a
// status request answered by a layout snapshot, and a web status
// request answered by a control response.
func querySession(sock string) (sessionInfo, error) {
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return sessionInfo{}, err
	}
	defer conn.Close()
	if err := handshakeServer(conn, sock); err != nil {
		return sessionInfo{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)
	if !cc.SendClient(ctx, protocol.MsgStatusRequest{}) ||
		!cc.SendClient(ctx, protocol.MsgWebServerControlRequest{Action: protocol.WebServerActionStatus}) {
		return sessionInfo{}, fmt.Errorf("status request to %s failed", sock)
	}
	var info sessionInfo
	haveSnap := false
	for !haveSnap || info.web == nil {
		select {
		case msg, ok := <-cc.ServerSendChan():
			if !ok {
				return sessionInfo{}, fmt.Errorf("%s closed before sending state", sock)
			}
			switch m := msg.(type) {
			case protocol.MsgLayoutSnapshot:
				info.clients, haveSnap = m.AttachedClients, true
			case protocol.MsgWebServerControlResponse:
				info.web = &m
			}
		case <-ctx.Done():
			return sessionInfo{}, ctx.Err()
		}
	}
	return info, nil
}

// describeClients is the state column of `wideboi ls`.
func describeClients(n int) string {
	switch n {
	case 0:
		return "detached"
	case 1:
		return "1 client"
	default:
		return fmt.Sprintf("%d clients", n)
	}
}

// describeWeb is the web column of `wideboi ls`: where the session's
// web server listens. Built from Addr, never from URL -- URL carries
// the auth token, and ls output lands in scrollback.
func describeWeb(web *protocol.MsgWebServerControlResponse) string {
	if web == nil || !web.Running || web.Addr == "" {
		return "-"
	}
	scheme := "http"
	if web.TLSEnabled {
		scheme = "https"
	}
	return scheme + "://" + web.Addr
}

// writeSessionList prints each live session in dir with its state and
// web address. A session that will not say -- a server from another
// protocol version, say -- is still listed, with ? for what it would
// not tell.
func writeSessionList(w io.Writer, dir string) error {
	names, err := listSessions(dir)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	for _, n := range names {
		info, err := querySession(filepath.Join(dir, n+".sock"))
		if err != nil {
			fmt.Fprintf(tw, "%s\t?\t?\n", n)
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", n, describeClients(info.clients), describeWeb(info.web))
	}
	return tw.Flush()
}

// runList prints the live sessions, one per line with their state and
// web address: `wideboi ls`.
func runList(w io.Writer) error {
	return writeSessionList(w, config.SessionDir())
}
