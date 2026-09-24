# Wire protocol

This document describes how a wideboi client and the wideboi server exchange
messages. It is written in Simplified Technical English.

## 1. Overview

The server runs the terminal panes. A client shows the panes and sends user
input to the server.

There are two types of client:

- The terminal client (`wideboi attach`). It connects through a Unix socket.
- The web client. It connects through a WebSocket.

Both clients use the same messages. The messages are Protocol Buffers
(protobuf). The schema is `internal/protocol/wirepb/wideboi.proto`.

## 2. Envelopes

Each message goes in an envelope. There are two envelopes:

- `ClientMessage` holds one message from a client to the server.
- `ServerMessage` holds one message from the server to a client.

Each envelope is a protobuf `oneof`. It holds exactly one message.

## 3. Messages

### 3.1 Client to server

| Message | Purpose |
|---|---|
| `MsgAttach` | Connect to the session. Gives the client size. If the session has no panes, the server starts them. |
| `MsgResize` | Gives a new client size. |
| `MsgVerb` | Asks for an action, for example "new column" or "kill pane". |
| `MsgInput` | Sends a key or text to a pane. |
| `MsgMouse` | Sends a mouse event to a pane. |
| `MsgScroll` | Scrolls a pane. |
| `MsgPaneResync` | Asks for a full update of one pane. See section 5. |
| `MsgStatusRequest` | Asks for a layout snapshot. `wideboi status` uses it. |
| `MsgDetach` | Disconnects this client. The session continues. |
| `MsgShutdown` | Stops the session. |

### 3.2 Server to client

| Message | Purpose |
|---|---|
| `MsgLayoutSnapshot` | Gives the columns, the pane statuses, and the pane titles. |
| `MsgPaneCreated` | Tells one client that its `MsgVerb` made a new pane. |
| `MsgPaneUpdate` | Gives the full contents of one pane. |
| `MsgPanePatch` | Gives the changed rows of one pane. |
| `MsgPaneClosed` | Tells the client that a pane closed. The server does not send it at this time. |
| `MsgPaneMetadata` | Gives the current working directory and user variables of one pane. |

Each client keeps its own focus and layout. The server does not send them.

## 4. Transports

### 4.1 Unix socket

A Unix socket is a byte stream. Thus, each message has a frame:

1. A 4-byte length, big-endian.
2. The protobuf envelope. Its length is the value in step 1.

The maximum frame size is 64 MiB. The code is in
`internal/transport/frame.go`.

The first frame in each direction is a hello, not an envelope. Its payload
is 16 bytes:

1. The magic `WIDEBOI\0` (8 bytes).
2. The protocol version, a 4-byte big-endian integer: `protocol.Version`.
3. The sender's pid, a 4-byte big-endian integer.

A later version may add bytes after these 16. It must not change them. Each
side sends its hello and reads the other's before it sends anything else. If
the versions are different, the connection closes. The code is in
`internal/transport/handshake.go`.

### 4.2 WebSocket

Each WebSocket message holds one envelope as a binary frame. The WebSocket
does the framing, so there is no length prefix. There is no hello. Before
upgrading, the client offers `wideboi.v<Version>` as a subprotocol and the
server selects it only when it matches `protocol.Version`. Missing or older
versions receive HTTP 426. The token may be offered as a separate subprotocol;
it is never selected as the negotiated protocol.

If a web client sends a message that is larger than 1 MiB, the server closes
the connection. The server sends a ping each 30 seconds.

## 5. Pane updates and patches

Each pane has a generation number. The number increases when the pane
contents change.

The server sends a `MsgPaneUpdate` in these conditions:

- The client does not have the pane yet.
- The pane size changed.
- The server sent a `MsgLayoutSnapshot`. Each snapshot is followed by a full
  update of each pane.
- The client sent `MsgPaneResync`.
- Half or more of the rows changed.

In other conditions, the server sends a `MsgPanePatch`. A patch holds:

- The generation that it changes (`base_generation`).
- The new generation.
- Each changed row, complete.
- The cursor position, the cursor visibility, and the mouse mode.

The client applies a patch only if `base_generation` is the same as the
generation that it has. If it is not the same, the client:

1. Deletes its copy of the pane.
2. Sends `MsgPaneResync`.

The server then sends a `MsgPaneUpdate` for that pane.

For more data about patches, see `docs/partial-pane-updates.md`. That document
gives measurements for the earlier JSON encoding.

## 6. Pane metadata (OSC 7 and OSC 1337)

Child processes and coding agents can emit out-of-band metadata for a pane:

- **OSC 7 (Current Working Directory):**
  Format: `ESC ] 7 ; file://[hostname]/path BEL` (or terminated by `ESC \`).
  The hostname must be empty, `"localhost"`, or the machine hostname. Foreign
  hostnames are ignored. The path must be an absolute path. It is percent-decoded
  and cleaned before storage.
- **OSC 1337 `SetUserVar` (User-defined metadata):**
  Format: `ESC ] 1337 ; SetUserVar=<name>=<base64-value> BEL` (or `ESC \`).
  `<name>` may contain letters, numbers, hyphens, and underscores (max 64 bytes).
  `<base64-value>` is standard base64 encoding of UTF-8 text (max 4096 bytes
  decoded).
  If the decoded value is empty, the variable is deleted.
  Each pane retains up to 64 variables.
  Malformed base64, invalid UTF-8, keys or values over size limits, or other
  OSC 1337 subcommands are dropped safely without error or side effect.

The server transmits metadata to clients via `MsgPaneMetadata`, which carries
`pane_id`, `cwd`, and `user_vars`. The server sends this message when a pane's
metadata changes, when a new client connects, and in response to `MsgStatusRequest`.

## 7. A missing field is zero

Protobuf does not send a field that has its zero value. For example, it does
not send `false`, `0`, an empty string, or an empty style. The receiver reads
a missing field as zero.

A missing field never means "no change". This is correct because a patch
always replaces complete rows. Also, a patch always gives the cursor and mouse
fields.

If you add a field that must mean "no change" when it is missing, use
`optional` in the schema.

## 8. Errors

On the Unix socket:

- If the peer's hello has a different version, or the first frame is not a
  hello, the connection closes before the peer is a client. The server logs
  it and the session continues. The client shows the version of each side,
  the socket and the server's pid. A peer from before the hello existed
  shows as version 0.
- If the peer closes the connection, this is a clean close. This is also
  true in the middle of a frame.
- If a complete frame does not decode, this is an error. The connection
  stops. `wideboi attach` shows the error.

On the WebSocket:

- A missing or mismatched version subprotocol receives HTTP 426 before the
  connection is upgraded.
- If a message does not decode, the server writes a warning to the log. The
  server ignores the message. The connection continues.
- If a message is not binary, the server does the same.

## 9. The Go code and the web code

The Go code does not use the generated types in the server or the client. It
uses its own structs in `internal/protocol`. The file
`internal/protocol/codec.go` converts between the structs and the generated
types. The conversion occurs only in the transports.

The web client uses the generated TypeScript types directly. Generation
numbers are `bigint` in TypeScript.

## 10. How to change the protocol

To add a field or a message:

1. Change `internal/protocol/wirepb/wideboi.proto`.
2. Do `make proto`. This makes the Go and TypeScript files again.
3. Change the Go struct in `internal/protocol`.
4. Change `internal/protocol/codec.go`.
5. For a new message, add it to `wireTypes` in
   `internal/protocol/wire_test.go`.
6. If a peer of the old version would misread or reject the change, increase
   `Version` in `internal/protocol/version.go` and the browser's offered
   subprotocol in `web/src/client.ts`.
7. Do `make check`.

The tests find these errors:

- `TestCodecRoundTripsEveryField` fails if the codec does not convert a field.
- `TestWireSchemaCoversEveryWireType` fails if the schema and `wireTypes`
  have a different number of messages.
- `TestEnumsMatchWireSchema` fails if an enum value in Go is different from
  the schema.

`make proto` needs `buf` and Node 22 or later. Do `npm ci --prefix web` first.
The server and the clients must use the same protocol version. The Unix hello
and WebSocket subprotocol check detect a difference; there is no negotiation.
