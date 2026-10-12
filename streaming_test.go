package ntpicture

import (
	"fmt"
	"image/color"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestStableIDFramesAndGeometry(t *testing.T) {
	p := testPicture(true)
	p.opts.StableID = true
	ids := 0
	p.opts.NextID = func() (int, error) { ids++; return ids, nil }
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{A: 255})))
	view := p.View()
	for i := 0; i < 10; i++ {
		messages := drivePicture(t, p, p.SetImage(testPixels(color.RGBA{R: uint8(i), A: 255})))
		if p.View() != view || ids != 1 || strings.Contains(output(messages), "a=d") {
			t.Fatal("stream changed grid or deleted shared ID")
		}
	}
	drivePicture(t, p, p.SetSize(3, 2))
	if ids != 2 || p.View() == view {
		t.Fatal("resize did not allocate a fresh placement")
	}
	drivePicture(t, p, p.SetTerminal(true, 2, 2))
	if ids != 3 {
		t.Fatal("cell geometry reused ID")
	}
	drivePicture(t, p, p.SetVisible(false))
	drivePicture(t, p, p.SetVisible(true))
	if ids != 4 {
		t.Fatal("show reused deleted ID")
	}
	messages := drivePicture(t, p, p.Close())
	if !strings.Contains(output(messages), kittyDeleteImage(4)) {
		t.Fatal("close did not delete stream")
	}
}

func TestStableIDCancelledPreparation(t *testing.T) {
	p := testPicture(true)
	p.opts.StableID = true
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{A: 255})))
	id, view := p.front.model.kittyID, p.View()
	pending := p.SetImage(testPixels(color.RGBA{R: 255, A: 255}))
	p.SetImage(testPixels(color.RGBA{B: 255, A: 255}))
	messages := drivePicture(t, p, pending)
	if p.View() != view || strings.Contains(output(messages), kittyDeleteImage(id)) || p.front.image.At(0, 0) != (color.RGBA{B: 255, A: 255}) {
		t.Fatal("cancelled borrower corrupted stream")
	}
	drivePicture(t, p, p.Close())
}

func TestStableIDAcceptedOutputKeepAndClose(t *testing.T) {
	for _, keep := range []bool{false, true} {
		t.Run(fmt.Sprint(keep), func(t *testing.T) {
			p := testPicture(true)
			p.opts.StableID = true
			drivePicture(t, p, p.SetImage(testPixels(color.RGBA{A: 255})))
			id, view := p.front.model.kittyID, p.View()
			prepared := p.SetImage(testPixels(color.RGBA{R: 255, A: 255}))()
			seq := reflect.ValueOf(p.Update(prepared)())
			seq.Index(0).Interface().(tea.Cmd)() // Accepted output may have updated the ID.
			var cleanup tea.Cmd
			if keep {
				cleanup = p.Keep()
			} else {
				cleanup = p.Close()
			}
			completion := seq.Index(1).Interface().(tea.Cmd)
			messages := drivePicture(t, p, completion)
			messages = append(messages, drivePicture(t, p, cleanup)...)
			raw := output(messages)
			if keep {
				if p.View() != view || strings.Contains(raw, kittyDeleteImage(id)) || !strings.Contains(raw, "a=T") {
					t.Fatal("keep failed to restore committed streaming frame")
				}
			} else if p.View() != "" || !strings.Contains(raw, kittyDeleteImage(id)) {
				t.Fatal("close leaked accepted stream")
			}
		})
	}
}

func TestStableIDClearOnReplaceTakesPrecedence(t *testing.T) {
	p := testPicture(true)
	p.opts.StableID = true
	p.opts.ClearOnReplace = true
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{A: 255})))
	id := p.front.model.kittyID
	messages := drivePicture(t, p, p.SetImage(testPixels(color.RGBA{R: 255, A: 255})))
	if p.front.model.kittyID == id || !strings.Contains(output(messages), kittyDeleteImage(id)) {
		t.Fatal("stream bypassed clear policy")
	}
	drivePicture(t, p, p.Close())
}

// Reports protocol bytes and changed View bytes, not Bubble Tea's escaped wire
// output: a stable View lets its renderer omit the unchanged placeholder grid.
func TestStreamingTraffic240x68(t *testing.T) {
	for _, stable := range []bool{false, true} {
		p := New(Options{StableID: stable, RetireDelay: -1, Render: Config{Fit: FitFill, KittyResolutionFactor: .125}})
		p.SetSize(240, 68)
		p.SetTerminal(true, 8, 16)
		var previous string
		bitmap, grid := 0, 0
		for i := 0; i < 10; i++ {
			img := randomNRGBA(240, 135)
			messages := drivePicture(t, p, p.SetImage(img))
			view := p.View()
			if i > 0 {
				bitmap += len(output(messages))
				if view != previous {
					grid += len(view)
				}
			}
			previous = view
		}
		t.Logf("StableID=%v: average APC=%d bytes/frame, changed placeholder content=%d bytes/frame", stable, bitmap/9, grid/9)
		if stable && grid != 0 {
			t.Fatal("stream changed placeholders")
		}
		drivePicture(t, p, p.Close())
	}
}

func TestStableIDKeepWaitsForAcceptedOutput(t *testing.T) {
	p := testPicture(true)
	p.opts.StableID = true
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{A: 255})))
	prepared := p.SetImage(testPixels(color.RGBA{R: 255, A: 255}))()
	transmission := reflect.ValueOf(p.Update(prepared)())
	transmission.Index(0).Interface().(tea.Cmd)()
	keep := reflect.ValueOf(p.Keep()())
	// The final keep command must not restore the old frame ahead of an already
	// accepted RawMsg. Exercise the wait concurrently with transmission completion.
	done := make(chan tea.Msg, 1)
	go func() { done <- keep.Index(keep.Len() - 1).Interface().(tea.Cmd)() }()
	select {
	case <-done:
		t.Fatal("keep overtook accepted output")
	case <-time.After(10 * time.Millisecond):
	}
	drivePicture(t, p, transmission.Index(1).Interface().(tea.Cmd))
	select {
	case msg := <-done:
		if _, ok := msg.(tea.RawMsg); !ok {
			t.Fatal("keep did not restore image")
		}
	case <-time.After(time.Second):
		t.Fatal("keep failed to drain accepted output")
	}
}

func TestStableIDDelayedRetirementDoesNotDeleteStream(t *testing.T) {
	p := testPicture(true)
	p.opts.StableID = true
	// Keep timer commands out of the synchronous test driver; explicitly deliver
	// the retirement event after advancing the ownership through several frames.
	p.opts.RetireDelay = time.Hour
	p.retirementPending = true
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{A: 255})))
	for i := 0; i < 5; i++ {
		drivePicture(t, p, p.SetImage(testPixels(color.RGBA{R: uint8(i), A: 255})))
	}
	for _, old := range p.retired {
		old.retireAt = time.Time{}
	}
	messages := drivePicture(t, p, p.Update(pictureRetire{p}))
	if strings.Contains(output(messages), "a=d") || !p.front.lease.active.Load() {
		t.Fatal("retirement deleted streaming image")
	}
	drivePicture(t, p, p.Close())
}
