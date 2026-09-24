// Client-local types. Wire messages are the generated protobuf types in
// ./gen/internal/protocol/wirepb/wideboi_pb.
export type LayoutMode = 0 | 1; // 0 = Scroll, 1 = Cards
export type PlacementKind = 0 | 1;

export interface Rectangle {
  Min: { X: number; Y: number };
  Max: { X: number; Y: number };
}

export interface PlacementData {
  PaneID: number;
  Src: Rectangle;
  Dst: Rectangle;
  Z: number;
  Kind: PlacementKind;
}
