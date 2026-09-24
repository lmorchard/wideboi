import { create, fromBinary, toBinary } from '@bufbuild/protobuf';
import {
  ClientMessageSchema, ServerMessageSchema,
  MsgAttachSchema, MsgVerbSchema, MsgFocusPaneSchema, MsgMouseSchema,
  MsgInputSchema, MsgResizeSchema, MsgScrollSchema, KeyDataSchema,
  type ClientMessage, type ServerMessage, type VerbType, type MouseKind,
} from './gen/internal/protocol/wirepb/wideboi_pb';

type SendArgs =
  | ['MsgAttach', { Cols: number; Rows: number }]
  | ['MsgResize', { Cols: number; Rows: number }]
  | ['MsgVerb', { Verb: VerbType }]
  | ['MsgFocusPane', { PaneID: number }]
  | ['MsgScroll', { PaneID: number; Delta: number }]
  | ['MsgMouse', { PaneID: number; Kind: MouseKind; X: number; Y: number; Button: number; Mod: number }]
  | ['MsgInput', { PaneID: number; Key: { Text: string; Mod: number; Code: number; ShiftedCode: number; BaseCode: number; IsRepeat: boolean }; Data: Uint8Array }];

export class WideboiClient {
  private ws: WebSocket | null = null;
  public onMessage?: (message: ServerMessage) => void;
  public onConnect?: () => void;
  public onDisconnect?: () => void;

  constructor(private readonly url: string) {}

  public connect() {
    this.ws = new WebSocket(this.url);
    this.ws.binaryType = 'arraybuffer';
    this.ws.onopen = () => this.onConnect?.();
    this.ws.onclose = () => { this.ws = null; this.onDisconnect?.(); };
    this.ws.onerror = (err) => console.error('[WideboiClient] WebSocket error:', err);
    this.ws.onmessage = async (event: MessageEvent<ArrayBuffer | Blob>) => {
      try {
        const data = event.data instanceof Blob ? await event.data.arrayBuffer() : event.data;
        this.onMessage?.(fromBinary(ServerMessageSchema, new Uint8Array(data)));
      } catch (err) {
        console.error('[WideboiClient] Failed to decode protobuf message:', err);
      }
    };
  }

  public send(...args: SendArgs) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    let message: ClientMessage;
    switch (args[0]) {
      case 'MsgAttach': message = create(ClientMessageSchema, { msg: { case: 'attach', value: create(MsgAttachSchema, { cols: args[1].Cols, rows: args[1].Rows }) } }); break;
      case 'MsgResize': message = create(ClientMessageSchema, { msg: { case: 'resize', value: create(MsgResizeSchema, { cols: args[1].Cols, rows: args[1].Rows }) } }); break;
      case 'MsgVerb': message = create(ClientMessageSchema, { msg: { case: 'verb', value: create(MsgVerbSchema, { verb: args[1].Verb }) } }); break;
      case 'MsgFocusPane': message = create(ClientMessageSchema, { msg: { case: 'focusPane', value: create(MsgFocusPaneSchema, { paneId: args[1].PaneID }) } }); break;
      case 'MsgScroll': message = create(ClientMessageSchema, { msg: { case: 'scroll', value: create(MsgScrollSchema, { paneId: args[1].PaneID, delta: args[1].Delta }) } }); break;
      case 'MsgMouse': message = create(ClientMessageSchema, { msg: { case: 'mouse', value: create(MsgMouseSchema, { paneId: args[1].PaneID, kind: args[1].Kind, x: args[1].X, y: args[1].Y, button: args[1].Button, mod: args[1].Mod }) } }); break;
      case 'MsgInput': {
        const { Key: key, Data: data, PaneID: paneId } = args[1];
        message = create(ClientMessageSchema, { msg: { case: 'input', value: create(MsgInputSchema, {
          paneId, data, key: create(KeyDataSchema, { text: key.Text, mod: key.Mod, code: key.Code, shiftedCode: key.ShiftedCode, baseCode: key.BaseCode, isRepeat: key.IsRepeat }),
        }) } });
        break;
      }
    }
    this.ws.send(toBinary(ClientMessageSchema, message));
  }

  public disconnect() { this.ws?.close(); this.ws = null; }
}
