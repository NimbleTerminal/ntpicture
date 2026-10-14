# Glyph compatibility fixtures

These fixtures preserve independent reference output after removing pixterm as
an executable test dependency. Do not regenerate them from ntpicture's encoder
just to make a test pass.

- `glyph.json` is the existing fit/anchor golden, retained unchanged. It covers
  both crop axes and every fit and anchor using deterministic `benchImage`
  inputs and a fixed opaque background.
- `glyph_compat.json` was captured on 2026-10-14 using the existing
  `TestGlyphMatchesPixterm` cases: RGBA subimages, nonzero-origin translucent
  NRGBA, Gray, and empty images, with three backgrounds and all fit modes.
  The reference called `ansimage.NewFromImage` on the prepared image, with
  `NoDithering`, then `RenderExt(false, false)` and trimmed trailing newlines.
- `glyph_legacy_uniform.json` was captured at the same time using
  `TestGlyphLegacyUniform`: uniform images through the former 8×16 cell
  preparation, then `ansimage.NewScaledFromImage` to 4×4 pixels with
  `ScaleModeResize` and `NoDithering`, followed by the same render/trim steps.
  Both input orientations, all fits, and all anchors are represented.

The captured reference dependency was
`github.com/NimbleMarkets/pixterm v0.0.0-20260501211346-dc18ac6c1a0f`.
The generator test bodies are preserved in repository commit `2c4e243`; inputs
and renderer code were unchanged when capturing these outputs. Compatibility
keys identify the input type/bounds, background, and fit, or dimensions/fit/anchor
for the legacy cases. Missing entries fail the tests explicitly.

To independently reproduce the references, use a temporary checkout of that
revision and capture the reference `want` values in those tests before comparing
ntpicture output. Keep the runtime dependency out of this module. Review any
intentional fixture change against the original reference and explain the
rendering difference.
