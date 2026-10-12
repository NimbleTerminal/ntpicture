# Changelog

## Unreleased

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
