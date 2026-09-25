import { describe, it, expect } from 'vitest';
import {
  findHistoryMatches,
  computeScrollTarget,
  navigateIndex,
  runeCount,
  createSearchSession,
  applySnapshot,
  cancelSearch,
  liveSearch,
  formatSearchStatus,
} from './search';

describe('search module', () => {
  describe('runeCount', () => {
    it('counts unicode code points correctly', () => {
      expect(runeCount('hello')).toBe(5);
      expect(runeCount('本 here')).toBe(6);
      expect(runeCount('✳ star')).toBe(6);
      expect(runeCount('🚀 rocket')).toBe(8); // 🚀 is 1 code point, though 2 UTF-16 code units
    });
  });

  describe('findHistoryMatches', () => {
    it('returns empty array when query is empty or rows are empty', () => {
      expect(findHistoryMatches([], 'test')).toEqual([]);
      expect(findHistoryMatches(['line 1', 'line 2'], '')).toEqual([]);
    });

    it('finds single match on a row', () => {
      const rows = ['first row', 'second target row', 'third row'];
      const matches = findHistoryMatches(rows, 'target');
      expect(matches).toEqual([{ row: 1, col: 7 }]);
    });

    it('finds multiple matches on the same row', () => {
      const rows = ['foo bar foo baz foo'];
      const matches = findHistoryMatches(rows, 'foo');
      expect(matches).toEqual([
        { row: 0, col: 0 },
        { row: 0, col: 8 },
        { row: 0, col: 16 },
      ]);
    });

    it('finds matches across multiple rows in scrollback and screen', () => {
      const rows = [
        'build succeeded',
        'running tests',
        'test foo passed',
        'build finished',
      ];
      const matches = findHistoryMatches(rows, 'build');
      expect(matches).toEqual([
        { row: 0, col: 0 },
        { row: 3, col: 0 },
      ]);
    });

    it('handles unicode characters and calculates rune column offset accurately', () => {
      // '本' is 3 bytes in UTF-8, 1 rune
      // '✳' is 3 bytes in UTF-8, 1 rune
      const rows = [
        'prefix 本 findme',
        'emoji ✳ findme',
        'rocket 🚀 findme',
      ];
      const matches = findHistoryMatches(rows, 'findme');
      expect(matches).toEqual([
        { row: 0, col: 9 }, // 'prefix 本 ' is 9 runes (7 + 1 + 1)
        { row: 1, col: 8 }, // 'emoji ✳ ' is 8 runes (6 + 1 + 1)
        { row: 2, col: 9 }, // 'rocket 🚀 ' is 9 runes (7 + 1 + 1)
      ]);
    });

    it('handles soft-wrapped physical rows independently', () => {
      // In terminal emulators, long lines soft-wrap into consecutive physical rows
      const rows = [
        'this is a long line that wrapped around to the next',
        'physical row and continues here with searchtarget inside',
      ];
      const matches = findHistoryMatches(rows, 'searchtarget');
      expect(matches).toEqual([{ row: 1, col: 37 }]);
    });

    it('returns empty array when no matches are found', () => {
      const rows = ['alpha', 'beta', 'gamma'];
      expect(findHistoryMatches(rows, 'delta')).toEqual([]);
    });
  });

  describe('computeScrollTarget', () => {
    it('computes scroll offset for matches in scrollback', () => {
      // 10 lines of scrollback (rows 0-9), screen starts at row 10
      // Match at row 8 (2nd line from bottom of scrollback): offset should be 10 - 8 = 2
      expect(computeScrollTarget(10, 8)).toBe(2);
      // Match at row 0 (oldest line): offset should be 10 - 0 = 10
      expect(computeScrollTarget(10, 0)).toBe(10);
      // Match at row 9 (last line of scrollback): offset should be 10 - 9 = 1
      expect(computeScrollTarget(10, 9)).toBe(1);
    });

    it('returns 0 for matches on the visible screen', () => {
      // Screen rows are at index >= scrollbackLen
      expect(computeScrollTarget(10, 10)).toBe(0);
      expect(computeScrollTarget(10, 15)).toBe(0);
    });

    it('clamps target between 0 and scrollbackLen', () => {
      expect(computeScrollTarget(5, -2)).toBe(5);
      expect(computeScrollTarget(5, 100)).toBe(0);
    });
  });

  describe('navigateIndex', () => {
    it('returns -1 when totalMatches is 0', () => {
      expect(navigateIndex(0, 0, 1)).toBe(-1);
      expect(navigateIndex(-1, 0, 1)).toBe(-1);
    });

    it('selects latest match (totalMatches - 1) on initial navigation from -1', () => {
      expect(navigateIndex(-1, 5, 0 as any)).toBe(4);
      expect(navigateIndex(-1, 1, 1)).toBe(0);
      expect(navigateIndex(-1, 3, 1)).toBe(2);
    });

    it('navigates next with wrap-around', () => {
      expect(navigateIndex(0, 3, 1)).toBe(1);
      expect(navigateIndex(1, 3, 1)).toBe(2);
      expect(navigateIndex(2, 3, 1)).toBe(0); // wraps
    });

    it('navigates prev with wrap-around', () => {
      expect(navigateIndex(2, 3, -1)).toBe(1);
      expect(navigateIndex(1, 3, -1)).toBe(0);
      expect(navigateIndex(0, 3, -1)).toBe(2); // wraps
    });
  });

  describe('search session operations', () => {
    it('creates search session capturing prior view', () => {
      const session = createSearchSession(42, 5, 20, 'err');
      expect(session).toEqual({
        paneId: 42,
        priorOffset: 5,
        priorHistoryLen: 20,
        query: 'err',
        status: 'input',
        matches: [],
        selectedIndex: -1,
      });
    });

    it('applies snapshot with matches and returns scroll target', () => {
      const session = createSearchSession(1, 0, 4, 'match');
      const snapshot = {
        scrollbackLen: 4,
        rows: ['old match', 'line 2', 'newer match', 'live screen'],
      };
      // Initial commit (direction 0): selects latest match (row 2)
      const res = applySnapshot(session, snapshot, 0);
      expect(res.nextState.status).toBe('navigating');
      expect(res.nextState.matches).toHaveLength(2);
      expect(res.nextState.selectedIndex).toBe(1); // row 2
      expect(res.scrollMsg).toEqual({
        paneId: 1,
        offset: 4 - 2, // 2
        anchorHistory: true,
        historyLen: 4,
      });

      // Navigating prev (-1): selects row 0
      const prevRes = applySnapshot(res.nextState, snapshot, -1);
      expect(prevRes.nextState.selectedIndex).toBe(0);
      expect(prevRes.scrollMsg?.offset).toBe(4 - 0); // 4
    });

    it('applies snapshot with no matches', () => {
      const session = createSearchSession(1, 0, 4, 'missing');
      const snapshot = {
        scrollbackLen: 4,
        rows: ['foo', 'bar', 'baz'],
      };
      const res = applySnapshot(session, snapshot, 0);
      expect(res.nextState.status).toBe('no_match');
      expect(res.nextState.matches).toHaveLength(0);
      expect(res.nextState.selectedIndex).toBe(-1);
      expect(res.scrollMsg).toBeUndefined();
    });

    it('returns cancel and live scroll targets', () => {
      const session = createSearchSession(2, 7, 50, 'test');
      expect(cancelSearch(session)).toEqual({
        paneId: 2,
        offset: 7,
        anchorHistory: true,
        historyLen: 50,
      });

      const zeroOffsetSession = createSearchSession(2, 0, 50, 'test');
      expect(cancelSearch(zeroOffsetSession)).toEqual({
        paneId: 2,
        offset: 0,
        anchorHistory: false,
        historyLen: 50,
      });

      expect(liveSearch(session)).toEqual({
        paneId: 2,
        offset: 0,
        anchorHistory: false,
        historyLen: 0,
      });
    });

    it('formats search status messages matching TUI', () => {
      const s = createSearchSession(1, 0, 0, 'test');
      expect(formatSearchStatus(s)).toBe('Enter find · Esc cancel');

      s.status = 'searching';
      expect(formatSearchStatus(s)).toBe('searching…');

      s.status = 'no_match';
      expect(formatSearchStatus(s)).toBe('no match · Esc restore · Ctrl+g live');

      s.status = 'navigating';
      s.matches = [{ row: 10, col: 5 }, { row: 15, col: 2 }];
      s.selectedIndex = 0;
      expect(formatSearchStatus(s)).toBe('1/2 row 11 char 6 · n/N next/prev · Enter keep · Esc restore · Ctrl+g live');
    });
  });
});
