# Extraction status

## Implemented in this checkout

- NTCharts renderer, fit/cell/grid geometry, glyph output, Kitty encodings,
  shared-memory transport, platform yields and corresponding tests extracted.
- Low-level `Model` API and process-wide capability/tmux helpers isolated in
  `nimbleterminal.dev/ntpicture/legacy` for aliases, keeping the root
  `ntpicture` package focused on `Picture`, `TerminalSession`, and `GridImage`
  with no `init()` environment side effects. Environment overrides use
  `NTPICTURE_KITTY` and `NTPICTURE_TMUX_PASSTHROUGH`.
- Picture owns immutable/copying submissions, retained fronts, one active job,
  latest-request coalescing, transmission ordering, retirement, visibility and
  late cleanup. Presentation state is private.
- Picture and playback Keep stop work and preserve the last presented view,
  retransmitting shared-memory fronts directly before releasing buffers. Direct
  fronts skip retransmission unless an accepted stable-ID update must be undone.
  The animation demo offers Gloss-style Q to keep its main-screen output.
- Optional StableID streaming reuses Kitty IDs at unchanged geometry; cancelled
  borrowers cannot delete the displayed image. Playback now defaults to this
  path, with FreshIDs as an explicit opt-out. Standalone Picture keeps its
  retained-replacement default; continuously supplied framebuffers opt in.
- Picture and playback expose `RasterGeometry` and runtime `SetResolution` so
  producers can render at the encoded target without changing cell layout.
  Kitty rounds the full scaled raster; `legacy.Model` keeps per-cell rounding.
  ntcharts3d can keep its adaptive policy and camera logic while using these
  dimensions. Its consumer migration and presentation-cost metrics remain open.
- Fresh single-ID allocation, failure reporting and bounded Picture geometry.
  Glyph bounds use the effective raster independently of terminal cell size.
  FitCover samples the visible area without allocating the overflowing raster.
  Playback recovers corrected presentation errors without clearing decode errors.
- A native example, browser workspace and temporary consumer compatibility
  checks.
- Glyph rendering prepares directly at half-block resolution and emits ANSI
  without runtime pixterm (`5c0bf14`). Pixterm has also been removed from test
  dependencies; frozen reference fixtures preserve fit/anchor, transparency,
  and legacy comparisons. Benchmarks now measure ntpicture's own encoder.

No sibling repository has been modified or released.

## Implemented: animation and playback

- `animation` contains Gloss's bounded GIF/WebP decoding and clip/player logic,
  without terminal dependencies. GIF stays paletted; WebP retains compressed
  input and decodes frames lazily. Input buffers and construction pixels are
  owned snapshots, and published player images expose read-only views.
- Default resource ceilings remain 128 MiB input, 32 Mi canvas pixels, 1,000
  frames and 64 MiB GIF frame storage. Callers can tighten them with Limits. GIF
  preflight also enforces input size; WebP checks bitstream dimensions before
  pixel decoding and caps container chunk counts.
- Context-aware decode, advance, restart and seek report indexed lazy failures.
  Standard codecs cannot be forcibly interrupted between reader calls; each
  playback controller allows only one outstanding composition job.
- `playback.Controller` owns a Picture, private playback state, cancellable
  timing, coalesced seeks, visibility and presentation pacing. It reuses
  Picture's replacement and cleanup. Multiple controllers route independently.
  Frame deadlines carry through rendering/output instead of adding a full delay
  after each presentation; control changes reset the timeline.
- The animation example plays Gloss's original GIF/WebP orbit fixtures and
  exposes controls through the browser's booba action bar.

## Implemented: discovery bridge and cell-size handling

KittyCapabilityMsg reports probe resolution through the Bubble Tea loop.
`legacy.Model.Update` and `Picture.Update` return notification commands on
transitions; Init/query also reports already-known results. Late positive
replies may follow a timeout; hosts must tolerate duplicates. Examples select
Kitty from this message instead of polling on every update. Legacy capability
state lives in the default session (`legacy.DefaultTerminalSession`);
`legacy.ForceKittyCapability` does not itself enqueue a message.
`legacy.Model.Update` now returns a command for capability transitions where it
previously returned nil; the NTCharts adapter migration must preserve and
execute these commands and update any tests that assumed nil.

Picture ignores zero/negative terminal cell-size reports without cancelling or
dropping a pending source. Explicit SetTerminal calls still reject bad geometry.

## Stable-ID streaming: reviewer evidence

For the TinyBubbles 240×68-cell framebuffer case, a reviewer reported these
Kitty output totals from a forced-capability harness, approximately 30 frames
per run, after the stable-ID change (`9ad2210`):

| Picture implementation | Total bytes | Placeholder cells | Image IDs |
| --- | ---: | ---: | ---: |
| NTCharts v2.8.0 | 570 KB | 16,320 | 1 |
| ntpicture, fresh IDs | 4.6 MB | 505,920 | 32 |
| ntpicture, StableID | 390 KB | 16,320 | 2 |

StableID emitted one full placeholder grid, matching NTCharts, and reduced
reported total output by about 92% compared with fresh IDs. These are external
reviewer measurements, not a locally reproduced benchmark. The exact harness,
frame counts, encoding settings, and capability/resize event sequence have not
been recorded here. Preserve those with a reproducible regression case before
treating the totals as a performance baseline. A cell-geometry change after the
first Kitty placement explains why a stable stream can legitimately use a second
ID. Initial glyph placement itself allocates no Kitty ID. NTCharts Model's
transitional glyph fallback (absent from Picture) is a plausible source of extra
output in the NTCharts run; the exact event and payload breakdown still needs
the harness trace to confirm both explanations.

The local `TestStreamingTraffic240x68` separately measures image protocol bytes
and changed View content; it does not measure Bubble Tea's total terminal
output. Neither measurement proves native Kitty or ghostty-web visual
correctness.

Visual acceptance results and remaining coverage are maintained in
[DEVELOP.md](./DEVELOP.md#visual-verification).

## Next: validation and consumer migration

The first implementation priority is installing the NTCharts alias adapter, to
stop maintaining two renderer copies. The fixture now also aliases
KittyCapabilityMsg. Temporary-copy consumer checks validate the proposed adapter
but do not install it in NTCharts.

1. Verify native Kitty and ghostty-web visually with StableID both enabled and
   disabled: animation/mesh updates, resize and cell geometry changes, multiple
   pictures, hide/show, cancellation, close, and keep-screen. Exercise direct
   transport, shared memory, and shared-memory fallback, plus glyph fallback.
   Native shared-memory tests need an environment permitting POSIX shared
   memory.
2. Capture the reviewer's reproducible output harness and account for startup
   and steady-state traffic separately. Confirm the startup geometry transitions
   and transitional glyph output.
3. Install NTCharts compatibility adapters and migrate real consumers as
   outlined below. Existing automated compatibility checks are a starting point;
   run each consumer's own tests and demos before removing its previous
   implementation.

## Release prerequisites

- Confirm `https://nimbleterminal.dev/ntpicture?go-get=1` serves the correct
  go-import metadata before tagging. A local replace directive is not a release
  validation; verify module resolution from a clean consumer outside the
  workspace.
- Browser builds still require wasm.work's explicit Bubble Tea replacement:
  `github.com/neomantra/bubbletea/v2 v2.0.0-20261009201241-c397c32a5074`. Keep
  that pin and its bridge checks until an upstream/browser-compatible
  replacement passes the demo and visual matrix. Native builds use upstream.

## Remaining architecture work

Generic rectangular composition is in animation.Composite, scheduled through
animation.Source, playback.NewSource, and playback.NewLayers. Player implements
the same immutable, context-aware source contract; standalone constructors and
seeking remain available. `playback.Layers` owns cell-aligned overlay planning
(defaulting to `FitFill` so composites rendered at `RasterGeometry().Pixels` map
1:1 onto the cell grid), overlapping-window merging, shared clocks across
repeated region keys, reduced-resolution enlarged overlays, Kitty placeholder
splicing, glyph fallback, playback activation (`SetActive`), zero-pixel position
harvesting (`Positions` and `Positions.WithPositions`), off-loop `SetComposite`
position carry and window cropping, committed-frame export (`Snapshot`), and
multi-layer `Close`/`Keep` over a host-owned `TerminalSession`. Window
crops/resamples a pixel source to reduce future composition/transmission;
Viewport clips its prepared cell presentation without retransmitting pixels just
for scrolling. See README's "Window versus Viewport" and "Layered composite
playback" sections.

Gloss still needs to migrate its typeset/composite consumers to `playback.Layers`
and remove `internal/animate` and `internal/app/typeset_overlay.go`. Keep
document destinations, deduplication, the eight-destination/64 MiB budgets,
document layout, scrolling, scroll-settle debounce, controls, exports, and
visibility policy in Gloss. Build new composites off the UI loop with
`layers.Positions()` and pass them to `layers.SetComposite`; reloads replace
clip identity.

Explicit TerminalSession now isolates discovery, cell geometry, tmux wrapping,
and shared-memory capability for pictures that opt in. `legacy.Model` and
package helpers in `ntpicture/legacy` retain their compatibility state through
`legacy.DefaultTerminalSession`. Migrate hosts to one explicit session per
terminal; process-wide resource budgets remain intentional. The animation
example shares one session across both controllers.

Viewport now clips prepared Kitty/glyph content without bitmap retransmission.
It supports source-cell offsets in both axes, with clipping at layout bounds.
Gloss still needs to map document regions into viewports and validate scrolling
in its own native/browser layouts; this API alone does not migrate that policy.

Retirement currently uses a per-Picture grace period and a bounded old-placement
list. Validate native and browser visual transitions before replacing this with
any stronger frame boundary. Keep now supplies the leave-displayed lifecycle;
Gloss still owns its final layout and screen policy. StableID now avoids
per-frame allocation at unchanged geometry. New placements still consume fresh
IDs and fail rather than wrapping the 24-bit range. Validate native Kitty and
ghostty-web visual parity before migrating high-rate consumers.

## Then: migrate consumers

Install the alias adapter in NTCharts and update nested module pins. Move
private renderer tests to this module; retain external compatibility tests in
NTCharts. Keep heatpicture and chartpicture as NTCharts components and leave
pictureurl's fetch/cache policy there. Static decoders can forward their blank
import.

Migrate Gloss's changing stills first, then standalone playback and Markdown
regions. Remove Animator's mutable public-state access and Transmission
wrapping. Keep previews and headless exports on their established first-frame
policy.

Replace NTCharts3d's encode/present coordination while keeping render busy/dirty
state, frame snapshots, quality adaptation, framebuffer cap, camera and fallback
policy. The inspected local NTCharts3d checkout now imports ntcanvas for its
wireframe renderer through a sibling replace directive; the temporary consumer
harness copies that sibling too. QR keeps direct module glyphs, the quiet zone,
unfiltered pixels and immediate clearing on failed fit. QR can replace its
geometry-event interception and clear/SetTerminal/SetImage sequence with
`Options.HostGeometry` and one `Submit(Frame{..., Exact: true})`.

Validate the complete native/glyph/browser and cleanup matrix before removing
old implementations. Use each repository's documented CI and generated-artifact
conventions. Existing compatibility tests do not prove visual terminal parity.
