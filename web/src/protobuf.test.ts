import { describe, expect, it } from 'vitest';
import { create, fromBinary, toBinary } from '@bufbuild/protobuf';
import { ClientMessageSchema, ColorKind, ServerMessageSchema } from './gen/internal/protocol/wirepb/wideboi_pb';

const bytes = (hex: string) => Uint8Array.from(hex.match(/../g) ?? [], (part) => parseInt(part, 16));

// The hex strings are what Go's codec produces (protocol.MarshalClient /
// MarshalServer), so these pin the browser to the server's actual bytes.
describe('Go and browser protobuf compatibility', () => {
  it('encodes a browser attach exactly as Go does', () => {
    const attach = create(ClientMessageSchema, { msg: { case: 'attach', value: { cols: 80, rows: 24 } } });
    expect(toBinary(ClientMessageSchema, attach)).toEqual(bytes('0a0408501018'));
  });

  it('decodes a Go pane update with a styled cell', () => {
    // MsgPaneUpdate{PaneID: 7, Generation: 3, Cols: 1, Rows: 1,
    //   Lines: [[{Content: "x", Width: 1, Style: {Fg: {Kind: Indexed, Index: 4}}}]],
    //   CursorVisible: true}
    const message = fromBinary(ServerMessageSchema, bytes('0a1b08071003180120012a0f0a0d0a017810011a060a04080210044001'));
    if (message.msg.case !== 'paneUpdate') throw new Error(`decoded ${message.msg.case}`);
    const pane = message.msg.value;
    expect(pane.paneId).toBe(7);
    expect(pane.generation).toBe(3n);
    expect(pane.lines[0].cells[0].content).toBe('x');
    expect(pane.lines[0].cells[0].style?.fg?.kind).toBe(ColorKind.INDEXED);
    expect(pane.lines[0].cells[0].style?.fg?.index).toBe(4);
    expect(pane.lines[0].cells[0].style?.bg).toBeUndefined();
    expect(pane.cursorVisible).toBe(true);
  });
});
