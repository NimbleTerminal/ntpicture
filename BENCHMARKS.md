# Rendering and terminal benchmarks

This suite separates application work from terminal display. It uses a versioned,
deterministic three-frame fixture shared by Go benchmarks and the terminal runner.
It does not measure visible FPS, GPU rendering time, or display acknowledgement.

## Repeatable Go benchmarks

Use Go 1.27.2 and run samples sequentially on an otherwise idle machine:

```sh
GOTOOLCHAIN=go1.27.2 task bench-record -- --out dist/benchmarks/baseline --count 5 --benchtime 200ms
# After a change, use a different output directory:
GOTOOLCHAIN=go1.27.2 task bench-record -- --out dist/benchmarks/candidate --count 5 --benchtime 200ms
```

Each directory contains `bench.txt` (standard Go benchmark output), `stderr.txt`,
and `metadata.json` (revision, dirty status, source hashes, platform, toolchain,
command, and exit code). Existing directories are never overwritten. Compare the
raw samples with `benchstat`; retain both records. Keep the benchmark code and
fixture hashes identical between revisions when attributing changes to production
code. Do not compare timings from overlapping runs, race-enabled builds, different
machines, or different Go versions as if they were a matched pair.

For a quick individual run:

```sh
GOTOOLCHAIN=go1.27.2 task bench-render -- -benchtime=100ms -count=3
```

The suite includes these workloads at source sizes 320×160 and 640×320:

| Pattern | Change between frames |
| --- | --- |
| unchanged | Identical pixels in independently owned images |
| localized | One centered 10×10 rectangle |
| scattered | Four corner pixels; their bounding box covers the full raster |
| full | Every pixel changes |

`BenchmarkPresentation` compares fresh IDs and StableID updates for PNG, RGBA,
and zlib. It also measures glyph rendering, which downsamples the source to
`cols × 2*rows`. Kitty raster sizes match the source exactly. Inputs are opaque
chart-like gradients and lines, not representative of all images or compression
ratios. Scaling, translucent images, and shared-memory performance have separate
existing benchmarks; this suite currently uses direct transmission only.

`BenchmarkPlaybackCache` compares enabled and disabled caches using the same
three-frame `animation.Player`:

- `cold-pass`: resets the source and presents all three positions. One operation
  is **three frames**, including cache invalidation and initial upload.
- `warm-frame`: primes all three positions before timing, then repeatedly seeks
  through them. One operation is **one frame**. Source seeking/composition is
  included even when the terminal frame is already cached.

Fixture generation and clip construction are outside timing. Public API calls,
command execution, pixel preparation/comparison, encoding, allocation, output
instrumentation, and changed View grids are inside timing. Commands execute
sequentially through a small test driver; this excludes the Bubble Tea scheduler
and renderer. The output writer discards bytes. Reported `ns/op`, `B/op`, and
`allocs/op` are per operation; `APC-bytes/frame` and `grid-bytes/frame` are per
frame. APC bytes include image deletion and tmux wrapping when enabled. The Go
suite runs without tmux; its explicit session ignores terminal environment hints.

## Terminal runs

Build once and run the same binary inside each terminal being compared:

```sh
GOTOOLCHAIN=go1.27.2 task build-bench
bin/ntpicture-bench -sink terminal -terminal Ghostty -terminal-version YOUR_VERSION \
  -kitty-support frames -mode kitty -policy stable -format zlib \
  -pattern localized -cols 40 -rows 10 -cell-width 8 -cell-height 16 \
  -frames 300 -interval 16.666667ms -out dist/benchmarks/ghostty-localized.json
```

Supply the actual terminal version and measured cell pixel dimensions. The example
8×16 geometry is a placeholder. The runner verifies that stdout is a terminal and
that the window has at least one spare row and column. Keep the window, font,
zoom, display scale, terminal configuration, and refresh rate fixed during a run.
Do not resize while measuring. Terminal dimensions and declared cell geometry are
recorded separately. The runner uses an alternate screen and removes its own
images on completion or Ctrl+C; it never deletes all terminal images globally.

Capability declarations are explicit, **not auto-detected**:

| `-kitty-support` | Allowed Kitty policies |
| --- | --- |
| `none` | Use `-mode glyph -policy fresh` |
| `static` | `-mode kitty -policy fresh` |
| `frames` | `fresh`, `stable`, `cache`, `uncached` |

Use the declaration appropriate for the installed terminal version and its
configuration. Do not infer support just from names such as Ghostty, Kitty, or
iTerm2. This harness does not implement iTerm2's separate inline-image protocol.
A glyph run must be reported as glyph rendering, not compared as a Kitty cache
result. `-policy cache` uses a 32 MiB cap; `cache_eligible` records whether the
three prepared frames fit. Keep unsupported cases marked N/A.

Use `-pattern full -policy cache` and `-policy uncached` for the playback pair.
The default three warmup frames populate the cache before samples begin. Set
`-warmup 0 -frames 9` to inspect first-pass uploads followed by cached passes.
Use `-interval 0` for saturation/backpressure experiments; the default pacing is
approximately 60 requested frame starts per second, not a claim of 60 visible FPS.
Tmux wrapping defaults on inside tmux and can be explicitly controlled with
`-tmux=true` or `-tmux=false`; compare direct and tmux runs separately.

A JSON report includes every frame's command-completion time, synchronous write
time, APC bytes, grid bytes, completion-time p50/p95/p99, wall duration including
pacing, warmup totals, actual raster geometry, capability declaration, terminal
labels, window size, toolchain, build revision, dirty flag, and selected environment
hints. Build a clean committed revision for shareable terminal results. Error exit
statuses and incomplete/empty files are not successful measurements. Interrupted
runs are labeled and contain only completed samples.

For an automated smoke run without terminal output:

```sh
GOTOOLCHAIN=go1.27.2 task bench-terminal -- -sink discard -mode kitty -policy cache -pattern full -frames 9 -interval 0 -out dist/benchmarks/smoke.json
```

Discard runs do not require a terminal capability declaration and are explicitly
labeled. They cannot validate terminal behavior.

## Display measurements and acceptance

A successful write only establishes that the OS accepted bytes. A terminal can
buffer them, render later, skip intermediate frames, or reject unsupported commands
quietly. `PresentedMsg` is also not a display acknowledgement. The runner uses its
own synchronous output driver, not the Bubble Tea renderer, so its times should not
be presented as application-wide Bubble Tea performance either.

To compare visible smoothness, flicker, or latency, accompany the JSON with a
screen/high-speed-camera recording or other independently calibrated display
measurement. Record the method, capture rate, display refresh rate, OS, terminal
version, font/cell geometry, revision, policy, and report filename. Inspect first
upload, repeated localized/full updates, and warm cache selection separately.
Record actual visual checks in DEVELOP.md acceptance records. The JSON deliberately
leaves visual verification unrecorded; no real-terminal visual acceptance was
performed merely by building or smoke-testing this harness.

Eviction behavior (`N=1`) and terminal GPU/memory usage need separate controlled
memory-pressure experiments. The current runner does not claim to measure them.

## Initial baseline

The [2026-10-27 baseline](testdata/benchmarks/presentation/2026-10-27/README.md)
archives 400 samples across 80 cases, with source hashes and selected medians.
It compares policies within the current revision; it is not a terminal display
measurement or a before/after reconstruction of an earlier commit.
