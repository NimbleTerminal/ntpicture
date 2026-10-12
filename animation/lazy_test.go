package animation

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"sync/atomic"
	"testing"
	"time"
)

func solid(rect image.Rectangle, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(rect)
	draw.Draw(img, rect, image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

// A clip whose frames after the first are decoded when painted: the second
// frame counts its loads, the third fails to decode.
func lazyClip(t *testing.T, loads *atomic.Int32) *Clip {
	t.Helper()
	return &Clip{Bounds: image.Rect(0, 0, 3, 1), LoopCount: 0, Frames: []ClipFrame{
		{Image: solid(image.Rect(0, 0, 3, 1), red), Rect: image.Rect(0, 0, 3, 1), Delay: time.Second, Blend: draw.Src},
		{Rect: image.Rect(1, 0, 2, 1), Delay: time.Second, Blend: draw.Src, Load: func() (image.Image, error) {
			loads.Add(1)
			return solid(image.Rect(1, 0, 2, 1), green), nil
		}},
		{Rect: image.Rect(2, 0, 3, 1), Delay: time.Second, Blend: draw.Src, Disposal: DisposalBackground, Load: func() (image.Image, error) {
			return nil, errors.New("corrupt frame")
		}},
		{Rect: image.Rect(0, 0, 1, 1), Delay: time.Second, Blend: draw.Src, Load: func() (image.Image, error) {
			return solid(image.Rect(0, 0, 1, 1), blue), nil
		}},
	}}
}

func TestLazyFramesDecodeWhenPainted(t *testing.T) {
	var loads atomic.Int32
	p, err := NewPlayer(lazyClip(t, &loads))
	if err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 0 {
		t.Fatal("frames decoded before they were shown")
	}
	assertRow(t, p.Image(), red, red, red)
	p, _ = p.Next()
	assertRow(t, p.Image(), red, green, red)
	if loads.Load() != 1 {
		t.Fatalf("second frame loaded %d times", loads.Load())
	}
	// A frame that fails to decode leaves the canvas as it was, and its
	// disposal still applies afterwards.
	p, _ = p.Next()
	assertRow(t, p.Image(), red, green, red)
	p, _ = p.Next()
	assertRow(t, p.Image(), blue, green, clear)
	// Seeking decodes only the frames it replays.
	s, err := p.Seek(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	assertRow(t, s.Image(), red, green, red)
	if loads.Load() != 2 {
		t.Fatalf("seek loaded the second frame %d times in all", loads.Load())
	}
}

func TestLazyFramesValidation(t *testing.T) {
	var loads atomic.Int32
	c := lazyClip(t, &loads)
	// Lazy frames hold no decoded storage, so a huge placement is fine.
	c.Bounds = image.Rect(0, 0, 4000, 4000)
	c.Frames[1].Rect = image.Rect(0, 0, 4000, 4000)
	if _, err := NewPlayer(c); err != nil {
		t.Fatal(err)
	}
	bad := lazyClip(t, &loads)
	bad.Frames[1].Rect = image.Rectangle{}
	if _, err := NewPlayer(bad); err == nil {
		t.Fatal("lazy frame without a placement accepted")
	}
	bad = lazyClip(t, &loads)
	bad.Frames[1].Load = nil
	if _, err := NewPlayer(bad); err == nil {
		t.Fatal("frame without image or loader accepted")
	}
	bad = lazyClip(t, &loads)
	bad.Frames[0].Rect = image.Rect(0, 0, 1, 1)
	if _, err := NewPlayer(bad); err == nil {
		t.Fatal("decoded frame whose placement disagrees with its image accepted")
	}
}
