package protocol

// Version is the wire protocol this build speaks. The Unix socket's
// first frame in each direction carries it (transport.Handshake), so a
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
const Version uint32 = 1
