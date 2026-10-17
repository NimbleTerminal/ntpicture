package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	picture "nimbleterminal.dev/ntpicture"
)

// drive runs a command tree through the model, returning rendered frames to
// the caller instead of delivering them, so tests control when a producer
// result reaches the event loop. Raw terminal output is counted as
// transmission. Picture-internal messages are delivered normally.
type driver struct {
	t      *testing.T
	m      *model
	frames []frameMsg
	raw    int
}

func (d *driver) run(cmd tea.Cmd) {
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
			d.run(v.Index(i).Interface().(tea.Cmd))
		}
		return
	}
	switch v := msg.(type) {
	case frameMsg:
		d.frames = append(d.frames, v)
		return
	case tea.RawMsg:
		d.raw++
		return
	case tea.QuitMsg:
		return
	}
	d.deliver(msg)
}

func (d *driver) deliver(msg tea.Msg) {
	_, cmd := d.m.Update(msg)
	d.run(cmd)
}

func (d *driver) takeFrame() frameMsg {
	d.t.Helper()
	if len(d.frames) != 1 {
		d.t.Fatalf("expected one rendered frame, got %d", len(d.frames))
	}
	f := d.frames[0]
	d.frames = nil
	return f
}

func newTestModel(t *testing.T, kitty picture.KittyCapability) *driver {
	t.Helper()
	m := newModel(picture.TerminalOptions{Kitty: kitty, SharedMemory: picture.KittyCapabilityUnsupported})
	m.interval = 0
	d := &driver{t: t, m: m}
	d.run(m.Init())
	return d
}

func keyPress(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Text: string(code)} }

var space = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}

func TestStaleFrameRejectedAfterResize(t *testing.T) {
	d := newTestModel(t, picture.KittyCapabilityUnsupported)
	d.deliver(tea.WindowSizeMsg{Width: 40, Height: 13})
	first := d.takeFrame()
	if g := d.m.pic.RasterGeometry(); first.geometry != g || first.img.Bounds().Dx() != g.Pixels.X || first.img.Bounds().Dy() != g.Pixels.Y {
		t.Fatalf("frame geometry %+v does not match raster %+v", first.geometry, g)
	}
	if g := first.geometry; g.Mode != picture.PictureGlyph || g.Pixels.X != g.Cells.X || g.Pixels.Y != 2*g.Cells.Y {
		t.Fatalf("glyph raster must be cols x 2*rows: %+v", g)
	}
	// Resize while the render is in flight: nothing new starts until it returns.
	d.deliver(tea.WindowSizeMsg{Width: 60, Height: 23})
	if len(d.frames) != 0 || !d.m.inflight {
		t.Fatal("a second render started before the first returned")
	}
	generation := d.m.pic.Status().Generation
	d.deliver(first)
	if d.m.frames != 0 || d.m.pic.Status().Generation != generation {
		t.Fatal("stale frame was submitted")
	}
	second := d.takeFrame()
	if second.geometry != d.m.pic.RasterGeometry() || second.geometry == first.geometry {
		t.Fatalf("replacement render used stale geometry %+v", second.geometry)
	}
	// If a frame with the current request ID carries stale geometry,
	// SetImageImmutableFor drops it and emits StaleFrameMsg, scheduling a fresh render.
	staleSameRequest := first
	staleSameRequest.request = second.request
	d.deliver(staleSameRequest)
	if d.m.frames != 0 || d.m.pic.Status().Generation != generation {
		t.Fatal("stale-geometry frame with current request was accepted")
	}
	third := d.takeFrame()
	if third.geometry != d.m.pic.RasterGeometry() {
		t.Fatalf("StaleFrameMsg recovery used wrong geometry %+v", third.geometry)
	}
	d.deliver(third)
	// The driver presents synchronously, so the next render is already queued.
	if d.m.frames != 1 || d.m.pic.Status().Generation == generation || d.m.pic.Status().Presented == 0 || len(d.frames) != 1 {
		t.Fatal("fresh frame was not submitted and presented")
	}
}

func TestPauseRejectsInFlightAndCancels(t *testing.T) {
	d := newTestModel(t, picture.KittyCapabilityUnsupported)
	_, cmd := d.m.Update(tea.WindowSizeMsg{Width: 40, Height: 13})
	ctx := d.m.cancel
	d.deliver(space) // pause before the render command runs
	if !d.m.paused || len(d.frames) != 0 {
		t.Fatal("pause did not take effect")
	}
	d.run(cmd)
	stale := d.takeFrame()
	if stale.err != context.Canceled {
		t.Fatalf("cancelled render returned %v", stale.err)
	}
	_ = ctx
	d.deliver(stale)
	if d.m.inflight || d.m.frames != 0 || len(d.frames) != 0 {
		t.Fatal("paused producer rendered or submitted")
	}
	d.deliver(space) // resume
	frame := d.takeFrame()
	if frame.err != nil || frame.request != d.m.request {
		t.Fatalf("resume did not start a fresh render: %+v", frame.err)
	}
	d.deliver(frame)
	if d.m.frames != 1 {
		t.Fatal("resumed frame was not accepted")
	}
}

func TestZeroSizedLayoutRendersNothing(t *testing.T) {
	d := newTestModel(t, picture.KittyCapabilityUnsupported)
	d.deliver(tea.WindowSizeMsg{Width: 20, Height: headerRows + helpRows})
	if d.m.inflight || len(d.frames) != 0 || d.m.pic.RasterGeometry().Pixels != (picture.RasterGeometry{}).Pixels {
		t.Fatal("zero-height viewport started a render")
	}
	if v := d.m.View(); strings.Count(v.Content, "\n") != headerRows+helpRows-1+1 {
		t.Fatalf("unexpected layout: %q", v.Content)
	}
	d.deliver(tea.WindowSizeMsg{Width: 20, Height: 8})
	if !d.m.inflight {
		t.Fatal("render did not start once space was available")
	}
}

// present drives frames until the picture has presented one.
func present(d *driver) {
	d.t.Helper()
	for i := 0; i < 5 && d.m.pic.Status().Presented == 0; i++ {
		if len(d.frames) == 0 {
			d.t.Fatal("producer stalled")
		}
		d.deliver(d.takeFrame())
	}
	if d.m.pic.Status().Presented == 0 {
		d.t.Fatal("picture never presented")
	}
}

func TestViewportPanWithoutRetransmission(t *testing.T) {
	d := newTestModel(t, picture.KittyCapabilitySupported)
	if d.m.pic.Mode() != picture.PictureKitty {
		t.Fatal("Kitty was not selected from the forced session capability")
	}
	d.deliver(tea.WindowSizeMsg{Width: 40, Height: 13})
	present(d)
	d.deliver(space) // pause so no further frames are produced
	d.deliver(d.takeFrame())
	if d.m.frames != 1 || len(d.frames) != 0 {
		t.Fatal("frame rendered before pausing was accepted")
	}
	raw, generation, request := d.raw, d.m.pic.Status().Generation, d.m.request
	if raw == 0 {
		t.Fatal("no Kitty transmission recorded")
	}
	before := d.m.pic.View()
	for _, code := range []rune{tea.KeyRight, tea.KeyDown, tea.KeyLeft, tea.KeyUp, tea.KeyDown} {
		d.deliver(tea.KeyPressMsg{Code: code})
	}
	after := d.m.pic.View()
	if after == before || d.m.view.Y != (d.m.rows-d.m.view.Height)/2+panStepY {
		t.Fatalf("viewport did not move: %+v", d.m.view)
	}
	if d.raw != raw || d.m.pic.Status().Generation != generation || len(d.frames) != 0 {
		t.Fatal("panning prepared or transmitted a bitmap")
	}
	if d.m.request != request || d.m.inflight {
		t.Fatal("panning invalidated the producer")
	}
	if ansi.StringWidth(strings.SplitN(after, "\n", 2)[0]) != d.m.view.Width || strings.Count(after, "\n")+1 != d.m.view.Height {
		t.Fatal("viewport view has the wrong dimensions")
	}
	// Panning while playing also leaves the current render alone.
	d.deliver(space)
	request = d.m.request
	frame := d.takeFrame()
	d.deliver(keyPress('c'))
	if d.m.request != request || d.m.panX != 0 || d.m.panY != 0 {
		t.Fatal("centering invalidated the producer")
	}
	d.deliver(frame)
	if d.m.frames != 2 {
		t.Fatal("frame rendered before panning was rejected")
	}
}

func TestResolutionChangesRasterNotFootprint(t *testing.T) {
	d := newTestModel(t, picture.KittyCapabilitySupported)
	d.deliver(tea.WindowSizeMsg{Width: 40, Height: 13})
	first := d.takeFrame()
	before := d.m.pic.RasterGeometry()
	d.deliver(keyPress(']'))
	after := d.m.pic.RasterGeometry()
	if after.Cells != before.Cells || after.DisplayAspect != before.DisplayAspect || after.Pixels == before.Pixels {
		t.Fatalf("resolution change: before %+v after %+v", before, after)
	}
	generation := d.m.pic.Status().Generation
	d.deliver(first)
	if d.m.frames != 0 || d.m.pic.Status().Generation != generation {
		t.Fatal("frame rendered for the old resolution was submitted")
	}
	if f := d.takeFrame(); f.geometry != after {
		t.Fatalf("replacement render geometry %+v", f.geometry)
	}
	// The highest setting is the limit; a further request is a no-op.
	d.deliver(keyPress(']'))
	d.deliver(keyPress(']'))
	if d.m.resolution != len(resolutions)-1 || resolutions[d.m.resolution] != 1 {
		t.Fatalf("resolution index %d", d.m.resolution)
	}
}

func TestGlyphIgnoresResolutionKeys(t *testing.T) {
	d := newTestModel(t, picture.KittyCapabilityUnsupported)
	d.deliver(tea.WindowSizeMsg{Width: 40, Height: 13})
	request, geometry := d.m.request, d.m.pic.RasterGeometry()
	d.deliver(keyPress(']'))
	if d.m.request != request || d.m.pic.RasterGeometry() != geometry || d.m.resolution != 1 {
		t.Fatal("glyph mode accepted a resolution change")
	}
	for _, b := range d.m.HelpKeys().ShortHelp() {
		if (b.Help().Desc == "Higher resolution" || b.Help().Desc == "Lower resolution" || b.Help().Desc == "Glyph / Kitty") && b.Enabled() {
			t.Fatalf("%s advertised without Kitty", b.Help().Desc)
		}
	}
}

func TestQuitAndKeepStopProducer(t *testing.T) {
	d := newTestModel(t, picture.KittyCapabilityUnsupported)
	d.deliver(tea.WindowSizeMsg{Width: 40, Height: 13})
	present(d)
	frame := d.takeFrame()
	before := d.m.View()
	request := d.m.request
	d.deliver(keyPress('Q'))
	kept := d.m.View()
	if !d.m.keeping || kept.AltScreen || !strings.HasSuffix(kept.Content, "\n") || d.m.request == request {
		t.Fatal("keep must stop the producer and finish on the main screen with a prompt row")
	}
	if strings.Count(kept.Content, "\n") != strings.Count(before.Content, "\n") || strings.Contains(ansi.Strip(kept.Content), "Quit") {
		t.Fatal("final layout changed height or retained help")
	}
	for _, keys := range d.m.HelpKeys().FullHelp() {
		for _, k := range keys {
			if k.Enabled() {
				t.Fatal("final view advertises active controls")
			}
		}
	}
	d.deliver(frame)
	d.deliver(tea.WindowSizeMsg{Width: 12, Height: 8})
	d.deliver(space)
	if len(d.frames) != 0 || d.m.View().Content != kept.Content {
		t.Fatal("late messages changed the kept view or restarted rendering")
	}

	d = newTestModel(t, picture.KittyCapabilityUnsupported)
	d.deliver(tea.WindowSizeMsg{Width: 40, Height: 13})
	_ = d.takeFrame()
	d.deliver(keyPress('q'))
	if d.m.pic.View() != "" || d.m.pic.Status().Busy {
		t.Fatal("quit did not close the picture")
	}
	if cmd := d.m.start(); cmd != nil {
		t.Fatal("closed picture restarted the producer")
	}
}
