# ntpicture — Terminal images for Bubble Tea

<p>
    <a href="https://pkg.go.dev/nimbleterminal.dev/ntpicture"><img src="https://pkg.go.dev/badge/nimbleterminal.dev/ntpicture.svg" alt="Go Reference"></a>
    <a href="https://github.com/NimbleTerminal/ntpicture/actions/workflows/ci.yml"><img src="https://github.com/NimbleTerminal/ntpicture/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
    <a href="./CODE_OF_CONDUCT.md"><img src="https://img.shields.io/badge/Contributor%20Covenant-2.1-4baaaa.svg" alt="Code Of Conduct"></a>
</p>

Show still images, GIF/WebP animations, and live framebuffers in a
[Bubble Tea v2](https://github.com/charmbracelet/bubbletea) app. ntpicture uses
Kitty graphics where available and colored half-block glyphs everywhere else.

- **`Picture`** keeps the current image on screen while its replacement is
  prepared, coalesces rapid updates, drops stale results, and cleans up
  terminal placements.
- **`TerminalSession`** discovers Kitty, shared-memory, and cell-size support
  once per terminal connection, through ordinary Bubble Tea messages.
- **`animation`** decodes GIF and WebP with bounded memory and composes several
  animations over one base image.
- **`playback`** paces those animations through a Picture, updating only the
  animated regions under Kitty.

ntpicture grew out of [NTCharts](https://github.com/NimbleMarkets/ntcharts) and
[Gloss](https://github.com/NimbleMarkets/gloss).  See the first [CHANGELOG entry](./CHANGELOG.md#v010-2026-10-17) for the history.

[Open the browser demo](https://nimbleterminal.github.io/ntpicture/)

## Install

```sh
go get nimbleterminal.dev/ntpicture@latest
```

Requires Go 1.26 or later.

## Quick start

```go
pic := ntpicture.New(ntpicture.Options{
    Render:   ntpicture.Config{Fit: ntpicture.FitContain},
    AutoMode: true, // use Kitty once the terminal confirms support
})

// Run these through your Bubble Tea event loop.
cmd := tea.Batch(pic.Init(), pic.SetSize(80, 30), pic.SetImage(img))
cmd = pic.Update(msg) // forward every message, even while hidden
content := pic.View()

// Before removing the picture or quitting:
cmd = tea.Sequence(pic.Close(), tea.Quit)
```

Three rules apply to every component in this module:

1. Forward every message to `Update`.
2. Run every command a method returns, including `Close()`, which deletes
   terminal placements.
3. Call methods from one event loop only, and don't copy a `Picture`.

Your app owns keys, layout, loading and error UI, and visibility.

## Pictures

`SetImage` copies pixels before returning. `SetImageImmutable` skips the copy
when you guarantee the image never changes. Use it for freshly rendered frames,
and use `SetImage` if you reuse a framebuffer.

The previous image stays visible while its replacement is prepared and
transmitted, including during resize. Each picture runs at most one job and
remembers only the latest request. `Options.ClearOnReplace` removes the old
image immediately instead.

`Status()` reports the current and last presented generation, busy state,
errors, and the transport in use. `PresentedMsg` marks a committed frame,
`StaleFrameMsg` reports a frame dropped by `SetImageFor` or
`SetImageImmutableFor` after a geometry change, and `ErrorMsg` reports a failed
preparation.

### Fit, crop, and viewport

- **Fit:** `FitContain`, `FitFill`, and `FitCover`, with anchors and an optional
  background color.
- **Crop** (`SetCrop`, `ResetCrop`): selects a source rectangle in pixels, then
  re-prepares it to fill the picture.
- **Viewport** (`SetViewport`, `ResetViewport`): shows a window of cells from
  the already-prepared image. Scrolling a viewport doesn't re-encode or
  retransmit anything.

```go
cmd = tea.Batch(pic.SetSize(80, 60), pic.SetImage(img))
pic.SetViewport(ntpicture.Viewport{Y: 20, Width: 80, Height: 15})
content := pic.View() // rows 20–34 of the prepared image
```

### Source-rectangle panning

In Kitty mode, `SetImageCropped` / `SetImageImmutableCropped` upload the whole
source once for transparent-background `FitContain`, or `FitFill` with a crop
whose aspect matches the cell rectangle in pixels, when it fits within
`MaxKittyPanPixels` (16 Mi pixels) and `Config.MaxRasterPixels`, if set.
Subsequent `SetCrop` calls update only the virtual placement's source rectangle;
they reuse the image ID and placeholder grid. Crops use source coordinates,
including nonzero image origins. Empty crops return to ordinary full-image fitting.

Panning requires `TerminalState.VirtualPlacement == KittyCapabilitySupported`.
Discovery sends a plain graphics query first and, after its `OK` reply, sends a
separate `e=T,U=1` feature query. Only an `OK` reply echoing `U=1` enables the
optimization. Hosts with verified support can set `TerminalOptions.VirtualPlacement`
explicitly; forcing basic Kitty support alone does not grant it. Unknown or
unsupported features, opaque/translucent backgrounds, mismatched `FitFill` aspect,
oversized sources, and `FitCover` use ordinary per-crop preparation.
StableID dirty-frame edits and playback caching remain separate from whole-source
panning, so changing between them replaces the image as needed.

### Framebuffers and exact pixels

`RasterGeometry()` reports the target cells, pixel size, and physical display
aspect. Render at exactly `Pixels` with `FitFill` and preparation does no
scaling at all.

- For streaming, set `Options.StableID`. Kitty frames then reuse one image ID
  and Bubble Tea redraws nothing.
- `SetResolution` (0–1] and `SetMaxRasterPixels` shrink the Kitty raster
  without changing its cell footprint or aspect.
- `SetImageFor` and `SetImageImmutableFor` drop a frame that was rendered for a
  geometry that has since changed and return a command delivering
  `StaleFrameMsg` with the current `RasterGeometry`.
- `Submit` applies an image and its geometry together, atomically. With
  `Exact: true`, it rejects any image that doesn't match the target pixel size
  instead of resampling it. QR codes use this.

See [examples/framebuffer](./examples/framebuffer/README.md) for a complete
producer.

### Stop, keep, and close

- `SetVisible(false)` removes placements but keeps the source.
- `Stop()` cancels pending work and keeps showing the last frame.
- `Close()` cancels work and deletes placements. It's final.
- `Keep()` and `KeepOnMainScreen()` are also final, but they leave the last
  frame on screen after your program exits. Use `KeepOnMainScreen()` when you
  leave the alternate screen on exit.

```go
return m, tea.Sequence(pic.Keep(), tea.Quit)
```

## Terminal sessions

Pictures created without a session own a private one. To share discovery
across several pictures on one terminal connection, create a session yourself:

```go
session := ntpicture.NewTerminalSession(ntpicture.TerminalOptions{})
a := ntpicture.New(ntpicture.Options{Session: session, AutoMode: true})
b := ntpicture.New(ntpicture.Options{Session: session, AutoMode: true})
cmd := tea.Batch(a.Init(), b.Init()) // one probe

// On exit: close both pictures, run their commands, then session.Close().
```

If the host already knows the terminal's capabilities, call
`SetTerminal(kitty, cellWidth, cellHeight)`, `SetCellSize`, or
`SetKittyCapability`; these skip probing. `ToggleMode()` switches between glyphs
and Kitty when Kitty is supported.

The `NTPICTURE_KITTY` and `NTPICTURE_TMUX_PASSTHROUGH` environment variables
override detection. Values set explicitly in `TerminalOptions` take precedence
over them.

Image IDs come from a process-wide allocator, `NextImageID`. If you mix
ntpicture with other Kitty image producers, give all of them the same allocator.

## Transport

Kitty output defaults to direct transmission (`KittyMediumDirect`) and supports
PNG (`KittyFormatPNG`, default), raw RGBA (`KittyFormatRGBA`), and
zlib-compressed (`KittyFormatZlib`) encodings. Setting
`Config{KittyMedium: KittyMediumSharedMemory}` sends raw RGBA through shared
memory where available: POSIX shared memory natively (CGO is required on macOS)
and the `globalThis.ghosttyKittySharedMemory` registry in the browser. If
shared memory is unavailable or fails, output falls back to direct transmission
in the configured `KittyFormat`. tmux passthrough is supported. Glyph output
uses colored half blocks, two pixel rows per cell.

Source and prepared images are capped at 32 Mi pixels. Pending shared memory is
capped at 64 MiB per process. These caps are safety bounds, not a memory
budget; your app still owns aggregate limits.

## Animation

```go
img, player, err := animation.DecodeContext(ctx, data, animation.Options{Animate: true})
if err != nil { return err }
if player == nil { return showStill(img) } // single frame

ctrl := playback.New(player, playback.Options{})
cmd := tea.Batch(ctrl.Init(), ctrl.SetSize(60, 20))
cmd = ctrl.Update(msg)
cmd = ctrl.Pause(true)
cmd = ctrl.Seek(12)   // one-based
cmd = ctrl.SetRate(2) // 0.25x–4x
cmd = ctrl.Close()
```

The decoders honor frame delays, transparency, disposal, and loop counts. GIF
frames stay paletted, and WebP frames decode lazily. Default limits are
128 MiB input, 32 Mi canvas pixels, 1,000 frames, and 64 MiB of GIF frame
storage; `animation.Options.Limits` can lower them. Playback is
deadline-paced, so a slow terminal slows playback instead of queuing a
backlog. `playback.New` and `playback.NewSource` stream with stable Kitty image
IDs by default (`playback.Options.FreshIDs` opts out; `Options.Picture.StableID`
is ignored). Errors appear in `ctrl.Status().Err`.

### Composites and layers

`animation.NewComposite` places several animated regions on an immutable base
image. Each region keeps its own timing. `Composite.Window` crops and rescales
a composite into a new, smaller source; it's the pixel-level counterpart to a
viewport. `playback.Layers` presents a composite under Kitty by sending the
base once (forcing `FitFill` and a fresh Kitty ID on the base picture so a
composite rendered at `RasterGeometry().Pixels` maps 1:1 onto the cell grid)
and updating only the cell-aligned animated windows (which use stable IDs
unless `Options.FreshIDs` is set). Under glyphs, it animates the flattened
image.

```go
source, err := animation.NewComposite(ctx, base, []animation.Region{
    {Key: "left", Rect: image.Rect(0, 0, 240, 180), Player: gifPlayer},
    {Key: "right", Rect: image.Rect(240, 0, 480, 180), Player: webpPlayer},
}, nil)
layers := playback.NewLayers(source, playback.Options{
    Picture: ntpicture.Options{Session: session},
})
```

### Mixing stills and animations

`*ntpicture.Picture`, `*playback.Controller`, and `*playback.Layers` share one
presentation method set: `Init`, `Update`, `View`, sizing, terminal, mode,
viewport, visibility, `Stop`, `Keep`, `KeepOnMainScreen`, and `Close`. A
document or list can hold any of them behind a small interface of its own.
[examples/animation/scroll.go](./examples/animation/scroll.go) scrolls a
document of mixed images this way.

## Other packages

- `nimbleterminal.dev/ntpicture/decoders` registers PNG, JPEG, GIF, WebP, BMP,
  and TIFF with `image.Decode`.
- `nimbleterminal.dev/ntpicture/legacy` provides the NTCharts `picture.Model`
  API and process-wide capability helpers, for existing NTCharts code. See
  [MIGRATION.md](./MIGRATION.md).

## Examples

```sh
task build-examples   # bin/ntpicture-{still,simple,grid,animation,framebuffer}
task run-still
task serve-wasm-site  # http://localhost:8000/web/
```

| Example | Shows |
| --- | --- |
| `still` | A generated gradient |
| `simple` | An embedded-image viewer |
| `grid` | An interactive `GridImage` board |
| `animation` | GIF and WebP playback, plus `--composite` layers and `--scroll` mixed documents |
| `framebuffer` | A live rendered scene with stable IDs, panning, and resolution steps |

Each example runs natively and in the browser via
[go-booba](https://github.com/NimbleMarkets/go-booba). The examples are a
separate Go module, so booba isn't a dependency of the library.

## Open Collaboration

Run `task ci` before submitting changes. See [DEVELOP.md](./DEVELOP.md) for
tooling, consumer checks, manual acceptance checks, and releases.

Contributions and feedback are welcome. Please follow our [Code of
Conduct](./CODE_OF_CONDUCT.md).

- [Report an issue](https://github.com/NimbleTerminal/ntpicture/issues)
- [Submit a pull request](https://github.com/NimbleTerminal/ntpicture/pulls)

## Acknowledgements

We are very thankful to [Kitty](https://sw.kovidgoyal.net/kitty/) and [Ghostty](https://ghostty.org) and [Charm](https://charm.land) for a powerful platform to build on.

## License

Source code: [MIT License](./LICENSE). Artwork exceptions are listed in
[NOTICE.md](./NOTICE.md); dependency licenses are in
[THIRD_PARTY_NOTICES.md](./THIRD_PARTY_NOTICES.md).

Copyright (c) 2026 [Neomantra Corp](https://www.neomantra.com).

----
Made with :heart: and :fire: by the team behind [Nimble
Terminal](https://nimbleterminal.dev).

### Kitty frame updates and playback caching

StableID updates use Kitty root-frame edits (`a=f,r=1,X=1`) when placement
and raster geometry are unchanged. The previous placement stays live during
chunked uploads. Changes covering less than 75% of the raster transmit their
bounding rectangle; identical pixels produce no transmission. This requires
animation-frame support (Kitty or Ghostty 1.4+). Use fresh IDs for terminals
that support static Kitty graphics but not frame editing.

Playback caches bounded sources exposing one-based `Frame()` and `Frames()`
positions, including `animation.Player`. Those positions must identify immutable
frames within the source. `playback.Options.FrameCacheBytes` limits terminal
uncompressed RGBA storage: zero defaults to 32 MiB, negative disables caching.
FreshIDs and ClearOnReplace disable it. After upload, repeated positions use
only `a=a` controls and skip Picture rasterization/encoding; source decoding and
composition still follow the source's normal timing and seeking semantics.
Out-of-order seeks map source positions to terminal append order. Geometry,
render configuration, viewport, source replacement, and hiding invalidate reuse.

`Config.Transient` adds the `N=1` eviction hint on image creation. FreshIDs
playback enables it automatically. `KittyFormatZlib` continues to emit `o=z`
through both full uploads and frame edits, including direct fallback from shared
memory; Ghostty 1.4's Wuffs decoder needs no separate client option.

For reproducible performance comparisons and terminal workload reports, see
[BENCHMARKS.md](BENCHMARKS.md). The harness keeps output-production timings separate
from visual terminal acceptance.
