// Window regression cases ported from Gloss (MIT, Neomantra Corp).
package animation

import (
	"context"
	"errors"
	"image"
	"image/color"
	"sync/atomic"
	"testing"
	"time"
)

// A window of a composite shows exactly what the full composite shows there,
// frame after frame, and a page laid out again can take over the positions
// its windows have reached.
func TestCompositeWindowMatchesPage(t *testing.T) {
	base := image.NewRGBA(image.Rect(0, 0, 60, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 60; x++ {
			base.SetRGBA(x, y, color.RGBA{uint8(x * 4), uint8(y * 6), 90, 255})
		}
	}
	_, player, err := DecodeGIF(gifTestData(t, 0), true)
	if err != nil {
		t.Fatal(err)
	}
	regions := []Region{{Rect: image.Rect(10, 10, 40, 30), Player: player, Key: "a.gif"}}
	page, err := NewComposite(context.Background(), base, regions, nil)
	if err != nil {
		t.Fatal(err)
	}
	window := image.Rect(5, 15, 45, 40)
	w, err := page.Window(context.Background(), window, image.Point{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Image().Bounds() != image.Rect(0, 0, 40, 25) || len(w.regions) != 1 || w.regions[0].Rect != image.Rect(5, -5, 35, 15) {
		t.Fatalf("window geometry: %v %+v", w.Image().Bounds(), w.regions)
	}
	same := func(step string) {
		full, part := page.Image(), w.Image()
		for y := window.Min.Y; y < window.Max.Y; y++ {
			for x := window.Min.X; x < window.Max.X; x++ {
				if full.At(x, y) != part.At(x-window.Min.X, y-window.Min.Y) {
					t.Fatalf("%s: pixel %d,%d differs: %v vs %v", step, x, y, full.At(x, y), part.At(x-window.Min.X, y-window.Min.Y))
				}
			}
		}
	}
	same("first frame")
	for i := 0; i < 3; i++ {
		nextPage, _, err := page.Advance(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		nextWindow, _, err := w.Advance(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		page, w = nextPage.(*Composite), nextWindow.(*Composite)
		d1, d2 := page.Delay(), w.Delay()
		if d1 != d2 {
			t.Fatalf("frame %d: delays differ %v %v", i, d1, d2)
		}
		same("after frame")
	}
	empty, err := page.Window(context.Background(), image.Rect(50, 0, 60, 10), image.Point{})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.regions) != 0 {
		t.Fatal("a window away from every region kept one")
	}
	fresh, err := NewComposite(context.Background(), base, regions, nil)
	if err != nil {
		t.Fatal(err)
	}
	carried, err := fresh.WithPositions(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if carried.Delay() != w.Delay() {
		t.Fatal("positions were not carried")
	}
	next, err := NewComposite(context.Background(), base, regions, carried)
	if err != nil {
		t.Fatal(err)
	}
	if next.Image().At(12, 12) != page.Image().At(12, 12) {
		t.Fatal("a relayout from the carried positions shows a different frame")
	}
}

// A window at another size resamples the base once and places each region at
// that scale, snapping to its frames' own size so they are copied in.
func TestCompositeWindowAtOwnResolution(t *testing.T) {
	base := image.NewRGBA(image.Rect(0, 0, 90, 60))
	for y := 0; y < 60; y++ {
		for x := 0; x < 90; x++ {
			base.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), 0, 255})
		}
	}
	_, player, _ := DecodeGIF(gifTestData(t, 0), true)
	own := player.Image().Bounds().Size() // 3×1
	// The page shows the GIF at 1.5× its pixels: 3×1 drawn in a 5×2 (rounded) rectangle.
	page, err := NewComposite(context.Background(), base, []Region{{Rect: image.Rect(30, 15, 35, 17), Player: player, Key: "a.gif"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	w, err := page.Window(context.Background(), image.Rect(15, 0, 60, 30), image.Pt(30, 20))
	if err != nil {
		t.Fatal(err)
	}
	if w.Image().Bounds().Size() != image.Pt(30, 20) {
		t.Fatalf("window size %v", w.Image().Bounds())
	}
	if len(w.regions) != 1 || w.regions[0].Rect != image.Rect(10, 10, 10+own.X, 10+own.Y) {
		t.Fatalf("region not at its own size and place: %+v", w.regions)
	}
	if c := w.Image().At(0, 0); c != base.At(15, 0) && c != base.At(16, 1) {
		t.Fatalf("base not resampled from the window: %v", c)
	}
	if w.Image().At(10, 10) != player.Image().At(0, 0) {
		t.Fatal("frame not copied at its own pixels")
	}
}

func TestCompositeOwnershipCarryAndBounds(t *testing.T) {
	ctx := context.Background()
	_, p, err := DecodeGIF(gifTestData(t, 0), true)
	if err != nil {
		t.Fatal(err)
	}
	base := image.NewRGBA(image.Rect(0, 0, 8, 8))
	regions := []Region{{image.Rect(0, 0, 3, 1), p, "a"}, {image.Rect(4, 0, 7, 1), p, "a"}}
	s, err := NewComposite(ctx, base, regions, nil)
	if err != nil {
		t.Fatal(err)
	}
	original := s.Image().At(7, 7)
	base.SetRGBA(7, 7, color.RGBA{R: 255, A: 255})
	regions[0].Key = "mutated"
	metadata := s.Regions()
	metadata[0].Key = "also mutated"
	if s.Image().At(7, 7) != original || s.Regions()[0].Key != "a" {
		t.Fatal("constructor retained mutable input")
	}
	next, more, err := s.Advance(ctx)
	if err != nil || !more {
		t.Fatal(err)
	}
	advanced := next.(*Composite)
	if s.positions["a"].player.Frame() != 1 || advanced.positions["a"].player.Frame() != 2 {
		t.Fatal("advance mutated snapshot or advanced duplicate key twice")
	}
	reset, err := advanced.Reset(ctx)
	if err != nil || reset.(*Composite).positions["a"].player.Frame() != 1 {
		t.Fatal("restart failed")
	}
	removed, err := NewComposite(ctx, base, nil, advanced)
	if err != nil || len(removed.positions) != 0 {
		t.Fatal("removed keys retained")
	}
	_, replacement, _ := DecodeGIF(gifTestData(t, 0), true)
	replaced, err := NewComposite(ctx, base, []Region{{image.Rect(0, 0, 3, 1), replacement, "a"}}, advanced)
	if err != nil || replaced.positions["a"].player != replacement {
		t.Fatal("replacement inherited another clip's position")
	}
	if _, err = NewComposite(ctx, base, []Region{{image.Rect(0, 0, 3, 1), p, "a"}, {image.Rect(0, 1, 3, 2), replacement, "a"}}, nil); err == nil {
		t.Fatal("conflicting key accepted")
	}
	if _, err = s.Window(ctx, image.Rect(10, 10, 20, 20), image.Point{}); err == nil {
		t.Fatal("empty window accepted")
	}
	if _, err = s.Window(ctx, base.Bounds(), image.Pt(MaxPixels, 2)); err == nil {
		t.Fatal("oversized window accepted")
	}
	if _, err = NewComposite(ctx, base, make([]Region, MaxCompositeRegions+1), nil); err == nil {
		t.Fatal("unbounded regions")
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err = s.Advance(cancelCtx); err != context.Canceled {
		t.Fatal("advance ignored cancellation")
	}
	if _, err = s.Window(cancelCtx, base.Bounds(), image.Point{}); err != context.Canceled {
		t.Fatal("window ignored cancellation")
	}
}

func TestCompositeLazyErrorIsAtomicAndIdentifiesRegion(t *testing.T) {
	var loads atomic.Int32
	p, err := NewPlayer(lazyClip(t, &loads))
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewComposite(context.Background(), image.NewRGBA(image.Rect(0, 0, 3, 1)), []Region{{image.Rect(0, 0, 3, 1), p, "bad"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := source.Advance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	old := next.Image()
	failed, _, err := next.Advance(context.Background())
	var region *RegionError
	var frame *FrameError
	if !errors.As(err, &region) || region.Key != "bad" || !errors.As(err, &frame) || frame.Frame != 3 {
		t.Fatalf("missing error context: %v", err)
	}
	if failed != next || next.Image() != old || source.positions["bad"].player.Frame() != 1 {
		t.Fatal("failure mutated a published source")
	}
}

func TestCompositeEarliestDueAndIndependentLoops(t *testing.T) {
	makePlayer := func(delay time.Duration) *Player {
		img := image.NewRGBA(image.Rect(0, 0, 1, 1))
		p, err := NewPlayer(&Clip{Bounds: img.Bounds(), LoopCount: -1, Frames: []ClipFrame{{Image: img, Delay: delay}, {Image: img, Delay: delay}}})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	fast, slow := makePlayer(10*time.Millisecond), makePlayer(30*time.Millisecond)
	source, err := NewComposite(context.Background(), image.NewRGBA(image.Rect(0, 0, 2, 1)), []Region{{image.Rect(0, 0, 1, 1), fast, "fast"}, {image.Rect(1, 0, 2, 1), slow, "slow"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []struct {
		delay      time.Duration
		fast, slow int
		more       bool
	}{
		{10 * time.Millisecond, 2, 1, true},
		{10 * time.Millisecond, 2, 1, true},
		{30 * time.Millisecond, 2, 2, true},
		{0, 2, 2, false},
	} {
		next, more, err := source.Advance(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		source = next.(*Composite)
		if source.Delay() != want.delay || source.positions["fast"].player.Frame() != want.fast || source.positions["slow"].player.Frame() != want.slow || more != want.more {
			t.Fatalf("step %d: incorrect independent timeline", i)
		}
	}
}

func TestCompositeCancellationDuringLazyDecode(t *testing.T) {
	started := make(chan struct{})
	bounds := image.Rect(0, 0, 1, 1)
	player, err := NewPlayer(&Clip{Bounds: bounds, Frames: []ClipFrame{
		{Image: image.NewRGBA(bounds), Delay: time.Second},
		{Rect: bounds, Delay: time.Second, LoadContext: func(ctx context.Context) (image.Image, error) { close(started); <-ctx.Done(); return nil, ctx.Err() }},
	}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewComposite(context.Background(), image.NewRGBA(bounds), []Region{{bounds, player, "lazy"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := source.Advance(ctx); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("decode never started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("decode was not cancelled")
	}
	if source.positions["lazy"].player.Frame() != 1 {
		t.Fatal("cancelled work changed published position")
	}
}
