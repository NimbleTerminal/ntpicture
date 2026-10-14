package ntpicture

import (
	"image"
	"image/color"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestPictureHostGeometryIgnoresTerminalEvents(t *testing.T) {
	session := NewTerminalSession(TerminalOptions{Kitty: KittyCapabilitySupported, CellPixelWidth: 1, CellPixelHeight: 1})
	defer session.Close()
	for _, opts := range []Options{{HostGeometry: true}, {HostGeometry: true, Session: session}} {
		opts.Render = Config{Fit: FitFill, CellPixelWidth: 1, CellPixelHeight: 1}
		opts.RetireDelay = -1
		p := New(opts)
		drivePicture(t, p, p.Submit(Frame{Image: testPixels(color.RGBA{R: 255, A: 255}), Cols: 2, Rows: 2, Mode: PictureKitty, CellWidth: 1, CellHeight: 1}))
		generation := p.Status().Generation
		before := DefaultTerminalSession().Snapshot()
		for _, msg := range []any{uv.CellSizeEvent{Width: 5, Height: 9}, TerminalSessionMsg{Session: session}} {
			if p.Update(msg) != nil {
				t.Fatalf("%T produced a command", msg)
			}
		}
		if w, h := p.CellPixelSize(); w != 1 || h != 1 || p.Status().Generation != generation {
			t.Fatal("host-managed picture applied terminal geometry")
		}
		if DefaultTerminalSession().Snapshot() != before || session.Snapshot().CellPixelWidth != 1 {
			t.Fatal("host-managed picture updated a terminal session")
		}
		drivePicture(t, p, p.Close())
	}
}

func TestPictureSubmitAppliesGeometryAndImageTogether(t *testing.T) {
	p := New(Options{Render: Config{Fit: FitFill}, ClearOnReplace: true, RetireDelay: -1})
	defer func() { drivePicture(t, p, p.Close()) }()
	img := image.NewRGBA(image.Rect(0, 0, 6, 8))
	before := p.Status().Generation
	cmd := p.Submit(Frame{Image: img, Cols: 3, Rows: 2, Mode: PictureKitty, CellWidth: 2, CellHeight: 4, Exact: true})
	if p.Status().Generation != before+1 {
		t.Fatal("submission did not produce exactly one generation")
	}
	img.Set(0, 0, color.White) // Copying submission must not observe this.
	drivePicture(t, p, cmd)
	g := p.RasterGeometry()
	if g.Cells != image.Pt(3, 2) || g.Pixels != image.Pt(6, 8) || p.Mode() != PictureKitty || p.View() == "" {
		t.Fatalf("submission not presented: %+v", g)
	}
	if _, _, _, a := p.front.image.At(0, 0).RGBA(); a != 0 {
		t.Fatal("copying submission aliased caller pixels")
	}
	drivePicture(t, p, p.Submit(Frame{Image: image.NewRGBA(image.Rect(0, 0, 2, 2)), Cols: 2, Rows: 1, Mode: PictureGlyph, CellWidth: 2, CellHeight: 4, Exact: true}))
	if p.Mode() != PictureGlyph || p.View() == "" || strings.Contains(p.View(), "\n") {
		t.Fatal("glyph submission not presented")
	}
}

func TestPictureSubmitRejectsWithoutPartialState(t *testing.T) {
	huge := MaxImagePixels
	for name, frame := range map[string]Frame{
		"exact mismatch": {Cols: 3, Rows: 3, Mode: PictureKitty, CellWidth: 1, CellHeight: 1, Exact: true},
		"exact empty":    {Cols: 0, Rows: 0, Mode: PictureKitty, CellWidth: 1, CellHeight: 1, Exact: true},
		"cell geometry":  {Cols: 3, Rows: 3, Mode: PictureKitty, CellWidth: 0, CellHeight: 1},
		"negative cells": {Cols: -1, Rows: 3, Mode: PictureKitty, CellWidth: 1, CellHeight: 1},
		"mode":           {Cols: 3, Rows: 3, Mode: PictureMode(7), CellWidth: 1, CellHeight: 1},
		"pixel limit":    {Cols: huge, Rows: huge, Mode: PictureKitty, CellWidth: 1, CellHeight: 1},
	} {
		t.Run(name, func(t *testing.T) {
			p := testPicture(true)
			defer func() { drivePicture(t, p, p.Close()) }()
			drivePicture(t, p, p.SetImage(testPixels(color.RGBA{R: 255, A: 255})))
			front, view, before := p.front, p.View(), p.RasterGeometry()
			frame.Image = testPixels(color.RGBA{B: 255, A: 255})
			msgs := drivePicture(t, p, p.Submit(frame))
			var reported bool
			for _, m := range msgs {
				if e, ok := m.(ErrorMsg); ok && e.Owner == p && e.Err != nil {
					reported = true
				}
			}
			if !reported || p.Status().Err == nil {
				t.Fatal("rejection not reported")
			}
			if p.RasterGeometry() != before || p.Mode() != PictureKitty || p.front != front || p.View() != view {
				t.Fatal("rejected submission changed geometry or front")
			}
			if w, h := p.CellPixelSize(); w != 1 || h != 1 {
				t.Fatal("rejected submission changed cell geometry")
			}
		})
	}
}

func TestPictureSubmitImmutableAndClear(t *testing.T) {
	p := New(Options{Render: Config{Fit: FitFill}, RetireDelay: -1})
	defer func() { drivePicture(t, p, p.Close()) }()
	img := testPixels(color.RGBA{G: 255, A: 255})
	drivePicture(t, p, p.Submit(Frame{Image: img, Cols: 2, Rows: 2, Mode: PictureKitty, CellWidth: 1, CellHeight: 1, Immutable: true, Exact: true}))
	if p.front == nil || p.front.image != image.Image(img) {
		t.Fatal("immutable submission copied pixels")
	}
	drivePicture(t, p, p.Submit(Frame{Cols: 4, Rows: 4, Mode: PictureKitty, CellWidth: 1, CellHeight: 1}))
	if p.View() != "" || p.RasterGeometry().Cells != image.Pt(4, 4) {
		t.Fatal("nil submission did not clear at new geometry")
	}
}
