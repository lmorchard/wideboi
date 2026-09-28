# Fix Concurrent PTY Master WriteBounded Race and Deadline Clobbering Implementation Plan

**Goal:** Eliminate concurrent PTY Master write races, write deadline clobbering, and upgrade synchronization gaps by serializing `ptyx.Pane.WriteBounded` and routing `RawBytes` (paste input) through `p.grid.SendText`.

**Approach:** Add `writeMu sync.Mutex` to `ptyx.Pane` to serialize deadline setting and writing in `WriteBounded`. Route `RawBytes` in `internal/server/pane.go` through `p.grid.SendText(string(ev))` so paste input flows through `vt`'s internal pipe and `pty-writer` in strict FIFO order, ensuring `p.ptyWritten` and `p.drainInput` cover paste writes.

**Tech stack:** Go, `github.com/charmbracelet/ultraviolet`, Unix ptys.

---

## Phase 1: `ptyx.Pane.WriteBounded` Serialization Mutex

Protect `ptyx.Pane.WriteBounded` with an internal mutex so concurrent callers cannot clobber deadlines or interleave writes to `p.Master`.

**Files:**
- Modify: `internal/server/ptyx/pane.go` — add `writeMu sync.Mutex` to `Pane` and lock in `WriteBounded`
- Test: `internal/server/ptyx/pane_test.go` — add `TestWriteBoundedConcurrent`

**Key changes:**
In `internal/server/ptyx/pane.go`:
```go
type Pane struct {
	Master  *os.File
	writeMu sync.Mutex
	...
}

func (p *Pane) WriteBounded(b []byte, timeout time.Duration) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if timeout > 0 {
		_ = p.Master.SetWriteDeadline(time.Now().Add(timeout))
		defer func() { _ = p.Master.SetWriteDeadline(time.Time{}) }()
	}
	return p.Master.Write(b)
}
```

In `internal/server/ptyx/pane_test.go`:
```go
func TestWriteBoundedConcurrent(t *testing.T) {
	p, err := Spawn([]string{"/bin/cat"}, 80, 24, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			data := []byte(fmt.Sprintf("line %d\n", idx))
			_, _ = p.WriteBounded(data, 100*time.Millisecond)
		}(i)
	}
	wg.Wait()
}
```

**Verification — automated:**
- [x] `go test -race -count=1 ./internal/server/ptyx` passes — **PASS (1.633s)**
- [x] `make quick` passes — **PASS (all unit tests + 151 web tests)**

**Verification — manual:**
- [x] Verify `ptyx.Pane` fields and comments are clean.

---

## Phase 2: Route `RawBytes` through `p.grid.SendText` in `key-writer` & Update Flush Hook

Route `RawBytes` (paste input) through `p.grid.SendText(string(ev))` in `key-writer` so it travels through `vt`'s pipe and `pty-writer`, ensuring single-writer PTY master access, correct `inputFlush` synchronization during upgrade, and `p.ptyWritten` accounting.

**Files:**
- Modify: `internal/server/pane.go` — in `key-writer`, change `case RawBytes:` to `p.grid.SendText(string(ev))`; in `Write`, call `p.ptyWritten(n)` if `p.ptyWritten != nil && n > 0`
- Test: `internal/server/upgrade_test.go` — add `TestDrainInputWaitsForRawBytesPtyWrite`

**Key changes:**
In `internal/server/pane.go`:
```go
				case RawBytes:
					if p.grid != nil {
						p.grid.SendText(string(ev))
					} else {
						_, _ = p.Write(ev)
					}
```
In `internal/server/pane.go`'s `Write`:
```go
func (p *Pane) Write(b []byte) (int, error) {
	if p.pty == nil {
		return len(b), nil
	}
	n, err := p.pty.WriteBounded(b, ptyWriteTimeout)
	if p.ptyWritten != nil && n > 0 {
		p.ptyWritten(n)
	}
	if errors.Is(err, os.ErrDeadlineExceeded) && n < len(b) {
		p.dropped.Add(uint64(len(b) - n))
	}
	return n, err
}
```

In `internal/server/upgrade_test.go`:
```go
func TestDrainInputWaitsForRawBytesPtyWrite(t *testing.T) {
	p, err := NewPane(1, []string{"/bin/cat"}, 40, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	var written atomic.Int64
	p.ptyWritten = func(n int) {
		time.Sleep(20 * time.Millisecond) // slow hop under test
		written.Add(int64(n))
	}
	p.Start(func() {})
	t.Cleanup(func() { _ = p.Close() })

	const pasteLen = 10
	p.SendBytes([]byte("0123456789"))
	if !p.drainInput(5 * time.Second) {
		t.Fatal("drainInput timed out")
	}
	if got := written.Load(); got != pasteLen {
		t.Fatalf("drainInput returned with %d of %d bytes written to the pty", got, pasteLen)
	}
}
```

**Verification — automated:**
- [x] `go test -race -count=1 ./internal/server/...` passes — **PASS (all tests green)**
- [x] `make quick` passes — **PASS (all unit tests + 151 web tests)**
- [x] `make check` passes — **PASS (41/41 smoke, 29/29 attachcheck, golden, exit verify, race, 50 browser specs)**

**Verification — manual:**
- [x] Inspect git diff to verify all aspects of issue #310 are addressed cleanly and idiomatically.
