package transport

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
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

func TestTruncatedFrameIsNotACleanClose(t *testing.T) {
	cases := [][]byte{
		{0, 0},            // partial length prefix
		{0, 0, 0, 3, 'a'}, // partial protobuf payload
	}
	for _, data := range cases {
		_, err := readFrame(bytes.NewReader(data))
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("got %v, want unexpected EOF", err)
		}
		if isCleanClose(err) {
			t.Fatalf("truncated frame classified as clean close: %v", err)
		}
	}
	_, err := readFrame(bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) || !isCleanClose(err) {
		t.Fatalf("frame boundary EOF classified incorrectly: %v", err)
	}
}
