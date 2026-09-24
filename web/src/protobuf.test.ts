import { describe, expect, it } from 'vitest';
import { create, fromBinary, toBinary } from '@bufbuild/protobuf';
import { ClientMessageSchema, MsgAttachSchema, ServerMessageSchema } from './gen/internal/protocol/wirepb/wideboi_pb';

const bytes = (hex: string) => Uint8Array.from(hex.match(/../g) ?? [], (part) => parseInt(part, 16));

describe('Go and browser protobuf compatibility', () => {
  it('encodes a browser attach as the Go client envelope', () => {
    const attach = create(ClientMessageSchema, {
      msg: { case: 'attach', value: create(MsgAttachSchema, { cols: 80, rows: 24 }) },
    });
    expect(toBinary(ClientMessageSchema, attach)).toEqual(bytes('0a0408501018'));
  });

  it('decodes a Go pane update with a styled cell', () => {
    const message = fromBinary(ServerMessageSchema, bytes('0a19080710011801220f0a0d0a017810011a060a04080110043801'));
    expect(message.msg.case).toBe('paneUpdate');
    if (message.msg.case !== 'paneUpdate') return;
    expect(message.msg.value.paneId).toBe(7);
    expect(message.msg.value.lines[0].cells[0].content).toBe('x');
    expect(message.msg.value.lines[0].cells[0].style?.fg?.index).toBe(4);
    expect(message.msg.value.cursorVisible).toBe(true);
  });
});
