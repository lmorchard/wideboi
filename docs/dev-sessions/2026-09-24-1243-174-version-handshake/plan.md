# Plan — #174 protocol version handshake

1. **transport: hello + Handshake (TDD).** `internal/protocol.Version`;
   `transport/handshake.go` with `Handshake(net.Conn) (peer Hello, err error)`,
   `MismatchError`. Tests over `net.Pipe`/socketpair: matching versions; peer
   on another version; peer sending gob bytes (0xFF A0 10 00 prefix); peer
   sending a non-hello protobuf frame; peer that closes without a word.
2. **server: handshake before registration.** `ListenSocket` handshakes per
   conn in a goroutine. Test: a raw conn sending a wrong-version hello is
   closed, the server keeps running, a real client still attaches and sees its
   panes. Owner: `runServer` handshakes the owner fd and exits on failure.
3. **client call sites.** `runClient` (incl. reconnect), `runStatus`,
   `runKillSession`; message formatting in `cmd/wideboi` with socket + pid.
   Update test fakes (`main_test.go`, `status_test.go`).
4. **Docs.** README troubleshooting line if one fits; LESSONS entry on bumping
   `protocol.Version`.
5. `make check`, x4 for the server test; real-binary check against a fake
   old server.
