package main

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func driveCompositeModel(t testing.TB, m *compositeModel, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	v := reflect.ValueOf(msg)
	if v.Kind() == reflect.Slice {
		for i := 0; i < v.Len(); i++ {
			driveCompositeModel(t, m, v.Index(i).Interface().(tea.Cmd))
		}
		return
	}
	_, next := m.Update(msg)
	driveCompositeModel(t, m, next)
}

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

	// Pause playback and configure Kitty geometry so driving Init + resize prepares
	// and presents the base composite and both overlays deterministically.
	m.layers.Pause(true)
	termCmd := m.layers.SetTerminal(true, 8, 16)
	initCmd := m.Init()
	_, resizeCmd := m.Update(tea.WindowSizeMsg{Width: 60, Height: 18})
	if resizeCmd == nil {
		t.Fatal("expected initial resize command")
	}
	driveCompositeModel(t, m, tea.Batch(termCmd, initCmd, resizeCmd))

	if len(m.layers.Layers()) != 2 {
		t.Fatalf("expected 2 Kitty overlay layers, got %d", len(m.layers.Layers()))
	}
	picView := m.layers.View()
	snapBefore := m.layers.Snapshot()
	if picView == "" || snapBefore == nil {
		t.Fatalf("expected non-empty layers view and snapshot before Keep, got view=%d bytes, snap=%v", len(picView), snapBefore)
	}

	before := m.View()
	next, keepCmd := m.Update(tea.KeyPressMsg{Code: 'Q', Text: "Q"})
	kept := next.(*compositeModel)
	view := kept.View()
	if keepCmd == nil || view.AltScreen || !strings.HasSuffix(view.Content, "\n") {
		t.Fatal("composite keep must finish on main screen with a blank prompt row")
	}
	if strings.Count(view.Content, "\n") != strings.Count(before.Content, "\n") || strings.Contains(ansi.Strip(view.Content), "quit") {
		t.Fatal("final composite layout changed height or retained help")
	}
	if !strings.Contains(view.Content, picView) || kept.layers.View() != picView {
		t.Fatal("kept view lost picture content")
	}
	if kept.layers.Snapshot() != snapBefore {
		t.Fatal("Keep did not preserve Snapshot identity")
	}
	for _, keys := range kept.HelpKeys().FullHelp() {
		for _, key := range keys {
			if key.Enabled() {
				t.Fatal("final view advertises active browser controls")
			}
		}
	}
	if kept.layers.Status().Source != nil || kept.layers.Status().Busy {
		t.Fatalf("Keep retained active playback: %+v", kept.layers.Status())
	}

	// Late resize and keys must leave the final layout and snapshot alone.
	next, _ = kept.Update(tea.WindowSizeMsg{Width: 12, Height: 8})
	next, _ = next.Update(tea.KeyPressMsg{Code: '.', Text: "."})
	if next.View().Content != view.Content || kept.layers.Snapshot() != snapBefore {
		t.Fatal("late message changed final composite view or snapshot")
	}
}
