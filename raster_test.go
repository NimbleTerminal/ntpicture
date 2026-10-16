package ntpicture

import (
	"image"
	"image/color"
	"math"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func rasterPicture(t *testing.T) *Picture {
	t.Helper()
	p := New(Options{StableID: true, RetireDelay: -1, Render: Config{Fit: FitFill, KittyFormat: KittyFormatRGBA}})
	p.SetSize(7, 3)
	p.SetTerminal(true, 9, 17)
	t.Cleanup(func() { drivePicture(t, p, p.Close()) })
	return p
}

func TestRasterGeometryModesAndViewport(t *testing.T) {
	p := rasterPicture(t)
	p.SetResolution(.3)
	g := p.RasterGeometry()
	if g.Cells != image.Pt(7, 3) || g.Pixels != image.Pt(19, 15) || g.Mode != PictureKitty || math.Abs(g.DisplayAspect-63.0/51) > 1e-12 {
		t.Fatalf("unexpected geometry: %+v", g)
	}
	p.SetViewport(Viewport{Y: 1, Width: 7, Height: 1})
	if p.RasterGeometry() != g {
		t.Fatal("viewport changed raster")
	}
	p.SetTerminal(false, 9, 17)
	glyph := p.RasterGeometry()
	if glyph.Pixels != image.Pt(7, 6) || glyph.DisplayAspect != g.DisplayAspect {
		t.Fatalf("glyph: %+v", glyph)
	}
	if cmd := p.SetResolution(.5); cmd != nil {
		t.Fatal("glyph resolution scheduled work")
	}
	p.SetTerminal(true, 9, 17)
	if p.RasterGeometry().Pixels != image.Pt(32, 26) {
		t.Fatal("factor not remembered")
	}
	p.SetSize(0, 0)
	if g := p.RasterGeometry(); g.Pixels != (image.Point{}) || g.DisplayAspect != 0 {
		t.Fatalf("empty: %+v", g)
	}
}

func TestResolutionPayloadAndStablePlacement(t *testing.T) {
	p := rasterPicture(t)
	p.SetResolution(.3)
	size := p.RasterGeometry().Pixels
	img := image.NewRGBA(image.Rectangle{Max: size})
	for y := range size.Y {
		for x := range size.X {
			img.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	msgs := drivePicture(t, p, p.SetImageImmutable(img))
	if !strings.Contains(output(msgs), "s=19,v=15") {
		t.Fatalf("wrong encoded dimensions: %q", output(msgs))
	}
	front, view := p.front, p.View()
	if cmd := p.SetResolution(.301); cmd != nil || p.front != front {
		t.Fatal("identical rounded target scheduled work")
	}
	drivePicture(t, p, p.SetImageImmutable(img))
	if p.front.cfg.KittyID != front.cfg.KittyID {
		t.Fatal("same raster lost stable ID")
	}
	front = p.front
	cmd := p.SetResolution(.5)
	if p.View() != view || p.front != front {
		t.Fatal("resolution discarded front")
	}
	if p.RasterGeometry().Pixels != image.Pt(32, 26) {
		t.Fatal("query returned retained dimensions")
	}
	msgs = drivePicture(t, p, cmd)
	if !strings.Contains(output(msgs), "s=32,v=26") || p.front.cfg.KittyID == front.cfg.KittyID {
		t.Fatal("new raster did not use new dimensions and placement")
	}
	if p.RasterGeometry().Cells != image.Pt(7, 3) {
		t.Fatal("resolution changed cells")
	}
}

func TestResolutionInvalidPreservesPendingWork(t *testing.T) {
	p := rasterPicture(t)
	pending := p.SetImage(testPixels(color.RGBA{A: 255}))
	job, generation, g := p.job, p.generation, p.RasterGeometry()
	for _, factor := range []float64{0, -1, 1.01, math.NaN(), math.Inf(1), math.Inf(-1)} {
		cmd := p.SetResolution(factor)
		if cmd == nil {
			t.Fatal("missing validation error")
		}
		if _, ok := cmd().(ErrorMsg); !ok {
			t.Fatal("wrong error message")
		}
		if p.job != job || p.generation != generation || p.RasterGeometry() != g {
			t.Fatal("invalid factor changed pending work")
		}
	}
	drivePicture(t, p, pending)
	if p.front == nil {
		t.Fatal("pending frame lost")
	}
}

func TestResolutionCancelsStalePreparation(t *testing.T) {
	p := rasterPicture(t)
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{A: 255})))
	stale := p.SetImage(testPixels(color.RGBA{R: 255, A: 255}))()
	drivePicture(t, p, p.SetResolution(.3))
	drivePicture(t, p, p.Update(stale))
	if p.Status().Busy || p.front.rasterSize != image.Pt(19, 15) {
		t.Fatal("stale work replaced new target")
	}
}

func TestResolutionKeepRestoresRaster(t *testing.T) {
	p := rasterPicture(t)
	p.SetResolution(.3)
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{A: 255})))
	prepared := p.SetImage(testPixels(color.RGBA{R: 255, A: 255}))()
	seq := reflect.ValueOf(p.Update(prepared)())
	seq.Index(0).Interface().(tea.Cmd)() // Accepted stable-ID output requires restoration.
	keep := p.Keep()
	drivePicture(t, p, seq.Index(1).Interface().(tea.Cmd))
	if raw := output(drivePicture(t, p, keep)); !strings.Contains(raw, "s=19,v=15") {
		t.Fatalf("keep changed raster: %q", raw)
	}
}

func TestGlyphRasterLimitUsesEffectiveDimensions(t *testing.T) {
	p := New(Options{Render: Config{CellPixelWidth: 1, CellPixelHeight: 1}})
	defer func() { _ = p.Close() }()
	cmd := p.SetSize(8192, 4096)
	if cmd == nil || p.Status().Err == nil {
		t.Fatal("accepted a glyph raster larger than MaxImagePixels")
	}
	if p.RasterGeometry().Cells != (image.Point{}) {
		t.Fatal("invalid dimensions were retained")
	}
	p.SetSize(8192, 2048)
	if p.Status().Err != nil || p.RasterGeometry().Pixels != image.Pt(8192, 4096) {
		t.Fatal("rejected boundary-sized raster")
	}
}
