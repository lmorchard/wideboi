package transport

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/lmorchard/wideboi/internal/protocol"
)

// A Unix socket is a byte stream, so every protobuf message has a
// four-byte big-endian length prefix. WebSocket messages are framed already.
const maxFrameSize = 64 << 20

var errFrameTooLarge = errors.New("protobuf frame too large")

// writeFrame sends the prefix and payload in one buffer: pane updates are
// the hot path, and one write per message beats two.
func writeFrame(w io.Writer, data []byte) error {
	if len(data) > maxFrameSize {
		return fmt.Errorf("%w: %d", errFrameTooLarge, len(data))
	}
	buf := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(buf, uint32(len(data)))
	copy(buf[4:], data)
	for len(buf) > 0 {
		n, err := w.Write(buf)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		buf = buf[n:]
	}
	return nil
}

// readFrame returns io.EOF only at a frame boundary. A peer that vanishes
// mid-frame yields io.ErrUnexpectedEOF, which isCleanClose also accepts:
// a server shutting down closes conns from outside the write pump.
func readFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size > maxFrameSize {
		return nil, fmt.Errorf("%w: %d", errFrameTooLarge, size)
	}
	payload := make([]byte, size)
	_, err := io.ReadFull(r, payload)
	return payload, err
}

// WriteClientFrame marshals and writes a length-prefixed ClientMessage frame.
func WriteClientFrame(w io.Writer, msg ClientMessage) error {
	payload, err := protocol.MarshalClient(msg)
	if err != nil {
		return err
	}
	return writeFrame(w, payload)
}

// ReadServerFrame reads and unmarshals a length-prefixed ServerMessage frame.
func ReadServerFrame(r io.Reader) (ServerMessage, error) {
	payload, err := readFrame(r)
	if err != nil {
		return nil, err
	}
	return protocol.UnmarshalServer(payload)
}
