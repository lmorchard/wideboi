# Pane clipboard passthrough (OSC 52) Spec

**Goal:** Let programs running in a pane, Claude Code especially, copy to the clipboard of the machine the user is sitting at, including over ssh and from the web client, and keep the latest copy fetchable via `wideboi show-clipboard`.

**Source:** Les, 2026-10-05. Copy and URL clicks inside agent panes don't reach the laptop over ssh.

## Current state

- Agents that request mouse tracking get wideboi's mouse events, so wideboi skips its own selection (`internal/client/mouse.go`, "No wideboi selection over a child that wants the mouse"). The agent copies by emitting OSC 52, or by running `pbcopy`, which runs on the remote host.
- The server drops OSC 52 from a pane. `grid.go` registers handlers only for 133/9/7/1337, vt has no clipboard support, and an unhandled OSC is discarded without a trace (research.md §OSC).
- Confirmed by hand: over ssh, `printf '\e]52;c;%s\a' "$(printf hello | base64)"` in a wideboi pane leaves the host clipboard unchanged. The iTerm2 clipboard setting is on.
- wideboi's own drag-select copy already works: `writeClipboard`, `cmd/wideboi/main.go:1377-1389`.

## Desired end state

- A pane writes OSC 52 `52;<sel>;<base64>`. The server decodes it and sends a new `MsgPaneClipboard{PaneID, Title, Text}` to one client: the last one that sent keyboard, paste or mouse input to that pane. If that client isn't attached (or none has sent input yet), it goes to every **attached** client (`isAttachedLocked`, `internal/server/clients.go:64`). Never plain `s.transports`: CLI command connections live there too (`RPCQuery` dials the server), and they shouldn't receive clipboard text.
- **TUI client:** writes the text to its host terminal through the existing `writeClipboard` path (OSC 52, plus the local `pbcopy`-family tool when not over ssh), unless `clipboard = false`.
- **Web client:** shows a non-modal toast in the bottom-right:
  - "📋 <title> (pane N) wants to copy · N characters", a plain-text preview of the first ~3 lines, and **[Dismiss] [Copy]** buttons.
  - Copy calls `navigator.clipboard.writeText` inside the click, so the browser treats it as a user gesture, falling back to `copyToClipboard`'s existing `execCommand` route.
  - A newer request replaces the current toast. There is no auto-dismiss timer. The toast doesn't take keyboard focus.
  - A settings-dialog pref, "Allow panes to copy", defaults to on. When it is off, no toast appears.
- **Config:** `clipboard` (bool, default true) in `config.toml`, `WIDEBOI_CLIPBOARD`, and a `--disable-clipboard` flag. Default-on booleans use `--disable-…` flags, as with `--disable-auto-cleanup`. It is client-side and gates only whether this client acts on `MsgPaneClipboard`.
- **Server-held copy:** the server keeps the most recent pane clipboard write (text, pane id, title, time). It lives in memory only and is lost when the server exits.
  - It is stored whether or not any client is attached, and regardless of any client's `clipboard` setting.
  - `wideboi show-clipboard` prints the text raw to stdout. `--json` prints `{pane_id, title, time, text}`. With nothing stored yet, it prints an error to stderr and exits non-zero.
  - It uses a new `MsgShowClipboardRequest`/`Response` pair, sent with the v30 bump.
- **Docs:** MANUAL §5 "Mouse and Clipboard" covers passthrough, the setting, the poisoning trade-off and the web toast.
- **Protocol:** Version 30. `web/src/version.ts` and the version guard hash are updated to match.

## Design decisions

- **Recipient: the last client to send input to the pane, falling back to all attached clients.**
  - **Why:** With a laptop over ssh and a phone on the web client, sending to everyone overwrites both clipboards. The client that just dragged in the pane is the one that wants the copy. `handleInputLocked`/`handleMouseLocked` already receive the sending `tp` (`internal/server/handlers.go:191-194`).
  - **What counts as input:** `MsgInput` (keys and paste) and a `MsgMouse` **press**. *(Revised after review: motion, wheel and release don't count, or scrolling from another client would steal delivery.)* **Not counted:** `MsgSendInputRequest` from CLI automation (`send-keys`, agents driving panes), since that isn't a person. `MsgScroll` doesn't count either.
  - **Disconnect:** the entry is cleared when its transport goes away.
  - **Rejected:** all clients (tmux-style, clobbers other devices); clients focused on the pane (focus is per client, so two clients can both have it focused).
- **Writes only. Reads (`52;c;?`) are dropped.**
  - **Why:** answering a read gives the host clipboard (passwords, tokens) to any pane program or `cat`ed file. Agents don't need reads to copy.
  - **Rejected:** opt-in reads, which need a query/reply round trip into the PTY. Out of scope.
- **On by default, opt-out via `clipboard = false`.**
  - **Why:** copying from agents is the point of the feature.
  - **Known trade-off:** clipboard poisoning. A hostile file you `cat` can replace your clipboard. tmux's default `set-clipboard external` blocks pane writes for this reason. The risk matches what a bare iTerm2 with clipboard access already allows. The web toast adds a human check, and the TUI does not.
  - **Rejected:** off by default (the fix would look missing); no setting at all (no way out).
- **The server decodes base64 and the message carries text.**
  - **Why:** the web client needs text, and validating once in the server keeps garbage off the wire.
  - **Rules:**
    - Drop invalid base64, empty payloads (a clear request), and invalid UTF-8.
    - Ignore the selection field (`c`/`p`/`s`/…) and always target the system clipboard, as `ansi.SetSystemClipboard` does.
  - **Rejected:** forwarding raw bytes and leaving decoding to each client.
- **Size cap: decoded text up to 1 MiB, and the raw payload up to 2 MiB, otherwise drop.** *(Raw cap added after review.)*
  - **Why:** vt's parser truncates OSC data at 4 MB without telling anyone (`vt/emulator.go:89`), and the handler still fires with partial base64. A cap well under that means a truncated payload can't be mistaken for a complete one. Reject when the base64 length exceeds `base64.StdEncoding.EncodedLen(1<<20)`, before decoding. This also bounds the wire message. **Revised after review:** that check runs on the payload after whitespace is stripped, so megabytes of padding could push a valid tail past x/vt's truncation while the stripped payload still looked small. The raw payload is therefore capped at 2 MiB first. That's still room for a maximal payload wrapped at 76 columns (~1.4 MB).
- **The TUI writes from the main loop under `screenLock`.**
  - **Why:** `writeClipboard` requires `screenLock` held and `!stopped` (`main.go:1377-1389`). Bell notifications write to stdout from `HandleServerMsg` outside the lock (research.md §TUI), but OSC 52 must not interleave with a frame flush.
  - **How:** `cmd/wideboi/main.go` intercepts `MsgPaneClipboard` in its server-message case, before `cli.HandleServerMsg`, and calls `writeClipboard` under `screenLock`. This is the same lock pattern as the mouse-copy path (`main.go:1285-1292`). `internal/client` needs no change, since its switch ignores the type.
- **`show-clipboard`: the latest pane copy only, any socket client may read it.**
  - **Why:** lets automation fetch what an agent copied, and still works with no client attached.
  - **Access:** anyone who can reach the socket can already `dump-pane` every pane's screen, so this adds no new access.
  - **`clipboard = false` doesn't affect it:** that setting means "don't write my host clipboard", and the buffer is passive.
  - **Rejected:**
    - a history ring (more surface before anyone has used it);
    - feeding wideboi's own drag-selects into the buffer (needs a client→server message, and makes your selections readable by every pane).
- **Delivery: one worker, one coalescing slot.** *(Revised after review; the first version spawned a goroutine per write, with a sequence number and a mutex to keep order.)* A pane can copy far faster than a stalled client accepts sends, and each pending goroutine held up to 1 MiB. `onPaneClipboard` now parks the write in a single slot that one worker drains. A newer write replaces a waiting one, so memory is bounded at one in flight plus one pending, order is kept, and the last copy wins. The callback still never takes `s.mu` (deadlock with `Resize`; see the bell pattern).
- **The message carries the pane title,** for the web toast. It uses the same fallback to `"Pane %d"` as `broadcastPaneBell` (`server.go:630-650`).

## Patterns to follow

- One-shot pane event: the bell. `grid.go:340-344` callback → `Pane.SetOnBell` (`pane.go:539`) → wired at spawn (`server.go:588-590`) **and** upgrade adoption (`upgrade.go:358-360`) → `go broadcast…` (`server.go:615-650`). Mirror it as `OnClipboard`/`SetOnClipboard`/`onPaneClipboard`.
- OSC handler shape and input hardening: the OSC 1337 handler (`grid.go:487-549`). It receives the full payload, splits on `;`, and enforces caps.
- New server→client message checklist: commit 2eb45c9 (`MsgPaneNotification`, v21). Touch the proto, `make proto`, `messages.go`, `codec.go` both directions, `version.go`, `version_guard_test.go`, `wire_test.go` lists (protocol and transport), and `web/src/version.ts`.
- Request/response CLI command: `dump-pane` (`internal/commands/registry.go:389`) and its `MsgDumpPaneRequest` handler (`handlers.go:283`). Palette and help exposure follow whatever the registry does for `dump-pane`.
- Client-side toggle: `notifications` end to end (`config.go`, `main.go:92` flag + help, `cli.SetNotifications` at 1007, MANUAL). Bool shape: `Mouse *bool`/`MouseEnabled` (`config.go:43-46,692`).
- Web: the message switch at `wideboi-app.ts:595-745`, prefs in `web/src/prefs.ts`, the toggle in `components/settings-dialog.ts`, the copy fallback in `copyToClipboard` (`wideboi-app.ts:996-1030`).

## What we're NOT doing

- **URL clicks** that go to the agent and run `open` remotely. Tracked separately in #399, pending Les's iTerm2 Cmd/Option-click test.
- **OSC 52 reads** (query/reply).
- **Per-selection targets** (primary vs clipboard).
- **Changing wideboi's own drag-select copy** or `writeLocalClipboard`.
- **Moving bell notification writes under `screenLock`,** even though they share the same unlocked-write shape. Note it, don't fix it.
- **Clipboard history,** a `set-clipboard`/paste-from-buffer command, or persisting the buffer across server restarts or upgrades.
- **A TUI confirmation prompt.** Only the web client gets the approval step.
- **Fixing the macOS `/tmp`-symlink config test failures** on main (in project memory).

## Open questions

- **Should `clipboard` be trust-gated in project `.wideboi.toml`?** ~~Default: no. An untrusted file setting it can only turn copying *off*, or back to the default.~~ **Revised after review: yes, one way.** That reasoning was wrong. If the user's global config says `clipboard = false`, an untrusted project's `clipboard = true` would silently undo their poisoning opt-out. An untrusted project may now set it to `false` only; `true` is honoured only from trusted files.
