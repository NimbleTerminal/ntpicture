# Changelog

## Unreleased

- Add `Composite.Positions` to export playback positions (clip identity,
  frame, remaining delay, finished state) without the base or composed image.
  `NewComposite`, `NewCompositeImmutable` and `WithPositions` accept a saved
  `Positions` or a `*Composite` through the new `PositionSource` interface.
- Add `animation.Seekable` (`Frame`, `Frames`, `SeekFrame`), implemented by
  `*Player`. `Controller.Seek` now seeks any seekable source, so host
  transforms that wrap a player keep frame seeking.
- Report the last committed playback position in `Status.Presented` and
  `Status.PresentedPlayer`, with the underlying `Picture` status, so hosts can
  export the displayed frame and match controls to it. Superseded or rejected
  submissions never become presented.
- Add `Picture.SetBackground` and `Controller.SetBackground` to change the
  background color at runtime, retaining the current front until the
  re-prepared frame is ready.

- Add `Picture.Submit` to validate and apply an image with its cell layout,
  mode and cell pixel geometry as one generation, with an `Exact` option that
  rejects mis-sized rasters instead of resampling them. Add
  `Options.HostGeometry` so hosts that manage terminal geometry can keep
  `Update` from reacting to terminal events.

- Restore ordinary `FitCover` preparation speed by using the separable scaler
  while the circumscribed rectangle is at most four times the visible target,
  keeping bounded allocation for extreme aspect mismatches.

- Add a live framebuffer demo that streams a procedurally rendered scene with
  stable Kitty IDs, an explicit terminal session, paced off-loop rendering with
  stale-result rejection, viewport panning, resolution steps, pause, keep-screen
  exit, and matching booba browser controls.

- Default playback to stable Kitty IDs with a fresh-ID opt-out; carry frame
  deadlines across rendering to avoid cumulative playback slowdown.
- Report Kitty capability resolution as a Bubble Tea message and ignore invalid
  terminal cell-size reports without dropping pending images.
- Avoid resending direct Kitty fronts on keep-screen exit unless a pending
  stable-ID transmission requires restoring the retained frame.

- Add opt-in stable-ID Kitty streaming to avoid placeholder redraws each frame;
  preserve cancellation, resize cleanup, and keep-screen restoration.

- Add Gloss-style keep-screen exit for pictures and playback, with a Q control
  and browser help button in the animation demo.

- Add bounded GIF and lazy animated-WebP decoding from Gloss, immutable players,
  frame seeking, loop/disposal handling, and indexed lazy errors.
- Add cancellable Bubble Tea playback through Picture and a GIF/WebP demo with
  booba play/pause and frame controls.

- Update native Bubble Tea to 2.1.0, example Bubbles to 2.2.1, and rendering
  dependencies; retain the tested WASM fork for browser compatibility.

- Publish demo help bindings as booba action buttons for browser controls.

- Port the NTCharts embedded-image and interactive grid examples to `Picture`.
- Add an isolated examples module, a booba browser demo site, Pages deployment,
  native/WASM demo builds, runtime smoke checks, and generated dependency notices.

- Extract NTCharts image rendering, Kitty transport, glyph fallback, static
  decoders, and native/browser shared-memory support into `nimbleterminal.dev/ntpicture`.
- Add `Picture` to retain displayed content while preparing replacements,
  reject stale results, and manage placement cleanup.
- Add a still-image example and compatibility checks for local consumers and
  the bundled booba browser registry.
- Establish Task-based QA, Linux/macOS CI, native/WASM vulnerability scans,
  and weekly dependency updates.

Compositor/session extraction and consumer migration remain planned; see
[MIGRATION.md](./MIGRATION.md).
