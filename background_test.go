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

func TestPictureSetFitAndSetAnchorRetainFrontAndReprepare(t *testing.T) {
	p := New(Options{Render: Config{Fit: FitMode(99), Anchor: FitAnchor(-1), Background: color.RGBA{B: 255, A: 255}}, RetireDelay: -1})
	defer func() { drivePicture(t, p, p.Close()) }()
	if p.Fit() != FitContain || p.Anchor() != AnchorCenter {
		t.Fatalf("out-of-range config did not normalize: fit=%v anchor=%v", p.Fit(), p.Anchor())
	}
	if p.SetFit(FitMode(-5)) != nil || p.SetAnchor(FitAnchor(99)) != nil || p.Status().Generation != 0 {
		t.Fatal("equivalent normalized fit/anchor started a generation")
	}
	if p.SetFit(FitCover) != nil || p.SetAnchor(AnchorTop) != nil || p.Status().Generation != 0 {
		t.Fatal("fit/anchor without an image started a generation")
	}
	if p.Fit() != FitCover || p.Anchor() != AnchorTop {
		t.Fatalf("getters did not reflect pending values: fit=%v anchor=%v", p.Fit(), p.Anchor())
	}

	// 1x4 vertical strip: top pixel red, bottom pixel green.
	src := image.NewRGBA(image.Rect(0, 0, 1, 4))
	for y := range 4 {
		c := color.RGBA{G: 255, A: 255}
		if y == 0 {
			c = color.RGBA{R: 255, A: 255}
		}
		src.SetRGBA(0, y, c)
	}
	drivePicture(t, p, p.Submit(Frame{Image: src, Cols: 2, Rows: 1, Mode: PictureGlyph, CellWidth: 1, CellHeight: 2}))
	if p.front == nil || p.front.cfg.Fit != FitCover || p.front.cfg.Anchor != AnchorTop {
		t.Fatal("pending fit/anchor not applied to the next submission")
	}

	view, generation := p.View(), p.Status().Generation
	if p.SetFit(FitCover) != nil || p.SetAnchor(AnchorTop) != nil || p.Status().Generation != generation {
		t.Fatal("unchanged fit/anchor started a generation")
	}

	// Change anchor from AnchorTop to AnchorBottom under FitCover.
	cmd := p.SetAnchor(AnchorBottom)
	if cmd == nil || p.Status().Generation != generation+1 || p.Anchor() != AnchorBottom {
		t.Fatal("changed anchor did not start a generation")
	}
	if p.View() != view || p.front == nil {
		t.Fatal("front not retained while the anchor replacement prepares")
	}
	drivePicture(t, p, cmd)
	if p.Status().Presented != generation+1 || p.View() == view || p.front.cfg.Anchor != AnchorBottom {
		t.Fatal("anchor replacement not presented")
	}

	// Change fit from FitCover to FitContain (shows blue letterbox bars on 2x1 cells).
	view, generation = p.View(), p.Status().Generation
	cmd = p.SetFit(FitContain)
	if cmd == nil || p.Status().Generation != generation+1 || p.Fit() != FitContain {
		t.Fatal("changed fit did not start a generation")
	}
	if p.View() != view || p.front == nil {
		t.Fatal("front not retained while the fit replacement prepares")
	}
	drivePicture(t, p, cmd)
	if p.Status().Presented != generation+1 || p.View() == view || p.front.cfg.Fit != FitContain {
		t.Fatal("fit replacement not presented")
	}

	drivePicture(t, p, p.Close())
	if p.SetFit(FitFill) != nil || p.SetAnchor(AnchorLeft) != nil || p.Fit() != FitContain || p.Anchor() != AnchorBottom {
		t.Fatal("closed picture accepted fit/anchor mutation")
	}
}
