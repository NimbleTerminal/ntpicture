package playback

import (
	"context"
	"image"
	"image/draw"
	"testing"

	"nimbleterminal.dev/ntpicture/animation"
)

// cropped is a host-style presentation transform over a player.
type cropped struct {
	*animation.Player
	rect image.Rectangle
	img  *image.RGBA
}

func crop(p *animation.Player, rect image.Rectangle) *cropped {
	img := image.NewRGBA(image.Rectangle{Max: rect.Size()})
	draw.Draw(img, img.Bounds(), p.Image(), rect.Min, draw.Src)
	return &cropped{p, rect, img}
}
func (c *cropped) Image() image.Image { return c.img }
func (c *cropped) wrap(s animation.Source) animation.Source {
	return crop(s.(*animation.Player), c.rect)
}
func (c *cropped) Advance(ctx context.Context) (animation.Source, bool, error) {
	n, more, err := c.Player.Advance(ctx)
	return c.wrap(n), more, err
}
func (c *cropped) Reset(ctx context.Context) (animation.Source, error) {
	n, err := c.Player.Reset(ctx)
	if err != nil {
		return nil, err
	}
	return c.wrap(n), nil
}
func (c *cropped) SeekFrame(ctx context.Context, frame int) (animation.Source, error) {
	n, err := c.Player.SeekFrame(ctx, frame)
	if err != nil {
		return nil, err
	}
	return c.wrap(n), nil
}

// opaque hides every method beyond Source.
type opaque struct{ animation.Source }

func TestSeekThroughCustomSeekableSource(t *testing.T) {
	src := crop(distinctPlayer(t, 3), image.Rect(0, 0, 1, 1))
	c := NewSource(src, Options{})
	c.Pause(true)
	c.SetSize(1, 1)
	drive(t, c, c.SetSource(src))
	drive(t, c, c.Seek(3))
	s := c.Status()
	got, ok := s.Source.(*cropped)
	if s.Err != nil || !ok || got.Frame() != 3 || !s.Paused {
		t.Fatalf("custom seek failed: %+v", s)
	}
	if s.Presented != s.Source || s.Player != nil {
		t.Fatalf("seek result not presented as the custom source: %+v", s)
	}
	if b := got.Image().Bounds(); b != image.Rect(0, 0, 1, 1) {
		t.Fatalf("transform lost across seek: %v", b)
	}
	if _, _, _, a := got.Image().At(0, 0).RGBA(); a == 0 {
		t.Fatal("seeked frame not composed")
	}
	drive(t, c, c.Seek(99)) // Clamped like Player.Seek.
	if c.Status().Source.(*cropped).Frame() != 3 {
		t.Fatal("out-of-range seek not clamped")
	}
	drive(t, c, c.Close())

	c = NewSource(opaque{distinctPlayer(t, 3)}, Options{})
	c.SetSize(1, 1)
	if c.Seek(2) != nil || c.Status().Err != ErrNotSeekable || !c.Status().Paused {
		t.Fatalf("non-seekable source: %+v", c.Status())
	}
	drive(t, c, c.Close())
}
