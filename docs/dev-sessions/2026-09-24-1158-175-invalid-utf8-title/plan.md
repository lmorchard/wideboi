# Invalid UTF-8 pane title ends the session — Implementation Plan

**Goal:** A pane title (or any string) that isn't valid UTF-8 never tears down a client connection or ends a session, and Claude Code's `✳` title survives intact.

**Approach:** Three independent layers (spec "Design"). Sanitise outbound wire strings in the codec; make the server write pumps drop an unencodable message instead of dying; put a streaming OSC scanner in front of x/vt so a UTF-8 continuation `0x9C` inside an OSC is never seen by the parser as ST. Finally, an attach-level regression case that exercises the real socket.

**Tech stack:** Go, protobuf (`google.golang.org/protobuf`), `charmbracelet/x/vt` + `x/ansi` v0.11.8, Python pty harness (`scripts/attachcheck.py`).

---

## Phase 1: Sanitise wire strings in the codec

A snapshot, update or patch carrying invalid UTF-8 now marshals, with the bad bytes replaced by U+FFFD.

**Files:**
- Modify: `internal/protocol/codec.go` — add `validUTF8`. Apply it to `PaneTitles` values in `MarshalServer`, to `cell.Content` in `encodeLine`, and to `k.Text` in `encodeKey`.
- Test: `internal/protocol/codec_test.go` (or the existing codec test file; check its name first)

**Key changes:**

```go
// validUTF8 returns s unchanged when it is valid UTF-8, the common case,
// and otherwise replaces each invalid byte sequence with U+FFFD. Protobuf
// string fields refuse invalid UTF-8, and one bad pane title used to make
// a whole snapshot unencodable (#175).
func validUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}
```

Test (write it first, watch it fail with "invalid UTF-8"):

```go
func TestMarshalReplacesInvalidUTF8(t *testing.T) {
	snap := MsgLayoutSnapshot{PaneTitles: map[int]string{1: "\xe2", 2: "ok ✳"}}
	data, err := MarshalServer(snap)
	if err != nil { t.Fatalf("snapshot with invalid title: %v", err) }
	got, _ := UnmarshalServer(data)
	titles := got.(MsgLayoutSnapshot).PaneTitles
	if titles[1] != "�" || titles[2] != "ok ✳" { t.Errorf("titles = %q", titles) }

	upd := MsgPaneUpdate{PaneID: 1, Cols: 1, Rows: 1, Lines: []LineData{{{Content: "\xff", Width: 1}}}}
	data, err = MarshalServer(upd)
	if err != nil { t.Fatalf("update with invalid cell: %v", err) }
	got, _ = UnmarshalServer(data)
	if c := got.(MsgPaneUpdate).Lines[0][0].Content; c != "�" { t.Errorf("cell = %q", c) }

	if _, err := MarshalClient(MsgInput{PaneID: 1, Key: KeyData{Text: "\xc3"}}); err != nil {
		t.Fatalf("input with invalid key text: %v", err)
	}
}
```

Patches share `encodeLine` with updates, so the update case covers them.

**Verification — automated:**
- [x] New test fails before the change, with `string field contains invalid UTF-8` — **FAIL: snapshot with an invalid title: string field contains invalid UTF-8**
- [x] `go test ./internal/protocol -run TestMarshalReplacesInvalidUTF8 -v` passes after — **PASS**
- [x] `make quick` passes (includes `TestCodecRoundTripsEveryField`) — **all packages ok**

**Verification — manual:**
- [ ] none

---

## Phase 2: Server write pumps survive an unencodable message

A `MarshalServer` error is logged and that message dropped; the pump keeps serving. Write errors still end the pump.

**Files:**
- Modify: `internal/transport/socket.go` — `ServerSocketConn.writeLoop`
- Modify: `internal/transport/websocket.go` — the write pump around line 78
- Test: `internal/transport/socket_test.go`, `internal/transport/websocket_test.go`

**Key changes** (socket; the WebSocket change has the same shape):

```go
payload, err := protocol.MarshalServer(msg)
if err != nil {
	// A message we cannot encode is a server bug, not a dead peer:
	// dropping it costs one frame, dropping the connection cost the
	// session when the peer was its owner (#175).
	slog.Error("dropping unencodable message", "type", fmt.Sprintf("%T", msg), "err", err)
	continue
}
if err := writeFrame(sc.conn, payload); err != nil {
	sc.set(fmt.Sprintf("writing %T to client", msg), err)
	return
}
```

Tests. `struct{}{}` is a message `MarshalServer` rejects ("unsupported server message"), so it stands in for any encode failure:

```go
func TestServerWritePumpSkipsAnUnencodableMessage(t *testing.T) {
	ours, theirs := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sc := transport.NewServerSocketConn(ours, 4)
	sc.RunPumps(ctx)
	cc := transport.NewClientSocketConn(theirs, 4)
	cc.RunPumps(ctx)
	defer sc.Close(); defer cc.Close()

	sc.SendServer(ctx, struct{}{})
	sc.SendServer(ctx, protocol.MsgPaneClosed{PaneID: 9})
	select {
	case msg := <-cc.ServerSendChan():
		if msg != (protocol.MsgPaneClosed{PaneID: 9}) { t.Fatalf("got %#v", msg) }
	case <-time.After(2 * time.Second):
		t.Fatal("the message after an unencodable one never arrived; the pump died")
	}
}
```

The WebSocket twin follows `TestWebSocketRoundTrip`'s setup: `SendServer(ctx, struct{}{})`, then a `MsgPaneClosed{PaneID: 9}`, and `readServer` must return the `MsgPaneClosed`.

**Verification — automated:**
- [x] Both new tests fail before the change (timeout / read error) and pass after — **socket: channel closed; ws: close 1006 unexpected EOF → both PASS**
- [x] `make quick` passes — **exit 0**
- [x] `go test -race -count=4 ./internal/transport` passes — **ok 1.5s** (pump change is concurrency-adjacent)

**Verification — manual:**
- [ ] none

---

## Phase 3: Streaming OSC scanner in front of x/vt

`ESC ] 0 ; ✳ Claude Code BEL` sets the title to `✳ Claude Code`, and no stray text reaches the screen.

**Files:**
- Create: `internal/server/term/oscfix.go` — `oscScanner`
- Modify: `internal/server/term/grid.go` — `vtGrid` gains `osc oscScanner`. `Write` routes through it and still returns `len(p)` on success.
- Test: `internal/server/term/oscfix_test.go`

**Design:** the scanner forwards bytes as it goes, so it never buffers whole payloads. The only things it holds between writes are an incomplete UTF-8 character inside an OSC (at most 3 bytes) and a lone trailing ESC state.

State:

```go
type oscScanner struct {
	state   oscState   // ground, escape, osc
	char    [4]byte    // UTF-8 character in progress inside an OSC
	charLen int        // bytes of it seen
	charWant int       // bytes it needs in total
	raw     []byte     // original OSC payload, for re-deriving a title; capped
	rawOver bool       // payload exceeded oscRawCap; give up on correcting it
	subst   bool       // this OSC had a character replaced
}

const oscRawCap = 4096
```

`write(p []byte, emit func([]byte), setTitle func(string))`, per state:

- **ground:** `i := bytes.IndexByte(p, 0x1B)`. If there's none, the rest of `p` passes through. Otherwise go to escape after the ESC. Pass-through spans are emitted as subslices of `p`, so nothing is copied.
- **escape:** `]` enters osc (reset `raw`, `rawOver`, `subst`, `charLen`). Another ESC stays in escape. Any other byte returns to ground. This mirrors the parser, where ESC in escape stays in escape.
- **osc:**
  - With a character in progress (`charLen > 0`):
    - A continuation byte (`0x80–0xBF`) is appended. Once complete: if it contains `0x9C`, emit `"�"` (EF BF BD, which has no 0x9C) and set `subst`; otherwise emit the original bytes.
    - Any other byte means the character was malformed. Emit it raw, clear it, and reprocess the byte.
  - A lead byte `0xC2–0xF4` starts a character. `charWant` is 2, 3 or 4 by lead range (`C2–DF`, `E0–EF`, `F0–F4`). The span before it is emitted first.
  - `0x07` or `0x9C` (not in a character): finalize, go to ground.
  - `0x1B`: finalize, go to escape. The parser dispatches the OSC on ESC; `ESC \` is its ST.
  - `0x18` / `0x1A`: abort without finalizing, go to ground.
  - Every other byte passes through.
  - Every original payload byte is appended to `raw` until it reaches `oscRawCap`, then `rawOver` is set.
- **finalize:** only if `subst && !rawOver`. Split `raw` at the first `;`. If the prefix is `0` or `2`, emit everything pending **through the terminator byte**, then `setTitle(rest)`. The parser dispatches the OSC on the terminator, so its `�` version has to be applied first for ours to override it. Anything after this point in the same write is processed normally, so a later title in the same write still wins.

In `grid.go`:

```go
func (g *vtGrid) Write(p []byte) (int, error) {
	... // existing lock / lastWriteTime / status
	var err error
	g.osc.write(p, func(b []byte) {
		if err == nil {
			_, err = g.em.Write(b)
		}
	}, func(title string) { g.title.Store(&title) })
	g.generation.Add(1)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}
```

One consequence to call out in the PR: for a corrected title, `Title()` may briefly read `� Claude Code` inside a single `Write` before the override lands. It's valid UTF-8 and superseded right away.

Tests (write first; table-driven through `NewVT`, plus scanner-level cases). The integration cases feed `NewVT(80, 24).Write(...)` and check `Title()` plus the screen text:
- `ESC]0;✳ Claude Code BEL` → title `✳ Claude Code`. The screen contains no `Claude`. This is the bug.
- `ESC]2;✳ x ESC\` → title `✳ x` (ESC-backslash terminator)
- The same bytes split at every offset over two `Write` calls → the same title. A loop over all split points, per LESSONS "enumerate finite input spaces".
- `ESC]0;héllo BEL`, `ESC]0;⠂ working BEL` → unchanged, valid (no-0x9C path)
- A later title in the same write wins: `ESC]0;✳ a BEL ESC]0;b BEL` → `b`
- `ESC]1;✳ icon BEL` (icon name, not title) → the title is unchanged and nothing leaks to the screen
- A real 8-bit ST after ASCII (`ESC]0;abc` `0x9C`) → title `abc`
- CAN abort mid-OSC → no title change
- `✳` in an OSC 8 hyperlink payload → no screen garbage (the character becomes FFFD in the forwarded payload)
- Plain SGR-coloured text with no OSC → the screen matches an unfiltered emulator (a direct `vt` emulator fed the same bytes)
- Scanner-level: after `oscRawCap+1` payload bytes with a `✳` early, finalize does not call `setTitle`, and every byte was still emitted (no loss)

**Verification — automated:**
- [x] Title and screen cases fail before `grid.go` routes through the scanner (title `"\xe2"`, stray `Claude Code` on screen) — **exactly that: Title()="\xe2", screen "Claude Code"**
- [x] `go test ./internal/server/term -run 'OSC|Title' -v` passes after — **15 PASS**
- [x] Break check: make the scanner treat continuation 0x9C as ST, watch the `✳` cases go red, restore — **5 tests red (hyperlink test only after moving ✳ mid-URL; it originally could not fail), green on restore**
- [x] `make quick` passes — **exit 0**

**Verification — manual:**
- [ ] none (covered in phase 4)

---

## Phase 4: Attach-level regression case

This proves the whole chain over a real socket: a `✳` title neither kills the connection nor leaves stray text.

**Files:**
- Modify: `scripts/attachcheck.py` — new case, registered however the existing cases are (check how `case_*` functions are collected)

**Key changes:**

```python
def case_multibyte_title_does_not_kill_the_connection(fail):
    """#175. U+2733 is E2 9C B3, and 0x9C is also C1 String Terminator:
    x/ansi cut the OSC after E2, the title became invalid UTF-8,
    protobuf refused the snapshot, the pump closed the connection and
    the owner's departure ended the session. The title text is split
    in the typed command so the command's echo cannot satisfy the
    'no stray text' check."""
    srv = Server()
    try:
        c = Client()
        c.type(b"printf '\\033]0;\\342\\234\\263 ti''tle-mk\\007'\r")
        c.type(b"echo title-survived\r")
        if not c.wait_for(lambda out: b"title-survived" in out):
            fail("the connection stopped carrying updates after a multibyte title")
        if b"title-mk" in c.output():
            fail("the tail of the title leaked onto the screen as text")
        if wait_for_exit(c.pid, 0.5) is not None:
            fail("the attached client exited after a multibyte title")
        c.kill()
    finally:
        srv.stop()
```

`wait_for` is in the helper set (it's used in `case_plain_wideboi_detaches_and_the_session_survives`). If `c.output()` already covers the wait, match the neighbouring case's style instead.

**Verification — automated:**
- [x] Case fails against a build with phases 1–3 stashed out (a temporary WIP commit, not a bare stash; see memory). At minimum confirm it fails with only the grid routing reverted. — **Code files checked out from origin/main: fails (connection stops + leak). Only grid.go reverted: fails (title not intact + leak). Full fix: passes.** Plan adaptation: the pane header legitimately draws the title, so 'title-mk not on screen' was wrong. The case now asserts `✳ title-mk` is shown whole and every `title-mk` is that whole title.
- [!] `make check` passes, run 4 times (pump/timing change; project rule) — **5 of 6 green, not 4 of 4.** Run 2 failed smoke `focus switch moves the cursor` ("cursor column did not move across panes"). That case passed 8/8 alone on this branch and again in runs 3–6. It doesn't touch titles or OSC, and the scanner holds no bytes outside an unfinished character inside an OSC. This matches the known parallel-load flake (memory: parallel-check-load-flakes). No origin/main baseline comparison was run for it.

**Verification — manual:**
- [ ] Les: `[[startup]] command = "claude"` in `.wideboi.toml`, `./bin/wideboi -L proj`. The session stays up and the sliver/card title shows `✳ …`

---

## Follow-ups (not in this PR)

- Report the OSC/0x9C ambiguity upstream to `charmbracelet/x` (`ansi/parser/transition_table.go:269`).
- x/vt's `handleTitle` ignores titles containing `;` (`len(parts) != 2`). The scanner's corrected path uses a split at the first `;`, so it's more lenient there. Note the difference in the PR.
- 8-bit `0x9D` OSC introducer and DCS/APC/SOS/PM strings are not covered by the scanner.
