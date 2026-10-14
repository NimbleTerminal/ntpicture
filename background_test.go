package ntpicture

import (
	"image"
	"image/color"
	"testing"
)

func TestPictureSetBackgroundRetainsFrontAndReprepares(t *testing.T) {
	p := New(Options{Render: Config{Fit: FitFill}, RetireDelay: -1})
	defer func() { drivePicture(t, p, p.Close()) }()
	red := color.RGBA{R: 255, A: 255}
	if p.SetBackground(red) != nil || p.Status().Generation != 0 {
		t.Fatal("background without an image started a generation")
	}
	drivePicture(t, p, p.Submit(Frame{Image: image.NewNRGBA(image.Rect(0, 0, 2, 2)), Cols: 2, Rows: 1, Mode: PictureGlyph, CellWidth: 1, CellHeight: 2}))
	if p.front == nil || p.front.cfg.Background != color.RGBA64Model.Convert(red) {
		t.Fatal("pending background not applied to the next submission")
	}
	view, generation := p.View(), p.Status().Generation
	if p.SetBackground(color.NRGBA{R: 255, A: 255}) != nil || p.Status().Generation != generation {
		t.Fatal("equivalent background started a generation")
	}
	cmd := p.SetBackground(color.RGBA{B: 255, A: 255})
	if cmd == nil || p.Status().Generation != generation+1 {
		t.Fatal("changed background did not start a generation")
	}
	if p.View() != view || p.front == nil {
		t.Fatal("front not retained while the background replacement prepares")
	}
	drivePicture(t, p, cmd)
	if p.Status().Presented != generation+1 || p.View() == view {
		t.Fatal("background replacement not presented")
	}
	drivePicture(t, p, p.SetBackground(nil))
	if p.front.cfg.Background != color.RGBA64Model.Convert(color.Transparent) {
		t.Fatal("nil background is not transparent")
	}
}
