# Extraction status

## Implemented in this checkout

- NTCharts renderer, fit/cell/grid geometry, glyph output, Kitty encodings,
  shared-memory transport, platform yields and corresponding tests extracted.
- Low-level Model API preserved for aliases, with explicit constructor names.
- Picture owns immutable/copying submissions, retained fronts, one active job,
  latest-request coalescing, transmission ordering, retirement, visibility and
  late cleanup. Presentation state is private.
- Fresh single-ID allocation, failure reporting and bounded Picture geometry.
- A native example, browser workspace and temporary consumer compatibility checks.

No sibling repository has been modified or released.

## Next: terminal-independent animation

Extract Gloss's clip/player, GIF/WebP decoder and generic compositor into
`animation`, retaining lazy WebP loading, disposal/loop semantics and limits:
128 MiB input, 32 Mi canvas pixels, 1,000 frames and 64 MiB GIF frame storage.
Publish validated configurable limits without an accidental unlimited zero value.
Make clip storage private or defensively snapshot construction input. Report
lazy failures with frame index and cause. Check WebP bitstream dimensions before
pixel allocation and enforce the input bound in GIF preflight.

Carry contexts through decoding, seek, composition and lazy loads. Bound jobs
through non-interruptible library calls. Preserve the distinction between frame
storage accounting and peak working memory. Generic keyed rectangular
composition can move; document destinations, deduplication, cell grouping and
reflow mapping remain in Gloss.

## Next: Bubble Tea playback and terminal sessions

Add `playback.Controller`, owning a Picture and routing its messages. It owns
cancellable timing, pause/rate/seek mechanics and presentation backpressure.
Gloss supplies visibility and owns keys, seek-bar UI, preview behavior, exports,
document loading/layout, selection, search and aggregate budgets.

Introduce explicit terminal session state for capability/probe/tmux/shared
transport configuration. Make legacy package helpers delegate to one default
session. Preserve old environment variables and message/type identity while old
and new imports coexist. New Picture's SetTerminal already accepts host-provided
capability without consulting global detection, but the legacy probe, tmux and
native shared-memory capability are still shared globals.

Retirement currently uses a per-Picture grace period and a bounded old-placement
list. Validate native and browser visual transitions before replacing this with
any stronger frame boundary. Add an explicit leave-displayed lifecycle for
Gloss's main-screen exit. Decide on tested ID reuse/stable-ID streaming before
promising indefinite high-rate playback; allocation currently fails rather
than wrapping the 24-bit range.

## Then: migrate consumers

Install the alias adapter in NTCharts and update nested module pins. Move private
renderer tests to this module; retain external compatibility tests in NTCharts.
Keep heatpicture and chartpicture as NTCharts components and leave pictureurl's
fetch/cache policy there. Static decoders can forward their blank import.

Migrate Gloss's changing stills first, then standalone playback and Markdown
regions. Remove Animator's mutable public-state access and Transmission wrapping.
Keep previews and headless exports on their established first-frame policy.

Replace NTCharts3d's encode/present coordination while keeping render busy/dirty
state, frame snapshots, quality adaptation, framebuffer cap, camera and fallback
policy. Its wireframe canvas still requires NTCharts. QR keeps direct module
glyphs, the quiet zone, unfiltered pixels and immediate clearing on failed fit.

Validate the complete native/glyph/browser and cleanup matrix before removing
old implementations. Use each repository's documented CI and generated-artifact
conventions. Existing compatibility tests do not prove visual terminal parity.
