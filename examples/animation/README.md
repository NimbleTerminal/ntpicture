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
not retransmit a bitmap. See README's "Layered composite playback" and "Window
versus Viewport" sections.
