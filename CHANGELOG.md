# Changelog

## Unreleased

Initial extraction of NTCharts and Gloss terminal image rendering into
`nimbleterminal.dev/ntpicture`.

- `Picture`, a Bubble Tea v2 component that keeps the displayed image on screen
  while preparing a replacement, coalesces rapid updates, rejects stale results,
  and cleans up placements. `Submit` and `SetImageFor` / `SetImageImmutableFor`
  tie frames to target geometry; `SetCrop` / `ResetCrop` crop source pixel
  regions before fitting; `SetViewport` and `ResetViewport` clip prepared Kitty
  or glyph cells without re-encoding; `RasterGeometry`, `SetResolution`, and
  `SetMaxRasterPixels` let producers target scaled and pixel-budgeted Kitty
  rasters while preserving cell footprint and camera aspect; `AutoMode` and
  `ToggleMode` manage automatic and manual glyph/Kitty switching; `Keep` freezes
  the last presented frame on exit.
- `TerminalSession` (`NewTerminalSession`, `TerminalOptions`,
  `TerminalSessionMsg`, `SetCellSize`, `SetKittyCapability`) for per-connection
  Kitty and shared-memory capability discovery, cell-pixel geometry tracking,
  and `NTPICTURE_KITTY` / `NTPICTURE_TMUX_PASSTHROUGH` environment overrides
  without import-time side effects. Standalone `Picture` instances own an
  isolated session by default when `Options.Session` is nil.
- Kitty graphics transport with `StableID` streaming, PNG/RGBA/zlib direct
  encodings, native POSIX and browser `ghosttyKittySharedMemory` shared memory,
  and half-block ANSI glyph fallback (`GridImage` and `PictureGlyph`).
- Static image decoders (`ntpicture/decoders`), bounded GIF and lazy
  animated-WebP decoding (`ntpicture/animation`), and immutable `Composite` /
  `Window` / `Positions` composition for multi-region animations.
- Playback controllers (`playback.Controller` and `playback.Layers`):
  deadline-paced playback, cell-aligned Kitty overlay planning and splicing over
  a static composite base, shared clocks across repeated region keys, off-loop
  `SetComposite` window cropping and position carry, `SetActive` suspension, and
  `Snapshot` export of the committed frame.
- `ntpicture/legacy` compatibility subpackage (`Model`, `NewModel`,
  `NewModelWithConfig`, `DefaultTerminalSession`, `QueryKittySupport`,
  `KittySupported`, `KittyEnvSignalled`, `KittyUnavailable`,
  `ForceKittyCapability`, `SetTmuxPassthrough`, `RequestCellSize`,
  `IsPictureMsg`) isolating the low-level NTCharts surface and process-wide
  capability helpers from the root package.
- Examples (still, simple, grid, animation, and live framebuffer) built for
  native terminals and the browser via booba, plus task-based QA
  (`golangci-lint`, race tests, fuzz targets, and `govulncheck`).

Consumer migration is tracked in [MIGRATION.md](./MIGRATION.md).
