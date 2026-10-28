package ntpicture

import (
	"image"
	"image/color"
	"nimbleterminal.dev/ntpicture/internal/framecache"
	"strings"
	"testing"
)

func TestStableFrameEdits(t *testing.T) {
	for _, format := range []KittyFormat{KittyFormatPNG, KittyFormatRGBA, KittyFormatZlib} {
		t.Run(format.String(), func(t *testing.T) {
			p := New(Options{StableID: true, RetireDelay: -1, Render: Config{Fit: FitFill, CellPixelWidth: 1, CellPixelHeight: 1, KittyFormat: format}})
			p.SetSize(200, 100)
			p.SetTerminal(true, 1, 1)
			defer func() { drivePicture(t, p, p.Close()) }()
			img := randomNRGBA(200, 100)
			initial := output(drivePicture(t, p, p.SetImage(img)))
			view := p.View()
			for y := 20; y < 30; y++ {
				for x := 30; x < 40; x++ {
					img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
				}
			}
			raw := output(drivePicture(t, p, p.SetImage(img)))
			for _, field := range []string{"a=f", "r=1", "X=1", "x=30", "y=20"} {
				if !strings.Contains(raw, field) {
					t.Fatalf("missing %s: %.150s", field, raw)
				}
			}
			if !strings.Contains(raw, "s=10") || !strings.Contains(raw, "v=10") {
				t.Fatalf("wrong dimensions: %.150s", raw)
			}
			if len(raw) >= len(initial)/4 || p.View() != view || strings.Contains(raw, "a=T") || strings.Contains(raw, "a=d") {
				t.Fatal("delta changed placement or did not reduce payload")
			}
			if got := output(drivePicture(t, p, p.SetImage(img))); got != "" {
				t.Fatalf("identical pixels emitted %d bytes", len(got))
			}
			raw = output(drivePicture(t, p, p.SetCellSize(2, 2)))
			if !strings.Contains(raw, "a=T") || !strings.Contains(raw, "U=1") {
				t.Fatal("geometry did not reset baseline")
			}
			p.SetViewport(Viewport{Width: 20, Height: 20})
			if raw = output(drivePicture(t, p, p.SetImage(img))); !strings.Contains(raw, "a=T") {
				t.Fatal("viewport did not reset baseline")
			}
		})
	}
}

func TestFrameContinuationAndTransient(t *testing.T) {
	for _, tmux := range []bool{false, true} {
		transport := kittyTransport{tmux: tmux, frame: true}
		raw := transport.buildKittyAPC(randomNRGBA(100, 100), 42, 10, 10, KittyFormatRGBA)
		if !strings.Contains(raw, "a=f,m=1") || !strings.Contains(raw, "a=f,m=0") || strings.Contains(raw, "a=T") {
			t.Fatal("missing frame continuation action")
		}
		if tmux && !strings.Contains(raw, "\x1bPtmux;") {
			t.Fatal("missing tmux wrapper")
		}
	}
	for _, transient := range []bool{false, true} {
		p := New(Options{Render: Config{Transient: transient}, RetireDelay: -1})
		p.SetSize(2, 2)
		p.SetTerminal(true, 1, 1)
		raw := output(drivePicture(t, p, p.SetImage(image.NewNRGBA(image.Rect(0, 0, 2, 2)))))
		if strings.Contains(raw, "N=1") != transient {
			t.Fatalf("Transient=%v: %q", transient, raw)
		}
		drivePicture(t, p, p.Close())
	}
}

// The private frame wrapper is exactly the input submitted by playback.Controller.
func testCachedSharedOwnership(t *testing.T) {
	p := New(Options{StableID: true, RetireDelay: -1, Session: NewTerminalSession(TerminalOptions{Kitty: KittyCapabilitySupported, SharedMemory: KittyCapabilitySupported}), Render: Config{Fit: FitFill, KittyMedium: KittyMediumSharedMemory}})
	p.SetSize(2, 2)
	p.SetTerminal(true, 1, 1)
	defer func() { drivePicture(t, p, p.Close()) }()
	src := &framecache.Source{Frames: 2, Bytes: 32 << 20}
	first := &framecache.Image{Image: randomNRGBA(2, 2), Source: src, Frame: 1}
	second := &framecache.Image{Image: randomNRGBA(2, 2), Source: src, Frame: 2}
	drivePicture(t, p, p.SetImageImmutable(first))
	drivePicture(t, p, p.SetImageImmutable(second))
	shared := p.front.kittyShared
	obj := shared.object
	if obj == nil {
		t.Skip("shared memory unavailable")
	}
	raw := output(drivePicture(t, p, p.SetImageImmutable(first)))
	if !strings.Contains(raw, "a=a") || p.medium != KittyMediumSharedMemory || p.front.kittyShared != shared || obj.Done() {
		t.Fatal("cache select dropped shared-memory ownership")
	}
	raw = output(drivePicture(t, p, p.Keep()))
	if !strings.Contains(raw, "a=T") || strings.Contains(raw, "t=s") {
		t.Fatal("Keep did not detach cached shared-memory image")
	}
	if !obj.Done() {
		t.Fatal("Keep leaked shared object")
	}
}
