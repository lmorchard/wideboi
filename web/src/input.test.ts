import { expect, it, vi } from 'vitest';
import { sendKeyboardInput, sendTextInput } from './input';

function key(key: string, code: string, mods: Partial<KeyboardEvent> = {}): KeyboardEvent {
  return { key, code, shiftKey: false, altKey: false, ctrlKey: false,
    metaKey: false, isComposing: false, repeat: false, ...mods } as KeyboardEvent;
}

it('emits terminal key messages for Unicode, shifted, modified and navigation keys', () => {
  const send = vi.fn();
  const sender = { send };
  expect(sendKeyboardInput(sender, 7, key('界', 'KeyA'))).toBe(true);
  expect(send.mock.lastCall).toEqual(['MsgInput', {
    PaneID: 7, Key: { Text: '界', Mod: 0, Code: 97, ShiftedCode: 0, BaseCode: 97, IsRepeat: false }, Data: ''
  }]);
  sendKeyboardInput(sender, 7, key('!', 'Digit1', { shiftKey: true }));
  expect(send.mock.lastCall?.[1].Key).toMatchObject({ Text: '!', Mod: 1, Code: 49 });
  sendKeyboardInput(sender, 7, key('c', 'KeyC', { ctrlKey: true, altKey: true }));
  expect(send.mock.lastCall?.[1].Key).toMatchObject({ Text: 'c', Mod: 6, Code: 99 });
  sendKeyboardInput(sender, 7, key('ArrowLeft', 'ArrowLeft', { ctrlKey: true }));
  expect(send.mock.lastCall?.[1].Key).toMatchObject({ Text: '', Mod: 4, Code: 0x110004 });
  expect(sendKeyboardInput(sender, 7, key('Process', 'KeyA', { isComposing: true }))).toBe(false);
});

it('sends pasted and composed text as UTF-8 bytes', () => {
  const send = vi.fn();
  sendTextInput({ send }, 3, 'é界');
  expect(send).toHaveBeenCalledWith('MsgInput', {
    PaneID: 3, Data: 'w6nnlYw='
  });
});
