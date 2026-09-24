package transport

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
)

// The hello is the first frame each side sends on a Unix socket: an
// ordinary length-prefixed frame whose payload starts with helloMagic,
// then the protocol version and the sender's pid, both uint32
// big-endian. Those 16 bytes are fixed for every future version, which
// may append to them but never change them; that is what lets any two
// builds at least tell each other apart.
const (
	helloMagic = "WIDEBOI\x00"
	helloSize  = 16
)

// HandshakeCeiling bounds the wait for the peer's hello. A peer of any
// version answers in microseconds; this only matters for one that says
// nothing and keeps the connection open.
const HandshakeCeiling = 5 * time.Second

// Hello is what a peer said about itself.
type Hello struct {
	Version uint32
	PID     uint32
}

// MismatchError is a peer speaking a different protocol. Theirs is 0,
// and PID unknown (0), when the peer predates the handshake: it sent a
// frame that is no hello, bytes that are no frame, or hung up on ours.
type MismatchError struct {
	Ours, Theirs uint32
	PID          uint32
	// cause is what the peer did instead of sending a matching hello.
	cause error
}

func (e *MismatchError) Error() string {
	if e.Theirs == 0 {
		return fmt.Sprintf("peer predates wideboi protocol versioning (this side speaks v%d): %v", e.Ours, e.cause)
	}
	return fmt.Sprintf("peer speaks wideboi protocol v%d; this side speaks v%d", e.Theirs, e.Ours)
}

func (e *MismatchError) Unwrap() error { return e.cause }

// Handshake exchanges hellos on conn before anything else is sent, and
// fails unless the peer speaks protocol.Version.
//
// Ours is sent while theirs is read, not before: net.Pipe, and any
// other unbuffered conn, would deadlock two peers that both write
// first. On failure the caller closes conn, which also releases a send
// the peer never read.
func Handshake(conn net.Conn) (Hello, error) {
	_ = conn.SetDeadline(time.Now().Add(HandshakeCeiling))
	defer conn.SetDeadline(time.Time{})

	sent := make(chan error, 1)
	go func() { sent <- writeFrame(conn, encodeHello()) }()

	peer, err := readHello(conn)
	if err != nil {
		return Hello{}, err
	}
	if peer.Version != protocol.Version {
		return peer, &MismatchError{Ours: protocol.Version, Theirs: peer.Version, PID: peer.PID}
	}
	if err := <-sent; err != nil {
		return peer, fmt.Errorf("sending hello: %w", err)
	}
	return peer, nil
}

func encodeHello() []byte {
	b := make([]byte, helloSize)
	copy(b, helloMagic)
	binary.BigEndian.PutUint32(b[8:], protocol.Version)
	binary.BigEndian.PutUint32(b[12:], uint32(os.Getpid()))
	return b
}

// readHello reads the peer's first frame. Anything but a hello means a
// peer from before the handshake, including the "frame too large" that
// a gob stream's first bytes make of a length prefix: that is the
// failure #174 was filed for, and it names the wrong problem.
func readHello(conn net.Conn) (Hello, error) {
	predates := func(cause error) error {
		return &MismatchError{Ours: protocol.Version, cause: cause}
	}
	payload, err := readFrame(conn)
	switch {
	case err == nil:
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return Hello{}, predates(fmt.Errorf("it hung up without a hello: %w", err))
	case errors.Is(err, syscall.ECONNRESET):
		// Linux resets instead of EOF when the peer closed with our
		// hello unread. Still a hang-up, and callers test for one with
		// io.EOF: the owner to spot its server's startup exit, the
		// server to keep liveness probes out of its warnings.
		return Hello{}, predates(fmt.Errorf("it hung up without a hello (%v): %w", err, io.EOF))
	case errors.Is(err, errFrameTooLarge):
		return Hello{}, predates(errors.New("its first bytes are not a wideboi frame, as from a gob-era server"))
	default:
		return Hello{}, fmt.Errorf("waiting for the peer's hello: %w", err)
	}
	if len(payload) < helloSize || !bytes.Equal(payload[:8], []byte(helloMagic)) {
		return Hello{}, predates(errors.New("its first frame is not a hello"))
	}
	return Hello{
		Version: binary.BigEndian.Uint32(payload[8:]),
		PID:     binary.BigEndian.Uint32(payload[12:]),
	}, nil
}
