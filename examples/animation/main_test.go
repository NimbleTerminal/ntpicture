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

func TestCompositeLayersAndKeepScreen(t *testing.T) {
	m, err := initialCompositeModel()
	if err != nil {
		t.Fatal(err)
	}
	defer m.session.Close()
	defer m.layers.Close()

	_, cmd := m.Update(tea.WindowSizeMsg{Width: 60, Height: 18})
	if cmd == nil {
		t.Fatal("expected initial resize command")
	}
	cmd = m.layers.SetTerminal(true, 8, 16)
	if len(m.layers.Layers()) != 2 {
		t.Fatalf("expected 2 Kitty overlay layers, got %d", len(m.layers.Layers()))
	}
	next, keepCmd := m.Update(tea.KeyPressMsg{Code: 'Q', Text: "Q"})
	kept := next.(*compositeModel)
	view := kept.View()
	if keepCmd == nil || view.AltScreen || !strings.HasSuffix(view.Content, "\n") {
		t.Fatal("composite keep must finish on main screen with a blank prompt row")
	}
}
