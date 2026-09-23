# Issue 26: Reconnect Handshake

## Goal
A dropped socket connection should not cause the `wideboi attach` process to exit unless it was a clean detach or shutdown. Instead, the client should automatically attempt to reconnect to the socket. When reconnected, it should send a new `MsgAttach` so that the server re-broadcasts the layout and pane state to this client.

## Context
- The client connects via `net.DialTimeout` or `net.Dial` to a unix socket.
- If the `ServerSendChan` channel closes unexpectedly (i.e. `!isCleanClose(err)` or just EOF when `routeDetach` wasn't used), the current client just prints "server closed the connection" and exits.
- Since the server sends cell state instead of PTY bytes, the client can just drop its local display and completely re-build it upon reconnecting and receiving the `MsgLayoutSnapshot` and `MsgPaneUpdate` frames.
- A reconnect loop needs to back off or sleep slightly, dial again, and on success replace the old connection with the new one and call `cli.Attach(ctx)`.

## Requirements
- If the socket connection drops, instead of immediately exiting, `runClient` (or a helper loop around it) should attempt to reconnect.
- Reconnection should only be attempted if `owner == false` (the client didn't start the server directly; if `owner == true` and the socket dropped, the subprocess is dead and the session is dead).
- If the reconnection succeeds, the client sends `Attach` and restores the stream.
- An explicit detach or quit (`C-b d` or `C-b q`) must still exit cleanly.
- If the server exited properly (the user shut it down), the client might see an EOF. Wait, if the user shut down the session from another client, all clients get dropped. If a client attempts to reconnect to a dead socket (server is gone), it should eventually time out or fail. If `Dial` fails, we can just exit after a short timeout or immediately.

## Implementation details
In `cmd/wideboi/main.go`, `runClient` does the following:
```go
if !ok {
    hungUp.Store(true)
    awaitReRaise()
    if err := cConn.Err(); err != nil {
        return fmt.Errorf("connection to wideboi server failed: %w", err)
    }
    if owner && !gotMsg {
        // ... startup failure ...
    }
    slog.Info("server closed the connection")
    return nil
}
```
If we get here and it was an unexpected drop, we can instead do:
If `owner` is true, we exit (server is dead).
If `owner` is false, and the server dropped us, we can try to `net.Dial` the socket. If `Dial` fails (e.g. `connect: no such file or directory` or `connection refused`), it means the server is actually gone, so we just exit. If `Dial` succeeds, the server was restarted or there's a proxy between us.
Wait, if the proxy dropped but the socket is a Unix domain socket?
In typical usage, a unix socket drop happens if the server process dies. If the server process dies, `Dial` will fail because the socket file is either dead (connection refused) or removed. If the server was immediately restarted, `Dial` might succeed. But `wideboi server` creates a new session. Reconnecting to a new session is fine: it's what "reconnect" means.

Wait, is it just about wrapping `runClient` in a loop?
In `runAttach`, we can just do:
```go
func runAttach(cfg config.Config, bindings []keys.Binding) error {
	for {
		conn, err := net.Dial("unix", cfg.Socket)
		if err != nil {
			return fmt.Errorf("no wideboi server running at %s (start one with 'wideboi server'): %w", cfg.Socket, err)
		}
		err = runClient(cfg, bindings, conn, nil)
		if errors.Is(err, errClientReconnecting) {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return err
	}
}
```
But wait, how does `runClient` know it should trigger a reconnect?
If the connection is lost and it was NOT due to a user `hangUp` (explicit detach/quit).
Currently, explicit detach calls `hangUp` which sends a message and waits for the server to close the socket.
When the server closes it, `runClient` sees `!ok` and `cConn.Err() == nil`.
Wait! If `cConn.Err() == nil`, it means it was a clean close.
So if `cConn.Err() != nil` OR if the server just closes it (which also might set EOF, but `isCleanClose(EOF)` makes `Err()` return `nil`).
If `Err() == nil`, is it a clean detach by the server?
Wait, if the server is killed (`kill -9`), does it look like EOF? Yes.
So `cConn.Err()` will be `nil` in both cases (explicit detach and server killed) because of `isCleanClose(io.EOF)`.

Let's read the issue closely again.
"Today a dropped socket connection means running `wideboi attach` again by hand. The client/server seam was built for this — the server ships cell data, not raw PTY bytes, precisely so a client that just reconnected can be handed a screen it never saw — but the handshake itself is unbuilt.

Already true and load-bearing:
- The server's emulator is the single authority; two clients fed raw bytes would drift.
- Placement is computed client-side, so two clients of different sizes are correct by construction.
- `Attach` already carries a protocol version."

Wait, what drops the connection? Maybe an SSH tunnel for the socket? Or a TCP proxy?
If it drops, `wideboi attach` exits. We want it to auto-reconnect.
If we reconnect seamlessly, how do we distinguish a reconnectable drop from an intentional `C-b d`?
When the user types `C-b d`, `runClient` explicitly calls `hangUp`, and then `runClient` returns `nil` directly from the `routeDetach` case!
Ah! In `main.go`:
```go
case routeDetach:
    slog.Info("client detaching")
    hangUp(ctx, cConn, protocol.MsgDetach{}, shutdownCeiling)
    return nil
```
Notice it returns `nil` IMMEDIATELY after `hangUp`. It doesn't go back to the `select` loop for `cConn.ServerSendChan()`.
Wait, if it returns, `defer guard.Stop()` and `defer cancel()` run. The context is cancelled.
So if `runClient` exits the `for` loop because of `!ok` on `ServerSendChan()`, it MUST mean the server closed the connection without the client asking for it (or another client shut down the whole server, or network dropped).

If the server shut down the session (e.g. another client did `C-b q` or `wideboi kill`), the socket will be removed or `Dial` will fail. So attempting to reconnect will fail immediately with `ECONNREFUSED` or `ENOENT`. That's fine! If it fails, we can just say "session ended" and exit.
Wait, if `Dial` succeeds, we've found a server on that socket. We should reconnect!
But wait, if we put the reconnect loop outside `runClient`, it means the terminal is restored (`scr.ExitAltScreen()`), and then if we reconnect, it re-enters the alt screen, causing a flicker.
Ideally, we'd reconnect *inside* `runClient` without dropping the alt screen if we don't have to, OR just drop the alt screen and re-enter.
Let's check if the prompt specifies inside `runClient`. "a dropped connection should not need a fresh attach" -> if we just wrap it in a retry loop inside `runAttach` or inside `runClient`.
If we reconnect *inside* `runClient`, we keep the terminal screen and cursor state, just pause rendering until we reconnect.
Let's see how `runClient` can reconnect without leaving `runClient`.