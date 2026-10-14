package animation

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"strings"
	"sync"
	"testing"
	"time"
)

var gifPalette = color.Palette{color.Transparent, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 255, 0, 255}, color.RGBA{0, 0, 255, 255}, color.RGBA{255, 255, 0, 255}}

func gifTestData(t testing.TB, loop int) []byte {
	t.Helper()
	frames := []*image.Paletted{
		image.NewPaletted(image.Rect(0, 0, 3, 1), gifPalette),
		image.NewPaletted(image.Rect(0, 0, 2, 1), gifPalette),
		image.NewPaletted(image.Rect(2, 0, 3, 1), gifPalette),
		image.NewPaletted(image.Rect(0, 0, 1, 1), gifPalette),
	}
	copy(frames[0].Pix, []byte{1, 1, 1})
	copy(frames[1].Pix, []byte{0, 2})
	frames[2].Pix[0] = 3
	frames[3].Pix[0] = 4
	var out bytes.Buffer
	if err := gif.EncodeAll(&out, &gif.GIF{Image: frames, Delay: []int{0, 1, 2, 15}, Disposal: []byte{gif.DisposalNone, gif.DisposalPrevious, gif.DisposalBackground, gif.DisposalNone}, LoopCount: loop, Config: image.Config{ColorModel: gifPalette, Width: 3, Height: 1}}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func assertGIFPixels(t *testing.T, img image.Image, indexes ...int) {
	t.Helper()
	for x, index := range indexes {
		if got, want := color.RGBAModel.Convert(img.At(x, 0)), color.RGBAModel.Convert(gifPalette[index]); got != want {
			t.Fatalf("pixel %d: %v, want %v", x, got, want)
		}
	}
}

func TestGIFCompositionAndImmutableFrames(t *testing.T) {
	img, p, err := DecodeGIF(gifTestData(t, -1), true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Frames() != 4 || p.Frame() != 1 {
		t.Fatal("wrong frame count")
	}
	assertGIFPixels(t, img, 1, 1, 1)
	next, ok := p.Next()
	if !ok {
		t.Fatal("stopped early")
	}
	assertGIFPixels(t, next.Image(), 1, 2, 1)
	next, _ = next.Next()
	assertGIFPixels(t, next.Image(), 1, 1, 3)
	next, _ = next.Next()
	assertGIFPixels(t, next.Image(), 4, 1, 0)
	assertGIFPixels(t, img, 1, 1, 1)
	if last, ok := next.Next(); ok || last != next {
		t.Fatal("once-only GIF did not stop on last frame")
	}
	// Several consumers may advance/export from the same immutable position.
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { _, _ = p.Next() })
	}
	wg.Wait()
	assertGIFPixels(t, img, 1, 1, 1)
}

func TestGIFLoopsAndDelays(t *testing.T) {
	for _, loop := range []int{-1, 0, 1, 2} {
		_, p, err := DecodeGIF(gifTestData(t, loop), true)
		if err != nil {
			t.Fatal(err)
		}
		for i, want := range []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 20 * time.Millisecond, 150 * time.Millisecond} {
			if p.Delay() != want {
				t.Fatalf("frame %d delay %v, want %v", i, p.Delay(), want)
			}
			if i < 3 {
				p, _ = p.Next()
			}
		}
		p = p.Restart()
		count := 1
		for count < 20 {
			next, more := p.Next()
			if !more {
				break
			}
			p = next
			count++
		}
		want := 4 * (loop + 1)
		if loop < 0 {
			want = 4
		}
		if loop == 0 {
			want = 20
		}
		if count != want {
			t.Fatalf("loop %d: %d frames, want %d", loop, count, want)
		}
		assertGIFPixels(t, p.Restart().Image(), 1, 1, 1)
	}
}

func TestGIFLogicalCanvasAndStillLoading(t *testing.T) {
	palette := color.Palette{color.RGBA{0, 0, 255, 255}, color.RGBA{255, 0, 0, 255}}
	frame := image.NewPaletted(image.Rect(1, 1, 2, 2), palette)
	frame.Pix[0] = 1
	var data bytes.Buffer
	if err := gif.EncodeAll(&data, &gif.GIF{Image: []*image.Paletted{frame}, Delay: []int{0}, Config: image.Config{ColorModel: palette, Width: 3, Height: 2}}); err != nil {
		t.Fatal(err)
	}
	for _, animate := range []bool{false, true} {
		img, p, err := DecodeGIF(data.Bytes(), animate)
		if err != nil || p != nil || img.Bounds() != image.Rect(0, 0, 3, 2) {
			t.Fatalf("single frame canvas: %v %v", p, err)
		}
		if img.At(0, 0) != palette[0] || img.At(1, 1) != palette[1] {
			t.Fatal("logical background or frame offset lost")
		}
	}

}

func TestGIFPreflightBoundsAndTruncation(t *testing.T) {
	data := gifTestData(t, 0)
	for i := 0; i < len(data); i++ {
		if err := checkGIF(data[:i]); err == nil {
			t.Fatalf("accepted truncated input of %d bytes", i)
		}
	}
	for _, tc := range []struct {
		w, h, n int
		want    string
	}{
		{1, 1, MaxGIFFrames + 1, "frames"},
		{8192, 4096, 3, "decoded pixels"},
		{8192, 4097, 1, "canvas"},
		{0, 1, 1, "canvas"},
	} {
		// Pixel data is deliberately invalid: the bound must fail BEFORE DecodeAll
		// reaches the LZW decoder or allocates any frame-sized buffer.
		raw := []byte("GIF89a\x00\x00\x00\x00\x00\x00\x00")
		binary.LittleEndian.PutUint16(raw[6:8], uint16(tc.w))
		binary.LittleEndian.PutUint16(raw[8:10], uint16(tc.h))
		for range tc.n {
			d := make([]byte, 12)
			d[0] = 0x2c
			binary.LittleEndian.PutUint16(d[5:7], uint16(tc.w))
			binary.LittleEndian.PutUint16(d[7:9], uint16(tc.h))
			d[10] = 2
			raw = append(raw, d...)
		}
		raw = append(raw, 0x3b)
		if err := checkGIF(raw); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	// A valid 1,000-frame file reaches the decoder, while 1,001 does not.
	frame := image.NewPaletted(image.Rect(0, 0, 1, 1), gifPalette)
	g := &gif.GIF{LoopCount: -1}
	for range MaxGIFFrames + 1 {
		g.Image = append(g.Image, frame)
		g.Delay = append(g.Delay, 2)
	}
	for _, n := range []int{MaxGIFFrames, MaxGIFFrames + 1} {
		var b bytes.Buffer
		part := *g
		part.Image = part.Image[:n]
		part.Delay = part.Delay[:n]
		if err := gif.EncodeAll(&b, &part); err != nil {
			t.Fatal(err)
		}
		_, p, err := DecodeGIF(b.Bytes(), true)
		if n == MaxGIFFrames {
			if err != nil || p.Frames() != n {
				t.Fatalf("boundary rejected: %v", err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "1000 frames") {
			t.Fatalf("boundary accepted: %v", err)
		}
	}
}

func FuzzGIFPreflight(f *testing.F) {
	f.Add([]byte("GIF89a\x01\x00\x01\x00\x00\x00\x00\x3b"))
	f.Add(gifTestData(f, 0))
	f.Add(gifTestData(f, -1))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		_ = checkGIF(data)
		limits := Limits{
			MaxInputBytes:    1 << 16,
			MaxCanvasPixels:  4096,
			MaxFrames:        16,
			MaxGIFFrameBytes: 1 << 18,
		}
		_, _ = PreflightWithLimits(t.Context(), data, limits)
		img, p, err := DecodeContext(t.Context(), data, Options{
			Animate: true,
			Limits:  limits,
		})
		if err != nil {
			return
		}
		if img == nil || img.Bounds().Empty() {
			t.Fatalf("empty decoded GIF image: %v", img)
		}
		if p != nil {
			cur := p
			for step := range min(8, p.Frames()+2) {
				target := (step % p.Frames()) + 1
				s1, err1 := cur.Seek(t.Context(), target)
				s2, err2 := p.Restart().Seek(t.Context(), target)
				if (err1 != nil) != (err2 != nil) {
					t.Fatalf("seek error mismatch at frame %d->%d: %v vs %v", cur.Frame(), target, err1, err2)
				}
				if err1 == nil {
					b := s1.Image().Bounds()
					for y := b.Min.Y; y < b.Max.Y; y++ {
						for x := b.Min.X; x < b.Max.X; x++ {
							if s1.Image().At(x, y) != s2.Image().At(x, y) {
								t.Fatalf("seek pixel mismatch at (%d,%d) from frame %d->%d", x, y, cur.Frame(), target)
							}
						}
					}
				}
				next, more := cur.Next()
				if !more {
					break
				}
				cur = next
			}
		}
	})
}

func TestGIFRejectsEmptyAndOutOfBoundsFrames(t *testing.T) {
	data := gifTestData(t, 0)
	// Find the first image descriptor without assuming where encoder extensions end.
	index := bytes.Index(data, []byte{0x2c, 0, 0, 0, 0, 3, 0, 1, 0})
	if index < 0 {
		t.Fatal("fixture descriptor missing")
	}
	for _, width := range []uint16{0, 4} {
		bad := bytes.Clone(data)
		binary.LittleEndian.PutUint16(bad[index+5:index+7], width)
		if _, _, err := DecodeGIF(bad, true); err == nil {
			t.Fatal(fmt.Sprint("accepted width ", width))
		}
	}
}

func TestGIFSeekDisposalCancellationAndImmutability(t *testing.T) {
	_, first, err := DecodeGIF(gifTestData(t, 1), true)
	if err != nil {
		t.Fatal(err)
	}
	positions := []*Player{first}
	for len(positions) < 4 {
		next, _ := positions[len(positions)-1].Next()
		positions = append(positions, next)
	}
	want := [][]int{{1, 1, 1}, {1, 2, 1}, {1, 1, 3}, {4, 1, 0}}
	for _, from := range positions {
		for target := 1; target <= 4; target++ {
			got, err := from.Seek(context.Background(), target)
			if err != nil || got.Frame() != target {
				t.Fatalf("seek %d: %v", target, err)
			}
			assertGIFPixels(t, got.Image(), want[target-1]...)
			// Subsequent playback must retain a valid disposal-previous snapshot.
			if target < 4 {
				next, _ := got.Next()
				assertGIFPixels(t, next.Image(), want[target]...)
			}
			assertGIFPixels(t, from.Image(), want[from.Frame()-1]...)
		}
	}
	for _, target := range []int{-10, 100} {
		got, err := first.Seek(context.Background(), target)
		if err != nil || got.Frame() != max(1, min(target, 4)) {
			t.Fatal("seek did not clamp")
		}
	}
	last := positions[3]
	repeated, _ := last.Next()
	sought, err := repeated.Seek(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, more := sought.Next(); !more {
		t.Fatal("seek did not reset finite loop count")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := first.Seek(ctx, 4); got != nil || err != context.Canceled {
		t.Fatal("cancelled seek composed a frame")
	}
	var wg sync.WaitGroup
	for _, target := range []int{1, 2, 3, 4} {
		wg.Go(func() { _, _ = last.Seek(context.Background(), target) })
	}
	wg.Wait()
	assertGIFPixels(t, last.Image(), want[3]...)
}
