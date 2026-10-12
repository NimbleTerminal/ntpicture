# Changelog

## Unreleased

- Extract NTCharts image rendering, Kitty transport, glyph fallback, static
  decoders, and native/browser shared-memory support into `nimbleterminal.dev/ntpicture`.
- Add `Picture` to retain displayed content while preparing replacements,
  reject stale results, and manage placement cleanup.
- Add a still-image example and compatibility checks for local consumers and
  the bundled booba browser registry.
- Establish Task-based QA, Linux/macOS CI, native/WASM vulnerability scans,
  and weekly dependency updates.

Animation and playback extraction remain planned; see [MIGRATION.md](./MIGRATION.md).
