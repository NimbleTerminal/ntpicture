package main

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	picture "nimbleterminal.dev/ntpicture"
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

func driveScrollModel(t testing.TB, m *scrollModel, cmd tea.Cmd, raw *[]string) {
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
			driveScrollModel(t, m, v.Index(i).Interface().(tea.Cmd), raw)
		}
		return
	}
	switch msg := msg.(type) {
	case tea.RawMsg:
		if raw != nil {
			if s, ok := msg.Msg.(string); ok {
				*raw = append(*raw, s)
			}
		}
		return
	case tea.QuitMsg:
		return
	}
	_, next := m.Update(msg)
	driveScrollModel(t, m, next, raw)
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
	defer func() { _ = m.layers.Close() }()

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

func TestScrollMixedImagesClippingPauseCellSizeAndKeep(t *testing.T) {
	m, err := initialScrollModelWithOptions(picture.TerminalOptions{
		Kitty:        picture.KittyCapabilitySupported,
		SharedMemory: picture.KittyCapabilityUnsupported,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.session.Close()
	defer func() {
		for i := range m.items {
			_ = m.items[i].pic.Close()
		}
	}()

	// Pause animation timer loop while driving deterministic steps.
	_, pauseCmd := m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	var raw []string
	driveScrollModel(t, m, tea.Batch(m.Init(), pauseCmd), &raw)

	// 1. Initial viewport (offset 0, 60x16, bodyHeight=14):
	// - Item 0 (animated GIF, rows 1..6) is fully visible (unclipped).
	// - Item 1 (still banner, rows 9..13) is clipped at the bottom edge (4 of 5 rows).
	// - Item 2 (animated WebP) and Item 3 (still grid) are completely offscreen.
	if !m.items[0].pic.Visible() {
		t.Fatal("expected item 0 (GIF) to be visible at offset 0")
	}
	if vp, ok := m.items[0].pic.Viewport(); ok {
		t.Fatalf("expected item 0 to be unclipped at offset 0, got %+v", vp)
	}
	if got := strings.Count(m.items[0].pic.View(), "\n") + 1; got != m.items[0].rows {
		t.Fatalf("expected item 0 View to have %d rows, got %d", m.items[0].rows, got)
	}
	if !m.items[1].pic.Visible() {
		t.Fatal("expected item 1 (still) to be visible at offset 0")
	}
	if vp, ok := m.items[1].pic.Viewport(); !ok || vp != (picture.Viewport{X: 0, Y: 0, Width: 60, Height: 4}) {
		t.Fatalf("expected item 1 bottom-clipped viewport {0 0 60 4}, got (%+v, %v)", vp, ok)
	}
	if got := strings.Count(m.items[1].pic.View(), "\n") + 1; got != 4 {
		t.Fatalf("expected item 1 clipped View to have 4 rows, got %d", got)
	}
	if m.items[2].pic.Visible() || m.items[3].pic.Visible() {
		t.Fatal("expected offscreen items 2 and 3 to have Visible() == false")
	}
	if m.items[2].pic.View() != "" || m.items[3].pic.View() != "" {
		t.Fatal("expected offscreen items 2 and 3 to have empty View()")
	}

	// 2. Partial top-edge clipping without bitmap retransmission:
	// Scroll down 3 rows (offset=3, visible rows 3..16):
	// - Item 0 (rows 1..6) is top-clipped at Y=2, Height=4 without re-preparing.
	// - Item 1 (rows 9..13) becomes fully visible (unclipped, 5 rows).
	rawBeforeClip := len(raw)
	genBeforeClip := m.items[0].anim.Status().Picture.Generation
	for range 3 {
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		driveScrollModel(t, m, cmd, &raw)
	}
	if vp, ok := m.items[0].pic.Viewport(); !ok || vp != (picture.Viewport{X: 0, Y: 2, Width: 60, Height: 4}) {
		t.Fatalf("expected item 0 top-clipped viewport {0 2 60 4}, got (%+v, %v)", vp, ok)
	}
	if got := strings.Count(m.items[0].pic.View(), "\n") + 1; got != 4 {
		t.Fatalf("expected item 0 top-clipped View to have 4 rows, got %d", got)
	}
	if m.items[0].anim.Status().Picture.Generation != genBeforeClip || len(raw) != rawBeforeClip {
		t.Fatalf("expected top-clipping scroll not to re-prepare or transmit item 0 (gen %d->%d, raw %d->%d)",
			genBeforeClip, m.items[0].anim.Status().Picture.Generation, rawBeforeClip, len(raw))
	}
	if vp, ok := m.items[1].pic.Viewport(); ok {
		t.Fatalf("expected item 1 to be unclipped at offset 3, got %+v", vp)
	}

	// 3. Offscreen pause/resume preserving playback frame position:
	// Seek item 0 (GIF) to frame 3 while visible, then scroll to the bottom so
	// item 0 is completely offscreen and item 2 (WebP) & item 3 (still) are visible.
	driveScrollModel(t, m, m.items[0].anim.Seek(3), &raw)
	if got := m.items[0].anim.Status().Player.Frame(); got != 3 {
		t.Fatalf("expected item 0 at frame 3 before scrolling offscreen, got %d", got)
	}
	_, endCmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	driveScrollModel(t, m, endCmd, &raw)
	if m.items[0].pic.Visible() || m.items[0].pic.View() != "" || m.items[0].anim.Status().Busy {
		t.Fatalf("expected offscreen item 0 to be hidden and idle, got visible=%v status=%+v",
			m.items[0].pic.Visible(), m.items[0].anim.Status())
	}
	if got := m.items[0].anim.Status().Player.Frame(); got != 3 {
		t.Fatalf("expected offscreen item 0 to retain frame 3, got %d", got)
	}
	if !m.items[2].pic.Visible() || !m.items[3].pic.Visible() {
		t.Fatal("expected items 2 and 3 to be visible at bottom of document")
	}
	if m.items[2].pic.View() == "" || m.items[3].pic.View() == "" {
		t.Fatal("expected items 2 and 3 to present non-empty views when scrolled into view")
	}

	// Scroll back to top (Home): item 0 becomes visible again and re-presents frame 3.
	_, homeCmd := m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	driveScrollModel(t, m, homeCmd, &raw)
	if !m.items[0].pic.Visible() || m.items[0].pic.View() == "" {
		t.Fatal("expected item 0 to be visible with non-empty view after scrolling back to top")
	}
	if got := m.items[0].anim.Status().PresentedPlayer.Frame(); got != 3 {
		t.Fatalf("expected item 0 to resume at preserved frame 3, got %d", got)
	}

	// 4. Cell-size changes (uv.CellSizeEvent) update geometry across mixed items:
	_, cellCmd := m.Update(uv.CellSizeEvent{Width: 12, Height: 16})
	driveScrollModel(t, m, cellCmd, &raw)
	for i := range m.items {
		cw, ch := m.items[i].pic.CellPixelSize()
		if cw != 12 || ch != 16 {
			t.Fatalf("item %d: expected cell pixel size 12x16 after CellSizeEvent, got %dx%d", i, cw, ch)
		}
		_, rows := m.items[i].pic.Size()
		if rows != m.items[i].rows || rows <= m.items[i].baseRows {
			t.Fatalf("item %d: expected widened cell aspect to increase row height from %d to %d (got Size rows=%d)",
				i, m.items[i].baseRows, m.items[i].rows, rows)
		}
	}

	// 5. Keep-screen exit (Q) from alt screen retransmits visible placements on main screen:
	raw = nil
	beforeKeep := m.View()
	visibleView0 := m.items[0].pic.View()
	next, keepCmd := m.Update(tea.KeyPressMsg{Code: 'Q', Text: "Q"})
	kept := next.(*scrollModel)
	keptView := kept.View()
	if keepCmd == nil || keptView.AltScreen || !strings.HasSuffix(keptView.Content, "\n") {
		t.Fatal("scroll keep must finish on main screen with a blank prompt row")
	}
	if strings.Count(keptView.Content, "\n") != strings.Count(beforeKeep.Content, "\n") || strings.Contains(ansi.Strip(keptView.Content), "Quit") {
		t.Fatal("final scroll layout changed height or retained help")
	}
	if !strings.Contains(keptView.Content, visibleView0) {
		t.Fatal("kept scroll view lost visible picture content")
	}
	driveScrollModel(t, kept, keepCmd, &raw)
	retransmitted := 0
	for _, seq := range raw {
		if strings.Contains(seq, "a=T") && strings.Contains(seq, "U=1") {
			retransmitted++
		}
	}
	if retransmitted == 0 {
		t.Fatalf("expected KeepOnMainScreen to retransmit visible Kitty placements, got raw=%v", raw)
	}
	for i := range kept.items {
		if !kept.items[i].pic.Closed() {
			t.Fatalf("item %d: expected Closed() == true after Q exit", i)
		}
	}
}
