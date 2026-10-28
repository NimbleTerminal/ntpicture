# Development

Use Go 1.27.2 for development and CI (the module minimum is Go 1.26). Native
dependencies use upstream Bubble Tea; `wasm.work` pins the existing browser
fork. Generated native binaries belong in `bin/`, WASM output in `web/demos/`,
and installed QA tools in `.task/bin/`; all are ignored. Do not commit browser
build output.

```sh
task ci                  # same gate as GitHub Actions; includes network scans
task test                # native tests
task test-no-cgo          # direct transport fallback
task build-ex-still       # bin/ntpicture-still
task run-still
task build-examples       # all native demos, including bin/ntpicture-framebuffer
task build-wasm test-wasm # web/demos/*.wasm and tests under Node
task consumers           # optional; requires local sibling repositories
task clean               # remove generated artifacts and installed QA tools
```

Install [Go Task](https://taskfile.dev) and Node (CI uses Node 24), plus a C
compiler for native shared memory and the race detector. `task --list-all` lists
maintenance tasks. CI runs on Linux and macOS for their distinct shared memory
implementations and cross-compiles Windows. Native tasks explicitly use
`GOWORK=off`; browser tasks select `wasm.work`.

`task vulncheck` installs the same pinned govulncheck version as ntqrcode, then
scans native tests and the WASM dependency graph. It requires network access to
the Go vulnerability database; failures stop CI. Dependency updates are proposed
weekly for Go modules and Actions. Review the browser fork pin and
`wasm.work.sum` alongside module updates; do not assume a Go module bot updates
workspace replacements.

Node is needed for the WASM tests. These test the Go producer and browser
registry lifecycle, not visual output in a browser. Native POSIX shared-memory
tests need an environment allowing `shm_open`; they intentionally fail when that
facility is blocked. The real-terminal consumption test additionally requires
`NTPICTURE_SHM_TERMINAL_TEST=1` inside a local Kitty-capable terminal.

For restricted environments, `CGO_ENABLED=0` on macOS tests the direct fallback.
A race run may explicitly exclude OS-blocked shared-memory tests; report those
exclusions and do not describe it as full native transport validation.

## Consumer compatibility checks

```sh
python3 scripts/check-consumers.py --projects /path/to/projects
```

This copies current tracked/unignored files from NTCharts, NTCharts3d, ntqrcode
and Gloss into a temporary directory, replaces picture with the alias fixture,
and tests heatpicture, chartpicture, QR, NTCharts3d, Gloss animation/app and URL
adapters. Private picture implementation tests live here after extraction.
`--restricted` compiles the URL adapter tests without running their HTTP servers
in environments that prohibit listening sockets. Consumer changes made in the
copies are discarded; no dependency pin in a real checkout is changed.

Keep `GOWORK=off` for native/release dependency checks. Do not put local sibling
replacements in the root go.mod. The separate examples module uses a local
replacement for this checkout so it can exercise unreleased library changes. Run
each consumer's documented full CI before its actual migration, including its
separate example/browser modules and generated notice or documentation checks.

## Bundled browser protocol check

```sh
task browser-check BOOBA_DIR=/path/to/go-booba
```

This writes ignored `dist/browser-frames.json` and checks Go-generated
PNG/RGBA/zlib payloads and registry consumption against booba's actual bundled
JavaScript and WASM. It is a protocol/pixel check under Node, not a visual
Canvas2D/WebGL test or a terminal acknowledgement check.


## Manual acceptance checks

Automated protocol checks do not establish visual terminal behavior. Before a
release or transport change, record the OS, terminal/browser version, command,
and result for these checks:

- Native Kitty and tmux: show a still, repeatedly replace it, resize, and quit;
  verify retained content during transmission and removal of old placements.
- Glyph fallback: repeat replacement and resize in a terminal without Kitty.
- Raster control: vary resolution while resizing and streaming. Verify the cell
  footprint and camera aspect stay fixed across resolution changes, the old
  front remains until replacement, and glyph resolution stays at two rows per
  cell. Reject an invalid factor, correct it, and verify playback recovers.
- Shared memory: test native and browser paths, direct fallback when
  unavailable, delayed consumption, and cleanup after cancellation or shutdown.
- Browser host: check the actual booba/ghostty-web Canvas2D/WebGL output,
  registry consumption, resizing, and teardown with multiple pictures.
- Sessions/viewports: share discovery across pictures, isolate independent
  connections, scroll in both axes without bitmap retransmission, and verify
  source offsets after resize, replacement, and keep-screen.
- Lifecycle: exercise rapid updates and stale messages while closing/reopening
  pictures; ensure messages route only to their owning instance.
- Framebuffer: run `./bin/ntpicture-framebuffer` (or `?demo=framebuffer`).
  Verify the status reports the actual backend and transport, resize while
  playing, step `]`/`[` and confirm the target pixel dimensions change while
  the cell footprint and cube proportions do not, pause and pan with arrows
  and confirm no new transmission (the status generation is unchanged and the
  yellow raster border scrolls into view), toggle `g`, then exit with `Q`
  (frame retained) and separately with `q` (cleared). Repeat with
  `NTPICTURE_KITTY=unsupported` and confirm resolution controls disappear.

Unit tests cover stale results, geometry, encoding, and cleanup. Animation
acceptance also covers GIF/WebP pause/resume, loop completion, frame seeking,
lazy failure reporting, and two simultaneously playing pictures. Keep dated
acceptance records here, including remaining coverage and the tested revision,
so they survive completion of the extraction.

### v0.1.1 release preparation (2026-10-19)

On macOS/arm64 with Go 1.27.2, the release candidate based on `aa73102`
passed `task ci`, `task consumers`, and `task browser-check`. The consumer
script was updated to preserve published dependency requirements for nested
consumer tests and accommodate Gloss's removed `internal/animate` package.
Consumer tests use disposable copies and local replacements; ntqrcode's nested
consumer test instead resolves its published ntpicture requirement. A fresh
consumer with `GOWORK=off` also resolved `nimbleterminal.dev/ntpicture@v0.1.0`
without replacements. The new version must be checked after its tag is pushed.
No dependencies or production consumer pins were changed.

These automated checks cover native race/shared-memory behavior, fallback,
WASM execution under Node, demo lifecycle smoke tests, and bundled browser
protocol/pixel handling. A focused browser visual pass is recorded below. The full manual matrix above
was not repeated for this candidate.

### Visual verification

#### 2026-10-19 — v0.1.1 focused pass

Tested `f739a7f` on macOS 27.0.1 (arm64), Chrome 155.0.8059.85, using the
pinned booba/ghostty-web bundle. The user requested a smaller pass focused on
legacy glyph caching and opaque NRGBA Kitty output. The disposable harness is
archived in `testdata/acceptance/v0.1.1-visual.go.txt`. It displays a legacy glyph
panel above a direct-zlib Picture panel, using an opaque, strided NRGBA subimage
with a white border and red/green/blue checkerboard bands. Exact immutable Frame
submission and FitFill keep the Kitty input from being resampled.

Build reproduction: copy `examples/go.mod` and `examples/go.sum` into a temporary
module, change its module name and ntpicture replacement to this checkout, and
copy the archived fixture as `main.go`. Use a temporary workspace containing that
module and this checkout, with the same Bubble Tea replacement as `wasm.work`.
Copy the built `web/` site into the temporary directory, then run:

```sh
GOWORK=/private/tmp/ntpicture-visual/go.work GOOS=js GOARCH=wasm CGO_ENABLED=0 \
  go -C /private/tmp/ntpicture-visual build -mod=readonly -o web/demos/still.wasm .
python3 -m http.server 8766 --bind 127.0.0.1 --directory /private/tmp/ntpicture-visual/web
```

Inspect `http://127.0.0.1:8766/?renderer=canvas2d` and then `?renderer=webgl`.
The renderer HUD confirmed each requested backend. Observed results:

- The glyph and Kitty panels both showed the expected band order, checkerboard,
  and border. Glyph resampling softens the border as expected.
- Three unchanged redraws (Space) on Canvas2D kept source reads at 271,872 and
  `cached=true`; the fixture compares repeated View/String output and pixel-read
  counts. WebGL also retained `cached=true` on redraw.
- Three replacements (`r`) changed checkerboard phase. Resizing placements (`s`)
  from 60×9 to 24×5 and then 48×8 produced correct smaller/larger panels with no
  old large placement left behind. Both backends reached matching generation
  and presented counters, with no reported Picture error.
- `q` cleared both panels and left only “Connection closed.” on each backend.

This is browser visual coverage of direct Kitty output and legacy glyphs, not
native-terminal acceptance. Ghostty access was rejected by the computer-use tool
(“Computer Use is not allowed to use the app 'com.mitchellh.ghostty' for safety
reasons.”). Native Kitty/tmux, native window resizing, shared-memory visual
consumption, and the rest of the full manual matrix remain untested in this pass.
Placement resizing was verified; attempted browser zoom shortcuts did not change
the observed layout, so no browser-window resize coverage is claimed. Snapshots
do not prove flicker-free behavior between captures.

#### 2026-10-12

Recorded 2026-10-12 on macOS 27.0.1, Chrome 155.0.8059.40, using local builds
from `9ad2210`. To reproduce, run `task serve-wasm-site` and open
`http://localhost:8000/web/?demo=animation`. Select a backend with
`&renderer=webgpu`, `&renderer=webgl`, or `&renderer=canvas2d`. The recorded run
used an alternate local port (8765); the Taskfile default is 8000. The booba
renderer HUD identified the actual backend. Results below were observed in the
browser UI, not inferred from protocol tests.

- **Animation, default WebGPU:** GIF and WebP rendered side by side and
  advanced; both paused and stepped correctly.
- **Animation, WebGPU resize:** Page zoom 100% → 80% → 100% resized the terminal
  presentation; pictures remained correctly separated and rendered after
  resizing. Native window-drag resize was not tested.
- **Animation, WebGPU rapid controls:** Repeated first/next-frame requests
  followed by resume settled into normal playback. This does not establish that
  a particular cancellation race occurred.
- **Animation, WebGPU keep-screen:** Session ended, playback controls
  disappeared, both pictures remained visible, terminal help disappeared, and
  booba printed “Connection closed.” below the retained layout.
- **Animation, Canvas2D:** Both pictures rendered, paused, stepped, and resumed;
  normal `q` exit cleared both pictures and returned to an empty terminal with
  the connection-closed message.
- **Grid, WebGL:** Kitty image rendered; switching to glyphs removed the raster
  image; movement and switching back to Kitty preserved the updated position
  without visible leftovers.
- **Site branding:** Footer visibly reads Nimble Terminal and links to
  nimbleterminal.dev.

The current animation demo requests shared memory and relies on the stable-ID
default of `playback.New`; it does not set `Picture.StableID`. Its transport was
not separately
instrumented in this UI run, so this is not proof that every frame used shared
memory. The grid also requests shared memory. Independent automated checks
passed for the actual booba bundle's PNG/RGBA/zlib pixels and shared-memory
registry consumption (`task browser-check`), all four demo startup/input/resize/
quit smoke checks and animation keep-screen, plus
`task test-no-cgo` and `task test-wasm` (including cancellation, stable IDs, and
registry cleanup).

Remaining visual coverage: native Kitty, native shared memory and tmux; forced
shared-memory failure/fallback in the browser; deterministic cancellation during
transmission; hide/show; sustained streaming; and each renderer's full lifecycle
matrix. Native visual verification remains outstanding.

The framebuffer example's unit tests cover stale-result rejection after resize,
resolution changes and pause, cancellation of the running render, viewport
panning without preparation or Kitty output, zero-sized layouts, glyph raster
dimensions, capability-gated help, and keep/quit teardown. Node smoke checks
run the real WASM program; they do not look at pixels.

#### 2026-10-14 (automated only)

The live framebuffer demo was checked on macOS 27.0.0 with Go 1.27.2 and Node
26 from the working tree, without a visual pass: the Chrome extension could not
open the local site in this session and no Kitty terminal was driven
interactively. Automated evidence: `task check-site` (Node smoke of all five
demos including framebuffer pause, pan, and keep-screen exit), native and WASM
unit tests with the race detector, and a pseudo-terminal run of
`bin/ntpicture-framebuffer`. With `NTPICTURE_KITTY=supported` and an answered
cell-size query, that run transmitted 57 zlib frames under one stable image ID
at 480×248 px for 120×31 cells, used a fresh ID at 720×372 px with the same
cells after `]`, another after a resize to 150×39 cells, and deleted all three
IDs on `q`; shared memory fell back to direct transport because the pseudo
terminal answered no probe. Glyph mode produced half-block rows at
cols × 2 rows. Visual verification of the framebuffer in a real Kitty terminal
and in the browser renderers remains outstanding.

For a native operator check, first run `task build-examples` from the repository
root. Then run `./bin/ntpicture-animation` in Kitty (or record Ghostty
separately), resize while playing, alternate Space and comma/period/Home, then
press `Q` and verify both images remain. Run again and press `q`, verifying
cleanup. Use `NTPICTURE_KITTY=unsupported ./bin/ntpicture-animation` for the
glyph case. Record terminal version and actual transport before marking native
coverage done.


## Releases

This is a Go library: CI runs for branches, pull requests, and tags. Like the
small companion libraries, release publication is manual. There is no binary
release packaging. The Pages workflow separately builds and deploys the demo
site after full QA. Do not commit generated WASM or copied browser assets.

Before the first release, configure the `nimbleterminal.dev/ntpicture`
`go-import` metadata to resolve to `github.com/NimbleTerminal/ntpicture`.
Confirm module discovery from a fresh consumer without workspace or local
replacements. Update CHANGELOG.md, run `task ci`, the local consumer checks, and
the manual acceptance checks above. Review dependency licenses and retain
upstream copyright notices when distributing copied source or browser assets.
Then commit the release preparation, tag the intended version, wait for tag CI
to pass, and publish the GitHub release with its changelog. Record remaining
session/compositor migration work separately from the implemented animation and
playback packages.


## Browser demo site

`task build-examples` builds still, simple, grid, animation, and framebuffer
into `bin/`. To run one without building all five, use
`GOWORK=off go -C examples run ./grid` (or `./simple`, `./still`,
`./animation`, `./framebuffer`). These examples use booba.Run for both
native and browser execution and `Picture` for ownership, command routing, and
cleanup.

`task build-wasm-site` compiles all five into `web/demos/`, exports the booba
and ghostty-web runtime using the pinned `go tool booba-assets` in the examples
module, and copies license notices. The exporter uses the active toolchain's
wasm_exec.js. Source HTML/CSS/JS and browser license texts are tracked;
generated runtime assets and binaries are ignored and removed by `task clean`.

`task serve-wasm-site` serves the repository at http://localhost:8000/web/. All
site URLs are relative so the same artifact works under GitHub Pages' repository
prefix. Example navigation reloads the page, giving each Go WASM program and
image registry a fresh lifetime. `?demo=still`, `?demo=simple`, and `?demo=grid`
select the example; `?demo=animation` selects GIF/WebP playback and
`?demo=framebuffer` the continuously rendered scene. Unknown names select still.

CI validates the site files and WASM binaries, then starts each real demo under
Node, exercises its booba input/resize bridge, and verifies a clean exit. These
are runtime smoke checks, not a visual browser check. Verify the rendered site
in a real browser for Canvas2D/WebGL, focus, mobile layout, and Kitty/glyph
toggling before release. See the recorded checks and remaining coverage in
[visual verification](#visual-verification).

The Pages workflow runs `task ci` on the exact revision before uploading `web/`.
Configure the repository's Pages source to **GitHub Actions**. A push to main or
a manual workflow run then deploys the site; this change does not itself
publish.

Run `task notices` after dependency updates, then review and commit
THIRD_PARTY_NOTICES.md. `task notices-check` enforces that generated text in CI.
The generator includes both upstream/native and forked/WASM dependencies, the Go
runtime, and the vendored browser runtime license texts. Review `web/licenses/`
when updating booba; its module does not ship those license files alongside the
exported ghostty-web assets.


The examples publish help through booba's `bubblehelp.NewModel` wrapper. Shared
key bindings drive input handling, native help, and OSC 7501 action snapshots.
The browser opts in with `allowOSC7501Actions` and uses `installActionBar`;
buttons send normal encoded key events, with the first alias preferred. The
grid's rendering toggle is advertised only after Kitty support is confirmed.
Smoke tests acknowledge the status query, decode the real action snapshot, and
quit via its advertised gesture. The framebuffer smoke check also verifies that
frames keep arriving, that Space pauses, that an arrow pans while paused, that
the Node run advertises no resolution controls (no Kitty reply), and keep-screen
exit. Browser click/touch verification remains part of manual acceptance.

Dependency refresh: the fork revision `v2.0.0-20261010153813-5097f2f73229`
returned by `@latest` fails to compile for js/wasm (missing platform methods).
Keep `c397c32a5074` until a newer WASM-capable revision passes the browser
checks.


## Animation ownership and bounds

The animation decoder/player is extracted from Gloss's `internal/animate`;
`playback.Controller` reuses ntpicture's existing Picture instead of copying
Gloss's public mutable Animator state or its 1,000-ID reservation scheme. Each
controller gates the next job on presentation and waits only the time remaining
on its frame deadline, carrying that deadline through composition and output.
Slow terminals slow playback rather than accumulate work. Seek and pause cancel
timers/composition, stale messages cannot replace newer state, and another
composition cannot start until an old cancelled job returns. Forward all
messages to each controller, even while hidden or closing, so its Picture can
finish cleanup. Execute commands returned by every mutating method.

`animation.DecodeContext` preflights the whole input, including preview loads.
`Animate: false` or a single frame returns an image and nil player. GIF decoded
storage is bounded independently from canvas size. WebP snapshots the input;
metadata `Stats.Bytes` counts the canvas plus retained file, while the first
decoded frame, working canvases, codec scratch space, old snapshots and multiple
players add memory. Limits can be tightened but cannot exceed hard defaults.
Hosts loading files should also bound their reads before calling DecodeContext.

Player positions and Image views are immutable. NewPlayer snapshots Clip
construction input; custom lazy loaders must be safe for concurrent calls and
must not mutate returned images. Prefer LoadContext for cancellation.
NextContext returns FrameError plus a skipped-frame position on lazy failure;
callers may choose whether to continue. Controller stops and exposes the error
in Status. Presentation corrections do not clear a source/decode failure;
restart, seek or source replacement are explicit retry paths. Successful
presentation corrections recover presentation errors, and stale errors cannot
stop a corrected request. Seek fails immediately on a lazy error. A standard
codec computation between context-reader calls is not forcibly interruptible;
no hard deadline is claimed.

GIF/WebP decoding/disposal/loop tests were ported from Gloss. Added tests cover
limits, ownership, cancellation, error indexing, independent message owners,
coalesced seeks, and presentation pacing. `go test -race ./animation ./playback`
runs those packages without requiring native shared memory. The browser smoke
check also exercises pause and frame stepping in the two-animation demo.

## Composite animation checks

The animation demo also accepts `--composite`; its browser URL is
`?demo=animation&composite=1`. `task check-site` exercises its real WASM startup,
actions, pause/restart input, resize, and quit alongside the standalone demo.
These smoke checks do not establish visual correctness.

Before migrating Gloss regions, verify mixed-rate GIF/WebP regions in one
picture, transparent/disposed frames revealing the base, window rescaling,
position carry-over after relayout, and cancellation during lazy decoding.
`Composite.Window` changes pixels and remaps regions; `Picture.SetViewport`
only clips prepared cells. Test both separately and together under Kitty,
glyph fallback, and the browser renderers. Record visual results here.

## Compositor performance

Run:

```sh
go test ./animation -run '^$' -bench 'Benchmark(ReadOnlyPageDraw|Composite)' -benchmem
```

The 1600×2100 page cases cover external drawing
of read-only views, advancing a small region on a large page, and both copying
and immutable-base relayout. Compare allocation counts as well as timings.

A local macOS/Apple M5 Max check with `-benchtime=3x` found:

| Operation | Before | After | Allocations before → after |
| --- | ---: | ---: | ---: |
| Public read-only page draw | 34.9 ms | 13.2 ms | 3,360,000 → 0 |
| Composite page advance | 31.3 ms | 1.22 ms | 3,403,210 → 10 |
| Copying composite relayout | 30.9 ms | 1.26 ms | 3,403,211 → 11 |

The new immutable-base relayout measured 0.55 ms and 9 allocations. These short
samples diagnose the wrapper regression, not portable latency guarantees. A
full composed output still needs a page-sized allocation. Public views implement
RGBA64At; trusted internal rendering unwraps only our own private view type to
recover concrete-image fast paths. No public mutable-image accessor is exposed.

WithPositions intentionally recomposes changed positions for snapshot consistency;
no-op merges reuse the original snapshot. Avoid merging just before a constructor
when its previous-composite argument already supplies the needed positions.

## NRGBA Kitty packing benchmarks

The 2026-10-18 Go 1.27.2 macOS/Apple M5 Max comparison is archived in
[testdata/benchmarks/nrgba/README.md](testdata/benchmarks/nrgba/README.md), with
raw samples, source hashes, GPU harness, profiles and validation output.
At 640×320, opaque NRGBA packing fell from 204,801 allocations/op to one
and from 1.734 ms/op to 0.161 ms/op (three-sample medians). The matched native
GPU camera presentation harness fell from 3.324 ms/op to 1.444 ms/op.
These are discard-sink output completion timings, not terminal display FPS.
Node WASM tests and demo lifecycle smoke checks passed; no new visual terminal
or interactive browser acceptance is claimed.

## Cover preparation benchmarks

```sh
CGO_ENABLED=0 go test . -run '^$' -bench '^BenchmarkFitCover$' \
  -benchmem -benchtime=1s -count=5
```

The cases use synthetic inputs: 800×600 rendered to a 640×320 terminal raster
or 80×40 glyph raster, a 3200×2400 input rendered to 640×320, 800×600 rendered
to 640×128 and 640×96 (3.75× and 5× overflow, either side of the strategy
boundary), and 1,024×1 and 1×1,024 inputs rendered to 1×128 and 128×1. `Direct`
uses per-job preparation, as Kitty does; `Scratch` exercises glyph-style buffer
reuse. These measure preparation only, excluding encoding and terminal output.

`coverTo` chooses between two x/image facilities. `CatmullRom.Scale` is
separable and fast but allocates a float64 row buffer of 32 bytes per pixel of
(circumscribed width × source height) plus weight tables for every column and
row of the circumscribed rectangle, and its horizontal pass visits every
column of that rectangle, visible or not. `CatmullRom.Transform` visits only
visible pixels and allocates nothing proportional to the overflow, but
evaluates a two-dimensional kernel per pixel. Both keep the same fractional
mapping, anchors, kernel support at crop boundaries and compositing; the unit
tests compare both against an independent naive resampler. The scaler is used
while the circumscribed area is at most `coverOverflowLimit` (4) times the
visible target, so its buffer is at most 4× what an equal-size `FitFill`
already allocates; larger overflows use Transform. A same-size rectangle is a
clipped copy.

The threshold comes from a 2026-10-14 experiment on Apple M5 Max, darwin/arm64
(temporary benchmark, 320×160 visible target, source dimensions chosen to
produce each downscale and overflow ratio, medians of 200 ms runs):

| Downscale | Overflow | Scale | Transform | Scale B/op |
| ---: | ---: | ---: | ---: | ---: |
| 0.5× (upscale) | 1.5 / 4 / 8 | 0.6 / 1.0 / 1.7 ms | 2.9 ms | 1.3 / 3.4 / 6.8 MB |
| 1.25× | 1.5 / 4 / 8 | 1.1 / 2.5 / 4.7 ms | 4.3 ms | 3.1 / 8.3 / 16.6 MB |
| 2.5× | 1.5 / 4 / 8 | 3.6 / 8.7 / 17.1 ms | 12.5 ms | 6.3 / 16.6 / 33.2 MB |
| 10× | 1.5 / 4 / 8 | 49 / 127 / 254 ms | 154 ms | 25 / 66 / 132 MB |

Transform allocated under 1 KB in every case. Scale wins by 1.2–2.9× at 4×
overflow and loses between 4× and 8× for every downscale, while its buffer
grows linearly with overflow, so 4 bounds both the wasted work and the memory.

On 2026-10-14, Apple M5 Max, darwin/arm64, medians of five one-second runs
compared `7a81181` (Transform for every overflowing rectangle, from `e5811e4`)
with this strategy:

| Case | Before | After | Before B/op | After B/op | Allocations before → after |
| --- | ---: | ---: | ---: | ---: | ---: |
| Terminal Direct | 17.52 ms | 4.90 ms | 819,409 | 13,250,804 | 5 → 9 |
| Terminal Scratch | 17.44 ms | 4.33 ms | 144 | 44,547 | 3 → 1 |
| Glyph Direct | 9.68 ms | 3.03 ms | 14,352 | 1,655,793 | 5 → 9 |
| Glyph Scratch | 9.63 ms | 2.92 ms | 720 | 3,769 | 3 → 1 |
| LargeDownscale Direct | 167.61 ms | 45.29 ms | 819,632 | 50,360,564 | 5 → 9 |
| LargeDownscale Scratch | 167.93 ms | 45.16 ms | 368 | 1,890,582 | 3 → 1 |
| Overflow3.75Scaler Direct | 7.12 ms | 3.84 ms | 327,888 | 12,759,282 | 5 → 9 |
| Overflow3.75Scaler Scratch | 7.12 ms | 3.67 ms | 144 | 37,717 | 3 → 1 |
| Overflow5Transform Direct | 5.39 ms | 5.28 ms | 245,969 | 245,969 | 5 → 5 |
| Overflow5Transform Scratch | 5.39 ms | 5.26 ms | 144 | 144 | 3 → 3 |
| ExtremeAspect Direct | 3.9 µs | 3.8 µs | 688 | 688 | 5 → 5 |
| ExtremeAspectTall Direct | 4.2 µs | 4.0 µs | 688 | 688 | 5 → 5 |

Ordinary cover latency is back to the pre-`e5811e4` figures (4.84 ms and
3.02 ms were recorded then), and the extreme-aspect cases keep their bounded
allocation. The cost is the scaler's row buffer in `Direct` mode: 32 bytes per
pixel of visible width × source height, so 13 MB for an 800×600 source and
50 MB for 3200×2400. That buffer is inherent to `CatmullRom.Scale` and is the
same one `FitFill` and `FitContain` allocate for the same source; the `Scratch`
path reuses it through the scaler's pool. Reducing it would require reusing
scalers across Kitty jobs or a different resampler, which this change does not
attempt. These local measurements are diagnostic, not portable latency
guarantees. Preserve anchor and sampling tests, including translucent pixels
and nonzero source origins, when revisiting this.

### Kitty frame update validation (2026-10-27)

Protocol regression tests cover StableID full/root edits, dirty rectangles,
identical pixels, geometry/viewport invalidation, direct and tmux continuation
actions, transient hints, and playback cache selection after seeks/resizes.
These are command-stream checks, not visual terminal verification. Ghostty 1.4
and Kitty visual acceptance for this change remains unverified.

## Repeatable presentation and terminal benchmark suite

See [BENCHMARKS.md](BENCHMARKS.md) for `task bench-record`, `task bench-render`,
and the terminal/discard runner (`task build-bench`, `task bench-terminal`).
The suite covers unchanged/localized/scattered/full updates and cold/warm playback
caches. Reports distinguish command/write completion from actual terminal display;
capability declarations and geometry accompany terminal comparisons.

### Review regression coverage (2026-10-28)

Panning regressions compare decoded pixels against ordinary preparation with 8×16
cells and transparent/opaque backgrounds. Capability tests separate plain detection
from U=1 feature replies, including errors, missing keys, late replies, and tmux.
Cached-frame ownership tests exercise both native shared memory and the browser
registry through selection and Keep. These are protocol/unit checks, not new
terminal or browser visual acceptance records.

### Kitty frame capability regression coverage

Frame-edit and playback-cache protocol tests declare their required features
explicitly. Negative tests cover plain OK replies, partial echoes, errors,
timeouts, foreign sessions, and older terminals that never answer feature
queries. StableID's fallback must emit full uploads while retaining the ID.
This protocol coverage does not constitute real-terminal visual acceptance.
