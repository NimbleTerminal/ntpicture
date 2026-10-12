package animation

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"
)

func TestDecodeLimitsAndCancellation(t *testing.T) {
	data := gifTestData(t, 0)
	for _, l := range []Limits{{MaxInputBytes: len(data) - 1}, {MaxCanvasPixels: 2}, {MaxFrames: 3}, {MaxGIFFrameBytes: 1}, {MaxFrames: -1}, {MaxFrames: MaxGIFFrames + 1}} {
		if _, _, err := DecodeContext(context.Background(), data, Options{Animate: true, Limits: l}); err == nil {
			t.Fatalf("accepted limits %+v", l)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := DecodeContext(ctx, data, Options{Animate: true}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_, p, err := Decode(data, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.NextContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestPlayerOwnsConstructionAndWebPInput(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	c := &Clip{Bounds: img.Bounds(), Frames: []ClipFrame{{Image: img, Delay: time.Second, Blend: draw.Src}}}
	p, err := NewPlayer(c)
	if err != nil {
		t.Fatal(err)
	}
	img.SetRGBA(0, 0, color.RGBA{B: 255, A: 255})
	c.Frames[0].Delay = time.Minute
	if p.Delay() != time.Second {
		t.Fatal("frame metadata was borrowed")
	}
	if r, _, _, _ := p.Restart().Image().At(0, 0).RGBA(); r != 65535 {
		t.Fatal("frame pixels were borrowed")
	}
	if _, ok := p.Image().(draw.Image); ok {
		t.Fatal("snapshot exposes mutable image")
	}
	data := webpTestData(t, webpFourFrames(0))
	_, p, err = Decode(data, true)
	if err != nil {
		t.Fatal(err)
	}
	for i := range data {
		data[i] = 0
	}
	next, _, err := p.NextContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertRow(t, next.Image(), red, green, red)
}
func TestLazyErrorIsIndexedAndCancellable(t *testing.T) {
	cause := errors.New("bad pixels")
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	c := &Clip{Bounds: img.Bounds(), Frames: []ClipFrame{
		{Image: img, Delay: time.Second},
		{Rect: img.Bounds(), Delay: time.Second, LoadContext: func(ctx context.Context) (image.Image, error) { return nil, cause }},
	}}
	p, err := NewPlayer(c)
	if err != nil {
		t.Fatal(err)
	}
	next, more, err := p.NextContext(context.Background())
	var frameError *FrameError
	if !more || !errors.As(err, &frameError) || frameError.Frame != 2 || !errors.Is(err, cause) || next.Err() != err {
		t.Fatalf("unreported lazy error: %v", err)
	}
	if _, err = p.Seek(context.Background(), 2); !errors.Is(err, cause) {
		t.Fatalf("seek error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.Frames[1].LoadContext = func(ctx context.Context) (image.Image, error) { cancel(); return nil, ctx.Err() }
	p, err = NewPlayer(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.NextContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestWebPRejectsChunkFloodAndOversizedBitstream(t *testing.T) {
	many := webpFourFrames(0)
	for range 4097 {
		many.extra = append(many.extra, chunk("JUNK", nil)...)
	}
	if _, err := PreflightWebP(webpTestData(t, many)); err == nil {
		t.Fatal("unbounded chunk metadata")
	}
	// A five-byte VP8L header declares 16384x16384 pixels under a 1x1 ANMF.
	// Its config must be rejected before the pixel codec sees this size.
	f := webpFrameInfo{rect: image.Rect(0, 0, 1, 1), data: chunk("VP8L", []byte{0x2f, 0xff, 0xff, 0xff, 0x0f})}
	if _, err := decodeWebPFrame(f); err == nil {
		t.Fatal("oversized bitstream accepted")
	}
}
