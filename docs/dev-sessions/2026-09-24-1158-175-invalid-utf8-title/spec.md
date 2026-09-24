
## Goal

A pane title (or any string) that isn't valid UTF-8 must not tear down the client connection, let alone end the session.

## Symptom

A session with `[[startup]]` entries for `nvim`, `claude`, `codex` and `opencode` opens a few panes and exits within about 0.4s, every time. It looks like a failing startup command is killing the session. It isn't. Server log:

```
level=ERROR msg="transport pump failed" where="encoding protocol.MsgLayoutSnapshot to client" err="string field contains invalid UTF-8"
level=INFO msg="owning client left without detaching; ending the session"
```

## Root cause (verified)

1. **x/vt truncates titles at an embedded `0x9C` byte.** Claude Code sets its title to `✳ Claude Code`. `✳` is UTF-8 `E2 9C B3`, and `0x9C` is the C1 String Terminator. The `charmbracelet/x/ansi` v0.11.8 parser treats it as ST inside an OSC string (`parser/transition_table.go:167`), so the OSC dispatches with the payload `"\xe2"`. `vtGrid.Title()` (`internal/server/term/grid.go`) then stores that lone byte. Probe results feeding `ESC ] 0 ; <title> BEL` into `term.NewVT`:
   - `"✳ Claude Code"` → `"\xe2"` (invalid)
   - `"⠂ working"`, `"héllo"`, `"nvim — file.go"` → unchanged (valid)

   Any character whose UTF-8 encoding contains byte `0x9C` hits this. The rest of the sequence (` Claude Code` BEL) presumably lands on the screen as text.
2. **Protobuf `string` fields reject invalid UTF-8.** `PaneTitles` in `MsgLayoutSnapshot` goes through `protocol.MarshalServer`, which fails. Gob never checked this, so the bug is latent until #166 makes it fatal.
3. **One marshal error kills the connection.** `ServerSocketConn.writeLoop` (`internal/transport/socket.go`) returns on any `MarshalServer` error and closes the conn.
4. **The owner's disconnect ends the session** (`internal/server/server.go`, "owning client left without detaching"). So a bad title in any pane takes out the whole session.

## Design (decided 2026-09-24)

Three layers, all in this PR:

1. **Sanitise at the codec edge.** `internal/protocol/codec.go` passes every outbound wire string (pane titles, cell `Content`, key `Text`) through `strings.ToValidUTF8(s, "\uFFFD")`. This is the backstop for every source of bad bytes, socket and WebSocket alike.
2. **An encode failure doesn't kill the connection.** In `ServerSocketConn.writeLoop` and the WebSocket write pump, a `MarshalServer` error is logged and that message is dropped. The pump keeps going. Write errors (a dead peer) still end the pump as they do today.
3. **Local workaround for the x/ansi 0x9C bug** (Les chose this over splitting it off). There's a small stateful scanner in `vtGrid.Write` (`internal/server/term/grid.go`), in front of `em.Write`. It follows 7-bit `ESC ]` OSC strings with UTF-8 awareness, keeping its state across `Write` calls, which already run under `writeResizeMu`. A `0x9C` byte that is an expected UTF-8 continuation inside an OSC is not a String Terminator. The scanner buffers the OSC until its real terminator (BEL, `ESC \`, or a non-continuation `0x9C`), then:
   - if the payload contains no continuation `0x9C`: forwards it unchanged. This is the common path.
   - OSC 0/2 (title) with a continuation `0x9C`: sets the title directly, correctly decoded, so `✳ Claude Code` survives. The sequence is not forwarded, because the parser would truncate it.
   - any other OSC with a continuation `0x9C`: replaces the affected characters with U+FFFD and forwards the result.
   - CAN/SUB aborts and oversized payloads (over a cap) are flushed through unchanged, so the scanner can never hold bytes indefinitely.

   Bytes outside OSC pass straight through. The ground-state parser is already UTF-8-aware, so the bug only exists inside string states. The fast path should cost about one `IndexByte(0x1B)` per write.

## What we're NOT doing

- 8-bit C1 OSC introducers (`0x9D`) and DCS/APC/SOS/PM strings. Only 7-bit OSC is handled; titles are what bit us. Note it as a known gap.
- Fixing or forking x/ansi. Filing an upstream report is a nice-to-have follow-up, noted in the PR.
- Changing owner-disconnect semantics. With layers 1–2 the owner simply doesn't disconnect.

## Acceptance

- A test that sets a pane title containing `✳`, or raw invalid bytes, keeps the owning client connected and the session alive.
- A codec test: marshalling a snapshot or patch with invalid UTF-8 strings succeeds.
- Repro: `[[startup]] command = "claude"` in `.wideboi.toml`, start a new named session. It stays up.
- `make check` green, run four times if the pump error path changes (timing).

