import type { WideboiClient } from './client';
import { sendKeyboardInput, sendTextInput } from './input';
import type { Macro as WireMacro, MacroStep as WireMacroStep } from './gen/internal/protocol/wirepb/wideboi_pb';

export interface MacroStep {
  text?: string;
  key?: string;
  code?: string;
  ctrl?: boolean;
  alt?: boolean;
  shift?: boolean;
}

export interface Macro {
  name: string;
  steps: MacroStep[];
}

export const DEFAULT_MACROS: Macro[] = [
  {
    name: 'History Search',
    steps: [{ key: 'r', code: 'KeyR', ctrl: true }],
  },
  {
    name: 'Git Status',
    steps: [
      { text: 'git status' },
      { key: 'Enter', code: 'Enter' },
    ],
  },
  {
    name: 'Interrupt',
    steps: [{ key: 'c', code: 'KeyC', ctrl: true }],
  },
  {
    name: 'Clear',
    steps: [{ key: 'l', code: 'KeyL', ctrl: true }],
  },
];

export function executeMacro(client: WideboiClient, paneId: number, macro: Macro): boolean {
  if (!client || !paneId || !macro.steps || macro.steps.length === 0) return false;
  for (const step of macro.steps) {
    if (step.text) {
      sendTextInput(client, paneId, step.text);
    } else if (step.key) {
      const code = step.code || (step.key.length === 1 ? `Key${step.key.toUpperCase()}` : step.key);
      const event = (typeof KeyboardEvent !== 'undefined'
        ? new KeyboardEvent('keydown', {
            key: step.key,
            code,
            ctrlKey: !!step.ctrl,
            altKey: !!step.alt,
            shiftKey: !!step.shift,
          })
        : ({
            key: step.key,
            code,
            ctrlKey: !!step.ctrl,
            altKey: !!step.alt,
            shiftKey: !!step.shift,
            metaKey: false,
            isComposing: false,
            repeat: false,
          } as unknown as KeyboardEvent));
      sendKeyboardInput(client, paneId, event);
    }
  }
  return true;
}

export function macroEndsWithEnter(macro: Macro): boolean {
  if (!macro.steps || macro.steps.length === 0) return false;
  const lastStep = macro.steps[macro.steps.length - 1];
  return lastStep.key === 'Enter';
}

export function wireToLocalMacro(m: WireMacro): Macro {
  return {
    name: m.name,
    steps: (m.steps ?? []).map((s: WireMacroStep) => ({
      text: s.text || undefined,
      key: s.key || undefined,
      code: s.code || undefined,
      ctrl: s.ctrl || undefined,
      alt: s.alt || undefined,
      shift: s.shift || undefined,
    })),
  };
}

export function localToWireMacro(m: Macro): { name: string; steps: { text: string; key: string; code: string; ctrl: boolean; alt: boolean; shift: boolean }[] } {
  return {
    name: m.name,
    steps: (m.steps ?? []).map(s => ({
      text: s.text || '',
      key: s.key || '',
      code: s.code || '',
      ctrl: !!s.ctrl,
      alt: !!s.alt,
      shift: !!s.shift,
    })),
  };
}

