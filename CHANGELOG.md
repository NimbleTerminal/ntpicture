# `ntpicture` CHANGELOG

## Unreleased

- Synchronize complete Picture uploads, including restoration and Keep, to prevent
  black flashes during chunked same-ID replacement on Ghostty 1.4 and Rex.

- Probe Kitty graphics support on unknown terminals instead of requiring a known
  environment identifier; preserve explicit overrides and glyph fallback.

## v0.1.3 (2026-10-28)

- Remove unsupported placement-only Kitty panning and `MaxKittyPanPixels`.
  Crops always prepare the requested pixels; use `SetViewport` for scrolling
  without image uploads.
- Update go-booba to v0.10.5, Bubble Tea to v2.1.1, Ultraviolet, and golden-test dependencies; align the WASM Bubble Tea fork with go-booba.

## v0.1.2 (2026-10-28)

- Pan cropped Kitty images without retransmitting pixels: upload a bounded
  source once, then update its virtual placement while retaining the image ID
  and placeholder grid. Sources are capped at 16 Mi pixels and any configured
  raster limit. Unsupported placements, oversized sources, and crops requiring
  stretching or background compositing retain per-crop preparation.
- Detect virtual placements, root-frame edits, frame appending, and frame
  selection independently through Kitty feature queries. Optimizations require
  positive replies with the requested feature fields; basic graphics support
  and terminal names or versions do not enable them. Missing or unsupported
  replies keep the compatible rendering path active.
- Update `StableID` images with dirty rectangles and skip unchanged pixels when
  frame editing is supported. Otherwise, send full image replacements under the
  same stable ID.
- Cache bounded playback frames in the terminal and select previously uploaded
  frames without re-encoding when both frame appending and selection are
  supported. `playback.Options.FrameCacheBytes` defaults to 32 MiB; negative
  values disable caching.
- Add `Config.Transient` for the Kitty `N=1` eviction hint. Fresh-ID playback
  marks its images transient.
- Coalesce unstarted image, configuration, size, and capability render jobs
  without consuming additional image IDs or queuing stale deletions.
- Preserve shared-memory ownership and transport status when selecting cached
  playback frames, including during `Keep`.
- Add repeatable presentation and playback benchmarks, plus a terminal workload
  runner that records JSON timing, bandwidth, and environment reports.

## v0.1.1 (2026-10-19)

- Avoid per-pixel allocations when packing opaque NRGBA images for Kitty zlib output.
- Fix `legacy.Model.View` and `String` discarding the glyph cache at the
  package bridge. Unchanged presentations now reuse rendered content without
  reading source pixels, while model copies and deferred Kitty renders retain
  their existing ownership behavior.
- Document NTCharts compatibility and full migration paths, shared sessions,
  and current consumer adoption.

## v0.1.0 (2026-10-17)

**First release of `ntpicture`!** [NimbleTerminal](https://nimbleterminal.dev) is proud to share this terminal image rendering library, focusing on the [Kitty terminal graphics protocol](https://sw.kovidgoyal.net/kitty/graphics-protocol/).

We gained tremendous experience and quality code from the development of [`ntcharts.Picture`](https://github.com/NimbleMarkets/ntcharts/blob/v2/examples/README.md#picture),
especially the performance engineering of our [GPU Shader example](https://nimblemarkets.github.io/ntcharts/demos/shaders/). It started at 4 frames-per-second native and is now >100 fps! We also gained experience from our NTCharts media widgets, `ntcharts-svg` and `ntcharts-pdf`, which layer over it. Our [`gloss` tool](https://nimbleterminal.github.io/gloss/) surfaced animation, GIF/WebP decoding, and more advanced compositing features. Finally, our [3D engine `nt3d`](https://nimbleterminal.github.io/nt3d/) and [`ntcharts3d` widget](https://nimblemarkets.github.io/ntcharts3d/) further pushed optimization and usability.

The whole time we also focused on WASM compatibility and browser embedding with our [`go-booba` tooling](https://github.com/NimbleMarkets/go-booba) and contributions to [TinyGo](https://tinygo.org). Our [`ntpicture` browser demo](https://nimbleterminal.github.io/ntpicture/) and all the NimbleTerminal and NimbleMarkets demos leverage this to help users understand the capabilities.

Here's what to expect:
* `Picture`: a Bubble Tea v2 image component with Kitty graphics and
  half-block glyph fallback. It keeps the old image on screen while preparing
  a new one, coalesces updates, rejects stale results (`StaleFrameMsg`), and
  cleans up placements. Supports fit modes, crop, viewports, stable-ID
  streaming, resolution control, atomic `Submit`, and `Stop` / `Keep` /
  `KeepOnMainScreen` / `Close`.
* `TerminalSession`: per-connection discovery of Kitty, shared-memory, and
  cell-size support, with `NTPICTURE_KITTY` and `NTPICTURE_TMUX_PASSTHROUGH`
  overrides. No import-time side effects.
* Kitty transport: PNG, RGBA, and zlib encodings, plus native and browser
  shared memory with direct fallback, and tmux passthrough.
* Examples (still, simple, grid, animation, framebuffer) for native terminals
  and the [browser demo](https://nimbleterminal.github.io/ntpicture/).

* `package animation`: bounded GIF and lazy WebP decoding, and immutable composites
  of several animated regions.
* `package playback`: deadline-paced `Controller` and Kitty-overlay `Layers`, sharing
  one presentation method set with `Picture`.
* `package decoders`: optional static image format registration.
* `package legacy`: adapters for users of the [NTCharts `picture.Model` API](https://pkg.go.dev/github.com/NimbleMarkets/ntcharts/v2@v2.8.0/picture) and process-wide capability helpers.
  See [MIGRATION.md](./MIGRATION.md).
