package animation

import (
	"context"
	"image"
	"image/draw"
	"nimbleterminal.dev/ntpicture/internal/imageview"
	"testing"
	"time"
)

func benchmarkPage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 1600, 2100))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	return img
}

func BenchmarkReadOnlyPageDraw(b *testing.B) {
	src := benchmarkPage()
	dst := image.NewRGBA(src.Bounds())
	for _, tc := range []struct {
		name string
		img  image.Image
	}{{"Concrete", src}, {"ReadOnly", imageview.New(src)}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				draw.Draw(dst, dst.Bounds(), tc.img, src.Bounds().Min, draw.Src)
			}
		})
	}
}
func BenchmarkCompositePage(b *testing.B) {
	ctx := context.Background()
	base := benchmarkPage()
	frame := image.NewRGBA(image.Rect(0, 0, 240, 180))
	p, err := NewPlayer(&Clip{Bounds: frame.Bounds(), Frames: []ClipFrame{{Image: frame, Delay: time.Millisecond}, {Image: frame, Delay: time.Millisecond}}})
	if err != nil {
		b.Fatal(err)
	}
	regions := []Region{{Rect: image.Rect(100, 100, 340, 280), Player: p, Key: "clip"}}
	source, err := NewComposite(ctx, base, regions, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("Advance", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := source.Advance(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Relayout", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := NewComposite(ctx, base, regions, source); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkCompositeImmutableRelayout(b *testing.B) {
	ctx := context.Background()
	base := benchmarkPage()
	frame := image.NewRGBA(image.Rect(0, 0, 240, 180))
	p, err := NewPlayer(&Clip{Bounds: frame.Bounds(), Frames: []ClipFrame{{Image: frame, Delay: time.Millisecond}}})
	if err != nil {
		b.Fatal(err)
	}
	regions := []Region{{Rect: image.Rect(100, 100, 340, 280), Player: p, Key: "clip"}}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := NewCompositeImmutable(ctx, base, regions, nil); err != nil {
			b.Fatal(err)
		}
	}
}
