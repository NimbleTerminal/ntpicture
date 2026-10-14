# Live framebuffer

Run `GOWORK=off go -C examples run ./framebuffer` from the repository root, or
select **Live framebuffer** on the booba demo site (`?demo=framebuffer`).

A rotating wireframe cube above a perspective floor is rendered off the Bubble
Tea event loop into a fresh framebuffer for every frame. Each frame is submitted
with `SetImageImmutable` and `FitFill` at exactly `RasterGeometry().Pixels`, so
no scaling or compositing happens during preparation. One explicit
`TerminalSession` owns discovery; the picture uses `StableID`, so Kitty frames
reuse the displayed image ID and Bubble Tea redraws nothing for the placeholder
grid. Shared memory is requested and falls back to direct zlib transport.

The prepared picture is a quarter larger than the visible area on every side.
A `Viewport` of the visible size pans across it: arrows, WASD or HJKL move it,
`c` recenters. Pan while paused to see that the image is re-addressed rather than
re-encoded or retransmitted; the status line keeps the same generation and the
viewport position changes.

`]` and `[` step the Kitty resolution factor through 0.25, 0.5, 0.75 and 1.0
without changing the cell footprint or the camera aspect; the status line shows
the resulting target pixel dimensions. In glyph mode those controls are hidden
because the raster stays at `cols × (2 * rows)`. `g` switches between glyph and
Kitty once Kitty support is confirmed (`NTCHARTS_KITTY=unsupported` forces the
glyph path). Space pauses and resumes, `Q` keeps the last frame on the main
screen and quits, and `q` clears and quits.

## Producer policy

Rendering is paced, not queued: a frame is rendered only after the previous one
is presented, and at most one render runs at a time. Each render captures the
picture's `RasterGeometry` and a request ID. Resize, resolution changes, mode
changes, pause and shutdown increment the request and cancel the running
render; its result is dropped when it returns, and a replacement render starts
only then, so concurrency stays bounded. A result whose geometry no longer
matches the picture is also dropped. Viewport changes never touch the producer.
A presentation error stops the producer until the next geometry, resolution or
playback change; the status line shows the error meanwhile.

The status shows the backend and actual transport, target pixel dimensions,
resolution, playback state, viewport position, display aspect, accepted frame
count and the last render time. Animation policy (frame interval, phase step,
overscan, and the resolution ladder) lives in this example, not in the library.
