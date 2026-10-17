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

func TestMaxRasterPixelsClampsKittyTargetAndPreservesAspect(t *testing.T) {
	// 800x400 target with MaxRasterPixels=100000:
	// sqrt(100000 / 320000) * (800, 400) = (447.213595, 223.606797).
	// Naive math.Round gives 447 * 224 = 100128 > 100000; MaxRasterPixels must clamp <= 100000.
	p := New(Options{
		StableID:    true,
		RetireDelay: -1,
		Render: Config{
			Fit:             FitFill,
			CellPixelWidth:  8,
			CellPixelHeight: 16,
			MaxRasterPixels: 100000,
			KittyFormat:     KittyFormatRGBA,
		},
	})
	defer func() { drivePicture(t, p, p.Close()) }()
	p.SetSize(100, 25) // 800x400 terminal pixels
	p.SetTerminal(true, 8, 16)

	g := p.RasterGeometry()
	if g.Pixels.X <= 0 || g.Pixels.Y <= 0 || g.Pixels.X*g.Pixels.Y > 100000 {
		t.Fatalf("Pixels = %v (%d px), want <= 100000", g.Pixels, g.Pixels.X*g.Pixels.Y)
	}
	if g.Pixels != image.Pt(447, 223) {
		t.Fatalf("Pixels = %v, want 447x223", g.Pixels)
	}
	if math.Abs(g.DisplayAspect-2.0) > 1e-12 {
		t.Fatalf("DisplayAspect = %v, want 2.0", g.DisplayAspect)
	}
	if p.MaxRasterPixels() != 100000 {
		t.Fatalf("MaxRasterPixels() = %d, want 100000", p.MaxRasterPixels())
	}

	// Glyph mode ignores MaxRasterPixels (stays at cols x 2*rows = 100x50).
	p.SetTerminal(false, 8, 16)
	if got := p.RasterGeometry().Pixels; got != image.Pt(100, 50) {
		t.Fatalf("glyph Pixels = %v, want 100x50", got)
	}
	p.SetTerminal(true, 8, 16)

	// Runtime SetMaxRasterPixels updates the raster target and re-prepares when needed.
	img := image.NewRGBA(image.Rectangle{Max: g.Pixels})
	drivePicture(t, p, p.SetImageImmutable(img))
	if cmd := p.SetMaxRasterPixels(100000); cmd != nil {
		t.Fatal("unchanged MaxRasterPixels scheduled work")
	}
	cmd := p.SetMaxRasterPixels(20000)
	if cmd == nil {
		t.Fatal("tightened MaxRasterPixels did not schedule work")
	}
	g2 := p.RasterGeometry()
	if g2.Pixels.X*g2.Pixels.Y > 20000 {
		t.Fatalf("Pixels = %v (%d px), want <= 20000", g2.Pixels, g2.Pixels.X*g2.Pixels.Y)
	}
	msgs := drivePicture(t, p, cmd)
	if !strings.Contains(output(msgs), "s=200,v=100") {
		t.Fatalf("expected encoded s=200,v=100, got %q", output(msgs))
	}

	// Removing cap (0 or negative) restores full 800x400 target.
	drivePicture(t, p, p.SetMaxRasterPixels(0))
	if got := p.RasterGeometry().Pixels; got != image.Pt(800, 400) {
		t.Fatalf("uncapped Pixels = %v, want 800x400", got)
	}
}

func TestSetImageForAndSetImageImmutableForDropStaleGeometry(t *testing.T) {
	var nilPic *Picture
	if nilPic.SetImageFor(nil, RasterGeometry{}) != nil || nilPic.SetImageImmutableFor(nil, RasterGeometry{}) != nil {
		t.Fatal("expected nil Picture SetImageFor / SetImageImmutableFor to return nil")
	}

	p := rasterPicture(t)
	g1 := p.RasterGeometry()
	frame1 := image.NewRGBA(image.Rectangle{Max: g1.Pixels})
	for y := range g1.Pixels.Y {
		for x := range g1.Pixels.X {
			frame1.SetRGBA(x, y, color.RGBA{G: 200, A: 255})
		}
	}
	cmd := p.SetImageImmutableFor(frame1, g1)
	if cmd == nil {
		t.Fatal("expected matching geometry frame to be accepted")
	}
	drivePicture(t, p, cmd)
	genAfterFirst := p.Status().Generation

	// Start a slow render for g1, then resize before the slow render finishes.
	p.SetSize(10, 4) // triggers re-prepare of frame1 for 10x4
	genAfterResize := p.Status().Generation
	inFlightJob := p.job
	if inFlightJob == nil {
		t.Fatal("expected resize to schedule re-preparation of retained front")
	}
	g2 := p.RasterGeometry()

	staleFrame := image.NewRGBA(image.Rectangle{Max: g1.Pixels})
	assertStale := func(t *testing.T, cmd tea.Cmd, wantGeom RasterGeometry) {
		t.Helper()
		if cmd == nil {
			t.Fatal("expected non-nil StaleFrameMsg command when dropping stale frame")
		}
		msg := cmd()
		stale, ok := msg.(StaleFrameMsg)
		if !ok {
			t.Fatalf("expected StaleFrameMsg, got %T (%+v)", msg, msg)
		}
		if stale.Owner != p || stale.Geometry != wantGeom {
			t.Fatalf("StaleFrameMsg = %+v, want Owner=%p Geometry=%+v", stale, p, wantGeom)
		}
		if p.Update(msg) != nil {
			t.Fatal("Picture.Update(StaleFrameMsg) must be a no-op")
		}
	}

	assertStale(t, p.SetImageImmutableFor(staleFrame, g1), g2)
	assertStale(t, p.SetImageFor(staleFrame, g1), g2)
	if p.Status().Generation != genAfterResize || p.Status().Err != nil || p.job != inFlightJob {
		t.Fatalf("stale SetImageFor disturbed picture state: status=%+v job=%v", p.Status(), p.job)
	}
	_ = genAfterFirst

	// Finish the resize job and submit a frame for the new geometry g2.
	drivePicture(t, p, func() tea.Msg {
		return picturePrepared{
			owner: p,
			job:   inFlightJob,
			frame: kittyFrameMsg{ID: inFlightJob.placement.cfg.KittyID, Seq: 1, APC: "a", Grid: "g"},
		}
	})
	freshFrame := image.NewRGBA(image.Rectangle{Max: g2.Pixels})
	freshCmd := p.SetImageFor(freshFrame, g2)
	if freshCmd == nil {
		t.Fatal("expected SetImageFor with current geometry to succeed")
	}
	drivePicture(t, p, freshCmd)

	// Mode, resolution, and zero-area changes also report the updated current geometry.
	p.SetResolution(0.5)
	gRes := p.RasterGeometry()
	assertStale(t, p.SetImageImmutableFor(freshFrame, g2), gRes)

	p.SetMode(PictureGlyph)
	gGlyph := p.RasterGeometry()
	assertStale(t, p.SetImageFor(freshFrame, gRes), gGlyph)

	p.SetSize(0, 0)
	gZero := p.RasterGeometry()
	assertStale(t, p.SetImageImmutableFor(freshFrame, gZero), gZero)

	// Closed picture returns nil rather than emitting StaleFrameMsg.
	drivePicture(t, p, p.Close())
	if p.SetImageFor(freshFrame, g2) != nil || p.SetImageImmutableFor(freshFrame, g2) != nil {
		t.Fatal("expected closed Picture SetImageFor / SetImageImmutableFor to return nil")
	}
}
