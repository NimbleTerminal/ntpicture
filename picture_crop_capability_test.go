package ntpicture

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

// U=1 reports placeholder support, not source rectangles on virtual placements.
func TestKittyCropAfterVirtualPlacementReply(t *testing.T) {
	t.Setenv("NTPICTURE_KITTY", "")
	t.Setenv("TMUX", "")
	for _, stable := range []bool{false, true} {
		for _, fit := range []FitMode{FitFill, FitContain, FitCover} {
			for _, opaque := range []bool{false, true} {
				sess := NewTerminalSession(TerminalOptions{CellPixelWidth: 8, CellPixelHeight: 16})
				p := New(Options{Session: sess, StableID: stable, RetireDelay: -1, Render: Config{Fit: fit, CellPixelWidth: 8, CellPixelHeight: 16}})
				t.Cleanup(func() { drivePicture(t, p, p.Close()); sess.Close() })
				p.SetSize(20, 10)
				probeMessages(p.Init())
				probeMessages(p.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: sess.probeID}, Payload: []byte("OK")}))
				probeMessages(p.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: sess.probeID + 2, VirtualPlacement: true}, Payload: []byte("OK")}))
				if sess.Snapshot().VirtualPlacement != KittyCapabilitySupported {
					t.Fatal("feature reply was not recorded")
				}
				p.SetMode(PictureKitty)
				if opaque {
					p.SetBackground(color.NRGBA{B: 200, A: 255})
				}
				img := image.NewNRGBA(image.Rect(100, 200, 180, 260))
				for y := 200; y < 260; y++ {
					for x := 100; x < 180; x++ {
						img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), A: 128})
					}
				}
				crop := image.Rect(110, 210, 130, 220)
				raw := output(drivePicture(t, p, p.SetImageImmutableCropped(img, crop)))
				id := p.front.cfg.KittyID
				for i := 0; i < 3; i++ {
					if i > 0 {
						crop = crop.Add(image.Pt(5, 5))
						raw = output(drivePicture(t, p, p.SetCrop(crop)))
					}
					if strings.Contains(raw, "a=p") || !strings.Contains(raw, "a=T") {
						t.Fatalf("crop did not upload pixels: %q", raw)
					}
					if stable && p.front.cfg.KittyID != id {
						t.Fatal("crop changed stable ID")
					}
					// Fresh-ID replacement also retires the old image. Decode only
					// the upload, excluding the trailing deletion command.
					upload, _, _ := strings.Cut(raw, "\x1b_Ga=d")
					upload = strings.TrimSuffix(strings.TrimPrefix(upload, ansi.SetModeSynchronizedOutput), ansi.ResetModeSynchronizedOutput)
					pixels, err := png.Decode(bytes.NewReader(decodeKittyPNG(t, upload, "a=T", "f=100", "c=20", "r=10", "U=1")))
					if err != nil {
						t.Fatal(err)
					}
					expected := prepareSourceCrop(img, crop, fit, 160, 160, 1, 1, p.opts.Render.Background, AnchorCenter)
					if pixels.Bounds().Size() != image.Pt(160, 160) || !bytes.Equal(kittyRGBABytes(pixels), kittyRGBABytes(expected)) {
						t.Fatalf("wrong crop pixels: stable=%v fit=%v opaque=%v crop=%v", stable, fit, opaque, crop)
					}
				}
			}
		}
	}
}
