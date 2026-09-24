// Opt-in browser render stats for `?stats=1` (#179). This records counts,
// byte sizes and timings only — never message contents, titles, or input.

export type ApplyKind = 'full' | 'patch' | 'resync';

export interface TimingSummary {
  count: number;
  avg: number;
  p50: number;
  p95: number;
  max: number;
}

export interface StatsSummary {
  elapsedMs: number;
  messages: number;
  bytes: number;
  fulls: number;
  patches: number;
  resyncs: number;
  draws: number;
  messagesPerSec: number;
  bytesPerSec: number;
  bytesPerMessage: number;
  fullsPerSec: number;
  patchesPerSec: number;
  resyncsPerSec: number;
  drawsPerSec: number;
  // Protobuf decode per message, which apply does not include.
  decode: TimingSummary;
  // One window per kind: a full is a cheap Map.set, so blending it with
  // patches would drag the patch figures towards zero.
  apply: Record<ApplyKind, TimingSummary>;
  draw: TimingSummary;
}

export function statsEnabled(search: string): boolean {
  return new URLSearchParams(search).get('stats') === '1';
}

// Nearest-rank percentile: the ceil(p/100 * n)-th smallest sample, with the
// rank clamped to [1, n]. It always returns an observed sample, never an
// interpolated one. An empty window is 0.
export function percentile(samples: number[], p: number): number {
  if (samples.length === 0) return 0;
  const sorted = [...samples].sort((a, b) => a - b);
  const rank = Math.min(Math.max(Math.ceil((p / 100) * sorted.length), 1), sorted.length);
  return sorted[rank - 1];
}

function summarize(samples: number[]): TimingSummary {
  if (samples.length === 0) return { count: 0, avg: 0, p50: 0, p95: 0, max: 0 };
  const total = samples.reduce((sum, v) => sum + v, 0);
  const sorted = [...samples].sort((a, b) => a - b);
  return {
    count: samples.length,
    avg: total / samples.length,
    p50: percentile(sorted, 50),
    p95: percentile(sorted, 95),
    // Not Math.max(...samples): spreading a large window throws RangeError.
    max: sorted[sorted.length - 1],
  };
}

export class RenderStats {
  messages = 0;
  bytes = 0;
  fulls = 0;
  patches = 0;
  resyncs = 0;
  draws = 0;
  private decodeMs: number[] = [];
  private applyMs: Record<ApplyKind, number[]> = { full: [], patch: [], resync: [] };
  private drawMs: number[] = [];

  recordMessage(bytes: number) {
    this.messages++;
    this.bytes += bytes;
  }

  recordDecode(ms: number) {
    this.decodeMs.push(ms);
  }

  // A patch the renderer rejected is recorded as 'resync', not 'patch'.
  recordApply(kind: ApplyKind, ms: number) {
    if (kind === 'full') this.fulls++;
    else if (kind === 'patch') this.patches++;
    else this.resyncs++;
    this.applyMs[kind].push(ms);
  }

  recordDraw(ms: number) {
    this.draws++;
    this.drawMs.push(ms);
  }

  summary(elapsedMs: number): StatsSummary {
    const perSec = (n: number) => (elapsedMs > 0 ? (n * 1000) / elapsedMs : 0);
    return {
      elapsedMs,
      messages: this.messages,
      bytes: this.bytes,
      fulls: this.fulls,
      patches: this.patches,
      resyncs: this.resyncs,
      draws: this.draws,
      messagesPerSec: perSec(this.messages),
      bytesPerSec: perSec(this.bytes),
      bytesPerMessage: this.messages > 0 ? this.bytes / this.messages : 0,
      fullsPerSec: perSec(this.fulls),
      patchesPerSec: perSec(this.patches),
      resyncsPerSec: perSec(this.resyncs),
      drawsPerSec: perSec(this.draws),
      decode: summarize(this.decodeMs),
      apply: {
        full: summarize(this.applyMs.full),
        patch: summarize(this.applyMs.patch),
        resync: summarize(this.applyMs.resync),
      },
      draw: summarize(this.drawMs),
    };
  }

  // Called after each report so windows do not overlap.
  reset() {
    this.messages = 0;
    this.bytes = 0;
    this.fulls = 0;
    this.patches = 0;
    this.resyncs = 0;
    this.draws = 0;
    this.decodeMs = [];
    this.applyMs = { full: [], patch: [], resync: [] };
    this.drawMs = [];
  }
}

function timing(label: string, t: TimingSummary): string {
  const ms = (v: number) => v.toFixed(2);
  return `${label} avg ${ms(t.avg)} p50 ${ms(t.p50)} p95 ${ms(t.p95)} max ${ms(t.max)} ms (n=${t.count})`;
}

export function formatSummary(s: StatsSummary): string {
  const rate = (v: number) => v.toFixed(1);
  return [
    `msg ${rate(s.messagesPerSec)}/s ${(s.bytesPerSec / 1024).toFixed(1)} KiB/s ${Math.round(s.bytesPerMessage)} B/msg`,
    `full ${rate(s.fullsPerSec)}/s patch ${rate(s.patchesPerSec)}/s resync ${rate(s.resyncsPerSec)}/s`,
    timing('decode', s.decode),
    timing('apply full', s.apply.full),
    timing('apply patch', s.apply.patch),
    timing('apply resync', s.apply.resync),
    `draws ${rate(s.drawsPerSec)}/s ${timing('draw', s.draw)}`,
  ].join(' | ');
}
