import { DEFAULT_MACROS, type Macro } from './macros';
import { getTheme } from './themes';

export interface Preferences {
  theme: string;
  prefix: string;
  macros: Macro[];
  fontSize: number;
  fontFamily: string;
}

const PREF_KEYS: Record<keyof Preferences, { modern: string; legacy?: string }> = {
  theme: { modern: 'wideboi:theme', legacy: 'wideboi.theme' },
  prefix: { modern: 'wideboi:prefix', legacy: 'wideboi.prefix' },
  macros: { modern: 'wideboi:macros', legacy: 'wideboi.macros' },
  fontSize: { modern: 'wideboi:fontSize' },
  fontFamily: { modern: 'wideboi:fontFamily' },
};

function getStorage(): Storage | null {
  try {
    if (typeof localStorage !== 'undefined') {
      return localStorage;
    }
  } catch {
    // localStorage might be unavailable or throw SecurityError
  }
  return null;
}

function readRawKey(storage: Storage, keys: { modern: string; legacy?: string }): string | null {
  try {
    const modern = storage.getItem(keys.modern);
    if (modern !== null) return modern;
    if (keys.legacy) {
      return storage.getItem(keys.legacy);
    }
  } catch {
    // storage.getItem may throw on blocked storage
  }
  return null;
}

export function getPref<K extends keyof Preferences>(key: K): Preferences[K] {
  const storage = getStorage();
  if (!storage) return getDefaultPref(key);

  const raw = readRawKey(storage, PREF_KEYS[key]);

  switch (key) {
    case 'theme': {
      try {
        return getTheme(raw).id as Preferences[K];
      } catch {
        return 'dark' as Preferences[K];
      }
    }
    case 'prefix': {
      return (raw || 'ctrl+b') as Preferences[K];
    }
    case 'macros': {
      if (!raw) return DEFAULT_MACROS as Preferences[K];
      try {
        const parsed = JSON.parse(raw);
        if (Array.isArray(parsed) && parsed.length > 0) {
          return parsed as Preferences[K];
        }
      } catch {
        // invalid JSON
      }
      return DEFAULT_MACROS as Preferences[K];
    }
    case 'fontSize': {
      const parsed = parseInt(raw || '14', 10);
      return (isNaN(parsed) || parsed < 1 ? 14 : parsed) as Preferences[K];
    }
    case 'fontFamily': {
      return (raw || 'monospace') as Preferences[K];
    }
  }
}

export function setPref<K extends keyof Preferences>(key: K, value: Preferences[K]): void {
  const storage = getStorage();
  if (!storage) return;

  const keyConfig = PREF_KEYS[key];
  try {
    const serialized = key === 'macros' ? JSON.stringify(value) : String(value);
    storage.setItem(keyConfig.modern, serialized);
    if (keyConfig.legacy) {
      storage.setItem(keyConfig.legacy, serialized);
    }
  } catch {
    // ignore quota/disabled errors
  }
}

export function getDefaultPref<K extends keyof Preferences>(key: K): Preferences[K] {
  switch (key) {
    case 'theme':
      return 'dark' as Preferences[K];
    case 'prefix':
      return 'ctrl+b' as Preferences[K];
    case 'macros':
      return DEFAULT_MACROS as Preferences[K];
    case 'fontSize':
      return 14 as Preferences[K];
    case 'fontFamily':
      return 'monospace' as Preferences[K];
  }
}
