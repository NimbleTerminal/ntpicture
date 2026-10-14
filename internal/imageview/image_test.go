package imageview

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func TestReadOnlyViewFastAccess(t *testing.T) {
	src := image.NewRGBA(image.Rect(3, 4, 6, 7))
	src.SetRGBA(4, 5, color.RGBA{R: 64, G: 32, A: 128})
	view := New(src)
	if _, ok := any(view).(draw.Image); ok {
		t.Fatal("view exposes mutation")
	}
	if view.Bounds() != src.Bounds() || view.RGBA64At(4, 5) != src.RGBA64At(4, 5) {
		t.Fatal("bounds or premultiplied color changed")
	}
	if Unwrap(view) != src || Unwrap(New(view)) != src {
		t.Fatal("trusted view did not unwrap")
	}
	if view.Opaque() {
		t.Fatal("transparent pixels marked opaque")
	}
	dst := image.NewRGBA(src.Bounds())
	allocs := testing.AllocsPerRun(5, func() { draw.Draw(dst, dst.Bounds(), view, src.Bounds().Min, draw.Src) })
	if allocs > 2 {
		t.Fatalf("public draw allocated per pixel: %v", allocs)
	}
}
