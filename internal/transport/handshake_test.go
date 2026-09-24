package transport

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
)

// handshakeWith runs Handshake on one end of a pipe while peer drives
// the other, and returns what Handshake returned.
func handshakeWith(t *testing.T, peer func(net.Conn)) (Hello, error) {
	t.Helper()
	ours, theirs := net.Pipe()
	defer ours.Close()
	go func() {
		defer theirs.Close()
		peer(theirs)
	}()
	return Handshake(ours)
}

// drainHello reads and discards the hello Handshake sends, as any peer
// on a pipe must: net.Pipe has no buffer. A peer writes its own frame
// first -- Handshake reads while it sends -- then drains.
func drainHello(c net.Conn) {
	_, _ = readFrame(c)
}

func helloFrame(version, pid uint32) []byte {
	payload := make([]byte, helloSize)
	copy(payload, helloMagic)
	binary.BigEndian.PutUint32(payload[8:], version)
	binary.BigEndian.PutUint32(payload[12:], pid)
	return payload
}

func TestHandshakeAcceptsMatchingPeer(t *testing.T) {
	peer, err := handshakeWith(t, func(c net.Conn) {
		_ = writeFrame(c, helloFrame(protocol.Version, 4242))
		drainHello(c)
	})
	if err != nil {
		t.Fatalf("Handshake: %v", err)
	}
	if peer.Version != protocol.Version || peer.PID != 4242 {
		t.Fatalf("peer = %+v, want version %d pid 4242", peer, protocol.Version)
	}
}

func TestHandshakeBetweenTwoPeersSucceeds(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	errc := make(chan error, 1)
	go func() {
		_, err := Handshake(b)
		errc <- err
	}()
	peer, err := Handshake(a)
	if err != nil {
		t.Fatalf("Handshake a: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("Handshake b: %v", err)
	}
	if peer.PID != uint32(os.Getpid()) {
		t.Fatalf("peer pid = %d, want %d", peer.PID, os.Getpid())
	}
}

// A future version may append to the hello; the first 16 bytes are
// what every version agrees on.
func TestHandshakeIgnoresBytesAppendedToHello(t *testing.T) {
	_, err := handshakeWith(t, func(c net.Conn) {
		_ = writeFrame(c, append(helloFrame(protocol.Version, 1), "future"...))
		drainHello(c)
	})
	if err != nil {
		t.Fatalf("Handshake: %v", err)
	}
}

func TestHandshakeReportsMismatch(t *testing.T) {
	cases := []struct {
		name       string
		peer       func(net.Conn)
		wantTheirs uint32
		wantPID    uint32
	}{
		{
			name: "other version",
			peer: func(c net.Conn) {
				_ = writeFrame(c, helloFrame(protocol.Version+1, 77))
				drainHello(c)
			},
			wantTheirs: protocol.Version + 1,
			wantPID:    77,
		},
		{
			// What a pre-protobuf server actually sent (#174): the start of
			// a gob stream, read as a length prefix of 4288679936.
			name: "gob stream",
			peer: func(c net.Conn) {
				_, _ = c.Write([]byte{0xFF, 0xA0, 0x10, 0x00, 0x01, 0x02})
			},
		},
		{
			// A protobuf server from before the handshake: framed, but
			// not a hello.
			name: "protobuf frame without hello",
			peer: func(c net.Conn) {
				_ = writeFrame(c, []byte{0x0a, 0x02, 0x08, 0x01})
				drainHello(c)
			},
		},
		{
			// A pre-handshake server rejects our hello and hangs up.
			name: "hang-up before hello",
			peer: func(c net.Conn) { drainHello(c) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handshakeWith(t, tc.peer)
			var mm *MismatchError
			if !errors.As(err, &mm) {
				t.Fatalf("err = %v, want *MismatchError", err)
			}
			if mm.Ours != protocol.Version || mm.Theirs != tc.wantTheirs || mm.PID != tc.wantPID {
				t.Fatalf("mismatch = %+v, want ours %d theirs %d pid %d",
					mm, protocol.Version, tc.wantTheirs, tc.wantPID)
			}
		})
	}
}

// A dial that hangs up at once -- `wideboi ls`, `cleanup`, the
// listener's own liveness probe -- is not a mismatch worth a warning,
// and the server needs to be able to tell.
func TestHandshakeHangUpIsEOF(t *testing.T) {
	_, err := handshakeWith(t, func(c net.Conn) {})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want one wrapping io.EOF", err)
	}
}
