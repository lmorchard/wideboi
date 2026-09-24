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

export type LineData = CellData[];

export interface MsgLayoutSnapshot {
  Columns: ColumnData[];
  FocusPaneID: number;
  PaneStatuses: Record<number, string>;
  PaneTitles: Record<number, string>;
}

export interface MsgPaneUpdate {
  PaneID: number;
  Cols: number;
  Rows: number;
  Lines: LineData[];
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



// Renderer data is kept separate from the generated transport schema.
import type { MsgLayoutSnapshot as WireLayout, MsgPaneUpdate as WirePane, ColorData as WireColor } from './gen/internal/protocol/wirepb/wideboi_pb';

function fromWireColor(color?: WireColor): ColorData {
  return { Kind: color?.kind ?? 0, Index: color?.index ?? 0, R: color?.r ?? 0, G: color?.g ?? 0, B: color?.b ?? 0, A: color?.a ?? 0 };
}

export function fromWireLayout(wire: WireLayout): MsgLayoutSnapshot {
  return {
    Columns: wire.columns.map((column) => ({ PaneID: column.paneId, Width: column.width, Height: column.height })),
    FocusPaneID: wire.focusPaneId,
    PaneStatuses: wire.paneStatuses,
    PaneTitles: wire.paneTitles,
  };
}

export function fromWirePane(wire: WirePane): MsgPaneUpdate {
  return {
    PaneID: wire.paneId, Cols: wire.cols, Rows: wire.rows,
    Lines: wire.lines.map((line) => line.cells.map((cell) => ({
      Content: cell.content, Width: cell.width,
      Style: {
        Fg: fromWireColor(cell.style?.fg), Bg: fromWireColor(cell.style?.bg),
        UnderlineColor: fromWireColor(cell.style?.underlineColor),
        Underline: cell.style?.underline ?? 0, Attrs: cell.style?.attrs ?? 0,
      },
    }))),
    CursorX: wire.cursorX, CursorY: wire.cursorY,
    CursorVisible: wire.cursorVisible, MouseTracking: wire.mouseTracking,
  };
}
