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
		p.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: 42069101}, Payload: []byte("OK")}),
	))

	var gotCap ntpicture.KittyCapability
	var matchedCap, matchedSess bool
	for _, msg := range msgs {
		switch v := msg.(type) {
		case ntpicture.KittyCapabilityMsg:
			matchedCap = true
			gotCap = v.Capability
		case ntpicture.TerminalSessionMsg:
			if v.Session == p.Session() && v.State.Kitty == ntpicture.KittyCapabilitySupported {
				matchedSess = true
			}
		}
	}
	if !matchedCap || !matchedSess || gotCap != ntpicture.KittyCapabilitySupported {
		t.Fatalf("expected KittyCapabilityMsg and TerminalSessionMsg from Options{} Picture without importing legacy; matchedCap=%v matchedSess=%v cap=%v msgs=%#v", matchedCap, matchedSess, gotCap, msgs)
	}
}

func TestOwnedSessionPictureEmitsTerminalSessionMsg(t *testing.T) {
	t.Setenv("NTPICTURE_KITTY", "")
	t.Setenv("TERM", "xterm-kitty")

	p := ntpicture.New(ntpicture.Options{})
	defer func() { _ = p.Close() }()

	msgs := collectBatchMsgs(tea.Batch(
		p.Init(),
		p.SetKittyCapability(ntpicture.KittyCapabilitySupported),
	))

	var matched bool
	for _, msg := range msgs {
		if sessMsg, ok := msg.(ntpicture.TerminalSessionMsg); ok && sessMsg.Session == p.Session() && sessMsg.State.Kitty == ntpicture.KittyCapabilitySupported {
			matched = true
		}
	}
	if !matched || p.KittySupported() != ntpicture.KittyCapabilitySupported {
		t.Fatalf("expected ntpicture.TerminalSessionMsg{State.Kitty: Supported} from owned-session Picture without importing legacy; matched=%v msgs=%#v", matched, msgs)
	}
}
