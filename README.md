# ntpicture

A reusable terminal image component for Bubble Tea v2. Module:
`nimbleterminal.dev/ntpicture`.

This is an unreleased extraction from NTCharts and Gloss. `Picture` handles
still images and continuously supplied frames. `animation` decodes GIF/WebP and
composes playback positions; `playback` schedules them through a Picture.

```go
pic := ntpicture.New(ntpicture.Options{
    Render: ntpicture.Config{Fit: ntpicture.FitContain},
})

// Return/run these commands through the host's Bubble Tea event loop.
cmd := tea.Batch(pic.Init(), pic.SetSize(80, 30), pic.SetImage(img))
cmd = pic.Update(msg) // forward every message, including while hidden
content := pic.View()

// After positive capability detection:
cmd = pic.SetMode(ntpicture.PictureKitty)

// Before removing it or quitting:
cmd = tea.Sequence(pic.Close(), tea.Quit)
```

Run `task run-still` for a native gradient example. `q` quits with placement
cleanup. The host owns keys, layout, loading/error UI and visibility.

## Picture ownership and replacement

`SetImage` takes a bounded pixel copy before returning. `SetImageImmutable`
avoids that copy when the producer can guarantee immutable pixels for all
outstanding uses. Use the latter for dedicated mesh frames; use `SetImage` if a
renderer reuses its framebuffer. Methods belong to one event loop; do not copy a
`Picture` or invoke its methods concurrently.

The previous picture stays displayed while its replacement is prepared and
transmitted, including during resize. There is at most one preparation/output
job and one latest requested image. Repeated changes cancel obsolete work and
coalesce. Cancellation checks surround preparation/encoding stages; an already
running scaler or codec cannot be interrupted. Waiting for its completion keeps
concurrency bounded. At most two old placements are held for delayed retirement.

`Options.ClearOnReplace` removes old content immediately. QR components need
this policy and should retain their own exact module rasterization and custom
glyph output. Rendering options survive every replacement.

Standalone `Picture` uses fresh IDs by default. For continuously supplied
mesh/framebuffer images, set `Options.StableID: true`. `playback.New` uses
stable IDs by default; set `playback.Options{FreshIDs: true}` to opt out.
With stable IDs, Kitty frames reuse the displayed image ID while cell dimensions,
cell pixel geometry and encoded raster dimensions remain unchanged. The
placeholder string stays identical, so Bubble Tea can skip redrawing it and
transmit only the image update. Each Picture has its own ID; resize,
hiding/clearing, and committed mode changes require a new placement.
`ClearOnReplace` takes precedence.

Streaming updates terminal pixels as soon as the transmission arrives, before
`PresentedMsg`. Cancellation rejects pending work but cannot recall bytes
already sent. Keep restores the last committed frame after accepted output
drains. Fresh-ID replacement retains the old image independently
during a replacement. Neither mode changes the configured image encoding or
resolution.

`PresentedMsg` identifies its `Owner` and committed `Generation`; it means the
ordered output/grid sequence completed, not that the terminal acknowledged
painting. `ErrorMsg` reports failed preparation or validation. `Status` exposes
current generation, last presented generation, busy state, error and actual
transport. Superseded generations are not presented; a producer coalescing its
own requests should track the newest `Status().Generation`.

Playback exposes failures through `Status().Err`. A successful size, terminal,
mode or resolution correction clears a presentation error and permits playback
to continue, subject to pause and visibility. Late errors from superseded or
corrected requests are ignored. Source/decode failures remain separate: geometry
changes do not clear them. Use `Restart`, a valid player `Seek`, or `SetSource`
to retry or replace the source; restart/seek preserve or set pause as documented.

`SetVisible(false)` cancels work and removes placements while retaining the
source. `Close` is final and idempotent. Local resources are released even if
its command is not run, but terminal deletion requires running that command. An
accepted transmission carries a cleanup guard for removal races. No component
clears other images globally.

`Keep()` is a final alternative to `Close()` for still images, mesh frames, and
playback controllers. It cancels pending replacements and freezes the last
presented view. Return `tea.Sequence(pic.Keep(), tea.Quit)` and include that
view in the application's final main-screen layout. Kitty content is
retransmitted directly before exit when shared buffers must be released or an
accepted streaming update must be undone. Direct fronts otherwise send no image
on exit; glyph content stays in the final view. Run the returned command. A
subsequent `Close()` is a no-op. The host controls screen selection, help
removal, and space for the shell prompt.

Default image IDs start at a randomized offset to reduce collisions with images
kept by previous processes. This is best effort, not a cross-process
reservation; applications needing guaranteed uniqueness must provide a shared
`NextID` policy.

Playback carries a frame deadline across composition, encoding, and output,
waiting only for the remaining interval. Presentation still gates the next job.
Pause, seek, visibility changes, source replacement, and rate changes reset the
timeline. Overruns do not create overlapping workers or an accumulating catch-up
debt; slow rendering can still limit the achievable frame rate.

## Geometry and transport

Kitty PNG, raw RGBA, zlib and shared-memory output retain the NTCharts wire
format. Glyph output uses colored half blocks. Fit contain/fill/cover, anchors,
cell pixel dimensions, placement depth and resolution factors are supported. The
exact-size `FitFill` path bypasses resampling.

Producers can query `pic.RasterGeometry()` for `Cells`, target `Pixels`, current
`Mode`, and physical `DisplayAspect`. Use the aspect for the mesh camera and the
pixel dimensions for its framebuffer; glyph pixels and integer rounding can
make those ratios differ. Requery after size, mode, terminal geometry or
resolution changes. The query describes the requested full picture, even while
an older front is displayed; `Viewport` clipping does not change it.

```go
// In the event loop, after choosing an adaptive resolution:
resolutionCmd := pic.SetResolution(0.3)
g := pic.RasterGeometry()
if g.Pixels.X <= 0 || g.Pixels.Y <= 0 {
    return resolutionCmd
}
// Synchronous sketch: renderMesh returns a fresh, immutable framebuffer.
frame := renderMesh(g.Pixels.X, g.Pixels.Y, g.DisplayAspect)
return tea.Batch(resolutionCmd, pic.SetImageImmutable(frame))
```

Configure `FitFill` and submit an opaque, immutable framebuffer at exactly
`Pixels` to bypass preparation scaling and compositing. Return both commands:
the resolution change may have scheduled work that a newer frame supersedes.
The sketch renders synchronously and is suitable only for inexpensive work.
For expensive meshes, run rendering in a command that captures the geometry and
an application request ID. In Update, reject obsolete results and submit the
accepted immutable frame. Bound producer concurrency separately; Picture's
preparation limit does not limit application render jobs.

`SetResolution` accepts a finite factor in `(0, 1]`, relative to terminal pixel
size. Kitty scales the full raster and rounds each dimension once, to at least
one pixel. It preserves cell footprint and physical aspect, with unavoidable
pixel rounding, and retains the front until the new placement is ready. Factors
that round to the same dimensions do not trigger preparation. Glyph output stays
at `cols × (2 * rows)`; the factor is saved for a later switch to Kitty. Invalid
factors report `ErrorMsg` without cancelling pending work. Playback controllers
forward both methods. Legacy `Model` retains its existing per-cell rounding.

`Picture` bounds source and prepared images to 32 Mi pixels and validates Kitty
placeholder dimensions. This is a pixel bound, not a total RAM budget: source,
front, retired frames, and encoder/scaler scratch may coexist. Applications own
aggregate document budgets. Glyph validation checks its actual two-pixel-row
raster independently of terminal cell dimensions. `FitCover` samples only the
visible destination when the scaled image overflows, preserving its anchor and
fractional sampling coordinates without allocating the full off-screen raster.

Use `SetTerminal(kitty, cellWidth, cellHeight)` when the host already knows its
capability and geometry. It does not probe. Otherwise run `Init`, route replies,
and select Kitty on `KittyCapabilityMsg{Capability: KittyCapabilitySupported}`.
The message also reports timeouts and already-known results; handle duplicates
and late positive replies. Invalid terminal cell-size reports are ignored, while
explicit `SetTerminal` calls still validate their inputs. This is the legacy
default-session flow; explicit sessions use `TerminalSessionMsg` as shown below.

Shared memory uses the unchanged browser registry
`globalThis.ghosttyKittySharedMemory` (`Map<string, Uint8Array>`), Linux named
objects, or macOS POSIX shared memory with CGO. Creation/probe failures fall
back to the configured direct format. Native pending objects retain the 64 MiB
process cap and abandoned-retirement grace. Browser producer accounting has not
yet been strengthened beyond the extracted implementation.

Image ID allocation returns **one** fresh positive 24-bit ID per call, not a
block. By default all `Picture` instances share `NextImageID`. Hosts mixing
legacy pictures, QR codes and new pictures must supply/use one allocator across
all of them. IDs do not wrap or recycle; exhaustion returns
`ErrImageIDExhausted`.

## Shared terminal sessions and viewports

Use one `TerminalSession` per Bubble Tea terminal connection. Share it across
pictures and playback controllers; discovery runs once through normal messages.
Background, fit, encoding, and glyph/Kitty selection remain per-picture policy.

```go
session := ntpicture.NewTerminalSession(ntpicture.TerminalOptions{})
a := ntpicture.New(ntpicture.Options{Session: session})
b := ntpicture.New(ntpicture.Options{Session: session})
cmd := tea.Batch(a.Init(), b.Init()) // one session probe

// In Update, forward every message to both pictures and run their commands.
cmd = tea.Batch(a.Update(msg), b.Update(msg))
if event, ok := msg.(ntpicture.TerminalSessionMsg); ok &&
    event.Session == session &&
    session.Snapshot().Kitty == ntpicture.KittyCapabilitySupported {
    cmd = tea.Batch(cmd, a.SetMode(ntpicture.PictureKitty),
        b.SetMode(ntpicture.PictureKitty))
}
```

`TerminalOptions` accepts known Kitty/shared-memory capabilities, initial cell
pixel dimensions, and an optional tmux override. Explicit values take precedence
over environment hints. Unknown capabilities are discovered independently;
allocation failures still fall back to direct transport. `Snapshot` reports
capabilities and geometry; each picture's `Status` reports its actual transport.
Forward raw terminal events only to the session for that connection. Close each
picture and execute its cleanup command, then call `session.Close()` to release
pending probe resources. Keep-screen also requires closing the session on exit.

Omitting `Options.Session` preserves legacy behavior. Package-level capability
helpers and `Model` use the process-wide compatibility session exposed by
`DefaultTerminalSession`; they do not change explicit sessions. The default
session has process lifetime and cannot be closed.

A viewport selects cells from the full prepared image, independently of layout:

```go
cmd = a.SetSize(80, 60) // prepare the full image at this cell size
cmd = tea.Batch(cmd, a.SetImage(img))
a.SetViewport(ntpicture.Viewport{X: 0, Y: 20, Width: 80, Height: 15})
content := a.View() // source rows 20–34; host positions this in its layout
// Scroll without preparing or transmitting pixels; the image ID stays the same.
a.SetViewport(ntpicture.Viewport{X: 0, Y: 21, Width: 80, Height: 15})
a.ResetViewport() // show the full prepared image again
```

Kitty views preserve original placeholder coordinates; glyph views preserve ANSI
colors while clipping rows and columns. Out-of-bounds portions are clipped and
nonpositive dimensions produce an empty view. Viewports persist across frames,
resize, and keep-screen. They retain the full image and do not pause playback;
use `SetVisible` for hiding/resource policy. Playback controllers expose the
same `SetViewport` and `ResetViewport` methods. Gloss continues to own document
layout and the mapping from document regions into these cell rectangles.

## Compatibility and migration

`Model`, `Config`, message types, geometry helpers and capability functions
preserve the low-level NTCharts surface. Its constructors are `NewModel` and
`NewModelWithConfig`; `New` constructs the lifecycle-owning `Picture`.
`Model.SetImage` retains pixels without copying, as before, and legacy callers
remain responsible for geometry/resource limits.

`testdata/compat/picture.go` is a proposed NTCharts alias adapter, exercised by
`scripts/check-consumers.py` in temporary checkouts. It preserves type/message
identity and shares capability state with this module. The script never edits
sibling repositories. Heatmap/chart and URL adapters remain in NTCharts.

Import `ntpicture/decoders` for optional PNG/JPEG/GIF/WebP/BMP/TIFF registration
with `image.Decode`. This is static image decoding, not animation playback.

See [DEVELOP.md](DEVELOP.md) for checks and [MIGRATION.md](MIGRATION.md) for
remaining milestones. The extracted rendering code and tests originate in
NTCharts; its MIT copyright notice is preserved in [LICENSE](LICENSE).

## GIF and WebP playback

```go
img, player, err := animation.DecodeContext(ctx, data, animation.Options{Animate: true})
if err != nil { return err }
// A single-frame image has no player: submit img to Picture and return.
if player == nil { return showStill(img) }
ctrl := playback.New(player, playback.Options{})
cmd := tea.Batch(ctrl.Init(), ctrl.SetSize(60, 20))
cmd = ctrl.Update(msg) // forward every message to each controller
content := ctrl.View()
cmd = ctrl.Pause(true)
cmd = ctrl.Seek(12)    // one-based; pauses and resets loop counting
cmd = ctrl.SetRate(2)  // 0.25x–4x
cmd = ctrl.Close()    // cancel work and retire placements; execute this command
```

GIF frames stay paletted; WebP frames decode lazily. Both honor frame delays,
rectangles, transparency, disposal, and loop counts. Decoding defaults to 128
MiB input, 32 Mi canvas pixels, 1,000 frames, and 64 MiB GIF frame storage.
`animation.Options.Limits` can tighten these ceilings; zero selects defaults.
Lazy failures identify the frame and pause playback through `ctrl.Status().Err`.
Hosts still own controls, visibility decisions, and aggregate resource budgets.

## Composing several animations

`animation.Composite` combines an immutable base image with pixel rectangles
containing players. It advances regions at their earliest due time, preserving
independent delays and loop counts. Repeated opaque keys share a playback
position and must refer to the same clip; slice order determines paint order.

```go
source, err := animation.NewComposite(ctx, base, []animation.Region{
    {Key: "left", Rect: image.Rect(0, 0, 240, 180), Player: gifPlayer},
    {Key: "right", Rect: image.Rect(240, 0, 480, 180), Player: webpPlayer},
}, nil)
if err != nil { return err }
ctrl := playback.NewSource(source, playback.Options{})
// Init, Update, SetSize, Pause, SetRate, Keep, and Close work as before.
cmd := ctrl.Restart() // resets all regions; preserves pause state
```

Construction snapshots base pixels and region metadata. If the host already
owns an immutable base, `NewCompositeImmutable` skips the base copy; its pixels
must remain unchanged for all derived positions and windows. Region metadata
is still copied and public images remain read-only. Each canvas and region
is bounded to 32 Mi pixels, with at most 1,024 region entries. These bounds are
not an aggregate memory budget: base, output, players, older positions, and
scaler scratch may coexist. Hosts retain their smaller document-specific limits.
Construction, window creation, and position merging can do substantial work;
run them off the UI loop and reject stale results before `SetSource`.

`animation.Source` uses immutable positions, so off-loop work needs no mutable
clone. Advance/reset accept a context and report errors. A failed composite
advance leaves its published position unchanged; `RegionError` wraps the
underlying `FrameError`. Playback pauses and reports the error in `Status.Err`.
Cancellation is checked between regions; a running scaler/codec must finish.
Rate scaling belongs to the controller and is applied once. When a tick only
changes loop bookkeeping and retains the same immutable image, the controller
skips picture preparation and transmission.

`Status.Source` supplies the current immutable source position. `Status.Player`
is populated only for a standalone player. Composites have no single frame
number: `Seek` pauses with `playback.ErrNotSeekable`; use `Restart` instead.
Existing `playback.New`, `SetPlayer`, and standalone seeking remain supported.

For relayout, pass the previous composite to `NewComposite`, or call
`WithPositions(ctx, windows...)` before constructing the new layout. Positions
carry only when both key and clip identity match; replaced clips start at their
supplied player, and removed keys are discarded. `WithPositions` recomposes
when positions change, so its returned pixels and
playback positions agree; unchanged positions reuse the original image. Later
arguments win. For a single previous composite, pass it directly to the
constructor instead of merging and then immediately recomposing a new layout.

### Window versus Viewport

These operations act at different stages:

- **`Composite.Window(ctx, pixelRect, pixelSize)` creates a new pixel source.**
  It crops/resamples the base once, remaps intersecting animation regions, and
  carries their playback positions. Future frames compose and transmit this
  smaller source. Zero size retains the intersected rectangle's pixel size;
  empty windows return an error. Regions within two pixels of native size snap
  to that size to avoid repeated resampling. Mild scaling uses ApproxBiLinear;
  larger changes use CatmullRom. Snapping/resampling can change edge pixels.
- **`Picture.SetViewport(Viewport{...})` clips presentation of an existing source.**
  It selects prepared Kitty placeholders or glyph cells without resampling,
  encoding, allocating another image ID, or transmitting another bitmap merely
  because the viewport moved. It retains the full source. For a changing
  animation, subsequent frames still compose/encode that full source.

Use `Window` to reduce the pixel work for a small animated overlay. Use
`Viewport` to scroll a prepared picture cheaply. They can be combined. Gloss
owns document-to-region mapping, overlay grouping, visibility, and budgets;
ntpicture does not decide which document window should be shown.

```go
window, err := source.Window(ctx, image.Rect(0, 0, 240, 180), image.Pt(120, 90))
if err != nil { return err }
cmd := ctrl.SetSource(window) // new pixels; requires preparation/transmission
ctrl.SetViewport(ntpicture.Viewport{Y: 2, Width: 30, Height: 8})
// Viewport only selects cells from that prepared window.
```

## Examples and browser demo

```sh
task build-examples       # bin/ntpicture-{still,simple,grid,animation}
task serve-wasm-site      # http://localhost:8000/web/
```

The site runs four examples locally in the browser through booba: a generated
still image, the embedded-image viewer ported from NTCharts, its interactive
`GridImage` board, and side-by-side GIF/WebP playback from Gloss. The same
programs run natively. Use arrows/WASD/HJKL in the grid, `g` to switch rendering
modes, and `q` to quit. Restart reloads a fresh browser session. Booba action
buttons above the terminal come from the same Go help bindings as the keyboard
controls; the grid exposes movement and, when Kitty is available, rendering
mode. Animation exposes play/pause, frame stepping, first-frame seeking, and
keep-screen exit. Use the keyboard’s `q` to clear and quit the animation demo.

Examples have their own Go module; booba stays out of the library's dependency
graph. The NTCharts URL, heatmap, and chart adapters remain NTCharts examples.
See [DEVELOP.md](./DEVELOP.md#browser-demo-site) for building and deploying the
site, and [NOTICE.md](./NOTICE.md) for the embedded artwork's separate license.

## Open Collaboration

Run `task ci` before submitting changes. It checks formatting and module
metadata, race tests, direct-transport fallback, vet, native/Windows/WASM
builds, browser registry tests, and known vulnerabilities. You need Go 1.27.2,
[Go Task](https://taskfile.dev), Node, a C compiler, and network access for the
vulnerability scans. `task test` runs native tests without querying the
vulnerability database. See [DEVELOP.md](./DEVELOP.md) for consumer checks,
manual acceptance checks, and release preparation.

Contributions and feedback are welcome. Please follow our [Code of
Conduct](./CODE_OF_CONDUCT.md).

- [Report an issue](https://github.com/NimbleTerminal/ntpicture/issues)
- [Submit a pull request](https://github.com/NimbleTerminal/ntpicture/pulls)

## Acknowledgements

ntpicture grows out of [NTCharts](https://github.com/NimbleMarkets/ntcharts) and
the image presentation work in Gloss. Thanks to [Charm](https://charm.sh/) for
Bubble Tea and its terminal libraries,
[pixterm](https://github.com/NimbleMarkets/pixterm) for the earlier glyph
renderer, and [go-booba](https://github.com/NimbleMarkets/go-booba) for the
browser runtime and image registry integration.

## License

Source code: [MIT License](./LICENSE). Artwork exceptions are listed in
[NOTICE.md](./NOTICE.md); dependency licenses are in
[THIRD_PARTY_NOTICES.md](./THIRD_PARTY_NOTICES.md).

Copyright (c) 2024–2026 [Neomantra Corp](https://www.neomantra.com).

----
Made with :heart: and :fire: by the team behind [Nimble
Terminal](https://nimbleterminal.dev).
