# Initial presentation benchmark baseline

Five sequential 200 ms samples per case; 80 cases, 400 samples. Recorded on
macOS/arm64, Apple M5 Max, Go 1.27.2, after the full CI gate finished. No other
agent-run tests or benchmarks ran concurrently. This is a discard-sink measurement,
not a real-terminal comparison or visual acceptance.

Production revision: `606f102739c57a14187c1a06f6ad30d15ad67d67`, with the new benchmark
suite in the working tree. `metadata.json` records the exact dirty state and source
hashes, platform, toolchain, command, and UTC start time. The directory date is the
local date. These compare policies within one revision; they
are not a reconstructed before/after measurement of the Ghostty feature commit.

## Selected results: 640×320, zlib, opaque chart fixture

Times and byte counts below are medians of five samples. Times include synchronous
command execution and changed grid output to a discard writer.

| Workload | Policy | ms/frame | APC bytes/frame | Allocated bytes/frame |
| --- | --- | ---: | ---: | ---: |
| unchanged | fresh | 1.512 | 163,583 | 2,661,656 |
| unchanged | stable | 0.071 | 0 | 839,688 |
| localized | fresh | 1.527 | 163,682 | 2,661,686 |
| localized | stable | 0.107 | 194 | 1,656,932 |
| scattered | fresh | 1.526 | 163,591 | 2,662,052 |
| scattered | stable | 1.546 | 163,715 | 3,469,882 |
| full | fresh | 1.558 | 163,711 | 2,662,189 |
| full | stable | 1.862 | 163,835 | 3,583,044 |
| warm playback | uncached | 2.537 | 163,835 | 4,401,467 |
| warm playback | cache | 0.496 | 26 | 840,832 |

Localized edits and unchanged frames substantially reduce work in this fixture.
Scattered corner edits have a full-canvas bounding rectangle and do not reduce
payload size. Full-canvas StableID updates are about 20% slower than fresh IDs here:
raster retention and comparison add work, despite avoiding grid replacement and
terminal placement churn. Warm cached playback is about 5.1× faster than uncached
playback here, but source seek/composition still consumes time and allocations.
These ratios are descriptive sample medians, not universal speedups or significance
claims. Fresh-ID APC totals include image deletion; grid bytes are reported
separately in the raw output.

The complete matrix, including PNG, RGBA, glyphs, 320×160, cold three-frame passes,
allocations and grid bytes, is in [bench.txt](bench.txt). A cold-pass operation
contains three frames; divide ns/op and allocation metrics by three before comparing
with warm-frame operations. Raw output remains compatible with benchstat.

## Validation and reproduction

- `GOTOOLCHAIN=go1.27.2 task ci` passed, including race, no-CGO, WASM, lint,
  native/Windows builds and Node lifecycle smoke checks.
- Both the capture script and discard CLI were smoke-tested. CLI tests verify
  capability declarations, no-clobber reports, cache-control samples, and interrupted
  report labeling.
- `task notices` regenerated identical notice content after promoting the existing
  x/term dependency to a direct import; no dependency version changed.
- No real Ghostty, Kitty, or iTerm2 display measurements were performed.

From the repository root, using a new output directory:

```sh
GOTOOLCHAIN=go1.27.2 task bench-record -- --out dist/benchmarks/repeat --count 5 --benchtime 200ms
```

Use the same fixture/harness source hashes and environment for a matched comparison.
See [BENCHMARKS.md](../../../../BENCHMARKS.md) for definitions and terminal-run steps.
