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
// 5 adds MsgTrafficRequest/MsgTrafficStats (#179).
// 6 adds VerbToggleStatus and MsgFocusPane (#196).
// 7 adds VerbClaimSize to VerbType for multi-client sizing control (#184).
// 8 adds agent-friendly pane control requests and responses (split, send, capture, close) (#198).
// 9 adds client-requested plain-text history snapshots and absolute
// per-client scroll requests for local search (#183).
// 10 adds split keep, pane exit metadata, and wait requests/responses (#227).
// 11 adds per-pane widths to size claims and exact owner width changes (#70).
// 12 adds input macro snapshots and save requests (#237).
// 13 adds the session's startup directory to layout snapshots (#123).
const Version uint32 = 13
