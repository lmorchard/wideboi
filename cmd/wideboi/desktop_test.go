//go:build desktop

package main

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestAvailableSessionNameFitsSocketAndCollision(t *testing.T) {
	base := sessionBase(strings.Repeat("a", 250))
	first, err := availableSessionName(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := availableSessionName(base, map[string]bool{first: true})
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasSuffix(second, "-2") {
		t.Fatalf("first=%q second=%q", first, second)
	}
	for _, name := range []string{first, second} {
		if !config.ValidSessionName(name) || len(config.SessionSocketPath(name)) > 103 {
			t.Fatalf("invalid session socket path: %q", config.SessionSocketPath(name))
		}
	}
}

type desktopPeer struct {
	owner     *desktopSession
	server    *transport.ServerSocketConn
	requests  chan transport.ClientMessage
	responses chan transport.ServerMessage
}

func newDesktopPeer(t *testing.T) *desktopPeer {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	client := transport.NewClientSocketConn(clientConn, 8)
	server := transport.NewServerSocketConn(serverConn, 8)
	client.RunPumps(ctx)
	server.RunPumps(ctx)
	peer := &desktopPeer{
		owner:  &desktopSession{conn: client, cancel: cancel, done: make(chan struct{})},
		server: server, requests: make(chan transport.ClientMessage, 8),
		responses: make(chan transport.ServerMessage, 8),
	}
	go func() {
		for msg := range client.ServerSendChan() {
			peer.responses <- msg
		}
		close(peer.owner.done)
	}()
	go func() {
		for msg := range server.ClientSendChan() {
			peer.requests <- msg
			switch msg.(type) {
			case protocol.MsgDetach, protocol.MsgShutdown:
				server.Close()
				return
			}
		}
	}()
	t.Cleanup(func() {
		client.Close()
		server.Close()
		cancel()
	})
	return peer
}

func peerRequest(t *testing.T, peer *desktopPeer) transport.ClientMessage {
	t.Helper()
	select {
	case msg := <-peer.requests:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("owner did not send a lifecycle message")
		return nil
	}
}

func TestDesktopKeepFromQuitReleasesOwnedSessions(t *testing.T) {
	first, second := newDesktopPeer(t), newDesktopPeer(t)
	d := &desktopApp{owned: map[string]*desktopSession{"first": first.owner, "second": second.owner}}
	if err := d.keepOwned(); err != nil {
		t.Fatal(err)
	}
	for _, peer := range []*desktopPeer{first, second} {
		if _, ok := peerRequest(t, peer).(protocol.MsgDetach); !ok {
			t.Fatal("keep did not detach an owner")
		}
	}
	if len(d.owned) != 0 {
		t.Fatalf("%d sessions remain owned after keep", len(d.owned))
	}
	d.shutdown()
}

func TestDesktopQuitStopsOnlyOwnedSession(t *testing.T) {
	owned, attached := newDesktopPeer(t), newDesktopPeer(t)
	d := &desktopApp{
		owned:   map[string]*desktopSession{"owned": owned.owner},
		windows: map[string]*application.WebviewWindow{"owned": nil, "attached": nil},
	}
	d.shutdown()
	if _, ok := peerRequest(t, owned).(protocol.MsgShutdown); !ok {
		t.Fatal("quit did not shut down the owned session")
	}
	if !attached.server.SendServer(context.Background(), protocol.MsgPaneCreated{PaneID: 1}) {
		t.Fatal("attached session was closed by quit")
	}
	select {
	case msg := <-attached.responses:
		if _, ok := msg.(protocol.MsgPaneCreated); !ok {
			t.Fatalf("attached session response = %#v", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("attached session stopped responding after quit")
	}
	select {
	case msg := <-attached.requests:
		t.Fatalf("quit sent unexpected message to attached session: %#v", msg)
	default:
	}
}
