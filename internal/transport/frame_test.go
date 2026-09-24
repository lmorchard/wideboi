package transport

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
)

type oneByteWriter struct{ bytes.Buffer }

func (w *oneByteWriter) Write(p []byte) (int, error) { return w.Buffer.Write(p[:1]) }

func TestFrameHandlesShortWrites(t *testing.T) {
	var wire oneByteWriter
	payload := []byte("protobuf")
	if err := writeFrame(&wire, payload); err != nil {
		t.Fatal(err)
	}
	got, err := readFrame(&wire.Buffer)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

func TestFrameRejectsOversizedPayload(t *testing.T) {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], maxFrameSize+1)
	if _, err := readFrame(bytes.NewReader(header[:])); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("got error %v, want oversized frame", err)
	}
}

// A server shutting down closes its conns from outside the write pump,
// so a frame cut off mid-write is an ordinary end of connection, not a
// fault that should make `wideboi attach` exit non-zero.
func TestTruncatedFrameIsACleanClose(t *testing.T) {
	cases := [][]byte{
		{0, 0},            // partial length prefix
		{0, 0, 0, 3, 'a'}, // partial protobuf payload
	}
	for _, data := range cases {
		_, err := readFrame(bytes.NewReader(data))
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("got %v, want unexpected EOF", err)
		}
		if !isCleanClose(err) {
			t.Fatalf("truncated frame classified as a fault: %v", err)
		}
	}
	_, err := readFrame(bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) || !isCleanClose(err) {
		t.Fatalf("frame boundary EOF classified incorrectly: %v", err)
	}
}

// Truncation is forgiven only at the framing layer. A frame that arrived
// whole but holds truncated protobuf is a real protocol fault and must
// still reach the user.
func TestMalformedPayloadIsNotACleanClose(t *testing.T) {
	_, err := protocol.UnmarshalClient([]byte{0x0a, 0x04, 0x08}) // attach, cut short
	if err == nil {
		t.Fatal("malformed payload decoded without error")
	}
	if isCleanClose(err) {
		t.Fatalf("malformed payload classified as clean close: %v", err)
	}
}
