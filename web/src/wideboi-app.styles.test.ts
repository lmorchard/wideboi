import { describe, expect, it } from 'vitest';
import { wideboiAppStyles } from './wideboi-app.styles';

describe('toolbar font styling', () => {
  it('applies sans-serif to toolbar and form controls, but monospace to tab selectors', () => {
    const css = wideboiAppStyles.cssText;
    expect(css).toMatch(/\.toolbar\s*\{[^}]*font-family:\s*-apple-system,\s*BlinkMacSystemFont/);
    expect(css).toMatch(/\.toolbar\s*\{[^}]*sans-serif/);
    expect(css).toMatch(/\.toolbar\s+button,\s*\.toolbar\s+select,\s*\.toolbar\s+input\s*\{[^}]*font-family:\s*inherit/);
    expect(css).toMatch(/\.pane-tab\s*\{[^}]*font-family:\s*monospace/);
    expect(css).toMatch(/\.toolbar\s+\.pane-tab\s*\{[^}]*font-family:\s*monospace/);
    expect(css).toMatch(/\.status\.search-bar\s+button\.search-btn\s*\{[^}]*font-family:\s*-apple-system/);
  });
});
