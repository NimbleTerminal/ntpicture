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
