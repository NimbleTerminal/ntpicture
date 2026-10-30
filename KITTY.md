# Kitty graphics

ntpicture uses Kitty graphics when the terminal confirms support and colored
half-block glyphs otherwise. This guide covers discovery, transport, and
optional frame optimizations. For component setup and lifecycle, start with
[the README](./README.md#terminal-sessions).

## Capability detection and overrides

Kitty support is queried even when the terminal has no recognized environment
identifier. Images stay in glyph mode until a positive response arrives; an
unanswered probe falls back after 250 ms, and late positive replies are accepted.
Explicitly disabling Kitty skips its probes.

Native shared-memory probes remain readable for up to five seconds for late
replies, then are unlinked even if the terminal never responds. Close the
terminal session on shutdown to release pending probes immediately; legacy
hosts can call `legacy.DefaultTerminalSession().Close()`.

The `NTPICTURE_KITTY` and `NTPICTURE_TMUX_PASSTHROUGH` environment variables
override detection. Values set explicitly in `TerminalOptions` take precedence
over them.

Image IDs come from a process-wide allocator, `NextImageID`. If you mix
ntpicture with other Kitty image producers, give all of them the same allocator.

## Transport

Kitty output defaults to direct transmission (`KittyMediumDirect`) and supports
PNG (`KittyFormatPNG`, default), raw RGBA (`KittyFormatRGBA`), and
zlib-compressed (`KittyFormatZlib`) encodings. Setting
`Config{KittyMedium: KittyMediumSharedMemory}` sends raw RGBA through shared
memory where available: POSIX shared memory natively (CGO is required on macOS)
and the `globalThis.ghosttyKittySharedMemory` registry in the browser. If
shared memory is unavailable or fails, output falls back to direct transmission
in the configured `KittyFormat`. tmux passthrough is supported. Glyph output
uses colored half blocks, two pixel rows per cell.

## Frame updates

StableID updates retain one image ID. Root-frame edits (`a=f,r=1,X=1`) are
used only after separate Kitty feature queries confirm the required fields.
Until then, or after unsupported replies/timeouts, updates use ordinary full
image uploads (`a=T`) under the same ID. No terminal name or version enables
frame operations.

After basic Kitty detection, the session sends independent feature queries for
root-frame editing (`e=f,r=1,X=1,x=1,y=1`), appending frames
(`e=f,c=1,X=1,z=-1`), and selecting frames (`e=a,c=1`). Only `OK` replies that
echo all requested non-default fields enable the corresponding capability.
The pinned input parser does not expose `e`, so each query has a separate ID
and requires the operation-specific echoes; a plain graphics `OK` cannot grant
support. `TerminalState.FrameEdits`, `FrameAppend`, and `FrameSelect` report the
results. Hosts with independent capability knowledge can explicitly configure
these fields in `TerminalOptions`; forcing basic `Kitty` support does not force
them. Late positive replies can enable optimizations after a timeout.

## Playback caching

When both frame appending and selection are supported, playback caches bounded
sources exposing one-based `Frame()` and `Frames()`
positions, including `animation.Player`. Those positions must identify immutable
frames within the source. `playback.Options.FrameCacheBytes` limits terminal
uncompressed RGBA storage: zero defaults to 32 MiB, negative disables caching.
FreshIDs and ClearOnReplace disable it. After upload, repeated positions use
only `a=a` controls and skip Picture rasterization/encoding; source decoding and
composition still follow the source's normal timing and seeking semantics.
Out-of-order seeks map source positions to terminal append order. Geometry,
render configuration, viewport, source replacement, and hiding invalidate reuse.

## Eviction hints and compression

`Config.Transient` adds the `N=1` eviction hint on image creation. FreshIDs
playback enables it automatically. `KittyFormatZlib` continues to emit `o=z`
through both full uploads and frame edits, including direct fallback from shared
memory; Ghostty 1.4's Wuffs decoder needs no separate client option.

For reproducible performance comparisons and terminal workload reports, see
[BENCHMARKS.md](BENCHMARKS.md). The harness keeps output-production timings separate
from visual terminal acceptance.
