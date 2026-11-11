# ntpicture — Terminal images for Bubble Tea

<p>
    <a href="https://pkg.go.dev/nimbleterminal.dev/ntpicture"><img src="https://pkg.go.dev/badge/nimbleterminal.dev/ntpicture.svg" alt="Go Reference"></a>
    <a href="https://github.com/NimbleTerminal/ntpicture/actions/workflows/ci.yml"><img src="https://github.com/NimbleTerminal/ntpicture/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
    <a href="./CODE_OF_CONDUCT.md"><img src="https://img.shields.io/badge/Contributor%20Covenant-2.1-4baaaa.svg" alt="Code Of Conduct"></a>
</p>

Show still images, GIF/WebP animations, and live framebuffers in a
[Bubble Tea v2](https://github.com/charmbracelet/bubbletea) app. ntpicture uses
Kitty graphics where available and colored half-block glyphs everywhere else.

No Bubble Tea program yet? `ntpicture.Print(os.Stdout, img, ntpicture.RenderOptions{})`
shows an image from any Go program: see
[Rendering without Bubble Tea](#rendering-without-bubble-tea).

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

## Rendering without Bubble Tea

Use ntpicture instead of writing your own half-block or Kitty renderer. You do
not need a Bubble Tea program to use the basic rendering functions.

```go
package main

import (
    "image"
    _ "image/png"
    "log"
    "os"

    "nimbleterminal.dev/ntpicture"
)

func main() {
    f, err := os.Open("photo.png")
    if err != nil {
        log.Fatal(err)
    }
    defer f.Close()
    img, _, err := image.Decode(f)
    if err != nil {
        log.Fatal(err)
    }
    // Kitty graphics where the terminal supports them, half-block glyphs elsewhere.
    if err := ntpicture.Print(os.Stdout, img, ntpicture.RenderOptions{}); err != nil {
        log.Fatal(err)
    }
}
```

`Print` leaves the image in the scrollback. `Render` returns the output as a
string of `Height` lines, plus a cleanup function that deletes a Kitty image:

```go
out, cleanup, err := ntpicture.Render(img, ntpicture.RenderOptions{
    Width: 40,                   // cells; Height follows from the aspect ratio
    Fit:   ntpicture.FitContain, // or FitFill, FitCover
})
if err != nil {
    return err
}
defer cleanup() // removes the image; skip it to keep the image on screen
fmt.Println(out)
```

`RenderOptions`:

| Field | Default |
| --- | --- |
| `Width`, `Height` | Cells. One follows from the other; with neither, the image's own size, shrunk to the terminal |
| `Fit` | `FitContain` |
| `Mode` | `RenderAuto`; `RenderGlyph` and `RenderKitty` skip detection |
| `Background` | Transparent under Kitty, black in glyphs |
| `CellPixelWidth`, `CellPixelHeight` | Asked from the terminal, then 8x16 |
| `Kitty` | `KittyCapabilityUnknown` (detect); `Supported` or `Unsupported` skip detection |
| `Output` | Where cleanup writes the delete sequence; `os.Stdout` |

With `RenderAuto`, the first call asks the controlling terminal whether it
supports Kitty graphics. That briefly puts the terminal in raw mode and waits
up to one second for a reply; the answer is cached for the process. Detection
runs on Linux, macOS, and the BSDs. Elsewhere, and when there is no terminal,
`RenderAuto` uses glyphs unless `Kitty` or `NTPICTURE_KITTY=supported` says
otherwise. `Print` to a file that is not a terminal also uses glyphs.

Detection reads terminal input, so inside a Bubble Tea program use `Picture`
(below) instead. These functions render one still image; animation, live
replacement, and shared memory need `Picture`.

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

`SetCrop` prepares and uploads the cropped pixels. Unicode-placeholder feature
support does not imply source-rectangle support on virtual placements. For
scrolling without image uploads, use `SetViewport` over a prepared placement.

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

With `AutoMode`, pictures use glyphs until the terminal confirms Kitty support.
Explicitly disabling Kitty skips its probes. Close a shared session on shutdown
after closing its pictures and running their cleanup commands.

See [Kitty graphics](./KITTY.md) for capability detection and overrides,
transport formats, shared-memory cleanup, tmux, frame updates, and playback
caching.

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

For terminal frame caching and its memory limit, see
[playback caching](./KITTY.md#playback-caching).

### Mixing stills and animations

`*ntpicture.Picture`, `*playback.Controller`, and `*playback.Layers` share one
presentation method set: `Init`, `Update`, `View`, sizing, terminal, mode,
viewport, visibility, `Stop`, `Keep`, `KeepOnMainScreen`, and `Close`. A
document or list can hold any of them behind a small interface of its own.
[examples/animation/scroll.go](./examples/animation/scroll.go) scrolls a
document of mixed images this way.

## Sandboxed decoding

`decoders/worker` decodes untrusted still images in a separate process
confined by [ntmediabox](https://github.com/NimbleTerminal/ntmediabox) and
[ntconfine](https://github.com/NimbleTerminal/ntconfine): Landlock plus
seccomp on Linux, `sandbox-exec` on macOS, and no enforcement on Windows. The
host treats the worker as hostile. Every length and dimension it sends is
checked against caps you choose (defaults: 16 Mi pixels, 8192 per side, 10 s)
before memory is allocated, the returned format name must be one of the six
the worker can produce, and a timeout or protocol violation kills the worker.

```go
// Host: one worker per session; requests on a session are serialized.
s, err := worker.Start(worker.Config{
    WorkerPath: exe, WorkerArgs: []string{"--decode-worker"}, StagePath: exe,
})
if err != nil { return err }
defer s.Close()
if !s.Enforced() { log.Println("decoding without OS confinement:", s.Support().Detail) }
img, format, err := s.Decode(ctx, data)
```

Your binary plays three roles: host (above), Linux stage (handle
`ntmediabox.StageArg` in `main`, or ship `ntconfine-stage` beside the binary
and leave `StagePath` empty), and worker (call `worker.Serve` on
`ntmediabox.WorkerConn()` when `--decode-worker` is set). The worker binary
must be a regular file owned by you or root and not group- or world-writable.
After a timeout the session is closed and must be started again.

What this does not cover. `Config.AllowUnsandboxed` runs the worker with no
OS confinement when enforcement is unavailable, and nothing stops the call;
check `Session.Support` or `ntmediabox.Probe` before relying on it. GIF and
WebP animation decoding in the `animation` package stays in-process. The
worker shares the host kernel; ntconfine's SECURITY.md has the full threat
model and syscall policy.

Importing `decoders/worker` links ntmediabox and ntconfine, which are
AGPL-3.0 with a linking exception that covers MIT modules such as this one.
The plain `decoders` blank import does not link them.

## Other packages

- `nimbleterminal.dev/ntpicture/decoders` registers PNG, JPEG, GIF, WebP, BMP,
  and TIFF with `image.Decode`. Its `worker` subpackage decodes untrusted
  images in an OS-confined subprocess (see [Sandboxed decoding](#sandboxed-decoding)).
- `nimbleterminal.dev/ntpicture/legacy` provides the NTCharts `picture.Model`
  API and process-wide capability helpers, for existing NTCharts code. See
  [MIGRATION.md](./MIGRATION.md).

## Examples

```sh
task build-examples   # bin/ntpicture-{still,simple,grid,animation,framebuffer,print}
task run-still
task serve-wasm-site  # http://localhost:8000/web/
GOWORK=off go -C examples run ./print photo.png   # no Bubble Tea: Print and Render
```

| Example | Shows |
| --- | --- |
| `still` | A generated gradient |
| `simple` | An embedded-image viewer |
| `grid` | An interactive `GridImage` board |
| `animation` | GIF and WebP playback, plus `--composite` layers and `--scroll` mixed documents |
| `framebuffer` | A live rendered scene with stable IDs, panning, and resolution steps |
| `print` | An image printed without Bubble Tea |

`print` is a plain command-line program that shows an image with `Print`, or
with `Render` and its cleanup function under `--transient`. Every other example
runs natively and in the browser via
[booba](https://github.com/NimbleTerminal/booba). The examples are a
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
