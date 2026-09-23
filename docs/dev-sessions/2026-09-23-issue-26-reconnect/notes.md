# Notes: Issue 26 Reconnect Handshake

- Added a `SetTransport` method to `Client` to allow replacing the transport without destroying the client (and losing layout mode / focus state).
- Modified the main loop in `runClient` so that if `ServerSendChan()` closes unexpectedly (i.e. not an intentional `hangUp()`), and the client isn't the owner, it attempts to `dialWithin` the socket for up to 2 seconds.
- Upon successful reconnection, the client initializes a new `ClientSocketConn`, assigns it via `SetTransport`, and immediately sends `MsgAttach`.
- Since we `continue` the loop after reconnecting, `gotMsg` remains true, meaning the terminal doesn't exit and re-enter the alt screen. The new `MsgLayoutSnapshot` arriving immediately afterwards seamlessly updates the UI in-place.
- Wrote a new integration test `case_dropped_connection_reconnects` in `scripts/attachcheck.py` that verifies the client survives a hard server kill and restarts.
- Confirmed thread safety: `runClient`'s loop is the sole accessor of the channel, so assigning the new transport in the same loop prevents any concurrent access panics.
