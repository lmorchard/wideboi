import { describe, expect, it } from 'vitest';
import { consumeLinkToken } from './token';

describe('consumeLinkToken', () => {
  it.each([
    ['fragment', 'http://localhost:8080/?view=wide#token=secret123', 'http://localhost:8080/?view=wide'],
    ['legacy query', 'http://localhost:8080/?view=wide&token=secret123#pane', 'http://localhost:8080/?view=wide#pane'],
  ])('removes a %s credential from browser history', (_name, href, cleanHref) => {
    const location = { href } as Location;
    const state = { existing: true };
    const calls: unknown[][] = [];
    const history = {
      state,
      replaceState: (...args: unknown[]) => calls.push(args),
    } as unknown as History;

    expect(consumeLinkToken(location, history)).toBe('secret123');
    expect(calls).toEqual([[state, '', cleanHref]]);
    expect(JSON.stringify(calls)).not.toContain('secret123');
  });
});
