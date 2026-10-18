package ntpicture

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/color"
	"io"
	"strings"
	"testing"

	"nimbleterminal.dev/ntpicture/internal/imageview"
)

type rgbGenericImage struct{ image.Image }

// Preserve the original generic loop as an independent packing oracle.
func referenceRGB(img image.Image) []byte {
	b := img.Bounds()
	out := make([]byte, 0, 3*b.Dx()*b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			out = append(out, c.R, c.G, c.B)
		}
	}
	return out
}

func opaqueNRGBA(rect image.Rectangle, padding int) *image.NRGBA {
	stride := 4*rect.Dx() + padding
	img := &image.NRGBA{Pix: make([]byte, stride*rect.Dy()), Stride: stride, Rect: rect}
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{byte(x*17 + y*3), byte(x*7 - y*11), byte(x + y*23), 255})
		}
	}
	return img
}

func TestKittyRGBPacking(t *testing.T) {
	for _, rect := range []image.Rectangle{image.Rect(0, 0, 8, 4), image.Rect(0, 0, 1, 1), image.Rect(0, 0, 5, 3), image.Rect(4, 7, 9, 10), image.Rect(-7, -9, -2, -6), image.Rect(0, 0, 0, 0), image.Rect(2, 3, 2, 6), image.Rect(2, 3, 5, 3)} {
		for _, padding := range []int{0, 12} {
			t.Run(fmt.Sprintf("%v/pad%d", rect, padding), func(t *testing.T) {
				src := opaqueNRGBA(rect, padding)
				images := []image.Image{src, imageview.New(src), rgbGenericImage{src}}
				rgba := image.NewRGBA(rect)
				for y := rect.Min.Y; y < rect.Max.Y; y++ {
					for x := rect.Min.X; x < rect.Max.X; x++ {
						rgba.Set(x, y, src.At(x, y))
					}
				}
				images = append(images, rgba, image.NewGray(rect))
				for _, img := range images {
					before := append([]byte(nil), src.Pix...)
					got := kittyRGBBytes(img)
					want := referenceRGB(img)
					if !bytes.Equal(got, want) {
						t.Fatalf("%T: got %v, want %v", img, got, want)
					}
					_ = kittyRGBBytes(opaqueNRGBA(rect, padding))
					if !bytes.Equal(before, src.Pix) {
						t.Fatal("packing mutated source")
					}
					if !bytes.Equal(got, want) {
						t.Fatal("later packing changed output")
					}
				}
			})
		}
	}
	parent := opaqueNRGBA(image.Rect(-10, -8, 12, 10), 8)
	sub := parent.SubImage(image.Rect(-3, -2, 6, 4))
	want := referenceRGB(sub)
	got := kittyRGBBytes(imageview.New(sub))
	clear(parent.Pix)
	_ = kittyRGBBytes(sub)
	if !bytes.Equal(got, want) {
		t.Fatal("subimage layout or output ownership")
	}
}

func TestKittyNRGBAEncoding(t *testing.T) {
	for _, alpha := range []byte{255, 127, 0} {
		for _, wrapped := range []bool{false, true} {
			src := opaqueNRGBA(image.Rect(-4, -3, 9, 8), 12)
			src = src.SubImage(image.Rect(-2, -1, 5, 4)).(*image.NRGBA)
			src.SetNRGBA(-2, -1, color.NRGBA{231, 117, 53, alpha})
			var img image.Image = src
			if wrapped {
				img = imageview.New(src)
			}
			for _, format := range []KittyFormat{KittyFormatZlib, KittyFormatRGBA} {
				apc := buildKittyAPC(img, 17, 7, 5, format)
				data := decodeKittyRaw(t, apc, "s=7", "v=5", "i=17", "a=T")
				want := make([]byte, 0, 7*5*4)
				for y := -1; y < 4; y++ {
					for x := -2; x < 5; x++ {
						c := src.NRGBAAt(x, y)
						want = append(want, c.R, c.G, c.B, c.A)
					}
				}
				if format == KittyFormatZlib {
					if !strings.Contains(apc, "o=z") {
						t.Fatal("missing zlib metadata")
					}
					r, err := zlib.NewReader(bytes.NewReader(data))
					if err != nil {
						t.Fatal(err)
					}
					data, err = io.ReadAll(r)
					if err != nil {
						t.Fatal(err)
					}
					if err = r.Close(); err != nil {
						t.Fatal(err)
					}
					if alpha == 255 {
						want = referenceRGB(src)
						if !strings.Contains(apc, "f=24") {
							t.Fatal("opaque NRGBA not RGB")
						}
					}
				}
				if (alpha != 255 || format == KittyFormatRGBA) && strings.Contains(apc, "f=24") {
					t.Fatal("lost alpha format")
				}
				if !bytes.Equal(data, want) {
					t.Fatalf("alpha=%d wrapped=%v format=%v: decoded pixels differ", alpha, wrapped, format)
				}
			}
		}
	}
}

var rgbBenchmarkResult []byte

func BenchmarkKittyRGBPacking(b *testing.B) {
	for _, size := range []image.Point{{320, 160}, {640, 320}, {1280, 640}} {
		src := opaqueNRGBA(image.Rectangle{Max: size}, 0)
		rgba := image.NewRGBA(src.Rect)
		copy(rgba.Pix, src.Pix)
		parent := opaqueNRGBA(image.Rect(-4, -3, size.X+4, size.Y+3), 16)
		sub := parent.SubImage(image.Rectangle{Max: size})
		for _, fixture := range []struct {
			name string
			img  image.Image
		}{{"RGBA", rgba}, {"NRGBA", src}, {"NRGBASubimage", sub}, {"Generic", rgbGenericImage{src}}} {
			b.Run(fmt.Sprintf("%dx%d/%s", size.X, size.Y, fixture.name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					rgbBenchmarkResult = kittyRGBBytes(fixture.img)
				}
			})
		}
	}
}
