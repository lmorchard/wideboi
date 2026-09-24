# Notes — #174 protocol version handshake

## What shipped

- `protocol.Version` (= 1) in `internal/protocol/version.go`.
- `transport.Handshake(conn)` in `internal/transport/handshake.go`: a 16-byte
  hello frame (`WIDEBOI\0`, version, pid) each way, sent while the peer's is
  read (net.Pipe would deadlock two peers that both write first), under a 5 s
  ceiling. `*MismatchError{Ours, Theirs, PID}`; `Theirs == 0` = pre-handshake
  peer. Hang-ups wrap `io.EOF` so the server can log liveness probes at debug.
- `frame.go` gained an `errFrameTooLarge` sentinel instead of string matching.
- Server: `admitSocketConn` handshakes in its own goroutine before the conn
  joins `s.transports`. Owner (socketpair) mismatch: `runServer` logs and
  exits (Les's call — no pane exists yet).
- Client: `runClient` (before the terminal starts; also on reconnect),
  `runStatus`, `runKillSession` (with a `kill <pid>` hint). Formatting lives
  in `cmd/wideboi/handshake.go`.
- Docs: PROTOCOL.md §4.1/§4.2/§7/§9, README note, LESSONS entry.

## Verification

- TDD throughout; each new test watched failing first. The owner-exit test was
  also proven by disabling the handshake (`runServer = <nil>`).
- Real binaries: new client vs a fake gob server and vs an `origin/main` build
  (protobuf, no hello) — clear messages, old server survived, old client still
  worked with it. Old client vs new server: new server logs
  `refusing socket client` and keeps serving.

## Found in self-review

- Owner path first routed *every* handshake failure through the spawned
  server's exit code. Since a mismatched server now exits by design, a real
  mismatch printed "server exited during startup". Now only an EOF (no hello
  at all) takes that path. `TestOwnerReportsAMismatchedSpawnedServer`.

## CI caught a Linux-only failure

PR #187's first CI run failed: on Linux a peer that closes with our hello
unread gives `ECONNRESET`, not EOF. The owner then missed "session taken"
(attach-check `two plain wideboi at once`) and a server test read a reset.
`readHello` now treats a reset as a hang-up wrapping `io.EOF`
(`TestHandshakeResetIsEOF`). Verified in `golang:1.27.1` under Docker: the
affected Go packages x4 and attachcheck x4, all green.

## Left alone / follow-ups

- An old client talking to a new server still prints its old, vague error —
  nothing new code can do about that.
- No peer-pid lookup for pre-handshake servers (`LOCAL_PEERPID` /
  `SO_PEERCRED`); the message says "kill the process holding <socket>".
- Nothing forces a `Version` bump when the schema changes; considered a test
  hashing `wideboi.proto`, left out as churn on comment-only edits.
