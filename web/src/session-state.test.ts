import { describe, expect, it } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { initialSessionState, reduceSession } from './session-state';
import {
  ColumnDataSchema,
  MsgLayoutSnapshotSchema,
  MsgPaneMetadataSchema,
  PaneStatus,
  type ColumnData,
  type MsgLayoutSnapshot,
} from './gen/internal/protocol/wirepb/wideboi_pb';

const col = (paneId: number, width = 80, height = 24): ColumnData =>
  create(ColumnDataSchema, { paneId, width, height });

const snapshotOf = (
  columns: ColumnData[],
  paneStatuses: Record<number, PaneStatus> = {},
  paneTitles: Record<number, string> = {},
): MsgLayoutSnapshot =>
  create(MsgLayoutSnapshotSchema, { columns, paneStatuses, paneTitles });

describe('session-state reducer', () => {
  it('initializes clean defaults', () => {
    const s = initialSessionState();
    expect(s.columns).toEqual([]);
    expect(s.activePanes).toEqual([]);
    expect(s.focusedPaneId).toBe(0);
    expect(s.previousFocusId).toBe(0);
    expect(s.stackFocusId).toBe(0);
    expect(s.focusTransition).toBe(0);
    expect(s.pendingFocusId).toBe(0);
    expect(s.displayWidths).toEqual({});
    expect(s.paneStatuses).toEqual({});
    expect(s.paneTitles).toEqual({});
    expect(s.paneMetadata).toEqual({});
    expect(s.macros).toEqual([]);
  });

  describe('layoutSnapshot', () => {
    it('populates columns, activePanes, and assigns initial focus', () => {
      const s0 = initialSessionState();
      const snapshot = snapshotOf(
        [col(1), col(2)],
        { 1: PaneStatus.IDLE, 2: PaneStatus.WORKING },
        { 1: 'bash', 2: 'vim' },
      );

      const s1 = reduceSession(s0, { type: 'layoutSnapshot', snapshot });
      expect(s1.columns).toEqual(snapshot.columns);
      expect(s1.activePanes).toEqual([1, 2]);
      expect(s1.focusedPaneId).toBe(1);
      expect(s1.stackFocusId).toBe(1);
      expect(s1.focusTransition).toBe(1);
      expect(s1.displayWidths).toEqual({ 1: 80, 2: 80 });
      expect(s1.paneStatuses).toEqual({ 1: PaneStatus.IDLE, 2: PaneStatus.WORKING });
      expect(s1.paneTitles).toEqual({ 1: 'bash', 2: 'vim' });
    });

    it('reconciles focus when current focused pane disappears', () => {
      let s = initialSessionState();
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1), col(2), col(3)]),
      });
      s = reduceSession(s, { type: 'focusPane', paneId: 3 });
      expect(s.focusedPaneId).toBe(3);

      // Now snapshot drops pane 3. Focus should fall back to adjacent pane.
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1), col(2)]),
      });
      expect(s.focusedPaneId).toBe(2);
      expect(s.activePanes).toEqual([1, 2]);
    });

    it('preserves custom displayWidths unless followPTY is enabled', () => {
      let s = initialSessionState();
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1, 80)]),
      });
      // Set custom width for pane 1
      s = reduceSession(s, { type: 'setDisplayWidth', paneId: 1, width: 120 });
      expect(s.displayWidths[1]).toBe(120);

      // Snapshot with different PTY width (90) without followPTY: keep custom 120
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1, 90)]),
        followPTY: false,
      });
      expect(s.displayWidths[1]).toBe(120);

      // Snapshot with followPTY: overwrites custom width
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1, 90)]),
        followPTY: true,
      });
      expect(s.displayWidths[1]).toBe(90);
    });

    it('prunes dead displayWidths and dead paneMetadata', () => {
      let s = initialSessionState();
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1), col(2)]),
      });
      s = reduceSession(s, {
        type: 'paneMetadata',
        metadata: create(MsgPaneMetadataSchema, { paneId: 1, cwd: '', userVars: {}, exited: false, exitCode: 0 }),
      });
      s = reduceSession(s, {
        type: 'paneMetadata',
        metadata: create(MsgPaneMetadataSchema, { paneId: 2, cwd: '', userVars: {}, exited: false, exitCode: 0 }),
      });

      // Snapshot drops pane 2
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1)]),
      });
      expect(s.displayWidths[2]).toBeUndefined();
      expect(s.paneMetadata[2]).toBeUndefined();
      expect(s.paneMetadata[1]).toBeDefined();
    });

    it('activates pendingFocusId if now in active panes', () => {
      let s = initialSessionState();
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1)]),
      });
      s = reduceSession(s, { type: 'paneCreated', paneId: 2 });
      expect(s.pendingFocusId).toBe(2);

      // Snapshot now includes pane 2
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1), col(2)]),
      });
      expect(s.focusedPaneId).toBe(2);
      expect(s.previousFocusId).toBe(1);
      expect(s.pendingFocusId).toBe(0);
    });
  });

  describe('paneClosed', () => {
    it('removes closed pane, reconciles focus, and cleans up metadata', () => {
      let s = initialSessionState();
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1), col(2)]),
      });
      s = reduceSession(s, { type: 'focusPane', paneId: 2 });
      s = reduceSession(s, {
        type: 'paneMetadata',
        metadata: create(MsgPaneMetadataSchema, { paneId: 2, cwd: '', userVars: {}, exited: false, exitCode: 0 }),
      });

      s = reduceSession(s, { type: 'paneClosed', paneId: 2 });
      expect(s.activePanes).toEqual([1]);
      expect(s.columns.map(c => c.paneId)).toEqual([1]);
      expect(s.focusedPaneId).toBe(1);
      expect(s.displayWidths[2]).toBeUndefined();
      expect(s.paneMetadata[2]).toBeUndefined();
    });

    it('sets stackFocusId to null in cards mode when focus changed and not mobile', () => {
      let s = initialSessionState();
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1), col(2)]),
      });
      s = reduceSession(s, { type: 'focusPane', paneId: 2 });

      s = reduceSession(s, {
        type: 'paneClosed',
        paneId: 2,
        layoutMode: 'cards',
        mobile: false,
      });
      expect(s.stackFocusId).toBeNull();
      expect(s.focusedPaneId).toBe(1);
    });

    it('clears previousFocusId and pendingFocusId if they match closed pane', () => {
      let s = initialSessionState();
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1), col(2)]),
      });
      s = reduceSession(s, { type: 'focusPane', paneId: 2 });
      s = reduceSession(s, { type: 'paneCreated', paneId: 2 });
      expect(s.previousFocusId).toBe(1);
      expect(s.pendingFocusId).toBe(2);

      s = reduceSession(s, { type: 'paneClosed', paneId: 2 });
      expect(s.pendingFocusId).toBe(0);

      s = reduceSession(s, { type: 'paneClosed', paneId: 1 });
      expect(s.previousFocusId).toBe(0);
    });
  });

  describe('focusPane and finishFocusStack', () => {
    it('switches focused pane and tracks previousFocusId', () => {
      let s = initialSessionState();
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1), col(2)]),
      });

      const transitionBefore = s.focusTransition;
      s = reduceSession(s, { type: 'focusPane', paneId: 2 });
      expect(s.focusedPaneId).toBe(2);
      expect(s.previousFocusId).toBe(1);
      expect(s.focusTransition).toBe(transitionBefore + 1);

      // Focusing already-focused pane is a no-op
      const unchanged = reduceSession(s, { type: 'focusPane', paneId: 2 });
      expect(unchanged).toBe(s);

      // Focusing non-existent pane is a no-op
      const nonexistent = reduceSession(s, { type: 'focusPane', paneId: 99 });
      expect(nonexistent).toBe(s);
    });

    it('clears stackFocusId when focusing in cards mode on desktop', () => {
      let s = initialSessionState();
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1), col(2)]),
      });

      s = reduceSession(s, {
        type: 'focusPane',
        paneId: 2,
        layoutMode: 'cards',
        mobile: false,
      });
      expect(s.stackFocusId).toBeNull();

      // Calling finishFocusStack with matching transition sets stackFocusId
      s = reduceSession(s, {
        type: 'finishFocusStack',
        paneId: 2,
        transition: s.focusTransition,
      });
      expect(s.stackFocusId).toBe(2);

      // With mismatched transition, finishFocusStack does nothing
      const noMatch = reduceSession(s, {
        type: 'finishFocusStack',
        paneId: 1,
        transition: 999,
      });
      expect(noMatch.stackFocusId).toBe(2);
    });
  });

  describe('splitResponse and paneCreated', () => {
    it('records pendingFocusId', () => {
      let s = initialSessionState();
      s = reduceSession(s, { type: 'paneCreated', paneId: 42 });
      expect(s.pendingFocusId).toBe(42);

      s = reduceSession(s, { type: 'splitResponse', paneId: 43 });
      expect(s.pendingFocusId).toBe(43);

      // splitResponse with 0 does not clear existing
      s = reduceSession(s, { type: 'splitResponse', paneId: 0 });
      expect(s.pendingFocusId).toBe(43);
    });
  });

  describe('reset and display width resets', () => {
    it('resets display widths to column widths', () => {
      let s = initialSessionState();
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1, 80), col(2, 80)]),
      });
      s = reduceSession(s, { type: 'setDisplayWidth', paneId: 1, width: 120 });
      expect(s.displayWidths[1]).toBe(120);

      s = reduceSession(s, { type: 'resetDisplayWidths' });
      expect(s.displayWidths[1]).toBe(80);
      expect(s.displayWidths[2]).toBe(80);
    });

    it('switches layout mode and bumps focus transition', () => {
      let s = initialSessionState();
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1)]),
      });
      const t = s.focusTransition;
      s = reduceSession(s, { type: 'setLayoutMode', mode: 'cards' });
      expect(s.stackFocusId).toBe(1);
      expect(s.focusTransition).toBe(t + 1);
    });

    it('resets back to initial state while preserving macros', () => {
      let s = initialSessionState();
      s = reduceSession(s, {
        type: 'macrosSnapshot',
        macros: [{ name: 'Test', steps: [{ text: 'hi' }] }],
      });
      s = reduceSession(s, {
        type: 'layoutSnapshot',
        snapshot: snapshotOf([col(1)]),
      });
      expect(s.activePanes).toEqual([1]);

      s = reduceSession(s, { type: 'reset' });
      expect(s.activePanes).toEqual([]);
      expect(s.columns).toEqual([]);
      expect(s.focusedPaneId).toBe(0);
      expect(s.macros).toEqual([{ name: 'Test', steps: [{ text: 'hi' }] }]);
    });
  });
});
