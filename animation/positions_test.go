package animation

import (
	"context"
	"image"
	"image/color"
	"slices"
	"testing"
	"time"
)

func positionsClip(t *testing.T, delay time.Duration, frames int, loop int) *Player {
	t.Helper()
	bounds := image.Rect(0, 0, 2, 1)
	clip := &Clip{Bounds: bounds, LoopCount: loop}
	for i := range frames {
		clip.Frames = append(clip.Frames, ClipFrame{Image: solid(bounds, color.NRGBA{R: uint8(40 * (i + 1)), A: 255}), Rect: bounds, Delay: delay})
	}
	p, err := NewPlayer(clip)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCompositePositionsSnapshotRestoresWithoutPixels(t *testing.T) {
	ctx := context.Background()
	fast := positionsClip(t, 10*time.Millisecond, 2, -1)
	slow := positionsClip(t, 30*time.Millisecond, 2, 0)
	regions := []Region{{image.Rect(0, 0, 2, 1), fast, "fast"}, {image.Rect(2, 0, 4, 1), slow, "slow"}}
	var s Source
	s, err := NewComposite(ctx, image.NewRGBA(image.Rect(0, 0, 4, 1)), regions, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if s, _, err = s.Advance(ctx); err != nil {
			t.Fatal(err)
		}
	}
	page := s.(*Composite)
	saved := page.Positions()
	if !page.positions["fast"].finished || page.positions["slow"].due != 10*time.Millisecond {
		t.Fatalf("unexpected fixture positions: %+v", page.positions)
	}
	if got := saved.Keys(); !slices.Equal(got, []string{"fast", "slow"}) {
		t.Fatalf("keys = %v", got)
	}
	if saved.positions == nil || &saved.positions == &page.positions {
		t.Fatal("snapshot shares composite state")
	}

	// Later playback of the source must not move the snapshot.
	if _, _, err = page.Advance(ctx); err != nil {
		t.Fatal(err)
	}

	// Restore onto a different base and layout; only key+clip matter.
	rebuilt, err := NewComposite(ctx, image.NewRGBA(image.Rect(0, 0, 8, 2)), []Region{{image.Rect(4, 1, 6, 2), slow, "slow"}, {image.Rect(0, 0, 2, 1), fast, "fast"}}, saved)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"fast", "slow"} {
		if rebuilt.positions[key] != page.positions[key] {
			t.Fatalf("%s position not restored: %+v vs %+v", key, rebuilt.positions[key], page.positions[key])
		}
	}
	if rebuilt.Delay() != 10*time.Millisecond {
		t.Fatalf("remaining delay = %v", rebuilt.Delay())
	}
	if rebuilt.Image().At(4, 1) != page.positions["slow"].player.Image().At(0, 0) {
		t.Fatal("restored composite not composed at saved frame")
	}

	// WithPositions accepts snapshots too, and a replaced clip restarts.
	other := positionsClip(t, 10*time.Millisecond, 2, -1)
	fresh, err := NewComposite(ctx, image.NewRGBA(image.Rect(0, 0, 4, 1)), []Region{{image.Rect(0, 0, 2, 1), other, "fast"}, {image.Rect(2, 0, 4, 1), slow, "slow"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	carried, err := fresh.WithPositions(ctx, saved)
	if err != nil {
		t.Fatal(err)
	}
	if carried.positions["fast"].player != other || carried.positions["fast"].finished || carried.positions["slow"] != page.positions["slow"] {
		t.Fatalf("snapshot carry ignored clip identity: %+v", carried.positions)
	}

	var none *Composite
	if empty := none.Positions(); len(empty.Keys()) != 0 {
		t.Fatal("nil composite exported positions")
	}
	if c, err := NewComposite(ctx, image.NewRGBA(image.Rect(0, 0, 4, 1)), regions, none); err != nil || c.positions["fast"].player != fast {
		t.Fatal("typed nil previous was not ignored")
	}
	if c, err := NewComposite(ctx, image.NewRGBA(image.Rect(0, 0, 4, 1)), regions, Positions{}); err != nil || c.positions["slow"].player != slow {
		t.Fatal("zero Positions was not ignored")
	}

	// Positions.WithPositions merges snapshots across windows without pixel work, later winning.
	wFast, err := page.Window(ctx, image.Rect(0, 0, 2, 1), image.Point{})
	if err != nil {
		t.Fatal(err)
	}
	wSlow, err := page.Window(ctx, image.Rect(2, 0, 4, 1), image.Point{})
	if err != nil {
		t.Fatal(err)
	}
	advancedSlow, _, err := wSlow.Advance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	merged := (Positions{}).WithPositions(nil, wFast.Positions(), advancedSlow.(*Composite))
	if got := merged.Keys(); !slices.Equal(got, []string{"fast", "slow"}) {
		t.Fatalf("merged keys = %v", got)
	}
	if merged.positions["slow"].player.Frame() != 2 {
		t.Fatalf("merged slow frame = %d, want 2", merged.positions["slow"].player.Frame())
	}
}
