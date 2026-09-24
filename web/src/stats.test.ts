import { describe, expect, it } from 'vitest';
import { RenderStats, formatSummary, percentile, statsEnabled } from './stats';

describe('statsEnabled', () => {
  it.each([
    ['?stats=1', true],
    ['?view=wide&stats=1', true],
    ['', false],
    ['?stats=0', false],
    ['?stats=true', false],
  ])('%s -> %s', (search, want) => {
    expect(statsEnabled(search)).toBe(want);
  });
});

describe('percentile (nearest-rank)', () => {
  it('returns the ceil(p/100 * n)-th smallest sample', () => {
    const samples = [5, 1, 4, 2, 3, 6, 7, 8, 9, 10];
    expect(percentile(samples, 50)).toBe(5);   // rank 5
    expect(percentile(samples, 95)).toBe(10);  // rank ceil(9.5) = 10
    expect(percentile(samples, 90)).toBe(9);   // rank 9
    expect(percentile(samples, 100)).toBe(10);
    expect(percentile(samples, 0)).toBe(1);    // rank clamps to 1
  });

  it('is zero with no samples', () => {
    expect(percentile([], 50)).toBe(0);
  });
});

describe('RenderStats', () => {
  it('summarises known samples over the elapsed window', () => {
    const stats = new RenderStats();
    for (const bytes of [100, 200, 300, 400]) stats.recordMessage(bytes);
    stats.recordApply('full', 4);
    stats.recordApply('patch', 1);
    stats.recordApply('patch', 2);
    stats.recordApply('resync', 3);
    for (let ms = 1; ms <= 20; ms++) stats.recordDraw(ms);

    const s = stats.summary(2000);
    expect(s.elapsedMs).toBe(2000);
    expect(s.messages).toBe(4);
    expect(s.bytes).toBe(1000);
    expect(s.messagesPerSec).toBe(2);
    expect(s.bytesPerSec).toBe(500);
    expect(s.bytesPerMessage).toBe(250);
    expect([s.fulls, s.patches, s.resyncs, s.draws]).toEqual([1, 2, 1, 20]);
    expect(s.fullsPerSec).toBe(0.5);
    expect(s.patchesPerSec).toBe(1);
    expect(s.resyncsPerSec).toBe(0.5);
    expect(s.drawsPerSec).toBe(10);

    expect(s.apply.full).toEqual({ count: 1, avg: 4, p50: 4, p95: 4, max: 4 });
    expect(s.apply.patch).toEqual({ count: 2, avg: 1.5, p50: 1, p95: 2, max: 2 });
    expect(s.apply.resync).toEqual({ count: 1, avg: 3, p50: 3, p95: 3, max: 3 });
    expect(s.draw).toEqual({ count: 20, avg: 10.5, p50: 10, p95: 19, max: 20 });
  });

  it('reports each apply kind unblended by the others', () => {
    // Fulls are a cheap Map.set; patches do the real work. A shared window
    // would drag the patch p50/avg down towards zero.
    const stats = new RenderStats();
    for (let i = 0; i < 9; i++) stats.recordApply('full', 0);
    stats.recordApply('patch', 5);
    stats.recordApply('patch', 7);

    const s = stats.summary(1000);
    expect(s.apply.patch).toEqual({ count: 2, avg: 6, p50: 5, p95: 7, max: 7 });
    expect(s.apply.full).toEqual({ count: 9, avg: 0, p50: 0, p95: 0, max: 0 });
    expect(s.apply.resync.count).toBe(0);
  });

  it('records a decode sample per message in its own window', () => {
    const stats = new RenderStats();
    stats.recordDecode(2);
    stats.recordDecode(4);
    stats.recordApply('patch', 100);

    const s = stats.summary(1000);
    expect(s.decode).toEqual({ count: 2, avg: 3, p50: 2, p95: 4, max: 4 });
  });

  it('summarises a window too large to spread into Math.max', () => {
    // Spreading this many arguments throws RangeError; the exact limit
    // depends on the stack size, so the window is well past it.
    const stats = new RenderStats();
    for (let i = 0; i < 2_000_000; i++) stats.recordDraw(i % 1000);
    expect(stats.summary(5000).draw.max).toBe(999);
  });

  it('reports zeros rather than NaN for an empty window', () => {
    const s = new RenderStats().summary(0);
    const empty = { count: 0, avg: 0, p50: 0, p95: 0, max: 0 };
    expect(s.messagesPerSec).toBe(0);
    expect(s.bytesPerMessage).toBe(0);
    expect(s.apply).toEqual({ full: empty, patch: empty, resync: empty });
    expect(s.decode).toEqual(empty);
  });

  it('reset clears counters and timing windows', () => {
    const stats = new RenderStats();
    stats.recordMessage(50);
    stats.recordApply('full', 9);
    stats.recordApply('patch', 9);
    stats.recordApply('resync', 9);
    stats.recordDecode(9);
    stats.recordDraw(9);
    stats.reset();
    stats.recordDraw(1);

    const s = stats.summary(1000);
    expect([s.messages, s.bytes, s.fulls, s.patches, s.resyncs, s.draws]).toEqual([0, 0, 0, 0, 0, 1]);
    expect([s.apply.full.count, s.apply.patch.count, s.apply.resync.count, s.decode.count]).toEqual([0, 0, 0, 0]);
    expect(s.draw).toEqual({ count: 1, avg: 1, p50: 1, p95: 1, max: 1 });
  });

  it('formats counts and timings on one line', () => {
    const stats = new RenderStats();
    stats.recordMessage(1024);
    stats.recordDecode(0.5);
    stats.recordApply('full', 0.125);
    stats.recordApply('patch', 0.25);
    stats.recordApply('resync', 1.5);
    stats.recordDraw(3);
    const line = formatSummary(stats.summary(1000));
    expect(line).toContain('msg 1.0/s');
    expect(line).toContain('1.0 KiB/s');
    expect(line).toContain('patch 1.0/s');
    expect(line).toContain('decode avg 0.50');
    expect(line).toContain('apply full avg 0.13');
    expect(line).toContain('apply patch avg 0.25');
    expect(line).toContain('apply resync avg 1.50');
    expect(line).toContain('draw avg 3.00');
  });
});
