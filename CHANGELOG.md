# `ntpicture` CHANGELOG

## Unreleased

- Detect Kitty root-frame edits, frame appending, and frame selection separately.
  StableID updates fall back to full image uploads until the required feature
  replies arrive; playback caching likewise requires append and select support.
  Basic Kitty support and terminal identity no longer imply animation support.

- Preserve FitFill stretching and background compositing when source crops cannot use placement-only panning.
- Retain shared-memory ownership and transport status when selecting cached playback frames, including through Keep.

- Restore bounded whole-source Kitty crop panning: upload once, then reuse the
  image and grid with placement-only crop updates when virtual-placement support
  is confirmed; otherwise retain per-crop preparation.

- Add repeatable presentation/playback benchmarks and a terminal workload runner with JSON timing, bandwidth, and environment reports.

- Update StableID Kitty images in place with dirty rectangles; skip unchanged pixels.
- Cache bounded playback frames in the terminal (32 MiB default) and select cached frames without re-encoding.
- Add `Config.Transient`; FreshIDs playback sends the `N=1` eviction hint.

- Query `e=T,U=1` separately after successful plain Kitty detection and track virtual-placement support for crop panning.
- Coalesce unstarted config/image/size/capability render jobs in place on `Picture` without advancing the Kitty image ID counter or queueing stale deletions.

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
