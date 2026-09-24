
## Goal

When a client and server built from different wire-protocol versions connect, fail with a clear, actionable message instead of a garbage decode error.

## Problem

There's no version exchange on the Unix socket. After #166 (gob → protobuf) a freshly built client attached to a server started before #166 and failed like this:

```
wideboi: connection to wideboi server failed: reading from server: protobuf frame too large: 4288679936
```

`4288679936` is `0xFFA01000`, which is the start of a gob stream read as a 4-byte big-endian length prefix (`internal/transport/frame.go` `readFrame`). Nothing in that message points at the actual cause (a stale server on the socket), and plain `wideboi` joins the existing session silently, so the user thinks their config or build is broken.

This will happen on every future wire change. Running long-lived sessions across rebuilds is normal usage.

Making it worse: `wideboi kill-session` sends its request in the new protocol, so it can't stop a server running an older one. The only way out is `kill <pid>`.

## Proposed shape

- The first message in each direction on the Unix socket is a small, fixed hello: a magic value plus a protocol version. It must be decodable by any future version, and ideally recognisable by the current one.
- On a mismatch, the client exits with something like: `the wideboi server at <socket> (pid N) speaks protocol vX; this client speaks vY. Detach from it with a matching build, or start a separate session with -L <name>.`
- The server logs the mismatch and closes the connection. It must **not** end the session, even when the mismatched client is the owner.
- Fallback for peers from before the handshake: if the first frame length is implausible (> maxFrameSize), report it as a probable version mismatch / stale server, not as "frame too large".
- WebSocket: the web client is served by the same binary, so a mismatch is unlikely there. Worth one line in the spec saying whether it's covered.

## Out of scope

- Keeping old protocols working (negotiation, downgrade). Detect and explain, nothing more.
- Automatically killing or replacing stale servers. Users keep live work in them.

## Acceptance

- A test that connects a peer sending a different version, or gob bytes, gets the clear error. The server keeps running and the session survives.
- `make check` green.


## Decisions (2026-09-24, session start)

- **Hello = one ordinary length-prefixed frame** whose 16-byte payload is
  `"WIDEBOI\0"`, uint32 BE protocol version, uint32 BE pid. Future versions may
  append bytes; the first 16 never change. `protocol.Version` starts at 1.
- **Synchronous `transport.Handshake(conn)`** before a conn is wrapped in a
  socket conn: write ours, read theirs, under a deadline. Failure is a
  `*transport.MismatchError`; `Theirs == 0` means the peer predates the
  handshake (gob bytes, a non-hello frame, or a hang-up mid-handshake).
- **Server:** listener conns handshake in their own goroutine *before* joining
  `s.transports`, so a mismatched client never counts as a client and cannot
  reach `dropClient`. A bare dial-and-close (`ls`, `cleanup`, the listener's own
  probe) logs at debug, not warn.
- **Owner mismatch** (socketpair; only possible at startup, before any pane):
  the server logs and exits. No session exists yet to protect, and a detached
  orphan would be invisible. Chosen by Les over "run detached".
- **Client:** attach, plain `wideboi`, reconnect, `status`, `kill-session` all
  handshake and print the issue's message; `kill-session` adds a `kill <pid>`
  hint when the pid is known. An owner whose spawned server exited during the
  handshake keeps the existing startup/session-taken reporting.
- **WebSocket is not covered:** the page and the server come from one binary.

## What we're NOT doing

- Negotiation or downgrade; peer-pid lookup (`LOCAL_PEERPID`/`SO_PEERCRED`) for
  pre-handshake servers; a test that forces a version bump when the schema moves.
