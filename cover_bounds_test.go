package ntpicture

import (
	"image"
	"image/color"
	"math"
	"runtime"
	"testing"
)

// referenceCover resamples src onto the circumscribed rectangle with a naive
// separable Catmull-Rom filter, independently of x/image, and composites it
// over bg into target. It follows the sampling convention of x/image's
// kernel scalers: destination pixel centers map to (d+0.5)*scale-0.5 in
// source space, the kernel support widens by the scale factor when shrinking,
// and weights are normalized over the visible support.
func referenceCover(src image.Image, cols, rows int, bg color.Color, anchor FitAnchor) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	var cw, ch int
	if sw*rows > sh*cols {
		ch = rows
		cw = sw * rows / sh
	} else {
		cw = cols
		ch = sh * cols / sw
	}
	cw, ch = max(cw, 1), max(ch, 1)
	ox, oy := (cols-cw)/2, (rows-ch)/2
	switch anchor {
	case AnchorTop:
		oy = 0
	case AnchorBottom:
		oy = rows - ch
	case AnchorLeft:
		ox = 0
	case AnchorRight:
		ox = cols - cw
	}
	catmullRom := func(t float64) float64 {
		t = math.Abs(t)
		switch {
		case t < 1:
			return (1.5*t-2.5)*t*t + 1
		case t < 2:
			return ((-0.5*t+2.5)*t-4)*t + 2
		}
		return 0
	}
	weights := func(d, dsize, ssize int) (int, []float64) {
		scale := float64(ssize) / float64(dsize)
		half, argScale := 2.0, 1.0
		if scale > 1 {
			half, argScale = 2*scale, 1/scale
		}
		center := (float64(d)+0.5)*scale - 0.5
		i, j := max(0, int(math.Floor(center-half))), min(ssize, int(math.Ceil(center+half)))
		w := make([]float64, max(0, j-i))
		total := 0.0
		for k := range w {
			if t := math.Abs(center-float64(i+k)) * argScale; t < 2 {
				w[k] = catmullRom(t)
				total += w[k]
			}
		}
		for k := range w {
			w[k] /= total
		}
		return i, w
	}
	out := image.NewRGBA(image.Rect(0, 0, cols, rows))
	br, bgG, bb, ba := bg.RGBA()
	for y := range rows {
		for x := range cols {
			var r, g, b, a float64
			if x >= ox && x < ox+cw && y >= oy && y < oy+ch {
				x0, wx := weights(x-ox, cw, sw)
				y0, wy := weights(y-oy, ch, sh)
				for ky, vy := range wy {
					for kx, vx := range wx {
						pr, pg, pb, pa := src.At(sb.Min.X+x0+kx, sb.Min.Y+y0+ky).RGBA()
						w := vx * vy
						r += float64(pr) * w
						g += float64(pg) * w
						b += float64(pb) * w
						a += float64(pa) * w
					}
				}
			}
			clamp := func(v float64) float64 { return min(0xffff, max(0, v)) }
			r, g, b, a = clamp(r), clamp(g), clamp(b), clamp(a)
			// Source-over in premultiplied 16-bit space, then to 8 bits.
			k := (0xffff - a) / 0xffff
			out.SetRGBA(x, y, color.RGBA{
				uint8(math.Round((r + float64(br)*k) / 0x101)),
				uint8(math.Round((g + float64(bgG)*k) / 0x101)),
				uint8(math.Round((b + float64(bb)*k) / 0x101)),
				uint8(math.Round((a + float64(ba)*k) / 0x101)),
			})
		}
	}
	return out
}

func translucentSource(size image.Point, origin image.Point) *image.RGBA {
	src := image.NewRGBA(image.Rectangle{Min: origin, Max: origin.Add(size)})
	for y := src.Bounds().Min.Y; y < src.Bounds().Max.Y; y++ {
		for x := src.Bounds().Min.X; x < src.Bounds().Max.X; x++ {
			a := uint8(120 + (x*7+y*13)%120)
			src.SetRGBA(x, y, color.RGBA{uint8(x*3) * a / 255, uint8(y*5) * a / 255, 10 * a / 255, a})
		}
	}
	return src
}

func expectClose(t *testing.T, label string, got image.Image, want *image.RGBA, tolerance int) {
	t.Helper()
	b := want.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			a := color.RGBAModel.Convert(got.At(x, y)).(color.RGBA)
			w := want.RGBAAt(x, y)
			for _, pair := range [][2]uint8{{a.R, w.R}, {a.G, w.G}, {a.B, w.B}, {a.A, w.A}} {
				if d := int(pair[0]) - int(pair[1]); d < -tolerance || d > tolerance {
					t.Fatalf("%s at %d,%d got %v want %v", label, x, y, a, w)
				}
			}
		}
	}
}

// Both strategies, every anchor, translucent pixels, and nonzero origins must
// match an independent reference within rounding.
func TestCoverMatchesReferenceAcrossStrategies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		size   image.Point
		scaler bool // expected strategy for an 8×8 target
	}{
		{"wide 8x overflow", image.Pt(32, 4), false},
		{"tall 8x overflow", image.Pt(4, 32), false},
		{"wide 1.5x overflow", image.Pt(24, 16), true},
		{"tall 1.5x overflow", image.Pt(16, 24), true},
		{"wide 4x boundary", image.Pt(64, 16), true},
		{"tall just past boundary", image.Pt(16, 68), false},
		{"upscaled wide", image.Pt(10, 2), false},
	} {
		src := translucentSource(tc.size, image.Pt(3, 5))
		sb := src.Bounds()
		var cw, ch int64
		if tc.size.X*8 > tc.size.Y*8 {
			ch, cw = 8, int64(tc.size.X*8/tc.size.Y)
		} else {
			cw, ch = 8, int64(tc.size.Y*8/tc.size.X)
		}
		if coverUsesScaler(cw, ch, 8, 8) != tc.scaler || (cw == int64(sb.Dx()) && ch == int64(sb.Dy())) {
			t.Fatalf("%s: strategy selection %v for %dx%d", tc.name, !tc.scaler, cw, ch)
		}
		for _, anchor := range []FitAnchor{AnchorCenter, AnchorTop, AnchorBottom, AnchorLeft, AnchorRight} {
			want := referenceCover(src, 8, 8, color.RGBA{200, 40, 40, 255}, anchor)
			got := prepareSource(src, FitCover, 8, 8, 1, 1, color.RGBA{200, 40, 40, 255}, anchor)
			expectClose(t, tc.name, got, want, 2)
		}
	}
}

// Glyph-style scratch reuse must survive alternating strategies and sizes
// across frames without stale scaler tables or buffers leaking into output.
func TestCoverScratchReuseAcrossStrategies(t *testing.T) {
	scratch := new(fitScratch)
	sizes := []image.Point{{24, 16}, {32, 4}, {24, 16}, {4, 32}, {16, 24}, {32, 4}}
	for frame, size := range sizes {
		src := translucentSource(size, image.Pt(frame, 2*frame))
		want := referenceCover(src, 8, 8, color.White, AnchorCenter)
		got := prepareSourceWithScratch(src, FitCover, 8, 8, 1, 1, color.White, AnchorCenter, scratch)
		expectClose(t, "frame "+string(rune('0'+frame)), got, want, 2)
		if got != image.Image(scratch.rgba) {
			t.Fatal("scratch target was not reused")
		}
	}
}

func TestCoverUsesScalerThreshold(t *testing.T) {
	for _, tc := range []struct {
		cw, ch, tw, th int64
		want           bool
	}{
		{640, 480, 640, 320, true},
		{640, 1280, 640, 320, true},  // exactly 4×
		{641, 1280, 640, 320, false}, // just past 4×
		{131072, 128, 1, 128, false},
		{1 << 40, 1 << 20, 1 << 20, 1 << 20, false}, // products beyond int64 stay comparable
		{1, 1, 1, 1, true},
	} {
		if got := coverUsesScaler(tc.cw, tc.ch, tc.tw, tc.th); got != tc.want {
			t.Fatalf("coverUsesScaler(%d,%d,%d,%d)=%v", tc.cw, tc.ch, tc.tw, tc.th, got)
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
