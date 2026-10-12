# GIF / WebP playback

Run `GOWORK=off go -C examples run ./animation` from the repository root,
or select **GIF & WebP** on the booba demo site.

Two independent playback controllers render the original Gloss orbit fixtures:
one GIF and one lazy animated WebP. Space pauses/plays both, comma/period step,
Home selects frame one, and q quits. The same bindings generate browser buttons.

The fixture files are copied unchanged from Gloss and covered by the MIT
license. Regenerate in Gloss with `task gen-assets` (requires gif2webp for WebP),
then copy both files here together and run the native/WASM checks.
