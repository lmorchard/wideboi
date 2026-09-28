import { describe, expect, it } from 'vitest';
import { filterCommands } from './components/command-palette';

describe('CommandMenu Component Logic', () => {
  it('filters commands by label', () => {
    const commands = [
      { id: 'new-pane', label: 'New Column', icon: '➕', shortcut: 'n', description: 'Open a new terminal pane' },
      { id: 'split', label: 'Split Pane', icon: '✂️', shortcut: '"', description: 'Split and open a new pane' },
      { id: 'close-pane', label: 'Close Pane', icon: '✕', shortcut: 'x', description: 'Close focused terminal pane' },
    ];
    const matches = filterCommands(commands, 'split');
    expect(matches).toHaveLength(1);
    expect(matches[0].id).toBe('split');
  });

  it('filters commands case-insensitively and by description', () => {
    const commands = [
      { id: 'search', label: 'Search Scrollback', icon: '🔍', shortcut: '/', description: 'Search history in focused pane' },
      { id: 'help', label: 'Help', icon: '❓', shortcut: '?', description: 'Keyboard shortcuts reference' },
    ];
    const matches = filterCommands(commands, 'history');
    expect(matches).toHaveLength(1);
    expect(matches[0].id).toBe('search');
  });

  it('filters commands by shortcut', () => {
    const commands = [
      { id: 'new-pane', label: 'New Column', icon: '➕', shortcut: 'n', description: 'Open a new terminal pane' },
      { id: 'settings', label: 'Settings', icon: '⚙', shortcut: ',', description: 'Preferences and appearance' },
    ];
    const matches = filterCommands(commands, ',');
    expect(matches).toHaveLength(1);
    expect(matches[0].id).toBe('settings');
  });

  it('returns all commands on empty query', () => {
    const commands = [
      { id: 'new-pane', label: 'New Column', icon: '➕', shortcut: 'n' },
      { id: 'close-pane', label: 'Close Pane', icon: '✕', shortcut: 'x' },
    ];
    expect(filterCommands(commands, '')).toHaveLength(2);
    expect(filterCommands(commands, '   ')).toHaveLength(2);
  });
});
