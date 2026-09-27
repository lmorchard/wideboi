import { expect, it, vi } from 'vitest';
import { sendKeyboardInput, sendTextInput, sendWheelInput } from './input';
import { MouseKind } from './gen/internal/protocol/wirepb/wideboi_pb';

function key(key: string, code: string, mods: Partial<KeyboardEvent> = {}): KeyboardEvent {
  return { key, code, shiftKey: false, altKey: false, ctrlKey: false,
    metaKey: false, isComposing: false, repeat: false, ...mods } as KeyboardEvent;
}

it('emits terminal key messages for Unicode, shifted, modified and navigation keys', () => {
  const send = vi.fn();
  const sender = { send };
  expect(sendKeyboardInput(sender, 7, key('界', 'KeyA'))).toBe(true);
  expect(send.mock.lastCall).toEqual([{ case: 'input', value: {
    paneId: 7, key: { text: '界', mod: 0, code: 97, shiftedCode: 0, baseCode: 97, isRepeat: false }
  } }]);
  sendKeyboardInput(sender, 7, key('!', 'Digit1', { shiftKey: true }));
  expect(send.mock.lastCall?.[0].value.key).toMatchObject({ text: '!', mod: 1, code: 49 });
  sendKeyboardInput(sender, 7, key('c', 'KeyC', { ctrlKey: true, altKey: true }));
  expect(send.mock.lastCall?.[0].value.key).toMatchObject({ text: 'c', mod: 6, code: 99 });
  sendKeyboardInput(sender, 7, key('ArrowLeft', 'ArrowLeft', { ctrlKey: true }));
  expect(send.mock.lastCall?.[0].value.key).toMatchObject({ text: '', mod: 4, code: 0x110004 });
  expect(sendKeyboardInput(sender, 7, key('Process', 'KeyA', { isComposing: true }))).toBe(false);
});

it('sends pasted and composed text as UTF-8 bytes', () => {
  const send = vi.fn();
  sendTextInput({ send }, 3, 'é界');
  expect(send).toHaveBeenCalledWith({ case: 'input', value: {
    paneId: 3, data: new TextEncoder().encode('é界')
  } });
});

it('sends mouse wheel messages for wheel up and down with cell coords and modifiers', () => {
  const send = vi.fn();
  const sender = { send };

  // Wheel up -> button 4
  expect(sendWheelInput(sender, 2, { x: 10, y: 5 }, { deltaY: -100 } as WheelEvent)).toBe(true);
  expect(send.mock.lastCall).toEqual([{
    case: 'mouse',
    value: {
      paneId: 2,
      kind: MouseKind.WHEEL,
      x: 10,
      y: 5,
      button: 4,
      mod: 0,
    },
  }]);

  // Wheel down -> button 5 with modifiers (shift=1, alt=2, ctrl=4 => 7)
  expect(sendWheelInput(sender, 2, { x: 15, y: 8 }, {
    deltaY: 120, shiftKey: true, altKey: true, ctrlKey: true,
  } as WheelEvent)).toBe(true);
  expect(send.mock.lastCall).toEqual([{
    case: 'mouse',
    value: {
      paneId: 2,
      kind: MouseKind.WHEEL,
      x: 15,
      y: 8,
      button: 5,
      mod: 7,
    },
  }]);

  // deltaY === 0 -> returns false, sends nothing
  expect(sendWheelInput(sender, 2, { x: 10, y: 5 }, { deltaY: 0 } as WheelEvent)).toBe(false);
  expect(send).toHaveBeenCalledTimes(2);
});
