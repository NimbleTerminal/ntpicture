package ntpicture

import (
	"image"
	"image/color"
	"runtime"
	"testing"

	"golang.org/x/image/draw"
)

func TestCoverClippedSamplingMatchesScale(t *testing.T) {
	for _, size := range []image.Point{{32, 4}, {4, 32}} {
		src := image.NewRGBA(image.Rectangle{Min: image.Pt(3, 5), Max: image.Pt(3+size.X, 5+size.Y)})
		for y := src.Bounds().Min.Y; y < src.Bounds().Max.Y; y++ {
			for x := src.Bounds().Min.X; x < src.Bounds().Max.X; x++ {
				src.SetRGBA(x, y, color.RGBA{uint8(x * 3), uint8(y * 3), 10, 180})
			}
		}
		for _, anchor := range []FitAnchor{AnchorCenter, AnchorTop, AnchorBottom, AnchorLeft, AnchorRight} {
			target := image.Rect(0, 0, 8, 8)
			cw, ch := 64, 8
			if size.Y > size.X {
				cw, ch = 8, 64
			}
			ox, oy := (8-cw)/2, (8-ch)/2
			switch anchor {
			case AnchorTop:
				oy = 0
			case AnchorBottom:
				oy = 8 - ch
			case AnchorLeft:
				ox = 0
			case AnchorRight:
				ox = 8 - cw
			}
			want := image.NewRGBA(target)
			draw.Draw(want, target, image.NewUniform(color.White), image.Point{}, draw.Src)
			draw.CatmullRom.Scale(want, image.Rect(ox, oy, ox+cw, oy+ch), src, src.Bounds(), draw.Over, nil)
			got := prepareSource(src, FitCover, 8, 8, 1, 1, color.White, anchor)
			for y := range 8 {
				for x := range 8 {
					a := color.RGBAModel.Convert(got.At(x, y)).(color.RGBA)
					b := want.RGBAAt(x, y)
					for _, pair := range [][2]uint8{{a.R, b.R}, {a.G, b.G}, {a.B, b.B}, {a.A, b.A}} {
						d := int(pair[0]) - int(pair[1])
						if d < -1 || d > 1 {
							t.Fatalf("size=%v anchor=%v at %d,%d got %v want %v", size, anchor, x, y, a, b)
						}
					}
				}
			}
		}
	}
}

func TestCoverExtremeAspectUsesBoundedScratch(t *testing.T) {
	for _, size := range []image.Point{{1024, 1}, {1, 1024}} {
		src := image.NewRGBA(image.Rectangle{Max: size})
		w, h := 1, 128
		if size.Y > size.X {
			w, h = 128, 1
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		got := prepareSource(src, FitCover, w, h, 1, 1, color.Black, AnchorCenter)
		runtime.ReadMemStats(&after)
		if got.Bounds().Size() != image.Pt(w, h) {
			t.Fatal("wrong output size")
		}
		if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
			t.Fatalf("small cover allocated %d bytes", allocated)
		}
	}
}
