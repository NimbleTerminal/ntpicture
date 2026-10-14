package playback

import (
	"context"
	"image"
	"image/color"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntpicture/animation"
)

func distinctPlayer(t *testing.T, frames int) *animation.Player {
	t.Helper()
	bounds := image.Rect(0, 0, 2, 2)
	clip := &animation.Clip{Bounds: bounds, LoopCount: -1}
	for i := range frames {
		img := image.NewRGBA(bounds)
		for y := range 2 {
			for x := range 2 {
				img.Set(x, y, color.RGBA{R: uint8(60 * (i + 1)), A: 255})
			}
		}
		clip.Frames = append(clip.Frames, animation.ClipFrame{Image: img, Rect: bounds, Delay: time.Millisecond})
	}
	p, err := animation.NewPlayer(clip)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func presentedFrame(c *Controller) int {
	if p := c.Status().PresentedPlayer; p != nil {
		return p.Frame()
	}
	return 0
}

// seekResult runs a seek's codec work and returns the Picture preparation.
func seekResult(t *testing.T, c *Controller, frame int) tea.Cmd {
	t.Helper()
	cmd := c.Seek(frame)
	if cmd == nil {
		t.Fatal("no seek command")
	}
	return c.Update(cmd())
}

func TestPresentedTracksCommittedPosition(t *testing.T) {
	c := New(distinctPlayer(t, 3), Options{})
	c.Pause(true)
	c.SetSize(2, 2)
	if c.Status().Presented != nil {
		t.Fatal("presented before any commit")
	}
	drive(t, c, c.SetPlayer(c.player))
	if presentedFrame(c) != 1 || c.Status().Presented != c.Status().Source {
		t.Fatalf("initial frame not presented: %d", presentedFrame(c))
	}

	prepare := seekResult(t, c, 3)
	if c.Status().Player.Frame() != 3 || presentedFrame(c) != 1 {
		t.Fatal("presented led its commit")
	}
	drive(t, c, prepare)
	if presentedFrame(c) != 3 {
		t.Fatalf("seek commit not presented: %d", presentedFrame(c))
	}
	if s := c.Status().Picture; s.Presented != s.Generation {
		t.Fatalf("picture status not exposed: %+v", s)
	}

	// A superseded submission never becomes presented, even if its
	// preparation completes late. Rejecting it starts the latest preparation.
	stale := seekResult(t, c, 2)
	latest := seekResult(t, c, 1)
	drive(t, c, stale)
	if presentedFrame(c) == 2 {
		t.Fatal("superseded frame presented")
	}
	drive(t, c, latest)
	if presentedFrame(c) != 1 {
		t.Fatalf("latest frame not presented: %d", presentedFrame(c))
	}

	// Re-preparing the same image keeps the same position.
	before := c.Status().Presented
	drive(t, c, c.SetSize(3, 3))
	if c.Status().Presented != before {
		t.Fatal("resize changed the presented position")
	}

	drive(t, c, c.SetSource(nil))
	if c.Status().Presented != nil {
		t.Fatal("cleared picture still reports a presented position")
	}
	drive(t, c, c.Close())
}

func TestPresentedSurvivesKeepAndClearsOnClose(t *testing.T) {
	c := New(distinctPlayer(t, 2), Options{})
	c.Pause(true)
	c.SetSize(2, 2)
	drive(t, c, c.SetPlayer(c.player))
	kept := c.Status().Presented
	drive(t, c, c.Keep())
	if kept == nil || c.Status().Presented != kept {
		t.Fatal("Keep discarded the presented position")
	}

	c = New(distinctPlayer(t, 2), Options{})
	c.Pause(true)
	c.SetSize(2, 2)
	drive(t, c, c.SetPlayer(c.player))
	drive(t, c, c.Close())
	if c.Status().Presented != nil {
		t.Fatal("Close retained the presented position")
	}
}

// sameSource advances through positions that share one image.
type sameSource struct {
	img  image.Image
	step int
}

func (s *sameSource) Image() image.Image   { return s.img }
func (s *sameSource) Delay() time.Duration { return time.Millisecond }
func (s *sameSource) Advance(context.Context) (animation.Source, bool, error) {
	if s.step == 2 {
		return s, false, nil
	}
	return &sameSource{s.img, s.step + 1}, true, nil
}
func (s *sameSource) Reset(context.Context) (animation.Source, error) {
	return &sameSource{s.img, 0}, nil
}

func TestPresentedFollowsPositionsWithIdenticalPixels(t *testing.T) {
	source := &sameSource{img: image.NewRGBA(image.Rect(0, 0, 2, 2))}
	c := NewSource(source, Options{Wait: func(context.Context, time.Duration) bool { return true }})
	c.SetSize(2, 2)
	drive(t, c, c.SetSource(source))
	s := c.Status()
	if !s.Finished || s.Source.(*sameSource).step != 2 {
		t.Fatalf("unexpected playback: %+v", s)
	}
	if s.Presented != s.Source || s.Picture.Generation != 2 {
		t.Fatalf("unsubmitted identical position not presented: %+v", s)
	}
	drive(t, c, c.Close())
}
