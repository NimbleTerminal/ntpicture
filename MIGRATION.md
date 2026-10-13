# Extraction status

## Implemented in this checkout

- NTCharts renderer, fit/cell/grid geometry, glyph output, Kitty encodings,
  shared-memory transport, platform yields and corresponding tests extracted.
- Low-level Model API preserved for aliases, with explicit constructor names.
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
- Fresh single-ID allocation, failure reporting and bounded Picture geometry.
- A native example, browser workspace and temporary consumer compatibility checks.
- Glyph rendering prepares directly at half-block resolution and emits ANSI
  without runtime pixterm (`5c0bf14`), with fit/anchor comparison tests and
  benchmarks. Keep this optimization covered during consumer migration.

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
  Frame deadlines carry through rendering/output instead of adding a full delay
  after each presentation; control changes reset the timeline.
- The animation example plays Gloss's original GIF/WebP orbit fixtures and
  exposes controls through the browser's booba action bar.

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
treating the totals as a performance baseline. A cell-geometry change after the first Kitty placement explains why a stable
stream can legitimately use a second ID. Initial glyph placement itself allocates
no Kitty ID. NTCharts Model's transitional glyph fallback (absent from Picture)
is a plausible source of extra output in the NTCharts run; the exact event and
payload breakdown still needs the harness trace to confirm both explanations.

The local `TestStreamingTraffic240x68` separately measures image protocol bytes
and changed View content; it does not measure Bubble Tea's total terminal output.
Neither measurement proves native Kitty or ghostty-web visual correctness.

## Visual verification: 2026-10-12

Environment: macOS 27.0.1, Chrome 155.0.8059.40, local builds from `9ad2210`,
served at `http://127.0.0.1:8765/web/`. The booba renderer HUD identified the
actual backend. Results below were observed in the browser UI, not inferred
from protocol tests.

| Scenario | Observed result |
| --- | --- |
| Animation, default WebGPU | GIF and WebP rendered side by side and advanced; both paused and stepped correctly. |
| Animation, WebGPU resize | Page zoom 100% → 80% → 100% resized the terminal presentation; pictures remained correctly separated and rendered after resizing. Native window-drag resize was not tested. |
| Animation, WebGPU rapid controls | Repeated first/next-frame requests followed by resume settled into normal playback. This does not establish that a particular cancellation race occurred. |
| Animation, WebGPU keep-screen | Session ended, playback controls disappeared, both pictures remained visible, terminal help disappeared, and booba printed “Connection closed.” below the retained layout. |
| Animation, Canvas2D | Both pictures rendered, paused, stepped, and resumed; normal `q` exit cleared both pictures and returned to an empty terminal with the connection-closed message. |
| Grid, WebGL | Kitty image rendered; switching to glyphs removed the raster image; movement and switching back to Kitty preserved the updated position without visible leftovers. |
| Site branding | Footer visibly reads Nimble Terminal and links to nimbleterminal.dev. |

The animation demo requests shared memory and enables StableID. Its transport
was not separately instrumented in this UI run, so this is not proof that every
frame used shared memory. The grid also requests shared memory. Independent
automated checks passed for the actual booba bundle's PNG/RGBA/zlib pixels and shared-memory
registry consumption (`task browser-check`), all four demo startup/input/resize/
quit smoke checks and animation keep-screen, plus `task test-no-cgo` and
`task test-wasm` (including cancellation, stable IDs, and registry cleanup).

Remaining visual coverage: native Kitty, native shared memory and tmux; forced
shared-memory failure/fallback in the browser; deterministic cancellation during
transmission; hide/show; sustained streaming; and each renderer's full lifecycle
matrix. No native visual pass is claimed: Kitty was not found in `/Applications`
or PATH, and Computer Use explicitly denied access to the installed Ghostty app.
The sandbox also denied starting a local listener; the user started the browser
server externally. Existing native shared-memory sandbox limitations still apply.

For a native operator check, run `./bin/ntpicture-animation` in Kitty (or record
Ghostty separately), resize while playing, alternate Space and comma/period/Home,
then press `Q` and verify both images remain. Run again and press `q`, verifying
cleanup. Use `NTCHARTS_KITTY=0 ./bin/ntpicture-animation` for the glyph case.
Record terminal version and actual transport before marking native coverage done.

## Next: validation and consumer migration

The first implementation priority is installing the NTCharts alias adapter, to
stop maintaining two renderer copies. The fixture now also aliases
KittyCapabilityMsg. The actual sibling checkout is outside this session's
writable scope; temporary-copy consumer checks do not constitute installation.

1. Verify native Kitty and ghostty-web visually with StableID both enabled and
   disabled: animation/mesh updates, resize and cell geometry changes, multiple
   pictures, hide/show, cancellation, close, and keep-screen. Exercise direct
   transport, shared memory, and shared-memory fallback, plus glyph fallback.
   Native shared-memory tests need an environment permitting POSIX shared memory.
2. Capture the reviewer's reproducible output harness and account for startup
   and steady-state traffic separately. Confirm the startup geometry transitions and transitional glyph output.
3. Install NTCharts compatibility adapters and migrate real consumers as outlined
   below. Existing automated compatibility checks are a starting point; run each
   consumer's own tests and demos before removing its previous implementation.
4. Extract rectangular composition when Gloss's animated document regions need
   it. Keep explicit terminal-session design separate from the initial consumer
   migration; it need not block hosts using explicit capabilities today.

## Implemented: discovery bridge and cell-size handling

KittyCapabilityMsg reports probe resolution through the Bubble Tea loop.
Model.Update and Picture.Update return notification commands on transitions;
Init/query also reports already-known results. Late positive replies may follow
a timeout; hosts must tolerate duplicates. Examples select Kitty from this
message instead of polling on every update. Capability state remains global;
ForceKittyCapability does not itself enqueue a message.

Picture ignores zero/negative terminal cell-size reports without cancelling or
dropping a pending source. Explicit SetTerminal calls still reject bad geometry.

## Release prerequisites

- Confirm `https://nimbleterminal.dev/ntpicture?go-get=1` serves the correct
  go-import metadata before tagging. A local replace directive is not a release
  validation; verify module resolution from a clean consumer outside the workspace.
- Browser builds still require wasm.work's explicit Bubble Tea replacement:
  `github.com/neomantra/bubbletea/v2 v2.0.0-20261009201241-c397c32a5074`.
  Keep that pin and its bridge checks until an upstream/browser-compatible
  replacement passes the demo and visual matrix. Native builds use upstream.

## Remaining architecture work

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
policy. The inspected local NTCharts3d checkout now imports ntcanvas for its
wireframe renderer through a sibling replace directive; the temporary consumer
harness copies that sibling too. QR keeps direct module
glyphs, the quiet zone, unfiltered pixels and immediate clearing on failed fit.

Validate the complete native/glyph/browser and cleanup matrix before removing
old implementations. Use each repository's documented CI and generated-artifact
conventions. Existing compatibility tests do not prove visual terminal parity.
