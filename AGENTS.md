# AGENTS.md

Guidance for coding agents working in this repository.

## What this is

`nimbleterminal.dev/ntpicture` is a Go library that renders images in Bubble Tea
v2 apps, using Kitty graphics where supported and colored half-block glyphs
elsewhere. It was extracted from NTCharts and Gloss, which (along with ntqrcode)
are its main consumers. Develop with Go 1.27.2; the module minimum is Go 1.26.

Longer-form docs: `README.md` (API overview), `DEVELOP.md` (tooling, acceptance
records, benchmarks, release steps), `MIGRATION.md` (extraction status and
consumer migration), `CHANGELOG.md`.

## Commands

All tasks run through [Go Task](https://taskfile.dev) (`Taskfile.yml`). Native
tasks set `GOWORK=off`; browser tasks use `wasm.work`. Do not run bare `go`
commands from the root with a workspace active.

```sh
task ci                  # full gate, same as GitHub Actions (vulncheck needs network)
task test                # native tests, root + examples modules
task test-race           # race detector (needs a C compiler)
task test-no-cgo         # CGO_ENABLED=0: exercises the direct-transport fallback
task test-wasm           # GOOS=js GOARCH=wasm under Node, root + examples
task lint                # golangci-lint v2 (installed into .task/bin)
task vet fmt fmt-check go-tidy-check
task build-examples      # bin/ntpicture-{still,simple,grid,animation,framebuffer}
task run-still           # or: GOWORK=off go -C examples run ./animation -- --composite
task build-wasm-site     # web/demos/*.wasm + booba runtime, then check-site smoke tests
task serve-wasm-site     # http://localhost:8000/web/?demo=framebuffer
task notices             # regenerate THIRD_PARTY_NOTICES.md after dependency changes
task clean
```

Single tests:

```sh
GOWORK=off go test -mod=readonly -run 'TestPictureCoalescesAndCancelsQueuedWork' .
GOWORK=off go test -mod=readonly -race ./animation ./playback   # no shared memory needed
GOWORK=$PWD/wasm.work GOOS=js GOARCH=wasm CGO_ENABLED=0 \
  go test -mod=readonly -exec="$(go env GOROOT)/lib/wasm/go_js_wasm_exec" \
  -run 'TestBrowserObject' ./internal/kittyshm
```

Benchmarks (results and methodology are recorded in `DEVELOP.md`):

```sh
GOWORK=off go test ./animation -run '^$' -bench 'Benchmark(ReadOnlyPageDraw|Composite)' -benchmem
GOWORK=off CGO_ENABLED=0 go test . -run '^$' -bench '^BenchmarkFitCover$' -benchmem -count=5
```

Native POSIX shared-memory tests need `shm_open` and fail deliberately where it
is blocked. If you must exclude them (for example in a sandbox), say so; that
run is not full native transport validation. `NTPICTURE_SHM_TERMINAL_TEST=1`
enables the real-terminal consumption test in a Kitty-capable terminal. CI runs
Linux and macOS (distinct shared-memory implementations) and cross-compiles
Windows (`task cross-windows`).

`task consumers` and `task browser-check` need sibling checkouts (NTCharts,
Gloss, ntqrcode, go-booba) and are not part of `task ci`.

## Module layout

Two Go modules plus a workspace file:

- **Root module** (`nimbleterminal.dev/ntpicture`): the library. Uses upstream
  Bubble Tea. Never add local sibling `replace` directives here.
- **`examples/`** (separate module): the five demos, which depend on `go-booba`
  so the library does not. It `replace`s ntpicture with `..` to test unreleased
  changes. Each demo runs natively and as WASM via `booba.Run`.
- **`wasm.work`**: browser builds only. Pins a browser-capable fork of Bubble Tea
  (`neomantra/bubbletea/v2`). The fork's `@latest` does not compile for
  js/wasm; keep the pinned revision until a newer one passes `task check-site`.
  Dependabot does not update `wasm.work` or `wasm.work.sum`; review them with
  module updates.

Generated output is ignored: `bin/`, `dist/`, `web/demos/`, `web/booba/`,
`web/ghostty-web/`, `.task/`. Never commit browser build output.

## Architecture

### Root package: `Picture` and `TerminalSession`

`Picture` (`picture.go`) is the central component. It owns asynchronous image
replacement:

- Each Picture runs **at most one `pictureJob`** and remembers only the latest
  request. A job prepares (fit/crop/resample) off the event loop, returns a
  `picturePrepared` message, and Update commits it via `pictureReady`. Jobs can
  be replaced before they start (`replacedBy` chain); stale results are rejected
  by comparing `generation` and the owner pointer.
- The displayed `picturePlacement` (the "front") stays on screen until its
  replacement commits. Replaced placements are retired, and their Kitty IDs and
  shared-memory objects are deleted later (`retired`, `retiredSHM`,
  `pictureRetire`).
- **Stable-ID streaming** (`Options.StableID`): a replacement borrows the
  front's Kitty image ID via a `pictureImageID` lease. Invariant: a cancelled
  borrower must never delete the still-displayed image. The `active`,
  `overwritten`, and `outputDone` fields order preparation, raw APC output, and
  cleanup.
- Terminal messages (`uv.CellSizeEvent`, `uv.KittyGraphicsEvent`,
  `KittyCapabilityMsg`, `TerminalSessionMsg`) route to the Picture's
  `TerminalSession`.

`TerminalSession` (`terminal_session.go`) discovers Kitty support, cell size,
and shared-memory/tmux transport once per terminal connection. Pictures without
`Options.Session` own a private one; sharing a session across pictures means one
probe. `NTPICTURE_KITTY` and `NTPICTURE_TMUX_PASSTHROUGH` override detection.
The root package has no `init()` environment side effects; keep it that way.

Rendering pipeline:

| File | Role |
| --- | --- |
| `renderer.go` | `Config`, modes, fit enums, prepare params |
| `fit.go` | resampling, incl. the `coverTo` Scale-vs-Transform strategy (see DEVELOP.md) |
| `glyph.go` | half-block output, two pixel rows per cell |
| `kitty.go`, `kitty_payload.go` | APC encoding: PNG, RGBA, zlib |
| `kitty_shared_memory.go` | POSIX shm natively, `globalThis.ghosttyKittySharedMemory` in the browser, direct fallback |
| `kitty_capability.go` | probing |
| `raster.go` | `RasterGeometry`, resolution, crop setters |
| `viewport.go` | cell window into the prepared image, no retransmission |
| `submit.go` | atomic `Frame` submit with `Exact` |
| `grid.go` | `GridImage` |

### Platform-specific code

Build tags select transport in `internal/kittyshm`: `object_native.go`
(`linux || (darwin && cgo)`), `object_js.go` (`js && wasm`), and
`object_other.go` (everything else; reports unsupported). macOS shared memory
requires CGO; `task test-no-cgo` covers the fallback. `yield_js.go` /
`yield_other.go` and `playback/wait_js.go` / `wait_other.go` follow the same
pattern. Tests with `_js_test.go` / `_native_test.go` suffixes carry matching
tags.

### `legacy` package and the bridge

`legacy/` exposes the NTCharts-era `Model` API and process-global capability
helpers for existing consumers. The implementation (`legacy_model.go`) lives in
the root package as unexported `legacyModel`; `internal/legacybridge` is a table
of function variables the root package fills at init and `legacy` calls. This
keeps the root free of exported legacy surface without an import cycle.
`legacy_aliases_test.go` aliases the unexported names for root tests.
`testdata/compat/picture.go` is the NTCharts alias adapter that
`scripts/check-consumers.py` installs into temporary consumer copies.

### `animation` and `playback`

- `animation` decodes GIF (frames stay paletted, storage bounded) and WebP (lazy
  frame decoding) into an immutable `Player` over a `Clip`. `Source`/`Seekable`
  are the interfaces playback consumes. `Composite` places animated `Region`s on
  an immutable base; `Composite.Window` crops/rescales pixels (unlike
  `Picture.SetViewport`, which only clips prepared cells). `Limits` can be
  tightened but never raised above hard defaults. Images reach the renderer
  through `internal/imageview` read-only views; never expose mutable pixels.
- `playback.Controller` paces a `Source` through a `Picture` by deadline: the
  next job waits for presentation and then only the remaining frame time, so
  slow terminals slow playback instead of queuing. `playback.Layers` presents a
  `Composite` under Kitty by sending the base once and updating only
  cell-aligned animated windows; under glyphs it animates the flattened image.
- `*Picture`, `*Controller`, and `*Layers` share one presentation method set
  (`Init`, `Update`, `View`, `SetSize`, terminal/mode setters, viewport,
  visibility, `Stop`, `Keep`, `KeepOnMainScreen`, `Close`). Keep them in sync
  when adding lifecycle methods, and update the README's list.

### Examples and browser site

`examples/*/main.go` use `booba.Run` and `examples/internal/demohelp` for shared
key bindings, native help, and OSC 7501 action snapshots. `scripts/smoke-demo.mjs`
runs each real WASM demo under Node through the booba input/resize bridge; these
are lifecycle smoke checks, not pixel checks. `web/` holds the tracked site
source; `task build-wasm-site` fills in generated assets.

## Invariants

Breaking these breaks consumers, even if tests still pass.

- **Host contract.** Components never read stdin, take over the terminal, or
  probe synchronously. Hosts forward every message to `Update` and run every
  returned command. `Close()` (or `Keep`) is the only thing that deletes or
  finalizes placements. No component clears other images globally.
- **Replacement.** Unless `ClearOnReplace` is set, the old front stays until
  its replacement is presented. Stale or superseded results never replace
  newer state.
- **Bounds.** Preserve the pixel caps (32 Mi source/prepared/canvas), the 64 MiB
  pending shared-memory cap, and the animation decode limits.
- **Wire compatibility.** Kitty output keeps the NTCharts wire format. The
  exact-size `FitFill` path must bypass resampling; ntqrcode relies on this for
  exact QR pixels.
- **Image IDs.** `NextImageID` returns one fresh 24-bit ID per call and never
  wraps or recycles. Consumers mixing legacy pictures, QR codes, and pictures
  share one allocator.

## Tests and reporting

- Glyph golden fixtures in `testdata/*.json` are independent reference output
  (see `testdata/GLYPH_FIXTURES.md`). Never regenerate them from ntpicture's own
  encoder to make a test pass.
- Automated tests and Node smoke checks do not establish visual terminal
  behavior. When reporting results, distinguish protocol/unit coverage from
  visual verification. Record real terminal and browser checks, with date, OS,
  terminal version, and revision, in DEVELOP.md's acceptance records; never
  claim a visual check that did not happen.
- Run `task ci` before finishing a change.

## Documentation

- Update `CHANGELOG.md` under an `Unreleased` heading for user-visible changes.
  Keep entries short and direct.
- Keep README, DEVELOP, MIGRATION, and CHANGELOG consistent with the code.
- Run `task notices` after dependency updates; `task notices-check` enforces it
  in CI.
