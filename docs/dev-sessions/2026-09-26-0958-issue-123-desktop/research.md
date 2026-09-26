# Research

- Wails v2 is single-window; v3 beta supports independent native windows. Pin
  v3.0.0-beta.26 and keep the ordinary CLI build free of Wails compile tags.
- An existing CLI session has a Unix socket but usually no WebSocket listener.
  The desktop process can bridge one local, authenticated WebSocket per client
  to the existing versioned protobuf socket transport.
- The current owner socketpair mechanism already makes a launched session end
  with its owner and supports `MsgDetach` to leave it running. The desktop app
  keeps that connection open and drains messages.
- The lifetime owner attaches with zero dimensions. The first visible client
  then sets PTY size, rather than leaving an invisible 80x24 owner in control.
- Pane CWD is mutable. Add the session's starting CWD to layout snapshots so
  project matching remains stable after a pane runs `cd`. The new protobuf
  field is additive; old servers still work through the pane-CWD fallback.
