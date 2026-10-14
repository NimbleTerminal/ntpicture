package playback

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"image"
	picture "nimbleterminal.dev/ntpicture"
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
	drive(t, c, c.Update(result{owner: c, job: old, source: c.player, more: true}))
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

func TestKeepCancelsCompositionAndPreservesFrame(t *testing.T) {
	c := New(testPlayer(t), Options{})
	c.Pause(true)
	c.SetSize(2, 2)
	drive(t, c, c.SetPlayer(c.player))
	view := c.View()
	pending := c.Seek(2)
	job := c.job
	drive(t, c, c.Keep())
	drive(t, c, c.Update(pending()))
	if job.ctx.Err() == nil || c.Status().Busy || c.player != nil || c.View() != view || view == "" {
		t.Fatal("keep lost frame or accepted cancelled composition")
	}
	drive(t, c, c.Close())
	if c.View() != view || c.SetPlayer(testPlayer(t)) != nil || c.Keep() != nil {
		t.Fatal("keep not final")
	}
}

func TestPlaybackStableIDDefaultAndOptOut(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		ids := 0
		c := New(testPlayer(t), Options{FreshIDs: fresh, Picture: picture.Options{NextID: func() (int, error) { ids++; return ids, nil }, RetireDelay: -1}})
		c.Pause(true)
		c.SetSize(2, 2)
		c.SetTerminal(true, 1, 1)
		drive(t, c, c.SetPlayer(c.player))
		view := c.View()
		drive(t, c, c.Seek(2))
		if fresh {
			if ids != 2 || c.View() == view {
				t.Fatal("fresh-ID opt-out ignored")
			}
		} else if ids != 1 || c.View() != view {
			t.Fatal("playback did not default to stable ID")
		}
		drive(t, c, c.Close())
	}
}

func TestPlaybackDeadlineIncludesRenderTime(t *testing.T) {
	now := time.Unix(100, 0)
	var waits []time.Duration
	c := New(testPlayer(t), Options{Wait: func(_ context.Context, d time.Duration) bool { waits = append(waits, d); now = now.Add(d); return true }})
	c.now = func() time.Time { return now }
	period := c.player.Delay()
	overhead := period / 4
	c.SetSize(2, 2)
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
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
				run(v.Index(i).Interface().(tea.Cmd))
			}
			return
		}
		if _, ok := msg.(result); ok {
			now = now.Add(overhead)
		}
		run(c.Update(msg))
	}
	run(c.SetPlayer(c.player))
	if len(waits) != 2 || waits[0] != period || waits[1] != period-overhead {
		t.Fatalf("waits=%v, period=%v: work time was added to frame period", waits, period)
	}
	c.Close()
}

func TestPlaybackControlsResetDeadline(t *testing.T) {
	c := New(testPlayer(t), Options{})
	defer c.Close()
	for _, change := range []func(){func() { c.Pause(true) }, func() { c.SetVisible(false) }, func() { c.SetRate(2) }, func() { c.Seek(1) }, func() { c.SetPlayer(testPlayer(t)) }} {
		c.nextDue = time.Unix(123, 0)
		change()
		if !c.nextDue.IsZero() {
			t.Fatal("control retained stale playback timeline")
		}
	}
}

func TestCompositeSourcePlaybackAndRestart(t *testing.T) {
	p := testPlayer(t)
	source, err := animation.NewComposite(context.Background(), image.NewRGBA(image.Rect(0, 0, 4, 2)), []animation.Region{{Rect: image.Rect(0, 0, 2, 2), Player: p, Key: "a"}, {Rect: image.Rect(2, 0, 4, 2), Player: p, Key: "b"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := NewSource(source, Options{Wait: func(context.Context, time.Duration) bool { return true }})
	c.SetSize(4, 2)
	drive(t, c, c.SetSource(source))
	if !c.Status().Finished || c.Status().Player != nil || c.Status().Source == nil || c.View() == "" {
		t.Fatal("composite did not finish")
	}
	drive(t, c, c.Pause(true))
	drive(t, c, c.Restart())
	if c.Status().Finished || !c.Status().Paused {
		t.Fatal("restart lost pause or failed to reset")
	}
	c.Seek(2)
	if c.Status().Err != ErrNotSeekable {
		t.Fatal("composite exposed meaningless frame seeking")
	}
	drive(t, c, c.Restart())
	drive(t, c, c.Pause(false))
	if !c.Status().Finished {
		t.Fatal("composite did not resume")
	}
	drive(t, c, c.Close())
	if c.Status().Source != nil {
		t.Fatal("closed controller retained source")
	}
}
