package ntpicture

import (
	"bytes"
	"image"
	"image/color"
	"testing"

	"nimbleterminal.dev/ntpicture/internal/imageview"
)

func TestReadOnlyViewRenderingMatchesConcrete(t *testing.T) {
	src := randomNRGBA(12, 10)
	view := imageview.New(src)
	for _, fit := range []FitMode{FitFill, FitContain, FitCover} {
		a := prepareSource(src, fit, 7, 4, 1, 2, color.Transparent, AnchorCenter)
		b := prepareSource(view, fit, 7, 4, 1, 2, color.Transparent, AnchorCenter)
		if !bytes.Equal(appendGlyph(nil, a), appendGlyph(nil, b)) {
			t.Fatal("wrapped glyph pixels changed")
		}
		for _, format := range []KittyFormat{KittyFormatPNG, KittyFormatRGBA, KittyFormatZlib} {
			if buildKittyAPC(a, 100, 7, 4, format) != buildKittyAPC(b, 100, 7, 4, format) {
				t.Fatal("wrapped encoding changed")
			}
		}
	}
	// Exact-size fast paths must recover concrete images, without making a copy.
	if got := prepareSource(view, FitFill, 12, 5, 1, 2, color.Transparent, AnchorCenter); got != src {
		t.Fatal("fit did not unwrap exact-size image")
	}
	dst := make([]byte, 4*12*10)
	writeKittyRGBA(dst, view)
	if !bytes.Equal(dst, src.Pix) {
		t.Fatal("wrapped straight-alpha bytes changed")
	}
	var _ image.Image = view
}
