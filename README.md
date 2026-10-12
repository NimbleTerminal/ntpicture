# ntpicture

A reusable terminal image component for Bubble Tea v2. Module:
`nimbleterminal.dev/ntpicture`.

This is an unreleased extraction from NTCharts and Gloss. `Picture` handles
still images and continuously supplied frames. `animation` decodes GIF/WebP
and composes playback positions; `playback` schedules them through a Picture.

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

Run `task run-still` for a native gradient example. `q` quits with
placement cleanup. The host owns keys, layout, loading/error UI and visibility.

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

For continuously supplied mesh/framebuffer images, set `Options.StableID: true`.
Kitty frames then reuse the displayed image ID while cell dimensions and cell
pixel geometry remain unchanged. The placeholder string stays identical, so
Bubble Tea can skip redrawing it and transmit only the image update. Each
Picture has its own ID; resize, hiding/clearing, and committed mode changes
require a new placement. `ClearOnReplace` takes precedence.

Streaming updates terminal pixels as soon as the transmission arrives, before
`PresentedMsg`. Cancellation rejects pending work but cannot recall bytes already
sent. Keep restores the last committed frame after accepted output drains. The
default fresh-ID behavior still retains the old image independently during a
replacement. Neither mode changes the configured image encoding or resolution.

`PresentedMsg` identifies its `Owner` and committed `Generation`; it means the
ordered output/grid sequence completed, not that the terminal acknowledged
painting. `ErrorMsg` reports failed preparation or validation. `Status` exposes
current generation, last presented generation, busy state, error and actual
transport. Superseded generations are not presented; a producer coalescing its
own requests should track the newest `Status().Generation`.

`SetVisible(false)` cancels work and removes placements while retaining the
source. `Close` is final and idempotent. Local resources are released even if its
command is not run, but terminal deletion requires running that command. An
accepted transmission carries a cleanup guard for removal races. No component
clears other images globally.

`Keep()` is a final alternative to `Close()` for still images, mesh frames, and
playback controllers. It cancels pending replacements and freezes the last
presented view. Return `tea.Sequence(pic.Keep(), tea.Quit)` and include that view
in the application's final main-screen layout. Kitty content is retransmitted
directly before exit, releasing shared-memory buffers; glyph content stays in
the final view. Run the returned command. A subsequent `Close()` is a no-op.
The host controls screen selection, help removal, and space for the shell prompt.

Default image IDs start at a randomized offset to reduce collisions with images
kept by previous processes. This is best effort, not a cross-process reservation;
applications needing guaranteed uniqueness must provide a shared `NextID` policy.

## Geometry and transport

Kitty PNG, raw RGBA, zlib and shared-memory output retain the NTCharts wire
format. Glyph output uses colored half blocks. Fit contain/fill/cover, anchors,
cell pixel dimensions, placement depth and resolution factors are supported.
The exact-size `FitFill` path bypasses resampling.

`Picture` bounds source and prepared images to 32 Mi pixels and validates Kitty
placeholder dimensions. This is a pixel bound, not a total RAM budget: source,
front, retired frames, and encoder/scaler scratch may coexist. Applications own
aggregate document budgets.

Use `SetTerminal(kitty, cellWidth, cellHeight)` when the host already knows its
capability and geometry. It does not probe. Otherwise run `Init`, route replies,
and choose Kitty after `KittySupported()` becomes affirmative. The extracted
legacy probe and tmux settings are still process-global in this milestone.

Shared memory uses the unchanged browser registry
`globalThis.ghosttyKittySharedMemory` (`Map<string, Uint8Array>`), Linux named
objects, or macOS POSIX shared memory with CGO. Creation/probe failures fall back
to the configured direct format. Native pending objects retain the 64 MiB
process cap and abandoned-retirement grace. Browser producer accounting has not
yet been strengthened beyond the extracted implementation.

Image ID allocation returns **one** fresh positive 24-bit ID per call, not a
block. By default all `Picture` instances share `NextImageID`. Hosts mixing
legacy pictures, QR codes and new pictures must supply/use one allocator across
all of them. IDs do not wrap or recycle; exhaustion returns
`ErrImageIDExhausted`. Long-running high-rate ID reuse is a later decision.

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
rectangles, transparency, disposal, and loop counts. Decoding defaults to
128 MiB input, 32 Mi canvas pixels, 1,000 frames, and 64 MiB GIF frame storage.
`animation.Options.Limits` can tighten these ceilings; zero selects defaults.
Lazy failures identify the frame and pause playback through `ctrl.Status().Err`.
Hosts still own controls, visibility decisions, and aggregate resource budgets.

## Examples and browser demo

```sh
task build-examples       # bin/ntpicture-{still,simple,grid,animation}
task serve-wasm-site      # http://localhost:8000/web/
```

The site runs four examples locally in the browser through booba: a generated
still image, the embedded-image viewer ported from NTCharts, its interactive
`GridImage` board, and side-by-side GIF/WebP playback from Gloss. The same programs run natively. Use arrows/WASD/HJKL in the
grid, `g` to switch rendering modes, and `q` to quit. Restart reloads a fresh
browser session. Booba action buttons above the terminal come from the same Go
help bindings as the keyboard controls; the grid exposes movement and, when
Kitty is available, rendering mode. Animation exposes play/pause, frame stepping,
and first-frame seeking. More includes Quit.

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

Contributions and feedback are welcome. Please follow our
[Code of Conduct](./CODE_OF_CONDUCT.md).

- [Report an issue](https://github.com/NimbleTerminal/ntpicture/issues)
- [Submit a pull request](https://github.com/NimbleTerminal/ntpicture/pulls)

## Acknowledgements

ntpicture grows out of [NTCharts](https://github.com/NimbleMarkets/ntcharts)
and the image presentation work in Gloss. Thanks to
[Charm](https://charm.sh/) for Bubble Tea and its terminal libraries,
[pixterm](https://github.com/NimbleMarkets/pixterm) for glyph rendering, and
[go-booba](https://github.com/NimbleMarkets/go-booba) for the browser runtime
and image registry integration.

## License

Source code: [MIT License](./LICENSE). Artwork exceptions are listed in
[NOTICE.md](./NOTICE.md); dependency licenses are in
[THIRD_PARTY_NOTICES.md](./THIRD_PARTY_NOTICES.md).

Copyright (c) 2024–2026
[Neomantra Corp](https://www.neomantra.com).

----
Made with :heart: and :fire: by the team behind [Nimble.Markets](https://nimble.markets).
