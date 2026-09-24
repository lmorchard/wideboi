import { create, fromBinary, toBinary } from '@bufbuild/protobuf';
import {
  ClientMessageSchema, ServerMessageSchema, type ServerMessage,
} from '../src/gen/internal/protocol/wirepb/wideboi_pb';

export function serverBytes(msg: NonNullable<ServerMessage['msg']>): Uint8Array {
  return toBinary(ServerMessageSchema, create(ServerMessageSchema, { msg }));
}

export function clientMessages(bytes: Uint8Array[]) {
  return bytes.map(data => fromBinary(ClientMessageSchema, data).msg);
}
