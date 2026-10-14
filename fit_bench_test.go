package ntpicture

import (
	"image"
	"image/color"
	"testing"
)

// Exercise ordinary image downscaling as well as the aspect mismatch
// that used to allocate scaler tables for invisible destination pixels.
// Direct mirrors Kitty's per-job preparation; Scratch mirrors glyph reuse.
func BenchmarkFitCover(b *testing.B) {
	for _, tc := range []struct {
		name           string
		source, target image.Point
	}{
		{"Terminal", image.Pt(800, 600), image.Pt(640, 320)},
		{"Glyph", image.Pt(800, 600), image.Pt(80, 40)},
		{"ExtremeAspect", image.Pt(1024, 1), image.Pt(1, 128)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			src := benchImage(tc.source.X, tc.source.Y)
			for _, reuse := range []bool{false, true} {
				name := "Direct"
				var scratch *fitScratch
				if reuse {
					name = "Scratch"
					scratch = new(fitScratch)
				}
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						got := prepareSourceWithScratch(src, FitCover, tc.target.X, tc.target.Y, 1, 1, color.Black, AnchorCenter, scratch)
						if got.Bounds().Size() != tc.target {
							b.Fatal("unexpected output geometry")
						}
					}
				})
			}
		})
	}
}
