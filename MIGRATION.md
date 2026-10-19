# Migrating from NTCharts picture

## Choose a migration path

Replacing `github.com/NimbleMarkets/ntcharts/v2/picture` with
`nimbleterminal.dev/ntpicture/legacy` alone does not compile. The compatibility
API is split: models and process-global helpers live in `legacy`, while shared
configuration types and constants live in the root package.

| NTCharts picture API | ntpicture destination |
| --- | --- |
| `picture.New()` | `legacy.NewModel()` |
| `picture.NewWithConfig(cfg)` | `legacy.NewModelWithConfig(cfg)` |
| `Model`, `KittyFrameMsg`, `IsPictureMsg` | `nimbleterminal.dev/ntpicture/legacy` |
| `KittySupported`, `ForceKittyCapability`, `QueryKittySupport`, `RequestCellSize`, `SetTmuxPassthrough` | `legacy` |
| `PictureMode`, `PictureGlyph`, `PictureKitty` | root `ntpicture` |
| `FitMode`, `FitContain`, `FitFill`, `FitCover`, `FitAnchor` and anchor constants | root `ntpicture` |
| `KittyMedium`, `KittyFormat` and their constants | root `ntpicture` |
| `KittyCapability`, `KittyCapabilityUnknown`, `KittyCapabilitySupported`, `KittyCapabilityUnsupported` | root `ntpicture` |
| `Config`, `GridImage`, `NewGridImage` | root `ntpicture` |
| `KittyCapabilityMsg` | root `ntpicture`; also aliased by `legacy` |
| New `Picture`, `Options`, `New`, `TerminalSession`, `NewTerminalSession` APIs | root `ntpicture` |

### Compatibility migration

Use root types plus legacy models/helpers when preserving existing rendering
and frame-message coordination. Keep the host's `legacy.KittyFrameMsg` handling,
forward messages to `legacy.Model.Update`, and execute every returned `tea.Cmd`.
Legacy glyph presentation remains synchronous in View; Kitty preparation and
APC/grid presentation still run through commands and messages. `SetImage`
retains its source without copying: keep source pixels immutable.

The following minimal host fragments show the import and constructor changes.
They implement `tea.Model`; the application supplies an immutable image and
runs the host with Bubble Tea. Returning commands from Init/Update lets Bubble
Tea execute them and deliver their messages back to Update. Calling a setter
and discarding its command is insufficient, even if glyph output appears to
work. Additional host-specific frame coordination belongs around this routing.

Before (NTCharts picture):

```go
package migration

import (
	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
	"image"
)

type host struct {
	pic picture.Model
	img image.Image
}

func newHost(img image.Image) *host {
	return &host{pic: picture.New(), img: img}
}
func (h *host) Init() tea.Cmd {
	return tea.Batch(h.pic.Init(), h.pic.SetFit(picture.FitContain),
		h.pic.SetSize(20, 10), h.pic.SetImage(h.img))
}
func (h *host) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := h.pic.Update(msg)
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		return h, tea.Batch(cmd, h.pic.SetSize(size.Width, size.Height))
	}
	return h, cmd
}
func (h *host) View() tea.View { return h.pic.View() }

var _ tea.Model = (*host)(nil)
```

After (compatibility API):

```go
package migration

import (
	tea "charm.land/bubbletea/v2"
	"image"
	"nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/legacy"
)

type host struct {
	pic legacy.Model
	img image.Image
}

func newHost(img image.Image) *host {
	return &host{pic: legacy.NewModel(), img: img}
}
func (h *host) Init() tea.Cmd {
	return tea.Batch(h.pic.Init(), h.pic.SetFit(ntpicture.FitContain),
		h.pic.SetSize(20, 10), h.pic.SetImage(h.img))
}
func (h *host) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := h.pic.Update(msg)
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		return h, tea.Batch(cmd, h.pic.SetSize(size.Width, size.Height))
	}
	return h, cmd
}
func (h *host) View() tea.View { return h.pic.View() }

var _ tea.Model = (*host)(nil)
```

Both blocks were compiled on 2026-10-18 as separate packages in a temporary
module (`GOWORK=off go test -mod=mod ./...`), using local replacements for the inspected NTCharts and ntpicture checkouts. This
checks the API mapping, not published-module resolution or terminal visuals.

### Full migration

Adopt `ntpicture.New(ntpicture.Options{...})`, which returns `*ntpicture.Picture`,
and optionally a shared `ntpicture.NewTerminalSession`. This requires adapting
the host's presentation lifecycle, not only renaming the constructor:

- Preparation is asynchronous for glyphs as well as Kitty. Execute returned
  commands and forward every message to `Picture.Update`, including internal
  preparation, presentation, and retirement messages. Preserve the returned
  commands alongside host commands with `tea.Batch`. Do not retain a routing
  filter that knows only `legacy.KittyFrameMsg`.
- Use `PresentedMsg` (matching `Owner` and `Generation`) and `Status()` where
  the host previously tracked legacy frame completion. Handle `ErrorMsg`.
  Picture retains the displayed front until its replacement is presented by
  default, coalesces queued replacements, and rejects stale results; adapt
  busy/dirty state and producer pacing to this behavior.
- `SetImage` copies pixels before returning; `SetImageImmutable` retains them
  and requires immutability while Picture or an in-flight command holds them.
  Keep one Picture pointer per component rather than copying its value. Share
  a session pointer across components on the same terminal connection.
- Execute `Close()` while the event loop can still process cleanup; for normal
  quit handling, return `tea.Sequence(pic.Close(), tea.Quit)`. Continue routing
  late results while the loop is alive. Use `Keep`/`KeepOnMainScreen` only when
  intentionally retaining output. The host owns an explicitly supplied session
  and closes it after its components finish; do not close a shared session from
  one component. See README for the complete lifecycle contract.

### Mixed legacy and Picture components

The supported bridge is `legacy.DefaultTerminalSession()`. Legacy models and
process-global helpers already use this session; pass it to new components via
`ntpicture.Options.Session`. Legacy has no setter for attaching an arbitrary
new TerminalSession. Creating an unrelated session for the new component does
not share legacy discovery.

```go
package migration

import (
	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/legacy"
)

type mixed struct {
	old    legacy.Model
	modern *ntpicture.Picture
}

func newMixed() *mixed {
	return &mixed{
		old: legacy.NewModel(),
		modern: ntpicture.New(ntpicture.Options{
			Session: legacy.DefaultTerminalSession(), AutoMode: true,
		}),
	}
}
func (h *mixed) Init() tea.Cmd {
	return tea.Batch(h.old.Init(), h.modern.Init())
}
func (h *mixed) Update(msg tea.Msg) tea.Cmd {
	return tea.Batch(h.old.Update(msg), h.modern.Update(msg))
}
```

This routing fragment is also compile-checked; its outer host must return these
commands to Bubble Tea. Broadcast terminal messages to both components, rather
than routing with mutually exclusive `else if` branches. They share discovery
state, cell geometry, and transport settings; this is not a guarantee that
calling both Init methods emits only one cell-size query. New Picture auto-mode
is opt-in here. Legacy mode selection still follows the host's existing
`KittySupported`/`Toggle` policy. Forward returned capability/session messages;
`legacy.ForceKittyCapability` does not enqueue a notification itself. Do not
close the process-wide compatibility session while other legacy users remain.

## Sibling migration evidence (2026-10-18)

These are observations of local source and tags, not a claim that all sibling
consumers have migrated or that their published modules and visual behavior
were independently validated in this documentation task.

- **ntcharts-osm:** the working tree based on `b397697` (v0.2.0) has migration
  changes that are not in that tag. `mapview/mapview.go` uses root types and
  `legacy.Model` for its buffered-panning/frame coordination. Its public
  `RenderMode`, `FitMode`, and `KittyMedium` aliases and constants shield
  ordinary mapview callers from the import split. The working-tree examples
  module pins **ntcharts3d v0.3.0**; `examples/mapview3d/main.go` passes
  `legacy.DefaultTerminalSession()` via `chart.WithTerminalSession` and routes
  messages to both mapview and the chart. The chart uses the newer presentation
  API while the map source retains compatibility coordination.
- **ntcharts3d:** the local **v0.3.0 tag** already uses `ntpicture v0.1.0` and
  `*Picture`, with shared-session options, `PresentedMsg` pacing, and cleanup
  through `Dispose`. Its changelog records the migration; it is no longer
  pending. The inspected working-tree HEAD is `561bb73`. This task did not
  re-measure presentation cost or visually validate its demos.
- **Gloss:** inspected at `0a59113`, it depends on ntpicture v0.1.0 and uses
  Picture, playback.Controller, and playback.Layers. Typeset composition now
  lives in `internal/app/typeset_layers.go`; the old typeset overlay file is
  absent. Host layout and scrolling policy remain Gloss responsibilities.
- **ntqrcode:** inspected at `c2d7b32`, it depends on ntpicture v0.1.0 and uses
  `Options.HostGeometry` plus `Submit(Frame{..., Exact: true})`. This is already
  implemented, not a proposed migration.
- **NTCharts:** the inspected `../ntcharts/picture` still contains its own
  renderer. `testdata/compat/picture.go` remains a proposed alias adapter used
  by temporary-copy checks; installing it in NTCharts remains separate work.

## Legacy glyph-cache fix (v0.1.1)

In v0.1.0, the public `legacy.Model.View()` and `String()` bridge rendered a
copy of its stored model and discarded the populated glyph cache. Repeated
unchanged presentations consequently read and rendered source pixels again.
The bridge now returns and stores the updated value state, retaining the
immutable presentation without sharing mutable glyph scratch between model
copies. Setters and terminal-message handling still invalidate presentation;
deferred Kitty preparation and stale-frame rejection are unchanged.

ntcharts-osm's `mapview/picture.go` presentation-cache adapter can be removed
once it uses this fix and its existing
`TestGlyphCacheSurvivesValueReceiverView` passes with the adapter bypassed.
On 2026-10-18, the full ntcharts-osm `mapview` test package passed in a
temporary copy using this checkout, with the adapter reduced to an embedded
`legacy.Model` and its regression test unchanged. This is automated coverage,
not visual terminal verification; the reference checkout was not modified.
Keep that regression test. Retain `*legacy.Model` in the host so a value-receiver
host View updates the same picture; copying a Model before its first View does
not populate the original's cache. Continue forwarding messages and executing
returned commands, and keep source pixels immutable until replaced.

The fix is included in `nimbleterminal.dev/ntpicture v0.1.1`; v0.1.0 remains
affected. Consumers can remove the workaround after upgrading and running the
regression test above. Do not treat a local replacement as verification of a
published module.

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
  ntcharts3d v0.3.0 uses the new presentation API while retaining its adaptive
  policy and camera logic. Presentation-cost metrics were not re-measured here.
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

Sibling adoption is in progress, with ntcharts3d migration present in its local
v0.3.0 release tag; see the dated evidence above for checkout-specific status.

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

KittyCapabilityMsg and TerminalSessionMsg report probe resolution through the
Bubble Tea loop. `legacy.Model.Update` and `Picture.Update` return notification
commands on transitions; Init/query also reports already-known results. Late
positive replies may follow a timeout; hosts must tolerate duplicates. Examples
use an explicit `TerminalSession` and select Kitty from `TerminalSessionMsg`
instead of polling on every update. Legacy capability state lives in the default
session (`legacy.DefaultTerminalSession`); `legacy.ForceKittyCapability` does
not itself enqueue a message.
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
for scrolling. See README's "Fit, crop, and viewport" and "Composites and
layers" sections.

Gloss has migrated its typeset/composite consumers to `playback.Layers`. Keep
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
Gloss now owns viewport layout and scroll-settle policy around its layers.
This documentation audit does not establish native/browser scrolling parity.

Retirement currently uses a per-Picture grace period and a bounded old-placement
list. Validate native and browser visual transitions before replacing this with
any stronger frame boundary. Keep now supplies the leave-displayed lifecycle;
Gloss still owns its final layout and screen policy. StableID now avoids
per-frame allocation at unchanged geometry. New placements still consume fresh
IDs and fail rather than wrapping the 24-bit range. Validate native Kitty and
ghostty-web visual parity before migrating high-rate consumers.

## Remaining consumer work

Install the alias adapter in NTCharts and update nested module pins. Move
private renderer tests to this module; retain external compatibility tests in
NTCharts. Keep heatpicture and chartpicture as NTCharts components and leave
pictureurl's fetch/cache policy there. Static decoders can forward their blank
import.

Gloss now uses Picture and playback for stills, standalone animation, Markdown,
and typeset layers. Preserve its preview and headless-export policies when
updating those integrations; the source audit here does not validate exports.

ntcharts3d's v0.3.0 migration already replaces encode/present coordination with
Picture while retaining chart scheduling and camera policy. ntqrcode already
uses HostGeometry and exact atomic Frame submission. Continue validating these
integrations' pacing, exact QR pixels, resize behavior, and cleanup; do not
repeat their migrations based on the earlier pending-work descriptions.

Validate the complete native/glyph/browser and cleanup matrix before removing
old implementations. Use each repository's documented CI and generated-artifact
conventions. Existing compatibility tests do not prove visual terminal parity.
