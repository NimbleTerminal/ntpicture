package playback

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"image"
	"nimbleterminal.dev/ntpicture/animation"
	"reflect"
	"testing"
	"time"
)

func testPlayer(t *testing.T) *animation.Player {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	p, err := animation.NewPlayer(&animation.Clip{Bounds: img.Bounds(), LoopCount: -1, Frames: []animation.ClipFrame{{Image: img, Delay: time.Millisecond}, {Image: img, Delay: time.Millisecond}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func drive(t *testing.T, c *Controller, cmd tea.Cmd) {
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
			drive(t, c, v.Index(i).Interface().(tea.Cmd))
		}
		return
	}
	drive(t, c, c.Update(msg))
}
func TestPlaybackWaitsForPresentationAndFinishes(t *testing.T) {
	waits := 0
	c := New(testPlayer(t), Options{Wait: func(context.Context, time.Duration) bool { waits++; return true }})
	c.SetSize(2, 2)
	if c.schedule() != nil {
		t.Fatal("scheduled before presentation")
	}
	drive(t, c, c.SetPlayer(c.player))
	if !c.Status().Finished || c.Status().Player.Frame() != 2 || waits != 2 {
		t.Fatalf("unexpected playback %+v waits=%d", c.Status(), waits)
	}
	drive(t, c, c.Close())
}
func TestCancelledSeekCoalescesAndRejectsOtherOwners(t *testing.T) {
	c := New(testPlayer(t), Options{})
	first := c.Seek(2)
	old := c.job
	if first == nil {
		t.Fatal("no seek command")
	}
	c.Seek(1)
	if c.job != old || old.ctx.Err() == nil {
		t.Fatal("old job not cancelled or overlapping job started")
	}
	other := New(testPlayer(t), Options{})
	other.Update(first())
	if other.player.Frame() != 1 {
		t.Fatal("foreign result consumed")
	}
	drive(t, c, c.Update(result{owner: c, job: old, player: c.player, more: true}))
	if c.player.Frame() != 1 || c.job != nil || !c.paused {
		t.Fatalf("coalesced seek: %+v", c.Status())
	}
	c.Close()
	if c.Seek(2) != nil {
		t.Fatal("closed controller restarted")
	}
}
func TestHidingCancelsTimerAndCloseRejectsLateWork(t *testing.T) {
	c := New(testPlayer(t), Options{})
	cmd := c.Seek(2)
	old := c.job
	c.SetVisible(false)
	if old.ctx.Err() == nil {
		t.Fatal("hidden work was not cancelled")
	}
	drive(t, c, c.Update(cmd()))
	if c.player.Frame() != 1 {
		t.Fatal("cancelled seek changed position")
	}
	c.SetVisible(true)
	cmd = c.Seek(2)
	c.Close()
	drive(t, c, c.Update(cmd()))
	if c.player != nil {
		t.Fatal("late seek restored closed controller")
	}
}

func TestCloseStopsWaitingTimer(t *testing.T) {
	c := New(testPlayer(t), Options{})
	c.paused = true
	c.SetSize(2, 2)
	drive(t, c, c.SetPlayer(c.player))
	// A very long wait makes a timer firing accidentally distinguishable from cancellation.
	c.wait = func(ctx context.Context, _ time.Duration) bool { return wait(ctx, time.Hour) }
	cmd := c.Pause(false)
	if cmd == nil {
		t.Fatal("no scheduled timer")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	c.Close()
	select {
	case msg := <-done:
		drive(t, c, c.Update(msg))
	case <-time.After(time.Second):
		t.Fatal("close did not stop timer")
	}
	if c.player != nil {
		t.Fatal("cancelled timer restored closed playback")
	}
}
