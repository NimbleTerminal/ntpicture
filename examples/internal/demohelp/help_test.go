package demohelp

import (
	"testing"

	"github.com/NimbleMarkets/go-booba/actions/bubblehelp"
)

func TestGridActionsFollowCapabilities(t *testing.T) {
	for _, kitty := range []bool{false, true} {
		// Snapshot also validates the OSC envelope size, including all key aliases.
		snapshot, diagnostics, err := bubblehelp.Snapshot(New(true, kitty), "demo", "grid", 1, bubblehelp.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if len(diagnostics) != 0 {
			t.Fatalf("unsupported keys: %v", diagnostics)
		}
		found := false
		for _, action := range snapshot.Actions {
			if action.Label == "Glyph / Kitty" {
				found = true
			}
		}
		if found != kitty {
			t.Fatalf("toggle advertised=%v, capability=%v", found, kitty)
		}
	}
}

func TestFramebufferActionsFollowMode(t *testing.T) {
	for _, tc := range []struct{ kitty, kittyMode bool }{{false, false}, {true, false}, {true, true}} {
		snapshot, diagnostics, err := bubblehelp.Snapshot(Framebuffer(false, tc.kitty, tc.kittyMode), "demo", "framebuffer", 1, bubblehelp.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if len(diagnostics) != 0 {
			t.Fatalf("unsupported keys: %v", diagnostics)
		}
		labels := map[string]bool{}
		for _, action := range snapshot.Actions {
			labels[action.Label] = action.Enabled
		}
		for _, label := range []string{"Pause", "Up", "Down", "Left", "Right", "Center view", "Keep screen & quit", "Quit"} {
			if !labels[label] {
				t.Fatalf("%s missing in %+v", label, tc)
			}
		}
		if labels["Glyph / Kitty"] != tc.kitty || labels["Higher resolution"] != tc.kittyMode || labels["Lower resolution"] != tc.kittyMode {
			t.Fatalf("capability gating wrong for %+v: %v", tc, labels)
		}
	}
	if Framebuffer(true, false, false).Play.Help().Desc != "Play" {
		t.Fatal("paused label")
	}
}
