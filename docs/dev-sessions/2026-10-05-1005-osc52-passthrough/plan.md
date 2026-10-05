# Pane clipboard passthrough (OSC 52) Implementation Plan

**Goal:** Pane OSC 52 writes reach the user's clipboard (TUI over ssh, web via an approval toast), and the latest one is fetchable with `wideboi show-clipboard`.

**Approach:**
- A new OSC 52 handler in the grid decodes and validates writes, and fires a callback in the same shape as the bell.
- The server stores the latest write and sends `MsgPaneClipboard` to the client that last sent input to that pane, falling back to every attached client.
- The TUI writes the text through the existing `writeClipboard`. The web client shows a toast whose Copy click does the write. All protocol changes go in one v30 bump.

**Tech stack:** Go (x/vt, ultraviolet, protobuf via buf), TypeScript/Lit web client (vitest, Playwright), Python pty smoke harness.

**Conventions for every phase:**
- Work in `.worktrees/osc52-passthrough`. Commit message: `Phase N: <name>`. Commits are signed with `SSH_AUTH_SOCK=/Users/lmorchard/.bitwarden-ssh-agent.sock`.
- **Known red on macOS:** three `internal/config` tests (TestLoadPrecedence, TestSessionAndSocketLayering env_path/flag_path, TestUntrustedProjectConfigIgnoresSensitiveSettings) fail on main with "/tmp is a symlink". "`make quick` passes" below means no failures *other than those three*. Record which failures appear; don't assume.
- Timing-sensitive tests (server delivery, smoke) run 4× before they count as green (CLAUDE.md).

---

## Phase 1: Capture OSC 52 in the server + `show-clipboard`

A pane's OSC 52 write is decoded, validated and stored by the server, and `wideboi show-clipboard` prints it. This is the end-to-end slice with no client display yet. The whole v30 protocol change lands here, so the version bumps once.

**Files:**
- Modify: `internal/server/term/grid.go`
  - add `OnClipboard(fn func(text string))` to the `Grid` interface, next to `OnBell` (~line 88);
  - add an `onClipboard` field next to `onBell` (~251);
  - register an OSC 52 handler after the 1337 one (~549);
  - add `parseOSC52`, `maxClipboardBytes` and the `OnClipboard` method next to `OnBell` (~775).
- Modify: `internal/server/pane_wedge_test.go:65`, `internal/server/status_test.go:100` — the fake grids gain a no-op `OnClipboard(func(string)) {}`.
- Modify: `internal/server/pane.go` — `SetOnClipboard`, next to `SetOnBell` (539).
- Modify: `internal/server/server.go`
  - Fields: `clipboardMu sync.Mutex`, `clipboardSeq atomic.Uint64`, `clipboard *clipboardEntry` (guarded by `s.mu`). *(Revised after review: replaced by `clipboardPendingMu`/`clipboardPending`/`clipboardDelivering`, a single coalescing worker. See spec "Delivery" and notes.md. The snippet below is the original.)*
  - Wire `p.SetOnClipboard` beside `SetOnBell` at spawn (~588).
  - Add `onPaneClipboard` and `deliverPaneClipboard` next to `onPaneBell`/`broadcastPaneBell` (~615-650).
- Modify: `internal/server/upgrade.go:358` — the same `SetOnClipboard` wiring for adopted panes.
- Modify: `internal/server/handlers.go`
  - `msgEffects.showClipboardResp`;
  - a dispatch case in `handleClientMsg` (~191-210);
  - `handleShowClipboardRequestLocked`;
  - response send beside `dumpResp` (~1120).
- Modify: `internal/protocol/wirepb/wideboi.proto` — three messages plus oneof fields. Then run `make proto`, which regenerates `wideboi.pb.go` and `web/src/gen/.../wideboi_pb.ts`.
- Modify: `internal/protocol/messages.go` and `internal/protocol/codec.go` (both directions, all three messages).
- Modify: `internal/protocol/version.go` (29→30 plus changelog line), `web/src/version.ts` (30), `internal/protocol/version_guard_test.go` (hash for 30).
- Modify: `internal/protocol/wire_test.go:51-54` (list the three zero values), `internal/transport/wire_test.go:193` (round-trip values).
- Modify: `internal/commands/registry.go` — register `show-clipboard` after `dump-pane` (~530).
- Modify: `internal/commands/commands_test.go:136` — add `"show-clipboard"` to `TestDefaultRegistryBuiltins`.
- Test: `internal/server/term/osc_clipboard_test.go` (new), `internal/server/server_test.go`.

**Key changes:**

```go
// grid.go

// maxClipboardBytes caps one OSC 52 write from a pane. It sits well
// under x/vt's 4 MB OSC buffer (vt/emulator.go SetDataSize), past which
// the parser drops bytes silently and still calls the handler -- a
// truncated payload would otherwise decode as if it were whole.
const maxClipboardBytes = 1 << 20

// parseOSC52 extracts the text of an OSC 52 clipboard write. data is
// the whole payload, "52;<selection>;<base64>", as x/vt hands every
// handler. Reads ("?") and clears (empty) are refused: answering a read
// would hand the host clipboard to whatever runs in the pane. The
// selection is ignored; everything goes to the system clipboard.
func parseOSC52(data []byte) (string, bool) {
	parts := strings.SplitN(string(data), ";", 3)
	if len(parts) != 3 {
		return "", false
	}
	payload := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1 // base64(1) wraps at 76 columns on Linux
		}
		return r
	}, parts[2])
	if payload == "" || payload == "?" {
		return "", false
	}
	if len(payload) > base64.StdEncoding.EncodedLen(maxClipboardBytes) {
		return "", false
	}
	enc := base64.StdEncoding
	if len(payload)%4 != 0 {
		enc = base64.RawStdEncoding
	}
	decoded, err := enc.DecodeString(payload)
	if err != nil || len(decoded) == 0 || len(decoded) > maxClipboardBytes || !utf8.Valid(decoded) {
		return "", false
	}
	return string(decoded), true
}

	// in NewVTWithTimeouts, after the 1337 handler:
	g.em.RegisterOscHandler(52, func(data []byte) bool {
		text, ok := parseOSC52(data)
		if !ok {
			return false
		}
		if g.onClipboard != nil {
			g.onClipboard(text)
		}
		return true
	})

func (g *vtGrid) OnClipboard(fn func(text string)) { g.onClipboard = fn }
```

```go
// pane.go
// SetOnClipboard configures a callback for OSC 52 clipboard writes.
func (p *Pane) SetOnClipboard(fn func(text string)) {
	if p.grid != nil {
		p.grid.OnClipboard(fn)
	}
}
```

```go
// server.go
type clipboardEntry struct {
	seq    uint64
	paneID int
	title  string
	text   string
	at     time.Time
}

// onPaneClipboard runs on the pane's PTY reader inside the grid's
// Write, which can hold writeResizeMu while s.mu holders wait on Resize
// -- so it must not take s.mu itself. The sequence number, taken here
// in emission order, lets the async delivery drop a write that a newer
// one overtook.
func (s *Server) onPaneClipboard(paneID int, text string) {
	seq := s.clipboardSeq.Add(1)
	go s.deliverPaneClipboard(paneID, text, seq)
}

// deliverPaneClipboard stores the write for show-clipboard and sends it
// to its recipients. clipboardMu spans store and send so two writes
// cannot reach a client in the opposite order to the one they were
// stored in.
func (s *Server) deliverPaneClipboard(paneID int, text string, seq uint64) {
	s.clipboardMu.Lock()
	defer s.clipboardMu.Unlock()

	s.mu.Lock()
	if s.clipboard != nil && s.clipboard.seq > seq {
		s.mu.Unlock()
		return
	}
	var title string
	if p := s.panes[paneID]; p != nil {
		title = p.Title()
	}
	if title == "" {
		title = fmt.Sprintf("Pane %d", paneID)
	}
	s.clipboard = &clipboardEntry{seq: seq, paneID: paneID, title: title, text: text, at: time.Now()}
	tps := s.clipboardRecipientsLocked(paneID) // Phase 1: returns nil; see Phase 2
	s.mu.Unlock()

	msg := protocol.MsgPaneClipboard{PaneID: paneID, Title: title, Text: text}
	for _, tp := range tps {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_ = tp.SendServer(ctx, msg)
		cancel()
	}
}

// Phase 1 stub, replaced in Phase 2.
func (s *Server) clipboardRecipientsLocked(paneID int) []transport.Transport { return nil }
```

```go
// handlers.go
func (s *Server) handleShowClipboardRequestLocked(tp transport.Transport, m protocol.MsgShowClipboardRequest) msgEffects {
	var eff msgEffects
	c := s.clipboard
	if c == nil {
		eff.showClipboardResp = &protocol.MsgShowClipboardResponse{Error: "no pane has copied anything yet"}
		return eff
	}
	eff.showClipboardResp = &protocol.MsgShowClipboardResponse{
		PaneID: c.paneID, Title: c.title, Text: c.text, UnixMilli: c.at.UnixMilli(),
	}
	return eff
}
```

```proto
message MsgPaneClipboard {
  int32 pane_id = 1;
  string title = 2;
  string text = 3;
}
message MsgShowClipboardRequest {}
message MsgShowClipboardResponse {
  int32 pane_id = 1;
  string title = 2;
  string text = 3;
  int64 unix_milli = 4;
  string error = 5;
}
// ServerMessage oneof: MsgPaneClipboard pane_clipboard = 26; MsgShowClipboardResponse show_clipboard_response = 27;
// ClientMessage oneof: MsgShowClipboardRequest show_clipboard_request = 28;
```

```go
// messages.go
// MsgPaneClipboard carries text a pane wrote to the clipboard with OSC 52.
type MsgPaneClipboard struct {
	PaneID int
	Title  string
	Text   string
}

// MsgShowClipboardRequest asks for the most recent pane clipboard write.
type MsgShowClipboardRequest struct{}

// MsgShowClipboardResponse returns the most recent pane clipboard write, or an error if there is none.
type MsgShowClipboardResponse struct {
	PaneID    int    `json:"pane_id"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	UnixMilli int64  `json:"unix_milli"`
	Error     string `json:"error,omitempty"`
}
```
Codec strings go through `validUTF8`, matching `MsgPaneNotification` (`codec.go:439-446`, `597-601`).

```go
// registry.go, after dump-pane
r.Register(Command{
	Name:        "show-clipboard",
	Description: "Print the most recent text a pane copied (OSC 52)",
	Category:    "Panes",
	ArgsUsage:   "[--json]",
	Run: func(ctx context.Context, inv Invocation) error {
		fs := flag.NewFlagSet("show-clipboard", flag.ContinueOnError)
		if inv.Stderr != nil {
			fs.SetOutput(inv.Stderr)
		}
		var asJSON bool
		fs.BoolVar(&asJSON, "json", false, "print pane id, title, time and text as JSON")
		if err := fs.Parse(inv.Args); err != nil {
			return err
		}
		if inv.Stdout == nil {
			return fmt.Errorf("show-clipboard prints to stdout; run it from a shell")
		}
		resp, err := RPCQuery[protocol.MsgShowClipboardResponse](ctx, inv, protocol.MsgShowClipboardRequest{}, 5*time.Second)
		if err != nil {
			return err
		}
		if resp.Error != "" {
			return fmt.Errorf("%s", resp.Error)
		}
		if !asJSON {
			fmt.Fprint(inv.Stdout, resp.Text)
			return nil
		}
		return json.NewEncoder(inv.Stdout).Encode(struct {
			PaneID int    `json:"pane_id"`
			Title  string `json:"title"`
			Time   string `json:"time"`
			Text   string `json:"text"`
		}{resp.PaneID, resp.Title, time.UnixMilli(resp.UnixMilli).UTC().Format(time.RFC3339), resp.Text})
	},
})
```

**Tests first:**
- `TestParseOSC52` (table, in `term`, internal package if `parseOSC52` is unexported). Cases:
  - `52;c;aGVsbG8=` → "hello"
  - `52;;aGVsbG8=` → "hello" (empty selection)
  - `52;p;aGVsbG8` → "hello" (unpadded)
  - base64 split by `\n` → decodes
  - `52;c;?` → refused
  - `52;c;` → refused
  - `52;c;!!!` → refused
  - invalid UTF-8 (`/w==` = 0xFF) → refused
  - payload of `EncodedLen(maxClipboardBytes)+4` chars → refused
  - `52;c` (no payload field) → refused
- `TestOSC52CallsOnClipboard` (`osc_clipboard_test.go`, external `term_test`). Write `\x1b]52;c;aGVsbG8=\x07` to `term.NewVT` with `OnClipboard` set, and assert the callback got "hello". Also write the ST-terminated form `\x1b]52;c;aGVsbG8=\x1b\\`. A read query must not call it.
- `TestServerStoresPaneClipboard` (`server_test.go`, modeled on `TestServerBellNotification` at 1000):
  1. Attach; send `MsgShowClipboardRequest` and expect an `Error` response.
  2. `srv.PaneGrid(1).Write` an OSC 52 for "first", then "second".
  3. Poll `MsgShowClipboardRequest` until `Text == "second"`, with a 2 s ceiling. `PaneID == 1` and `Title` is not empty.
- `TestDefaultRegistryBuiltins` gains `"show-clipboard"`.
- Watch each fail before implementing:
  - parse/callback: undefined symbol, then wrong result;
  - server: no response case.

**Verification — automated:**
- [x] `go test ./internal/server/term -run 'OSC52|ParseOSC52' -v` fails first, then passes — **failed: undefined parseOSC52/maxClipboardBytes; then 10/10 subtests + callback test pass**
- [x] `go test ./internal/server -run TestServerStoresPaneClipboard -count=4 -v` passes 4× — **failed first (timeout: no response); then ok ×4, twice**
- [x] `make proto-check` clean after `make proto` — **clean once generated files are staged. It diffs against the index, so it always fails on unstaged regenerated bindings.**
- [x] `go test ./internal/protocol ./internal/transport ./internal/commands` passes (version guard hash recorded for 30) — **ok; guard demanded hash 584f6600…, recorded. TestShowClipboardCommand (real socket + real shell emitting OSC 52) ok ×4**
- [x] `make quick` — record failures; only the three known config ones are allowed — **only TestLoadPrecedence, TestSessionAndSocketLayering, TestUntrustedProjectConfigIgnoresSensitiveSettings; `make web-test` run separately (quick stops at `test`): 184/184**
- [x] *(added)* CLI routing: `parseCLI` test for `show-clipboard`; the binary against a missing session prints "no wideboi server running" — **ok**

**Verification — manual:**
- [ ] In a dev build session: `printf '\e]52;c;%s\a' "$(printf hello | base64)"` in a pane, then `./wideboi show-clipboard` prints `hello`, and `--json` shows pane id, title, RFC3339 time
- [ ] `show-clipboard` on a fresh session exits non-zero with "no pane has copied anything yet"

---

## Phase 2: Deliver to the right TUI client + `clipboard` setting

`MsgPaneClipboard` goes to the last client that sent input to the pane, falling back to every attached client. The TUI writes it to the host terminal through `writeClipboard`, unless `clipboard = false`.

**Files:**
- Modify: `internal/server/server.go`
  - Field `lastInput map[int]transport.Transport` (paneID → transport), initialised in `NewServer` (~403).
  - Replace the Phase 1 `clipboardRecipientsLocked` stub with the real one.
- Modify: `internal/server/handlers.go` — record `s.lastInput[m.PaneID] = tp` in `handleInputLocked` (~957, inside `if p, ok := s.panes[...]`, when `tp != nil`) and in `handleMouseLocked` (~994, same guard). Not in `handleSendInputRequestLocked`, and not in `handleScrollLocked`.
- Modify: `internal/server/clients.go` — `removeTransportLocked` (~119) deletes every `lastInput` entry whose value is `tp`.
- Modify: `internal/config/config.go`
  - Fields: `Clipboard *bool \`toml:"clipboard"\``, `ClipboardEnabled bool \`toml:"-"\``, `ConfigFlags.DisableClipboard bool`.
  - `applyFile`: beside `Mouse` (~381), before the trust gate. *(Revised after review: untrusted files may only set it to `false`; see spec "Open questions".)*
  - Env: `WIDEBOI_CLIPBOARD` via `ParseBoolEnv`, beside `WIDEBOI_AUTO_CLEANUP` (~540).
  - Flag: `flags.DisableClipboard` beside `DisableAutoCleanup` (~607).
  - Resolve: `cfg.ClipboardEnabled = cfg.Clipboard == nil || *cfg.Clipboard` beside `MouseEnabled` (~692).
- Modify: `cmd/wideboi/main.go`
  - `--disable-clipboard` flag (~92) plus help text (~232) and an env line in the help block;
  - intercept `protocol.MsgPaneClipboard` in the server-message case, right before `cli.HandleServerMsg(msg)` (~1171).
- Modify: `scripts/smoke.py` — new case `case_pane_osc52_passes_through`, registered in `CASES` (~1192).
- Test: `internal/server/server_test.go`, `internal/config/config_test.go`.

**Key changes:**

```go
// server.go
// clipboardRecipientsLocked picks who gets a pane's clipboard write:
// the client that last typed or clicked in that pane -- the one whose
// user just copied -- or, if it is gone or nobody has, every attached
// client. Attached only: CLI command connections share s.transports and
// must not receive clipboard text. s.mu must be held.
func (s *Server) clipboardRecipientsLocked(paneID int) []transport.Transport {
	if tp, ok := s.lastInput[paneID]; ok && s.isAttachedLocked(tp) {
		return []transport.Transport{tp}
	}
	var out []transport.Transport
	for _, tp := range s.transports {
		if s.isAttachedLocked(tp) {
			out = append(out, tp)
		}
	}
	return out
}
```

```go
// main.go, server-message case, before cli.HandleServerMsg(msg)
if m, ok := msg.(protocol.MsgPaneClipboard); ok && cfg.ClipboardEnabled {
	screenLock.Lock()
	if !stopped.Load() {
		writeClipboard(scr, m.Text)
	}
	screenLock.Unlock()
}
```
`HandleServerMsg` ignores the type (its switch has no default), so no client change is needed.

```python
# smoke.py
def case_pane_osc52_passes_through(fail):
    # The pane's printf emits OSC 52 itself; wideboi must re-emit it to
    # the host. The typed command line holds the base64 only as plain
    # text, never after ESC ] 52 ; c ; -- so a match is the passthrough.
    # SSH_TTY is set because that is the scenario, and so that
    # writeLocalClipboard does not run pbcopy on the dev's machine.
    s = Session(args=["--layout", "scroll"], env={"SSH_TTY": "/dev/null"})
    s.type("printf '\\033]52;c;%s\\a' cGFzc3Rocm91Z2gtb2s=\r")  # "passthrough-ok"
    out = s.output()
    s.close()
    found = [base64.b64decode(m) for m in re.findall(rb"\x1b\]52;c;([A-Za-z0-9+/=]*)(?:\x07|\x1b\\)", out)]
    if b"passthrough-ok" not in found:
        fail(f"pane OSC 52 never reached the host terminal; OSC 52 payloads seen: {found!r}")
```
`Session.type()` already blocks until output settles (`settle_output`, a ceiling rather than a sleep; `smoke.py:182-190`). The drag case relies on the same thing for its OSC 52, so no extra wait is needed.

**Tests first:**
- `TestPaneClipboardGoesToLastInputClient`:
  1. `tp1` from `NewServer`, `tp2 := transport.NewInProcChannel(32)` + `srv.AddClientForTest(ctx, tp2)`; both send `MsgAttach`.
  2. `tp2` sends `MsgInput{PaneID: 1, Data: []byte(" ")}`.
  3. Write OSC 52 to pane 1's grid.
  4. Assert `tp2` receives `MsgPaneClipboard{Text: "hello"}` within 2 s, and `tp1` receives none within the same window (drain and check types).
- `TestPaneClipboardFallsBackToAttachedClients`: no input sent; both attached clients receive it.
- `TestPaneClipboardSkipsUnattachedTransports`: a third transport added with `AddClientForTest` that never attaches receives nothing.
- `TestPaneClipboardLastInputClearedOnDisconnect`: `tp2` sends input, then disconnects. Use whatever removal path existing tests use, e.g. closing the channel, and find it via `removeTransportLocked` callers. `tp1` then receives the write through the fallback.
- `TestPaneClipboardIgnoresSendInputRequest`: `tp2` attached; a CLI-style `MsgSendInputRequest` from `tp2` for pane 1 doesn't set `lastInput`, so both clients receive it.
- Config, in `config_test.go`:
  - default `ClipboardEnabled == true`;
  - `clipboard = false` in the user file → false;
  - `WIDEBOI_CLIPBOARD=0` → false;
  - `DisableClipboard: true` flag → false;
  - an invalid env value → error.
  Use the existing table style.
- The smoke case fails on Phase 1 code. Run it before the main.go change.

**Verification — automated:**
- [x] New server tests fail first (`tp1` gets nothing / nil recipients), then pass with `-count=4` — **6 tests (incl. an added mouse-counts case) failed "never received"; then ok ×4; full `go test ./internal/server` ok**
- [x] `go test ./internal/config -run Clipboard -v` passes — **failed first (undefined fields); TestLoadClipboard PASS: default, file, env, env-over-file, invalid env, flag**
- [x] `python3 scripts/smoke.py` (or the Makefile `smoke` target) — new case fails before the main.go change, passes after; run the full smoke 4× — **failed on the Phase 1 binary ("payloads seen: []"), passed after. Added `--disable-clipboard blocks pane OSC 52`. OSC 52 cases 4/4 green. Full smoke ×4: 43/43 three times; once 42/43 inside `make check` ("reclaimed control keys pass through", which passed 6/6 in isolation; unrelated code path, so treated as the known load flake)**
- [!] `make check` — record failures; only the three known config ones are allowed — **DOES NOT HOLD for one run: besides the three config failures (in `test` and `race`), one smoke flake, "reclaimed control keys pass through". Not reproducible (6/6 isolated, 3 clean full runs), and doesn't touch this change. See notes.md.**

**Verification — manual (Les, over ssh, iTerm2):**
- [ ] In a wideboi pane on the remote: `printf '\e]52;c;%s\a' "$(printf hello | base64)"` → laptop clipboard is `hello`
- [ ] In Claude Code inside wideboi over ssh: drag-select copies to the laptop clipboard
- [ ] With `clipboard = false` in config (or `--disable-clipboard`): neither of the above changes the clipboard; `show-clipboard` still prints the text

---

## Phase 3: Web client approval toast

When the web client receives `MsgPaneClipboard`, it shows a non-modal toast with a preview and Copy/Dismiss buttons. Copy writes the clipboard inside the click gesture. A settings pref turns this off.

**Files:**
- Create: `web/src/components/clipboard-toast.ts` — `<wideboi-clipboard-toast>` Lit element.
- Create: `web/src/components/clipboard-toast.test.ts` — unit tests (preview, char count), following the `context-menu.test.ts` pattern.
- Modify: `web/src/prefs.ts`
  - `paneClipboard: boolean` in `Preferences`;
  - key `wideboi:paneClipboard`;
  - `getPref` case: null → true, `'false'`/`'off'` → false, as `notifications` does (~82);
  - `getDefaultPref` → true.
- Modify: `web/src/prefs.test.ts` — default true, stored false reads false.
- Modify: `web/src/components/settings-dialog.ts` — a row "Allow panes to copy" with a toggle button. The handler mirrors `handleToggleNotifications` (70) without the permission request. Labels: `Enabled (Click to Disable)` / `Disabled (Click to Enable)`.
- Modify: `web/src/wideboi-app.ts`
  - state `pendingClipboard: {paneId, title, text} | null`;
  - case `'paneClipboard'` in the message switch (~739);
  - render `<wideboi-clipboard-toast>` beside `<wideboi-context-menu>` (~2397);
  - `handleClipboardToastCopy`.
- Create: `web/tests/pane-clipboard.spec.ts` — Playwright, using the `links-and-clipboard.spec.ts` setup.

**Key changes:**

```ts
// clipboard-toast.ts
export const PREVIEW_LINES = 3;

/** First PREVIEW_LINES lines of text, with an ellipsis line if there were more. */
export function clipboardPreview(text: string): string {
  const lines = text.split('\n');
  const head = lines.slice(0, PREVIEW_LINES);
  return lines.length > PREVIEW_LINES ? [...head, '…'].join('\n') : head.join('\n');
}

@customElement('wideboi-clipboard-toast')
export class WideboiClipboardToast extends LitElement {
  @property({ type: Number }) paneId = 0;
  @property({ type: String }) paneTitle = '';
  @property({ type: String }) text = '';
  // Styles: position fixed, bottom/right 16px, z-index under the context
  // menu (9000), var(--wb-bg-surface)/var(--wb-border-divider) like
  // context-menu.ts; preview in a <pre> with white-space: pre, overflow
  // hidden, text-overflow ellipsis, max-width ~40ch.
  render() {
    const n = [...this.text].length.toLocaleString();
    return html`
      <div class="toast" role="status" aria-live="polite">
        <div class="title">📋 ${this.paneTitle} (pane ${this.paneId}) wants to copy</div>
        <div class="count">${n} characters</div>
        <pre class="preview">${clipboardPreview(this.text)}</pre>
        <div class="actions">
          <button type="button" class="dismiss" @click=${() => this.emit('dismiss')}>Dismiss</button>
          <button type="button" class="copy" @click=${() => this.emit('copy')}>Copy</button>
        </div>
      </div>`;
  }
  private emit(name: 'copy' | 'dismiss') {
    this.dispatchEvent(new CustomEvent(name, { bubbles: true, composed: true }));
  }
}
```
Text goes through Lit's `${}`, which escapes it, so a pane can't inject markup. Buttons receive focus only when clicked. Nothing calls `.focus()`, so typing keeps going to the pane.

```ts
// wideboi-app.ts, message switch
case 'paneClipboard': {
  const clip = message.msg.value;
  if (getPref('paneClipboard')) {
    this.pendingClipboard = { paneId: clip.paneId, title: clip.title, text: clip.text }; // newer replaces older
    this.requestUpdate();
  }
  break;
}

// handler: runs synchronously inside the click, so writeText has its user gesture
private handleClipboardToastCopy = async () => {
  const clip = this.pendingClipboard;
  this.pendingClipboard = null;
  this.requestUpdate();
  if (clip) await this.copyToClipboard(clip.text);
};

// render, beside the context menu
${this.pendingClipboard ? html`
  <wideboi-clipboard-toast
    .paneId=${this.pendingClipboard.paneId}
    .paneTitle=${this.pendingClipboard.title}
    .text=${this.pendingClipboard.text}
    @copy=${this.handleClipboardToastCopy}
    @dismiss=${() => { this.pendingClipboard = null; this.requestUpdate(); }}
  ></wideboi-clipboard-toast>` : ''}
```
`copyToClipboard` (`wideboi-app.ts:996-1030`) calls `navigator.clipboard.writeText` first, synchronously from the click's call stack. That keeps the gesture. Check this holds: the `await` comes after `writeText` has been called, not before.

```ts
// tests/pane-clipboard.spec.ts (sketch of the assertions)
// 1. grant clipboard-read/write, installMockWebSocket, connect, send layoutSnapshot (pane 1)
// 2. writeText('before'); send serverBytes({case: 'paneClipboard', value: {paneId: 1, title: 'claude', text: 'line1\nline2\nline3\nline4'}})
// 3. expect toast visible with "claude (pane 1) wants to copy", "23 characters", preview containing 'line3' and '…', not 'line4'
// 4. clipboard still 'before' (nothing written without the click)
// 5. click Copy → readText() === 'line1\nline2\nline3\nline4'; toast gone
// 6. send another paneClipboard, click Dismiss → clipboard unchanged, toast gone
// 7. two messages in a row → toast shows the second
// 8. localStorage 'wideboi:paneClipboard'='false' + reload/connect → message shows no toast
```

**Tests first:**
- `clipboardPreview`: 1 line → unchanged; 3 lines → unchanged; 4 lines → 3 + `…`.
- Prefs test.
- The Playwright spec fails (no toast) before the app change.

**Verification — automated:**
- [x] `cd web && npm run lint` (tsc) passes — **clean**
- [x] `make web-test` passes; new unit tests fail first — **prefs test failed (undefined) and toast test failed (module missing) first; then 187/187**
- [x] `make web-accept` passes, including `pane-clipboard.spec.ts` (fails before the app change) — **3/4 failed before wiring (the opt-out test passed trivially). After: 4/4, and 63/63 in web-accept. Removing the pref check made the opt-out test fail, so it's real.**
- [!] `make check` — record failures; only the three known config ones are allowed — **DOES NOT HOLD, on main either. Smoke under `make check`'s parallel load flakes in a different case each run. Branch, 4 runs: reclaimed control keys ✘✘, host resize ✘, clean. origin/main, 3 runs: clean, card layout toggles ✘, clean. Every failing case passes in isolation. Not attributable to this branch on this evidence; sample too small to rule out a small rate change. See notes.md.**

**Verification — manual:**
- [ ] Web client in a browser, in a pane: `printf '\e]52;c;%s\a' "$(printf hello | base64)"` → toast appears bottom-right; typing still goes to the pane; Copy puts `hello` on the clipboard (Safari and Chrome if possible)
- [ ] Settings → "Allow panes to copy" off → no toast
- [ ] Toast looks right in light and dark themes

---

## Phase 4: Docs

Doc-only, so no TDD.

**Files:**
- Modify: `docs/MANUAL.md`
  - **§5 "Mouse and Clipboard" (~236-259).** Add a subsection "Copying from programs in a pane":
    - pane OSC 52 writes pass through to the client that last typed or clicked in the pane (or all attached clients);
    - reads are refused;
    - the 1 MiB cap;
    - the web toast;
    - `show-clipboard`;
    - `clipboard = false` / `WIDEBOI_CLIPBOARD` / `--disable-clipboard`;
    - the poisoning trade-off in one or two sentences (a `cat`ed file can replace your clipboard; tmux defaults the other way).
  - **Config section (~500-530).** Add `clipboard` to the example TOML. Don't add it to the trust list (spec: not trust-gated). *(Revised after review: it is now partly trust-gated, so the MANUAL's untrusted list says "`clipboard` (off only)".)*
  - **Commands reference.** Wherever `dump-pane` is listed, add `show-clipboard [--json]`.
- Modify: `README.md:189` — one phrase covering pane copy passthrough, if the existing line doesn't already cover it.
- Modify: `docs/LESSONS.md`. Add only if execution taught something non-obvious. Candidate: "x/vt truncates OSC data at 4 MB silently and still calls the handler; cap below it."

**Verification — automated:**
- [x] `make quick` — record failures; only the three known config ones are allowed — **only the three config failures**

**Verification — manual:**
- [ ] Les reads the MANUAL section and it matches the behavior seen in Phases 2-3
