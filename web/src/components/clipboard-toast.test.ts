import { describe, expect, it } from 'vitest';
import { clipboardPreview, PREVIEW_LINES } from './clipboard-toast';

describe('clipboardPreview', () => {
  it('shows short text whole', () => {
    expect(clipboardPreview('one line')).toBe('one line');
    expect(clipboardPreview('a\nb\nc')).toBe('a\nb\nc');
  });

  it('cuts after PREVIEW_LINES lines and marks the cut', () => {
    expect(PREVIEW_LINES).toBe(3);
    expect(clipboardPreview('a\nb\nc\nd\ne')).toBe('a\nb\nc\n…');
  });
});
