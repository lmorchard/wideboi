# Notes: #301 shrink the upgrade snapshot

## Baseline Measurements (2026-09-27, Apple M5 Max)

- BenchmarkRestoreStateHeavy: 18.65s (18,645,101,251 ns), 2,041 MB
- BenchmarkEncodeStateHeavy: 5.24s (5,239,161,958 ns), 2,041 MB

## Final Measurements (#301 Optimization, 2026-09-27, Apple M5 Max)

- BenchmarkRestoreStateHeavy: **570 ms** (570,558,125 ns), **0.23 MB (238 KB)**
  - Speedup: **32x faster**
  - Size reduction: **8,755x smaller (99.988% reduction)**
- BenchmarkEncodeStateHeavy: **50.5 ms** (50,466,084 ns), **0.23 MB (238 KB)**
  - Speedup: **104x faster** (reduces `s.mu` hold window from 5.2s down to 50ms)
- BenchmarkRestoreStateHeavyUncompressed: **518 ms**, **8.29 MB**


