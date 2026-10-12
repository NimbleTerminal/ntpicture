package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestKeepScreenFinalLayout(t *testing.T) {
	m, err := initialModel()
	if err != nil {
		t.Fatal(err)
	}
	m.width = 80
	before := m.View()
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'Q', Text: "Q"})
	kept := next.(model)
	view := kept.View()
	if cmd == nil || view.AltScreen || !strings.HasSuffix(view.Content, "\n") {
		t.Fatal("keep must finish on main screen with a blank prompt row")
	}
	if strings.Count(view.Content, "\n") != strings.Count(before.Content, "\n") || strings.Contains(ansi.Strip(view.Content), "quit") {
		t.Fatal("final layout changed height or retained help")
	}
	for _, keys := range kept.HelpKeys().FullHelp() {
		for _, key := range keys {
			if key.Enabled() {
				t.Fatal("final view advertises active browser controls")
			}
		}
	}
	for _, c := range kept.tracks {
		if c.Status().Player != nil || c.Status().Busy {
			t.Fatal("keep retained playback")
		}
	}
	// Late resize and keys must leave the final layout alone.
	next, _ = kept.Update(tea.WindowSizeMsg{Width: 12, Height: 8})
	next, _ = next.Update(tea.KeyPressMsg{Code: '.', Text: "."})
	if next.View().Content != view.Content {
		t.Fatal("late message changed final view")
	}
}
