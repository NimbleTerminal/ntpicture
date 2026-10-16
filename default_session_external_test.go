package ntpicture_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"
	"nimbleterminal.dev/ntpicture"
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

func TestDefaultSessionPictureEmitsExportedKittyCapabilityMsg(t *testing.T) {
	t.Setenv("NTPICTURE_KITTY", "")
	t.Setenv("TERM", "xterm-kitty")

	p := ntpicture.New(ntpicture.Options{})
	defer func() { _ = p.Close() }()

	// Route a probe response (probe ID 42069101) or Init notification through p.Update
	// without importing nimbleterminal.dev/ntpicture/legacy.
	msgs := collectBatchMsgs(tea.Batch(
		p.Init(),
		p.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: 42069101}}),
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
		t.Fatalf("expected ntpicture.KittyCapabilityMsg{Capability: Supported} from default-session Picture without importing legacy; matched=%v cap=%v msgs=%#v", matched, gotCap, msgs)
	}
}
