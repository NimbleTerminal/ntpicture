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
- Shared memory: test native and browser paths, direct fallback when
  unavailable, delayed consumption, and cleanup after cancellation or shutdown.
- Browser host: check the actual booba/ghostty-web Canvas2D/WebGL output,
  registry consumption, resizing, and teardown with multiple pictures.
- Lifecycle: exercise rapid updates and stale messages while closing/reopening
  pictures; ensure messages route only to their owning instance.

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
default of `playback.New`; it does not set `Picture.StableID`. Its transport was not separately
instrumented in this UI run, so this is not proof that every frame used shared
memory. The grid also requests shared memory. Independent automated checks
passed for the actual booba bundle's PNG/RGBA/zlib pixels and shared-memory
registry consumption (`task browser-check`), all four demo startup/input/resize/
quit smoke checks and animation keep-screen, plus
`task test-no-cgo` and `task test-wasm` (including cancellation, stable IDs, and registry cleanup).

Remaining visual coverage: native Kitty, native shared memory and tmux; forced
shared-memory failure/fallback in the browser; deterministic cancellation during
transmission; hide/show; sustained streaming; and each renderer's full lifecycle
matrix. Native visual verification remains outstanding.

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

`task build-examples` builds still, simple, grid, and animation into `bin/`. To
run one without building all four, use `GOWORK=off go -C examples run ./grid`
(or `./simple`, `./still`, `./animation`). These examples use booba.Run for both
native and browser execution and `Picture` for ownership, command routing, and
cleanup.

`task build-wasm-site` compiles all four into `web/demos/`, exports the booba
and ghostty-web runtime using the pinned `go tool booba-assets` in the examples
module, and copies license notices. The exporter uses the active toolchain's
wasm_exec.js. Source HTML/CSS/JS and browser license texts are tracked;
generated runtime assets and binaries are ignored and removed by `task clean`.

`task serve-wasm-site` serves the repository at http://localhost:8000/web/. All
site URLs are relative so the same artifact works under GitHub Pages' repository
prefix. Example navigation reloads the page, giving each Go WASM program and
image registry a fresh lifetime. `?demo=still`, `?demo=simple`, and `?demo=grid`
select the example; `?demo=animation` selects GIF/WebP playback. Unknown names
select still.

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
quit via its advertised gesture. Browser click/touch verification remains part
of manual acceptance.

Dependency refresh: the fork revision `v2.0.0-20261010153813-5097f2f73229`
returned by `@latest` fails to compile for js/wasm (missing platform methods).
Keep `c397c32a5074` until a newer WASM-capable revision passes the browser
checks.


## Animation ownership and bounds

The animation decoder/player is extracted from Gloss's `internal/animate`;
`playback.Controller` reuses ntpicture's existing Picture instead of copying
Gloss's public mutable Animator state or its 1,000-ID reservation scheme. Each
controller schedules the next delay only after the current picture is presented.
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
in Status. Seek fails immediately on a lazy error. A standard codec computation
between context-reader calls is not forcibly interruptible; no hard deadline is
claimed.

GIF/WebP decoding/disposal/loop tests were ported from Gloss. Added tests cover
limits, ownership, cancellation, error indexing, independent message owners,
coalesced seeks, and presentation pacing. `go test -race ./animation ./playback`
runs those packages without requiring native shared memory. The browser smoke
check also exercises pause and frame stepping in the two-animation demo.
