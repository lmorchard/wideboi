// Key codes mirror ultraviolet's KeyExtended range and named key order.
const extended = 0x110000;
const namedCodes: Record<string, number> = {
  ArrowUp: extended + 1, ArrowDown: extended + 2,
  ArrowRight: extended + 3, ArrowLeft: extended + 4,
  Insert: extended + 7, Delete: extended + 8,
  PageUp: extended + 10, PageDown: extended + 11,
  Home: extended + 12, End: extended + 13,
  Backspace: 127, Tab: 9, Enter: 13, Escape: 27
};

export interface InputSender {
  send(type: string, payload: unknown): void;
}

export function sendKeyboardInput(sender: InputSender, paneID: number, event: KeyboardEvent): boolean {
  if (event.isComposing || event.key === 'Process' || event.key === 'Dead' ||
      event.metaKey || event.key === 'Unidentified') return false;
  const code = namedCodes[event.key] ?? event.key.codePointAt(0);
  if (code === undefined || (Array.from(event.key).length !== 1 && !(event.key in namedCodes))) return false;
  const base = /^Key[A-Z]$/.test(event.code) ? event.code.charAt(3).toLowerCase().codePointAt(0)! :
    /^Digit[0-9]$/.test(event.code) ? event.code.charAt(5).codePointAt(0)! : code;
  sender.send('MsgInput', {
    PaneID: paneID,
    Key: {
      Text: event.key in namedCodes ? '' : event.key,
      Mod: (event.shiftKey ? 1 : 0) | (event.altKey ? 2 : 0) | (event.ctrlKey ? 4 : 0),
      Code: base, ShiftedCode: 0, BaseCode: base, IsRepeat: event.repeat
    },
    Data: ''
  });
  return true;
}

export function sendTextInput(sender: InputSender, paneID: number, text: string): boolean {
  if (!text) return false;
  const bytes = new TextEncoder().encode(text);
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  sender.send('MsgInput', { PaneID: paneID, Data: btoa(binary) });
  return true;
}
