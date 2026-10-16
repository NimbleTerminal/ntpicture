package legacy_test

import (
	"image"
	"image/color"
	"testing"

	"nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/legacy"
)

func TestLegacyModelAndHelpers(t *testing.T) {
	origCap := legacy.KittySupported()
	defer legacy.ForceKittyCapability(origCap)

	legacy.ForceKittyCapability(ntpicture.KittyCapabilitySupported)
	if got := legacy.KittySupported(); got != ntpicture.KittyCapabilitySupported {
		t.Fatalf("KittySupported() = %v, want Supported", got)
	}
	if err := legacy.KittyUnavailable(); err != nil {
		t.Fatalf("KittyUnavailable() = %v, want nil", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})

	m := legacy.NewModelWithConfig(ntpicture.Config{Fit: ntpicture.FitFill, CellPixelWidth: 1, CellPixelHeight: 1})
	m.SetSize(2, 2)
	// In glyph mode, SetImage renders synchronously.
	if cmd := m.SetImage(img); cmd != nil {
		t.Fatalf("expected nil cmd from synchronous glyph SetImage, got %v", cmd)
	}
	if m.String() == "" || m.View().Content == "" {
		t.Fatal("expected non-empty String and View after glyph render")
	}

	tCmd := m.Toggle()
	if tCmd == nil || m.Mode() != ntpicture.PictureKitty {
		t.Fatalf("expected Toggle into PictureKitty, got mode=%v", m.Mode())
	}
	kMsg := tCmd()
	if _, ok := kMsg.(legacy.KittyFrameMsg); !ok {
		t.Fatalf("expected KittyFrameMsg after Toggle, got %T", kMsg)
	}
	if !legacy.IsPictureMsg(kMsg) {
		t.Fatal("expected IsPictureMsg(true) for KittyFrameMsg")
	}
	if m.Update(kMsg) == nil {
		t.Fatal("expected raw APC command from KittyFrameMsg Update")
	}
}
