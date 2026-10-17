# GIF / WebP playback

Run `GOWORK=off go -C examples run ./animation` from the repository root, or
select **GIF & WebP** on the booba demo site.

Two independent playback controllers render the original Gloss orbit fixtures:
one GIF and one lazy animated WebP. Both share one `TerminalSession` for
discovery, cell geometry, and transport settings. Playback defaults to stable-ID
Kitty streaming, so frame updates reuse their placeholder grids. Space pauses/plays
both, comma/period step, Home selects frame one, q quits with cleanup, and Q
keeps the last displayed frames on the main screen and quits. Pending frames are
cancelled. Browser buttons offer play/pause, previous/next frame, first frame,
and **Keep screen & quit**. Use the keyboard for clear-and-quit (`q`).

The fixture files are copied unchanged from Gloss and covered by the MIT
license. Regenerate in Gloss with `task gen-assets` (requires gif2webp for
WebP), then copy both files here together and run the native/WASM checks.

## Still background with animated layers

Run `GOWORK=off go -C examples run ./animation --composite`, or open
`?demo=animation&composite=1` on the browser site. A painted still background
and two animated regions (GIF and WebP) form an `animation.Composite` managed
by `playback.NewLayers`. Under Kitty graphics, the base picture stays still and
only the two cell-aligned region overlays update; under glyph rendering, the
flattened composite animates as a whole. Resizing rebuilds the composite at the
new viewport pixel dimensions while carrying region playback positions via
`layers.Positions()`. Space pauses/resumes, Home restarts every region, Q keeps
the screen, and q clears and quits. Frame stepping is omitted because the
regions have independent frame sequences. The same enabled bindings produce
browser buttons.

`Composite.Window` creates cropped/rescaled pixels and remapped regions for a
smaller source. `Picture.SetViewport` only clips prepared cells; moving it does
not retransmit a bitmap. See the root README's "Composites and layers" and
"Fit, crop, and viewport" sections.

## Scrolling mixed-image document

Run `GOWORK=off go -C examples run ./animation --scroll`, or open
`?demo=animation&scroll=1` on the browser site. Four document items—an animated
GIF (`*playback.Controller`), a painted still card (`*ntpicture.Picture`), an
animated WebP (`*playback.Controller`), and a second still card—are managed
through one local `scrollablePicture` interface sharing a `TerminalSession`:

- **Partial clipping:** Items straddling the top or bottom edge of the scroll
  window call `SetViewport(Viewport{Y: visY, Width: cols, Height: visH})`,
  emitting only the visible Kitty placeholder or glyph rows without resampling
  or retransmitting the bitmap. Fully visible items call `ResetViewport()`.
- **Offscreen pause and resume:** Items scrolled completely out of view call
  `SetVisible(false)`, deleting their terminal placement and stopping offscreen
  animation timers while preserving the current frame; scrolling back into view
  calls `SetVisible(true)` and resumes from that frame.
- **Cell-size changes:** `uv.CellSizeEvent` updates on the shared
  `TerminalSession` propagate `SetCellSize(cw, ch)` and recompute item row
  heights for the new cell aspect ratio.
- **Keep-screen exit:** Pressing `Q` switches from the alternate screen to the
  main screen, calls `KeepOnMainScreen()` on visible items so their retained
  Kitty placements are recreated after `ESC[?1049l`, and closes offscreen items.

