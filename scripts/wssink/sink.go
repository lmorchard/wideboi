package main

import (
	"compress/flate"
	"io"
	"sync"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
)

// Summary is what wssink prints on exit, one JSON object.
type Summary struct {
	// Messages counts every binary message received, of any kind
	// (layout snapshots, titles, and so on as well as pane traffic).
	Messages, PaneUpdates, RowPatches, ShiftPatches, Resyncs uint64
	// FullBytes, RowPatchBytes and ShiftPatchBytes split PayloadBytes by
	// pane message kind (full updates, row-only patches, shift patches),
	// so a report can give bytes per full and per patch rather than a
	// blend. The server's per-client counters do not split by kind. A
	// patch that fails to apply is still counted: its bytes were sent.
	FullBytes, RowPatchBytes, ShiftPatchBytes uint64
	// PayloadBytes is the protobuf payload as received (frame headers
	// excluded). The Deflate fields estimate the same payloads under
	// permessage-deflate, also excluding frame headers; see handle.
	PayloadBytes uint64
	// DeflateNoCtx* is what gorilla/websocket v1.5.3 would send if
	// EnableCompression were turned on: it only implements
	// permessage-deflate without context takeover, at level 1 (BestSpeed),
	// so every message is compressed from an empty history.
	DeflateNoCtxBytes, DeflateNoCtxNanos uint64
	// Deflate* is a shared-history stream (context takeover) at the same
	// level: what a compressor that kept its window across messages would
	// send. gorilla cannot do this today; it is the bound on what
	// switching to one would buy.
	DeflateBytes, DeflateNanos uint64
	// DecodeApplyNanos covers UnmarshalServer plus applying the message.
	DecodeApplyNanos uint64
}

type countingWriter struct{ n uint64 }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += uint64(len(p))
	return len(p), nil
}

// sink is the testable core of wssink: it mirrors pane frames the way a
// real client does and asks for a resync when a patch does not apply.
type sink struct {
	// mu lets main snapshot the summary (SIGUSR1) while the reader runs.
	mu     sync.Mutex
	frames map[int]protocol.MsgPaneUpdate
	zw     *flate.Writer // one stream for the connection: context takeover
	zbuf   countingWriter
	nw     *flate.Writer // reset per message: no context takeover
	sum    Summary
	// onTraffic, if set, receives replies to MsgTrafficRequest and their
	// payload size. Called from handle with mu held.
	onTraffic func(protocol.MsgTrafficStats, int)
}

func newSink() *sink {
	s := &sink{frames: map[int]protocol.MsgPaneUpdate{}}
	// A valid level never returns an error.
	s.zw, _ = flate.NewWriter(&s.zbuf, flate.BestSpeed)
	s.nw, _ = flate.NewWriter(io.Discard, flate.BestSpeed)
	return s
}

// strippedTail is the 0x00 0x00 0xff 0xff that ends each sync flush. RFC
// 7692 (section 7.2.1) has the sender strip it, so it is never sent.
const strippedTail = 4

// handle applies one binary payload and returns any message to send back.
func (s *sink) handle(payload []byte) (reply any, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	start := time.Now()
	msg, err := protocol.UnmarshalServer(payload)
	decode := time.Since(start)
	if err != nil {
		return nil, err
	}
	if ts, ok := msg.(protocol.MsgTrafficStats); ok {
		// The answer to our own MsgTrafficRequest is measurement
		// plumbing, not session traffic: hand it aside uncounted.
		if s.onTraffic != nil {
			s.onTraffic(ts, len(payload))
		}
		return nil, nil
	}

	s.sum.Messages++
	s.sum.PayloadBytes += uint64(len(payload))

	// Without context takeover, as gorilla's compressNoContextTakeover
	// does: a pooled writer Reset onto the message, written, flushed, and
	// the tail truncated.
	start = time.Now()
	var one countingWriter
	s.nw.Reset(&one)
	if _, err := s.nw.Write(payload); err != nil {
		return nil, err
	}
	if err := s.nw.Flush(); err != nil {
		return nil, err
	}
	s.sum.DeflateNoCtxNanos += uint64(time.Since(start))
	if one.n > strippedTail {
		s.sum.DeflateNoCtxBytes += one.n - strippedTail
	}

	// With context takeover: one compressor for the whole connection,
	// flushed at each message boundary.
	start = time.Now()
	if _, err := s.zw.Write(payload); err != nil {
		return nil, err
	}
	if err := s.zw.Flush(); err != nil {
		return nil, err
	}
	s.sum.DeflateNanos += uint64(time.Since(start))
	if stripped := strippedTail * s.sum.Messages; s.zbuf.n > stripped {
		s.sum.DeflateBytes = s.zbuf.n - stripped
	} else {
		s.sum.DeflateBytes = 0
	}

	start = time.Now()
	defer func() { s.sum.DecodeApplyNanos += uint64(decode + time.Since(start)) }()
	switch m := msg.(type) {
	case protocol.MsgPaneClosed:
		delete(s.frames, m.PaneID)
	case protocol.MsgPaneUpdate:
		s.frames[m.PaneID] = m
		s.sum.PaneUpdates++
		s.sum.FullBytes += uint64(len(payload))
	case protocol.MsgPanePatch:
		if m.ShiftRows != 0 {
			s.sum.ShiftPatchBytes += uint64(len(payload))
		} else {
			s.sum.RowPatchBytes += uint64(len(payload))
		}
		next, ok := protocol.ApplyPanePatch(s.frames[m.PaneID], m)
		if !ok {
			// Hold nothing until the full snapshot arrives, as the
			// real clients do: a partly patched pane is never shown.
			delete(s.frames, m.PaneID)
			s.sum.Resyncs++
			return protocol.MsgPaneResync{PaneID: m.PaneID}, nil
		}
		s.frames[m.PaneID] = next
		if m.ShiftRows != 0 {
			s.sum.ShiftPatches++
		} else {
			s.sum.RowPatches++
		}
	}
	return nil, nil
}

func (s *sink) summary() Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sum
}
