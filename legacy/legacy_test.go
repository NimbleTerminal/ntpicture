package legacy_test

import (
	"image"
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"
	"nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/legacy"
)

func collectBatchMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, collectBatchMsgs(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestLegacyDefaultSessionPictureEmitsExportedKittyCapabilityMsg(t *testing.T) {
	origCap := legacy.KittySupported()
	defer legacy.ForceKittyCapability(origCap)

	t.Setenv("NTPICTURE_KITTY", "")
	t.Setenv("TERM", "xterm-kitty")
	legacy.ForceKittyCapability(ntpicture.KittyCapabilityUnknown)

	p := ntpicture.New(ntpicture.Options{Session: legacy.DefaultTerminalSession()})
	defer func() { _ = p.Close() }()

	msgs := collectBatchMsgs(tea.Batch(
		p.Init(),
		p.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: 42069101}, Payload: []byte("OK")}),
	))

	var gotCap ntpicture.KittyCapability
	var matched bool
	for _, msg := range msgs {
		if capMsg, ok := msg.(ntpicture.KittyCapabilityMsg); ok {
			matched = true
			gotCap = capMsg.Capability
		}
	}
	if !matched || gotCap != ntpicture.KittyCapabilitySupported {
		t.Fatalf("expected ntpicture.KittyCapabilityMsg{Capability: Supported} from legacy default-session Picture; matched=%v cap=%v msgs=%#v", matched, gotCap, msgs)
	}
}

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
