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
	id, view := p.front.cfg.KittyID, p.View()
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
			id, view := p.front.cfg.KittyID, p.View()
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
	id := p.front.cfg.KittyID
	messages := drivePicture(t, p, p.SetImage(testPixels(color.RGBA{R: 255, A: 255})))
	if p.front.cfg.KittyID == id || !strings.Contains(output(messages), kittyDeleteImage(id)) {
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

func TestPictureStopBoundariesAndSharedMemory(t *testing.T) {
	for _, stable := range []bool{false, true} {
		for _, medium := range []KittyMedium{KittyMediumDirect, KittyMediumSharedMemory} {
			t.Run(fmt.Sprintf("stable=%v/medium=%v", stable, medium), func(t *testing.T) {
				// 1. Stop during preparation (before picturePrepared arrives).
				p := testPicture(true)
				p.opts.StableID = stable
				p.opts.Render.KittyMedium = medium
				drivePicture(t, p, p.SetImage(testPixels(color.RGBA{G: 255, A: 255})))
				frontID, view := p.front.cfg.KittyID, p.View()
				pending := p.SetImage(testPixels(color.RGBA{R: 255, A: 255}))
				job := p.job
				stopMsgs := drivePicture(t, p, p.Stop())
				lateMsgs := drivePicture(t, p, pending)
				if job.ctx.Err() == nil || p.Status().Busy || p.Status().Presented == 0 || p.Status().Generation != p.Status().Presented {
					t.Fatalf("stop during prepare left invalid status: %+v", p.Status())
				}
				if p.View() != view || p.front == nil || p.front.image.At(0, 0) != (color.RGBA{G: 255, A: 255}) {
					t.Fatal("stop during prepare did not retain committed front")
				}
				if strings.Contains(output(stopMsgs)+output(lateMsgs), kittyDeleteImage(frontID)) {
					t.Fatal("stop during prepare deleted retained front ID")
				}
				// Subsequent SetImage resumes normal replacement after Stop.
				drivePicture(t, p, p.SetImage(testPixels(color.RGBA{B: 255, A: 255})))
				if p.front == nil || p.front.image.At(0, 0) != (color.RGBA{B: 255, A: 255}) {
					t.Fatal("replacement after Stop did not commit")
				}
				replacedID := p.front.cfg.KittyID
				closeMsgs := drivePicture(t, p, p.Close())
				if !strings.Contains(output(closeMsgs), kittyDeleteImage(replacedID)) {
					t.Fatal("close after Stop did not delete retained image")
				}

				// 2. Stop after picturePrepared (SHM submitted) but before step 0 accepts transmission.
				p2 := testPicture(true)
				p2.opts.StableID = stable
				p2.opts.Render.KittyMedium = medium
				drivePicture(t, p2, p2.SetImage(testPixels(color.RGBA{G: 255, A: 255})))
				front2 := p2.front
				front2ID, view2 := front2.cfg.KittyID, p2.View()
				front2SHM := front2.kittyShared.object
				preparedMsg := p2.SetImage(testPixels(color.RGBA{R: 255, A: 255}))()
				job2 := p2.job
				seqCmd := p2.Update(preparedMsg)
				borrowerSHM := job2.placement.kittyShared.object
				stop2Msgs := drivePicture(t, p2, p2.Stop())
				if medium == KittyMediumSharedMemory && p2.session().transport().shared {
					if !stable && (borrowerSHM == nil || !borrowerSHM.Done()) {
						t.Fatal("fresh-ID borrower SHM was not released immediately by Stop")
					}
					if front2SHM == nil || front2SHM.Done() {
						t.Fatal("retained front SHM was released by Stop")
					}
				}
				seq2 := reflect.ValueOf(seqCmd())
				if got := seq2.Index(0).Interface().(tea.Cmd)(); got != nil {
					t.Fatalf("cancelled step 0 emitted %v", got)
				}
				step1Msgs := drivePicture(t, p2, seq2.Index(1).Interface().(tea.Cmd))
				raw2 := output(stop2Msgs) + output(step1Msgs)
				if strings.Contains(raw2, kittyDeleteImage(front2ID)) || p2.View() != view2 {
					t.Fatalf("pre-step0 Stop corrupted front: raw=%q", raw2)
				}
				if stable && strings.Contains(raw2, "a=T") {
					t.Fatalf("pre-step0 Stop unnecessarily retransmitted unoverwritten stable ID: %q", raw2)
				}
				drivePicture(t, p2, p2.Close())
				if (front2SHM != nil && !front2SHM.Done()) || (borrowerSHM != nil && !borrowerSHM.Done()) {
					t.Fatal("close after pre-step0 Stop leaked front or borrower SHM")
				}

				// 3. Stop after step 0 accepted transmission (in-flight output).
				p3 := testPicture(true)
				p3.opts.StableID = stable
				p3.opts.Render.KittyMedium = medium
				drivePicture(t, p3, p3.SetImage(testPixels(color.RGBA{G: 255, A: 255})))
				front3 := p3.front
				front3ID, view3 := front3.cfg.KittyID, p3.View()
				prepared3 := p3.SetImage(testPixels(color.RGBA{R: 255, A: 255}))()
				job3 := p3.job
				borrower3ID := job3.placement.cfg.KittyID
				seq3 := reflect.ValueOf(p3.Update(prepared3)())
				if got := seq3.Index(0).Interface().(tea.Cmd)(); got == nil {
					t.Fatal("expected step 0 to accept transmission")
				}
				stopCmd := p3.Stop()
				if stable {
					stopSeq := reflect.ValueOf(stopCmd())
					restoreCmd := stopSeq.Index(stopSeq.Len() - 1).Interface().(tea.Cmd)
					done := make(chan tea.Msg, 1)
					go func() { done <- restoreCmd() }()
					select {
					case <-done:
						t.Fatal("Stop restore overtook accepted in-flight transmission")
					case <-time.After(10 * time.Millisecond):
					}
					step1Out := drivePicture(t, p3, seq3.Index(1).Interface().(tea.Cmd))
					if strings.Contains(output(step1Out), kittyDeleteImage(front3ID)) {
						t.Fatal("step 1 deleted active stable ID after Stop")
					}
					select {
					case msg := <-done:
						raw, ok := msg.(tea.RawMsg)
						if !ok || !strings.Contains(fmt.Sprint(raw.Msg), "a=T") || strings.Contains(fmt.Sprint(raw.Msg), "t=s") {
							t.Fatalf("Stop restore did not emit direct Kitty restore APC: %#v", msg)
						}
					case <-time.After(time.Second):
						t.Fatal("Stop restore did not drain accepted transmission")
					}
					if front3.lease.overwritten.Load() {
						t.Fatal("Stop restore did not clear overwritten flag")
					}
				} else {
					msgs := drivePicture(t, p3, stopCmd)
					msgs = append(msgs, drivePicture(t, p3, seq3.Index(1).Interface().(tea.Cmd))...)
					raw := output(msgs)
					if !strings.Contains(raw, kittyDeleteImage(borrower3ID)) || strings.Contains(raw, kittyDeleteImage(front3ID)) {
						t.Fatalf("fresh-ID Stop did not delete accepted replacement or deleted front: %q", raw)
					}
				}
				if p3.View() != view3 || p3.Status().Busy {
					t.Fatal("post-step0 Stop lost view or stayed busy")
				}
				close3 := drivePicture(t, p3, p3.Close())
				if !strings.Contains(output(close3), kittyDeleteImage(front3ID)) {
					t.Fatal("close after post-step0 Stop did not delete retained front")
				}
			})
		}
	}
}

func TestKeepOnMainScreenRetransmitsDirectSharedAndAcceptedStableID(t *testing.T) {
	for _, stable := range []bool{false, true} {
		for _, medium := range []KittyMedium{KittyMediumDirect, KittyMediumSharedMemory} {
			t.Run(fmt.Sprintf("stable=%v/medium=%v", stable, medium), func(t *testing.T) {
				p := testPicture(true)
				p.opts.StableID = stable
				p.opts.Render.KittyMedium = medium
				// Keep a retired placement when testing fresh IDs so cleanup + retransmit ordering is exercised.
				p.opts.RetireDelay = time.Hour
				p.retirementPending = true
				drivePicture(t, p, p.SetImage(testPixels(color.RGBA{R: 10, A: 255})))
				firstID := p.front.cfg.KittyID
				drivePicture(t, p, p.SetImage(testPixels(color.RGBA{G: 200, A: 255})))
				front := p.front
				frontID, view := front.cfg.KittyID, p.View()
				frontSHM := front.kittyShared.object

				// Start an accepted stable-ID replacement on a second picture to test accepted-overwrite drain too.
				messages := drivePicture(t, p, p.KeepOnMainScreen())
				raw := output(messages)
				if !strings.Contains(raw, "a=T") || strings.Contains(raw, "t=s") {
					t.Fatalf("KeepOnMainScreen did not retransmit direct Kitty placement: %q", raw)
				}
				if strings.Contains(raw, kittyDeleteImage(frontID)) {
					t.Fatalf("KeepOnMainScreen deleted kept front ID %d: %q", frontID, raw)
				}
				if !stable {
					delIdx := strings.Index(raw, kittyDeleteImage(firstID))
					txIdx := strings.Index(raw, "a=T")
					if delIdx < 0 || delIdx > txIdx {
						t.Fatalf("retired placement was not deleted before KeepOnMainScreen retransmit: %q", raw)
					}
				}
				if frontSHM != nil && !frontSHM.Done() {
					t.Fatal("KeepOnMainScreen leaked front SHM object")
				}
				if p.View() != view || !p.Closed() {
					t.Fatal("KeepOnMainScreen did not preserve View or mark Picture closed")
				}
				if p.KeepOnMainScreen() != nil || p.Keep() != nil || p.Close() != nil || p.Stop() != nil || p.View() != view {
					t.Fatal("KeepOnMainScreen is not final and idempotent")
				}
			})
		}
	}
}
