export type LayoutMode = 0 | 1; // 0 = Scroll, 1 = Cards
export type PlacementKind = 0 | 1;

export interface Rectangle {
  Min: { X: number; Y: number };
  Max: { X: number; Y: number };
}

export interface ColumnData {
  PaneID: number;
  Width: number;
  Height: number;
}

export interface PlacementData {
  PaneID: number;
  Src: Rectangle;
  Dst: Rectangle;
  Z: number;
  Kind: PlacementKind;
}

export interface ColorData {
  Kind: number;
  Index: number;
  R: number;
  G: number;
  B: number;
  A: number;
}

export interface StyleData {
  Fg: ColorData;
  Bg: ColorData;
  UnderlineColor: ColorData;
  Underline: number;
  Attrs: number;
}

export interface CellData {
  Content: string;
  Width: number;
  Style: StyleData;
}

// One entry per terminal column. Width describes paint width and does not
// change the index of the following entry.
export type LineData = CellData[];

export interface MsgLayoutSnapshot {
  Columns: ColumnData[];
  PaneStatuses: Record<number, number>;
  PaneTitles: Record<number, string>;
}

export interface MsgPaneUpdate {
  PaneID: number;
  Generation: number;
  Cols: number;
  Rows: number;
  Lines: LineData[];
  CursorX: number;
  CursorY: number;
  CursorVisible: boolean;
  MouseTracking: boolean;
}

export interface MsgPanePatch {
  PaneID: number;
  Cols: number;
  Rows: number;
  BaseGeneration: number;
  Generation: number;
  ChangedRows: { Y: number; Cells: LineData }[] | null;
  CursorX: number;
  CursorY: number;
  CursorVisible: boolean;
  MouseTracking: boolean;
}

export interface MsgPaneClosed {
  PaneID: number;
  ExitCode: number;
}

export interface MsgAttach {
  Cols: number;
  Rows: number;
}

export interface MsgResize {
  Cols: number;
  Rows: number;
}

export interface WSEnvelope {
  t: string;
  p: any;
}
