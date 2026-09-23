# Old shared server.log / client.log are no longer written or cleaned up

Since #107, logs sit beside each session's socket: `<socket minus .sock>.{server,client}.log`, e.g. `$TMPDIR/wideboi-<uid>/default.server.log` (`logger.Path` in `internal/logger/logger.go`). The old shared `$TMPDIR/wideboi-<uid>/server.log` and `client.log` from before that change are no longer written, and nothing removes them. They sit there, appended-to-forever files that have stopped growing, and can mislead someone who goes looking in the old place.

Separately, the per-session logs themselves are appended to forever (no rotation), and the `.lock` files beside sockets are never deleted, by design (see `NewSocketListener` and the LESSONS entry). So the session directory accumulates files for every session name ever used.

**Possible directions (pick a scope):**
- One-time: have the server (or `wideboi ls`) remove the legacy `server.log`/`client.log` if present. Or just mention them in release notes / the README.
- Ongoing: a size cap or rotation for per-session logs.
- Leave the `.lock` files alone. Deleting them reopens the ownership race, so any cleanup has to skip them or hold the lock while deciding.

Explicitly out of scope in #107 ("Log rotation, and migrating or removing old `server.log`/`client.log`").
