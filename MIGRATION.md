# Extraction status

## Implemented in this checkout

- NTCharts renderer, fit/cell/grid geometry, glyph output, Kitty encodings,
  shared-memory transport, platform yields and corresponding tests extracted.
- Low-level Model API preserved for aliases, with explicit constructor names.
- Picture owns immutable/copying submissions, retained fronts, one active job,
  latest-request coalescing, transmission ordering, retirement, visibility and
  late cleanup. Presentation state is private.
- Picture and playback Keep stop work and preserve the last presented view,
  retransmitting Kitty frames directly before releasing shared buffers.
  The animation demo offers Gloss-style Q to keep its main-screen output.
- Optional StableID streaming reuses Kitty IDs at unchanged geometry; cancelled
  borrowers cannot delete the displayed image. Animation demos use this path.
- Fresh single-ID allocation, failure reporting and bounded Picture geometry.
- A native example, browser workspace and temporary consumer compatibility checks.

No sibling repository has been modified or released.

## Implemented: animation and playback

- `animation` contains Gloss's bounded GIF/WebP decoding and clip/player logic,
  without terminal dependencies. GIF stays paletted; WebP retains compressed
  input and decodes frames lazily. Input buffers and construction pixels are
  owned snapshots, and published player images expose read-only views.
- Default resource ceilings remain 128 MiB input, 32 Mi canvas pixels, 1,000
  frames and 64 MiB GIF frame storage. Callers can tighten them with Limits.
  GIF preflight also enforces input size; WebP checks bitstream dimensions
  before pixel decoding and caps container chunk counts.
- Context-aware decode, advance, restart and seek report indexed lazy failures.
  Standard codecs cannot be forcibly interrupted between reader calls;
  each playback controller allows only one outstanding composition job.
- `playback.Controller` owns a Picture, private playback state, cancellable
  timing, coalesced seeks, visibility and presentation pacing. It reuses
  Picture's replacement and cleanup. Multiple controllers route independently.
- The animation example plays Gloss's original GIF/WebP orbit fixtures and
  exposes controls through the browser's booba action bar.

## Next: composition, terminal sessions, and consumers

Generic multi-region rectangular composition is still in Gloss. Extract it
when migrating typeset regions; document destinations, deduplication, cell
mapping and reflow remain application policy. The demo currently uses two
independent controllers. Gloss supplies keys, seek-bar UI, preview behavior,
exports, loading/layout, selection, search and aggregate budgets.

Introduce explicit terminal session state for capability/probe/tmux/shared
transport configuration. Make legacy package helpers delegate to one default
session. Preserve old environment variables and message/type identity while old
and new imports coexist. New Picture's SetTerminal already accepts host-provided
capability without consulting global detection, but the legacy probe, tmux and
native shared-memory capability are still shared globals.

Retirement currently uses a per-Picture grace period and a bounded old-placement
list. Validate native and browser visual transitions before replacing this with
any stronger frame boundary. Keep now supplies the leave-displayed lifecycle;
Gloss still owns its final layout and screen policy. StableID now avoids
per-frame allocation at unchanged geometry. New placements
still consume fresh IDs and fail rather than wrapping the 24-bit range. Validate
native Kitty and ghostty-web visual parity before migrating high-rate consumers.

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
