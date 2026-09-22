# Issue #52 Implementation Plan: Skip Render/Flush When Idle

**Goal:** Eliminate wideboi's ~1.1 KB/s idle emission by skipping `Render` and `Flush` when the composed frame is unchanged.

**Approach:** Double-buffered offscreen frame comparison in `Client.Draw` returning `bool` (`dirty`), gating `scr.Render()` and `scr.Flush()` in `cmd/wideboi/main.go`. Remove `_FRAME_NOISE` from `scripts/ptylib.py` and assert 0 B/s idle emission in `scripts/smoke.py`.

**Tech stack:** Go, Ultraviolet, Python (PTY test harness).

---

## Phase 1: Double-buffered frame staging and change detection in `internal/client`

Implement offscreen frame staging in `Client.Draw`. `Draw` composes into an internal offscreen buffer, compares against the previously rendered frame (cells + cursor visibility + cursor position) and returns `true` only if the frame changed or the target `HostScreen` instance is new.

**Files:**
- Modify: `internal/client/client.go` — double-buffered staging and `Draw(...) bool`
- Test: `internal/client/screen_test.go` — unit tests for dirty detection

**Key changes:**
In `internal/client/client.go`:
```go
type offscreenHostScreen struct {
	compose.Surface
	cursorShown bool
	cursorX     int
	cursorY     int
}

func newOffscreenHostScreen(cols, rows int) *offscreenHostScreen {
	return &offscreenHostScreen{
		Surface: compose.NewSurface(cols, rows),
	}
}

func (s *offscreenHostScreen) HideCursor()                { s.cursorShown = false }
func (s *offscreenHostScreen) ShowCursor()                { s.cursorShown = true }
func (s *offscreenHostScreen) SetCursorPosition(x, y int) { s.cursorX, s.cursorY = x, y }

func (s *offscreenHostScreen) clear() {
	b := s.Bounds()
	w, h := b.Dx(), b.Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s.SetCell(x, y, &uv.EmptyCell)
		}
	}
	s.cursorShown = false
	s.cursorX = 0
	s.cursorY = 0
}

func (s *offscreenHostScreen) equal(other *offscreenHostScreen) bool {
	if s == nil || other == nil {
		return false
	}
	if s.cursorShown != other.cursorShown {
		return false
	}
	if s.cursorShown && (s.cursorX != other.cursorX || s.cursorY != other.cursorY) {
		return false
	}
	b1 := s.Bounds()
	b2 := other.Bounds()
	if b1 != b2 {
		return false
	}
	w, h := b1.Dx(), b1.Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c1 := s.CellAt(x, y)
			c2 := other.CellAt(x, y)
			if c1 == c2 {
				continue
			}
			if c1 == nil || c2 == nil {
				return false
			}
			if !c1.Equal(c2) {
				return false
			}
		}
	}
	return true
}

func copyToHostScreen(src *offscreenHostScreen, dst HostScreen) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; {
			c := src.CellAt(x, y)
			if c == nil {
				x++
				continue
			}
			dst.SetCell(x, y, c)
			width := c.Width
			if width <= 0 {
				width = 1
			}
			x += width
		}
	}
	if src.cursorShown {
		dst.SetCursorPosition(src.cursorX, src.cursorY)
		dst.ShowCursor()
	} else {
		dst.HideCursor()
	}
}
```

In `Client` struct:
```go
	stagingScreen      *offscreenHostScreen
	lastRenderedScreen *offscreenHostScreen
	lastHostScreen     HostScreen
```

In `Draw`:
```go
func (c *Client) Draw(scr HostScreen, drawPane func(id int, dst uv.Screen, area image.Rectangle), cursorInfo func(id int) (image.Point, bool)) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stagingScreen == nil || c.stagingScreen.Bounds().Dx() != c.cols || c.stagingScreen.Bounds().Dy() != c.rows {
		c.stagingScreen = newOffscreenHostScreen(c.cols, c.rows)
	}
	c.stagingScreen.clear()

	c.drawToScreenLocked(c.stagingScreen, drawPane, cursorInfo)

	targetChanged := scr != c.lastHostScreen
	if !targetChanged && c.stagingScreen.equal(c.lastRenderedScreen) {
		return false
	}

	copyToHostScreen(c.stagingScreen, scr)
	c.lastHostScreen = scr

	// Swap or copy staging into lastRenderedScreen
	if c.lastRenderedScreen == nil || c.lastRenderedScreen.Bounds().Dx() != c.cols || c.lastRenderedScreen.Bounds().Dy() != c.rows {
		c.lastRenderedScreen = newOffscreenHostScreen(c.cols, c.rows)
	}
	copyOffscreen(c.stagingScreen, c.lastRenderedScreen)

	return true
}
```

**Verification — automated:**
- [x] `go test ./internal/client -v -run TestClientDrawDirtyDetection` passes — **PASS (0.00s)**
- [x] `make quick` passes — **OK: all vet, seam, and unit tests pass**

**Verification — manual:**
- [x] Confirm in test that `Draw` returning `false` leaves `scr` untouched — **verified by TestClientDrawDirtyDetection asserting marker cell remains**

---

## Phase 2: Render & Flush gating in `cmd/wideboi/main.go`

Gate `scr.Render()` and `scr.Flush()` on `cli.Draw` returning `true` in both `run` and `runAttach`.

**Files:**
- Modify: `cmd/wideboi/main.go` — lines 260-266 and lines 405-413

**Key changes:**
In `runAttach`:
```go
		case <-frame.C:
			screenLock.Lock()
			if cli.Draw(scr, nil, nil) {
				dirtyFrames = 2
			}
			if dirtyFrames > 0 {
				dirtyFrames--
				scr.Render()
				_ = scr.Flush()
			}
			screenLock.Unlock()
```

In `run`:
```go
		case <-frame.C:
			screenLock.Lock()
			if !stopped.Load() {
				if cli.Draw(scr, srv.DrawPane, srv.CursorInfo) {
					dirtyFrames = 2
				}
				if dirtyFrames > 0 {
					dirtyFrames--
					scr.Render()
					_ = scr.Flush()
				}
			}
			screenLock.Unlock()
```

**Verification — automated:**
- [x] `make quick` passes — **OK**
- [x] `make check` passes — **OK: all unit, race, exit, smoke, and attach tests pass**

**Verification — manual:**
- [x] Verify that existing smoke and attach tests pass — **verified 26/26 smoke and 7/7 attach pass**

---

## Phase 3: Delete `_FRAME_NOISE` and add zero-emission test in `scripts/smoke.py`

Remove the cursor hide/show filter from `scripts/ptylib.py` and assert zero bytes emitted during idle in `scripts/smoke.py`.

**Files:**
- Modify: `scripts/ptylib.py` — remove `_FRAME_NOISE` and update `settle_output`
- Modify: `scripts/smoke.py` — add `case_idle_emits_no_bytes`

**Key changes:**
In `scripts/ptylib.py`:
Delete `_FRAME_NOISE = re.compile(...)`.
Update `settle_output`:
```python
def settle_output(drainer: "Drainer", timeout: float, quiet: float = 0.25,
                  poll: float = 0.01) -> bool:
    def size() -> int:
        return len(drainer.output())
    ...
```

In `scripts/smoke.py`:
```python
def case_idle_emits_no_bytes(fail):
    s = Session()
    time.sleep(0.1)
    initial_len = len(s.output())
    time.sleep(0.5)
    idle_bytes = len(s.output()) - initial_len
    s.close()
    if idle_bytes > 0:
        fail(f"wideboi emitted {idle_bytes} bytes over 0.5s while completely idle")
```

**Verification — automated:**
- [x] `python3 scripts/smoke.py` passes — **26 passed, 0 failed**
- [x] `make check` passes 4 times consecutively (per LESSONS.md) — **4/4 consecutive runs clean**

**Verification — manual:**
- [x] Confirm idle emission is exactly 0 bytes over 1.0s — **verified: 0 bytes over idle window, and intentionally broken gating reproduces 558 bytes failure**
