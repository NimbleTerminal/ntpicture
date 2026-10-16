package playback

import (
	"context"
	"errors"
	"image"
	"image/color"
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	picture "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"
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

func TestPlaybackPresentationOverrunRebasesTimeline(t *testing.T) {
	now := time.Unix(100, 0)
	var waits []time.Duration
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	p, err := animation.NewPlayer(&animation.Clip{
		Bounds:    img.Bounds(),
		LoopCount: -1,
		Frames: []animation.ClipFrame{
			{Image: img, Delay: 10 * time.Millisecond},
			{Image: img, Delay: 10 * time.Millisecond},
			{Image: img, Delay: 10 * time.Millisecond},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := New(p, Options{Wait: func(_ context.Context, d time.Duration) bool {
		waits = append(waits, d)
		now = now.Add(d)
		return true
	}})
	c.now = func() time.Time { return now }
	c.SetSize(2, 2)
	overran := false
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
		// Simulate frame 2's Picture preparation/presentation taking 50ms (> 10ms
		// frame delay) after Update(result) has already set nextDue.
		if _, ok := msg.(result); ok && !overran {
			overran = true
			next := c.Update(msg)
			now = now.Add(50 * time.Millisecond)
			run(next)
			return
		}
		run(c.Update(msg))
	}
	run(c.SetPlayer(c.player))
	// Frame 1 waits 10ms; frame 2 overruns presentation so it waits 0ms;
	// frame 3 must still receive its full 10ms interval rather than 0ms.
	want := []time.Duration{10 * time.Millisecond, 0, 10 * time.Millisecond}
	if !reflect.DeepEqual(waits, want) {
		t.Fatalf("waits=%v, want %v", waits, want)
	}
	c.Close()
}

func TestPlaybackControlsResetDeadline(t *testing.T) {
	c := New(testPlayer(t), Options{})
	defer func() { _ = c.Close() }()
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

func TestCompositeUnchangedTickSkipsPictureSubmission(t *testing.T) {
	p := testPlayer(t)
	// Offset the second region's delay while keeping both finite.
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	slow, err := animation.NewPlayer(&animation.Clip{Bounds: frame.Bounds(), LoopCount: -1, Frames: []animation.ClipFrame{{Image: frame, Delay: 3 * time.Millisecond}, {Image: frame, Delay: 3 * time.Millisecond}}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := animation.NewComposite(context.Background(), image.NewRGBA(image.Rect(0, 0, 4, 2)), []animation.Region{{Rect: image.Rect(0, 0, 2, 2), Player: p, Key: "fast"}, {Rect: image.Rect(2, 0, 4, 2), Player: slow, Key: "slow"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := NewSource(source, Options{Wait: func(context.Context, time.Duration) bool { return true }})
	c.SetSize(4, 2)
	drive(t, c, c.SetSource(source))
	// Initial image, fast frame two, slow frame two. Finishing the fast clip
	// while the slow one waits must not submit the unchanged composition.
	if got := c.pic.Status().Generation; got != 4 { // SetSize plus three image submissions.
		t.Fatalf("unchanged tick submitted a picture: generation=%d", got)
	}
	drive(t, c, c.Close())
}

func TestResolutionCorrectionRecoversPresentationError(t *testing.T) {
	for _, kitty := range []bool{false, true} {
		c := New(testPlayer(t), Options{Wait: func(context.Context, time.Duration) bool { return true }})
		c.paused = true
		c.SetSize(2, 2)
		c.SetTerminal(kitty, 8, 16)
		drive(t, c, c.SetPlayer(c.player))
		stale := c.SetResolution(0)()
		drive(t, c, c.Update(stale))
		if c.Status().Err == nil {
			t.Fatal("missing validation error")
		}
		drive(t, c, c.SetResolution(.5))
		drive(t, c, c.Update(stale)) // Late validation must not poison the corrected state.
		drive(t, c, c.Pause(false))
		if c.Status().Err != nil || !c.Status().Finished {
			t.Fatalf("correction did not resume: %+v", c.Status())
		}
		drive(t, c, c.Close())
	}
}

func TestResolutionCorrectionPreservesSourceError(t *testing.T) {
	c := New(testPlayer(t), Options{})
	defer func() { _ = c.Close() }()
	c.err = ErrNotSeekable
	drive(t, c, c.SetResolution(0))
	drive(t, c, c.SetResolution(.5))
	if c.Status().Err != ErrNotSeekable {
		t.Fatal("presentation correction cleared source error")
	}
}

func TestPauseAndSetRatePreserveInFlightSeekAndRestart(t *testing.T) {
	c := New(testPlayer(t), Options{Wait: func(context.Context, time.Duration) bool { return true }})
	defer func() { _ = c.Close() }()
	c.SetSize(2, 2)
	drive(t, c, c.Init())

	// Start a Seek, then call SetRate and Pause(true) before the seek worker runs.
	seekCmd := c.Seek(2)
	c.SetRate(2)
	c.Pause(true)
	drive(t, c, seekCmd)
	if c.Status().Player.Frame() != 2 || !c.Status().Paused {
		t.Fatalf("in-flight seek was dropped by SetRate/Pause: frame=%d paused=%v", c.Status().Player.Frame(), c.Status().Paused)
	}

	// Start a Restart while paused at frame 2, then call SetRate before the restart worker runs.
	restartCmd := c.Restart()
	c.SetRate(1.5)
	drive(t, c, restartCmd)
	if c.Status().Player.Frame() != 1 || !c.Status().Paused {
		t.Fatalf("in-flight restart was dropped by SetRate: frame=%d paused=%v", c.Status().Player.Frame(), c.Status().Paused)
	}
}

func TestControllerPresentedStaysInSyncAfterRejectionAndAsyncFailure(t *testing.T) {
	distinctPlayer := func(t *testing.T) *animation.Player {
		t.Helper()
		f1 := image.NewRGBA(image.Rect(0, 0, 2, 2))
		f2 := image.NewRGBA(image.Rect(0, 0, 2, 2))
		f2.Pix[0] = 255
		p, err := animation.NewPlayer(&animation.Clip{
			Bounds:    f1.Bounds(),
			LoopCount: -1,
			Frames: []animation.ClipFrame{
				{Image: f1, Delay: time.Millisecond},
				{Image: f2, Delay: time.Millisecond},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("ScheduleRejectionRetainsSubmissionForLaterRecovery", func(t *testing.T) {
		var calls int
		c := New(distinctPlayer(t), Options{
			Wait:     func(context.Context, time.Duration) bool { return true },
			FreshIDs: true,
			Picture: picture.Options{
				RetireDelay: -1,
				NextID: func() (int, error) {
					calls++
					if calls == 2 {
						return 0, errors.New("id exhausted on frame 2")
					}
					return 100 + calls, nil
				},
			},
		})
		defer func() { _ = c.Close() }()
		c.Pause(true)
		c.SetSize(2, 2)
		c.SetTerminal(true, 8, 16)
		drive(t, c, c.Init())
		if c.Status().PresentedPlayer == nil || c.Status().PresentedPlayer.Frame() != 1 {
			t.Fatalf("expected initial PresentedPlayer at frame 1, got %+v", c.Status())
		}

		// Seek to frame 2: NextID fails synchronously in Picture.schedule(), leaving Picture's
		// p.img set to frame 2 while reporting an error.
		drive(t, c, c.Seek(2))
		if c.Status().Err == nil {
			t.Fatal("expected error from failed NextID on frame 2")
		}

		// A subsequent resize succeeds and commits Picture's retained p.img (frame 2);
		// Controller.Status().PresentedPlayer must promote to frame 2 rather than staying on frame 1.
		drive(t, c, c.SetSize(3, 2))
		if c.Status().PresentedPlayer == nil || c.Status().PresentedPlayer.Frame() != 2 {
			t.Fatalf("PresentedPlayer after recovery following NextID failure = %+v, want frame 2", c.Status().PresentedPlayer)
		}
	})

	t.Run("FailedFrameSubmissionRevertsPictureAndPreservesPresented", func(t *testing.T) {
		f1 := &imageSource{img: image.NewRGBA(image.Rect(0, 0, 2, 2))}
		badFrame2 := &badAdvanceSource{first: f1.img}
		c := NewSource(badFrame2, Options{
			Wait:    func(context.Context, time.Duration) bool { return true },
			Picture: picture.Options{RetireDelay: -1},
		})
		defer func() { _ = c.Close() }()
		c.Pause(true)
		c.SetSize(2, 2)
		c.SetTerminal(true, 8, 16)
		drive(t, c, c.Init())
		if c.Status().Presented != badFrame2 {
			t.Fatalf("expected initial Presented == badFrame2, got %+v", c.Status().Presented)
		}

		// Advancing produces a source whose Image() has empty bounds; SetImageImmutable
		// triggers failSubmission on Picture, which reverts p.img back to frame 1's front.
		drive(t, c, c.Pause(false))
		if c.Status().Err == nil {
			t.Fatal("expected error from invalid frame 2 image")
		}

		// A subsequent resize re-prepares Picture's reverted p.img (frame 1).
		// Controller.Status().Presented must remain frame 1 (badFrame2), not promote the failed frame 2.
		drive(t, c, c.SetSize(3, 2))
		if c.Status().Presented != badFrame2 {
			t.Fatalf("Presented after resize following failed frame 2 = %#v, want %#v", c.Status().Presented, badFrame2)
		}
	})
}

type badAdvanceSource struct {
	first image.Image
	step  int
}

func (s *badAdvanceSource) Image() image.Image {
	if s.step > 0 {
		return image.NewRGBA(image.Rectangle{})
	}
	return s.first
}
func (s *badAdvanceSource) Delay() time.Duration { return time.Millisecond }
func (s *badAdvanceSource) Advance(context.Context) (animation.Source, bool, error) {
	return &badAdvanceSource{first: s.first, step: s.step + 1}, false, nil
}
func (s *badAdvanceSource) Reset(context.Context) (animation.Source, error) {
	return &badAdvanceSource{first: s.first, step: 0}, nil
}

func FuzzControllerActions(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	f.Add([]byte{3, 2, 1, 4, 3, 6, 5, 0, 8, 11})
	f.Fuzz(func(t *testing.T, ops []byte) {
		if len(ops) > 64 {
			ops = ops[:64]
		}
		c := New(testPlayer(t), Options{
			Wait:    func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil },
			Picture: picture.Options{RetireDelay: -1},
		})
		var queue []tea.Cmd
		enqueue := func(cmd tea.Cmd) {
			if cmd != nil {
				queue = append(queue, cmd)
			}
		}
		flushOne := func() {
			if len(queue) == 0 {
				return
			}
			cmd := queue[0]
			queue = queue[1:]
			drive(t, c, cmd)
		}
		enqueue(c.Init())
		for _, op := range ops {
			switch op % 14 {
			case 0:
				flushOne()
			case 1:
				enqueue(c.Pause(op&16 != 0))
			case 2:
				enqueue(c.SetRate(float64(int(op)%6) * 0.5))
			case 3:
				enqueue(c.Seek(int(op%4) - 1))
			case 4:
				enqueue(c.Restart())
			case 5:
				enqueue(c.SetVisible(op&16 != 0))
			case 6:
				enqueue(c.SetSize(int(op%5), int((op>>2)%5)))
			case 7:
				enqueue(c.SetTerminal(op&16 != 0, int(op%10)+1, int((op>>2)%10)+1))
			case 8:
				enqueue(c.SetResolution(float64(op%5) * 0.25))
			case 9:
				c.SetViewport(picture.Viewport{X: int(op%5) - 1, Y: int((op>>2)%5) - 1, Width: int(op % 4), Height: int((op >> 3) % 4)})
			case 10:
				c.ResetViewport()
			case 11:
				enqueue(c.SetPlayer(testPlayer(t)))
			case 12:
				enqueue(c.SetFit(picture.FitMode(op % 4)))
			case 13:
				enqueue(c.SetAnchor(picture.FitAnchor(op % 6)))
			}
			_ = c.View()
			_ = c.Fit()
			_ = c.Anchor()
			st := c.Status()
			if st.Rate < 0.25 || st.Rate > 4 {
				t.Fatalf("rate out of bounds: %v", st.Rate)
			}
		}
		for len(queue) > 0 {
			flushOne()
		}
		drive(t, c, c.Close())
		if c.Status().Source != nil || c.Status().Player != nil || c.Status().Busy {
			t.Fatalf("closed controller retained state: %+v", c.Status())
		}
	})
}

func TestControllerHiddenRepeatSetVisiblePreservesQueuedSeekAndPostCloseGuards(t *testing.T) {
	c := New(testPlayer(t), Options{Wait: func(context.Context, time.Duration) bool { return true }})
	c.Pause(true)
	c.SetSize(2, 2)
	c.SetTerminal(true, 9, 18)
	drive(t, c, c.Init())

	if c.Mode() != picture.PictureKitty {
		t.Fatalf("Mode() = %v, want PictureKitty", c.Mode())
	}
	if w, h := c.CellPixelSize(); w != 9 || h != 18 {
		t.Fatalf("CellPixelSize() = %dx%d, want 9x18", w, h)
	}

	// Hide, queue a Seek(2) while hidden, then call SetVisible(false) again:
	// redundant SetVisible(false) must not drop the queued Seek.
	drive(t, c, c.SetVisible(false))
	drive(t, c, c.Seek(2))
	if cmd := c.SetVisible(false); cmd != nil {
		t.Fatal("expected redundant SetVisible(false) to return nil")
	}
	drive(t, c, c.SetVisible(true))
	if c.Status().Player == nil || c.Status().Player.Frame() != 2 {
		t.Fatalf("Player.Frame() after SetVisible(true) = %+v, want 2", c.Status().Player)
	}

	// After Close(), Pause and SetRate must be no-ops that do not mutate Status.
	if c.Closed() {
		t.Fatal("expected controller not to be closed before Close()")
	}
	drive(t, c, c.Close())
	if !c.Closed() || !(*Controller)(nil).Closed() {
		t.Fatal("expected Closed() to report true after Close() and on nil receiver")
	}
	before := c.Status()
	if c.Pause(false) != nil || c.SetRate(3) != nil {
		t.Fatal("expected Pause and SetRate after Close to return nil")
	}
	after := c.Status()
	if after.Paused != before.Paused || after.Rate != before.Rate {
		t.Fatalf("post-Close Pause/SetRate mutated status: before=%+v after=%+v", before, after)
	}

	session := picture.NewTerminalSession(picture.TerminalOptions{
		Kitty:           picture.KittyCapabilitySupported,
		CellPixelWidth:  11,
		CellPixelHeight: 22,
	})
	defer session.Close()
	cSess := New(testPlayer(t), Options{Picture: picture.Options{Session: session, AutoMode: true}})
	if cSess.Session() != session || !cSess.AutoMode() || !cSess.Visible() {
		t.Fatalf("unexpected controller getters: Session=%p AutoMode=%v Visible=%v", cSess.Session(), cSess.AutoMode(), cSess.Visible())
	}
	if cSess.KittySupported() != picture.KittyCapabilitySupported {
		t.Fatalf("KittySupported() = %v, want Supported", cSess.KittySupported())
	}
	if st := cSess.TerminalState(); st.CellPixelWidth != 11 || st.CellPixelHeight != 22 {
		t.Fatalf("TerminalState() = %+v, want 11x22", st)
	}
	drive(t, cSess, cSess.SetSize(3, 2))
	if cols, rows := cSess.Size(); cols != 3 || rows != 2 {
		t.Fatalf("Size() = %dx%d, want 3x2", cols, rows)
	}
	drive(t, cSess, cSess.SetCellSize(13, 26))
	if cw, ch := cSess.CellPixelSize(); cw != 13 || ch != 26 || !cSess.AutoMode() {
		t.Fatalf("after SetCellSize: %dx%d AutoMode=%v", cw, ch, cSess.AutoMode())
	}
	drive(t, cSess, cSess.SetResolution(0.5))
	if cSess.Resolution() != 0.5 {
		t.Fatalf("Resolution() = %v, want 0.5", cSess.Resolution())
	}
	bg := color.RGBA{R: 5, G: 6, B: 7, A: 255}
	drive(t, cSess, cSess.SetBackground(bg))
	if cSess.Background() != color.RGBA64Model.Convert(bg) {
		t.Fatalf("Background() = %v", cSess.Background())
	}
	drive(t, cSess, cSess.SetCrop(image.Rect(0, 0, 1, 1)))
	if cSess.Crop() != image.Rect(0, 0, 1, 1) {
		t.Fatalf("Crop() = %v, want (0,0)-(1,1)", cSess.Crop())
	}
	drive(t, cSess, cSess.ResetCrop())
	if !cSess.Crop().Empty() {
		t.Fatalf("Crop() after ResetCrop = %v, want empty", cSess.Crop())
	}
	drive(t, cSess, cSess.ToggleMode())
	if cSess.Mode() != picture.PictureGlyph || cSess.AutoMode() {
		t.Fatalf("after ToggleMode to Glyph: Mode=%v AutoMode=%v", cSess.Mode(), cSess.AutoMode())
	}
	drive(t, cSess, cSess.SetMode(picture.PictureAuto))
	drive(t, cSess, cSess.SetKittyCapability(picture.KittyCapabilityUnsupported))
	if cSess.Mode() != picture.PictureGlyph || cSess.ToggleMode() != nil {
		t.Fatalf("expected Glyph mode and no-op ToggleMode when Unsupported; Mode=%v", cSess.Mode())
	}
	drive(t, cSess, cSess.Keep())
	if !cSess.Closed() {
		t.Fatal("expected Closed() to report true after Keep()")
	}
}
