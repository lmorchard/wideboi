package protocol

// Version is the wire protocol this build speaks. The Unix socket's
// first frame in each direction carries it (transport.Handshake), and the
// WebSocket upgrade selects wideboi.v<Version>, so a
// client and server built from different wire formats refuse each
// other with a clear message instead of a decode error (#174).
//
// Bump it for any change an older peer would misread or reject: a new
// or renumbered message, a field whose meaning moved, a codec change.
// Sessions routinely outlive a rebuild, so this is the normal case, not
// an edge. There is no negotiation: peers either match or refuse.
//
// 1 is the first version with a handshake. Everything before it --
// gob, then protobuf without a hello -- shows up as version 0.
// 2 adds whole-pane shift semantics to MsgPanePatch. A version 1 client
// would ignore shift_rows and silently display stale retained rows.
// 3 adds scroll_offset, scrollback_len, and unread_output to MsgPaneUpdate
// and MsgPanePatch for independent per-client scrollback.
// 4 adds MsgPaneMetadata carrying CWD and OSC 1337 user variables.
const Version uint32 = 4
