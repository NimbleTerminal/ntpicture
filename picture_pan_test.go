package ntpicture

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func newPanPicture(t *testing.T, stable bool) *Picture {
	t.Helper()
	p := New(Options{Session: NewTerminalSession(TerminalOptions{FrameEdits: KittyCapabilitySupported, VirtualPlacement: KittyCapabilitySupported, CellPixelWidth: 1, CellPixelHeight: 1}), StableID: stable, RetireDelay: -1, Render: Config{Fit: FitFill, CellPixelWidth: 1, CellPixelHeight: 1}})
	p.SetKittyCapability(KittyCapabilitySupported)
	p.SetSize(20, 10)
	p.SetMode(PictureKitty)
	t.Cleanup(func() { drivePicture(t, p, p.Close()) })
	return p
}

func TestSourcePanUploadAndPut(t *testing.T) {
	for _, stable := range []bool{false, true} {
		p := newPanPicture(t, stable)
		img := image.NewNRGBA(image.Rect(100, 200, 180, 260))
		raw := output(drivePicture(t, p, p.SetImageImmutableCropped(img, image.Rect(110, 210, 130, 220))))
		for _, field := range []string{"a=T", "p=1", "x=10", "y=10", "w=20", "h=10"} {
			if !strings.Contains(raw, field) {
				t.Fatalf("missing %s in %q", field, raw)
			}
		}
		id, grid := p.front.cfg.KittyID, p.View()
		for _, crop := range []image.Rectangle{image.Rect(120, 220, 140, 230), image.Rect(100, 200, 120, 210)} {
			raw = output(drivePicture(t, p, p.SetCrop(crop)))
			if !strings.Contains(raw, "a=p") || strings.Contains(raw, "a=T") || strings.Contains(raw, "a=f") || len(raw) > 200 || id != p.front.cfg.KittyID || p.View() != grid {
				t.Fatalf("pan retransmitted or changed grid: %q", raw)
			}
		}
		if !strings.Contains(raw, "x=0,y=0") {
			t.Fatalf("origin not reset: %q", raw)
		}
		raw = output(drivePicture(t, p, p.KeepOnMainScreen()))
		if !strings.Contains(raw, "a=T") || !strings.Contains(raw, "p=1") || !strings.Contains(raw, "w=20") {
			t.Fatalf("keep lost source crop: %q", raw)
		}
	}
}

func TestSourcePanTransitionsAndRasterCap(t *testing.T) {
	p := newPanPicture(t, true)
	img := randomNRGBA(80, 60)
	drivePicture(t, p, p.SetImageImmutableCropped(img, image.Rect(0, 0, 20, 10)))
	id := p.front.cfg.KittyID
	raw := output(drivePicture(t, p, p.SetMaxRasterPixels(300))) // raster=200, source=4800
	if p.front.panSource || !strings.Contains(raw, "a=T") || p.front.cfg.KittyID == id {
		t.Fatal("cap did not switch to per-crop upload with a fresh ID")
	}
	raw = output(drivePicture(t, p, p.SetMaxRasterPixels(0)))
	if !p.front.panSource || !strings.Contains(raw, "a=T") {
		t.Fatal("removing cap did not restore whole-source upload")
	}
	raw = output(drivePicture(t, p, p.SetImageImmutableCropped(randomNRGBA(80, 60), image.Rect(0, 0, 20, 10))))
	if !strings.Contains(raw, "a=T") || strings.Contains(raw, "a=f") {
		t.Fatal("new pan source used a frame delta")
	}
	drivePicture(t, p, p.ResetCrop())
	if p.front.panSource {
		t.Fatal("ResetCrop retained the pan path")
	}
	// The newer Ghostty StableID frame-edit path still works after leaving pan mode.
	changed := randomNRGBA(80, 60)
	changed.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	raw = output(drivePicture(t, p, p.SetImage(changed)))
	if !strings.Contains(raw, "a=f") {
		t.Fatal("StableID frame edits lost after pan mode")
	}
}

func TestSourcePanGhosttyFallback(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	p := New(Options{RetireDelay: -1})
	p.SetSize(20, 10)
	p.SetTerminal(true, 1, 1)
	t.Cleanup(func() { drivePicture(t, p, p.Close()) })
	drivePicture(t, p, p.SetImageImmutableCropped(randomNRGBA(80, 60), image.Rect(0, 0, 20, 10)))
	raw := output(drivePicture(t, p, p.SetCrop(image.Rect(1, 1, 21, 11))))
	if p.front.panSource || !strings.Contains(raw, "a=T") {
		t.Fatal("Ghostty must retain per-crop fallback")
	}
}

func TestSourcePanCanceledPutRestoresFront(t *testing.T) {
	p := newPanPicture(t, false)
	drivePicture(t, p, p.SetImageImmutableCropped(randomNRGBA(80, 60), image.Rect(0, 0, 20, 10)))
	crop := p.front.panCrop
	// Accept and emit the new placement, then stop before its ready message.
	prepared := p.SetCrop(image.Rect(10, 10, 30, 20))()
	seq := p.Update(prepared)()
	v := reflect.ValueOf(seq)
	var cmds []tea.Cmd
	for i := 0; i < v.Len(); i++ {
		cmds = append(cmds, v.Index(i).Interface().(tea.Cmd))
	}
	if len(cmds) != 2 {
		t.Fatalf("expected output and ready, got %d commands", len(cmds))
	}
	_ = cmds[0]()
	stop := p.Stop()
	drivePicture(t, p, cmds[1])
	raw := output(drivePicture(t, p, stop))
	if p.front.panCrop != crop || !strings.Contains(raw, "a=T") || !strings.Contains(raw, "x=0") {
		t.Fatalf("cancel failed to restore committed crop: %q", raw)
	}
}

func TestSourcePanSharedOwnershipAndCoalescing(t *testing.T) {
	for _, medium := range []KittyMedium{KittyMediumDirect, KittyMediumSharedMemory} {
		p := newPanPicture(t, false)
		p.opts.Render.KittyMedium = medium
		p.session().state.SharedMemory = KittyCapabilitySupported
		img := randomNRGBA(80, 60)
		drivePicture(t, p, p.SetImageImmutableCropped(img, image.Rect(0, 0, 20, 10)))
		shared := p.front.kittyShared
		id := p.front.cfg.KittyID
		cmd := p.SetCrop(image.Rect(1, 1, 21, 11))
		_ = p.SetCrop(image.Rect(2, 2, 22, 12))
		_ = p.SetCrop(image.Rect(3, 3, 23, 13))
		raw := output(drivePicture(t, p, cmd))
		if !strings.Contains(raw, "a=p") || strings.Contains(raw, "a=T") || p.front.cfg.KittyID != id || p.front.panCrop != image.Rect(3, 3, 23, 13) {
			t.Fatalf("coalesced crop did not retain source ID and newest crop: %q", raw)
		}
		if p.front.kittyShared != shared {
			t.Fatal("pan released the original shared-memory ownership")
		}
	}
}

func TestSourcePanPreservesFitAndBackground(t *testing.T) {
	for _, fit := range []FitMode{FitFill, FitContain} {
		for _, opaque := range []bool{false, true} {
			p := newPanPicture(t, false)
			p.SetCellSize(8, 16)
			p.SetFit(fit)
			if opaque {
				p.SetBackground(color.NRGBA{B: 200, A: 255})
			}
			img := image.NewNRGBA(image.Rect(0, 0, 80, 60))
			for y := 0; y < 60; y++ {
				for x := 0; x < 80; x++ {
					img.SetNRGBA(x, y, color.NRGBA{R: 200, A: 128})
				}
			}
			raw := output(drivePicture(t, p, p.SetImageImmutableCropped(img, image.Rect(0, 0, 20, 10))))
			wantPan := fit == FitContain && !opaque
			if p.front.panSource != wantPan {
				t.Fatalf("fit=%v opaque=%v pan=%v", fit, opaque, p.front.panSource)
			}
			if wantPan {
				continue
			}
			pixels, err := png.Decode(bytes.NewReader(decodeKittyPNG(t, raw, "a=T", "f=100", "c=20", "r=10", "U=1")))
			if err != nil {
				t.Fatal(err)
			}
			if pixels.Bounds().Size() != image.Pt(160, 160) {
				t.Fatalf("unprepared source: %v", pixels.Bounds())
			}
			expected := prepareSourceCrop(img, image.Rect(0, 0, 20, 10), fit, 160, 160, 1, 1, p.opts.Render.Background, AnchorCenter)
			if !bytes.Equal(kittyRGBABytes(pixels), kittyRGBABytes(expected)) {
				t.Fatal("pan fallback changed fit/background pixels")
			}
			raw = output(drivePicture(t, p, p.SetCrop(image.Rect(5, 5, 25, 15))))
			if strings.Contains(raw, "a=p") || !strings.Contains(raw, "a=T") {
				t.Fatal("unsafe crop used placement-only update")
			}
		}
	}
}
