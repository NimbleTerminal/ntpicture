//go:build js && wasm

package ntpicture

import (
	"strings"
	"syscall/js"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestBrowserDisplayScale(t *testing.T) {
	t.Setenv("NT_DISPLAY_SCALE", "")
	old := js.Global().Get("devicePixelRatio")
	t.Cleanup(func() { js.Global().Set("devicePixelRatio", old) })

	js.Global().Set("devicePixelRatio", 2)
	s, raw := scaleSession(t, TerminalOptions{})
	if strings.Contains(raw, "1337") {
		t.Fatalf("browser session sent a terminal query: %q", raw)
	}
	if got := s.Snapshot().DisplayScale; got != 2 {
		t.Fatalf("DisplayScale = %v, want devicePixelRatio 2", got)
	}
	// Moving the page between displays or zooming changes the ratio, and the
	// next terminal event announces it.
	js.Global().Set("devicePixelRatio", 1.5)
	msgs := sessionMsgs(s.Update(uv.CellSizeEvent{Width: 9, Height: 18}))
	if len(msgs) != 1 || msgs[0].State.DisplayScale != 1.5 {
		t.Fatalf("changed ratio was not announced: %v", msgs)
	}
	if s.Update(cellSizeReply("17;8;3")) != nil || s.Snapshot().DisplayScale != 1.5 {
		t.Fatal("browser session accepted an unsolicited terminal reply")
	}
	for _, bad := range []any{js.Undefined(), "2", 0, 99} {
		js.Global().Set("devicePixelRatio", bad)
		if got := s.Snapshot().DisplayScale; got != 0 {
			t.Fatalf("devicePixelRatio %v gave %v, want unknown", bad, got)
		}
	}

	js.Global().Set("devicePixelRatio", 2)
	forced, _ := scaleSession(t, TerminalOptions{DisplayScale: 3})
	if got := forced.Snapshot().DisplayScale; got != 3 {
		t.Fatalf("option did not beat devicePixelRatio: %v", got)
	}
}
