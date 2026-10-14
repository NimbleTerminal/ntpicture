# Development

Use Go 1.27.2 for development and CI (the module minimum is Go 1.26.8). Native
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
`NTCHARTS_SHM_TERMINAL_TEST=1` inside a local Kitty-capable terminal.

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
  `NTCHARTS_KITTY=unsupported` and confirm resolution controls disappear.

Unit tests cover stale results, geometry, encoding, and cleanup. Animation
acceptance also covers GIF/WebP pause/resume, loop completion, frame seeking,
lazy failure reporting, and two simultaneously playing pictures. Keep dated
acceptance records here, including remaining coverage and the tested revision,
so they survive completion of the extraction.

### Visual verification

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
`bin/ntpicture-framebuffer`. With `NTCHARTS_KITTY=supported` and an answered
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
cleanup. Use `NTCHARTS_KITTY=unsupported ./bin/ntpicture-animation` for the
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

## Cover preparation benchmarks

```sh
CGO_ENABLED=0 go test . -run '^$' -bench '^BenchmarkFitCover$' \
  -benchmem -benchtime=100ms -count=3
```

The cases use synthetic 800×600 inputs rendered to a 640×320 terminal
raster or 80×40 glyph raster, plus a 1,024×1 input rendered to 1×128. `Direct`
uses per-job preparation, as Kitty does; `Scratch` exercises glyph-style buffer
reuse. These measure preparation only, excluding encoding and terminal output.

On 2026-10-13, Apple M5 Max, darwin/arm64, three short samples compared the
renderer at `b12e0a8` with `acfa695`'s fit.go supplied through a temporary Go
overlay. Median `Direct` measurements were:

| Case | Before | After | Before bytes/op | After bytes/op |
| --- | ---: | ---: | ---: | ---: |
| Terminal | 4.84 ms | 17.45 ms | 13,250,801 | 819,408 |
| Glyph | 3.02 ms | 9.67 ms | 1,655,798 | 14,352 |
| Extreme aspect | 2.24 ms | 0.0038 ms | 17,823,482 | 688 |

The affine sampling path removes allocations proportional to invisible output,
but ordinary downscaling is roughly 3–4× slower in this run. This is an open
performance follow-up, not a reason to restore unbounded scaler allocation.
Evaluate a bounded separable scaling path or a measured, allocation-limited
fast path. Preserve anchor and sampling tests, including translucent pixels and
nonzero source origins. Repeat longer benchmark runs before choosing a design;
these short local measurements are diagnostic, not portable latency guarantees.
