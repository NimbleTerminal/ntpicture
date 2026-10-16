// Package playback schedules immutable animation sources through ntpicture.Picture.
// Hosts own key bindings, layout, visibility decisions, and aggregate budgets.
package playback

import (
	"context"
	"errors"
	"image"
	"image/color"
	"reflect"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	picture "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"
	"nimbleterminal.dev/ntpicture/internal/imageview"
)

// Wait must honor cancellation. Nil uses a cancellable timer.
type Wait func(context.Context, time.Duration) bool
type Options struct {
	// Picture supplies rendering options. Its StableID field is ignored;
	// FreshIDs controls playback ID policy instead.
	Picture picture.Options
	Wait    Wait
	// FreshIDs opts out of the stable-ID default for playback. Use it when
	// retaining independently transmitted old/new Kitty images is required.
	// Picture.ClearOnReplace also overrides stable-ID reuse.
	FreshIDs bool
}

// ErrNotSeekable reports frame seeking on a source that is not animation.Seekable.
var ErrNotSeekable = errors.New("playback: source does not support frame seeking")

type Status struct {
	Source                 animation.Source  // Immutable current source position; may lead presentation.
	Player                 *animation.Player // Immutable current position; presentation may still be pending.
	Paused, Finished, Busy bool
	Rate                   float64
	Err                    error
	// Presented is the position whose image was last committed to output, with
	// picture.PresentedMsg semantics: ordered output, not terminal
	// acknowledgement. Use it to export the displayed frame or label controls.
	// A later position with identical pixels is not resubmitted and becomes
	// Presented immediately. Nil before the first commit and after clearing;
	// Keep retains it. A failed or superseded submission never becomes Presented.
	Presented       animation.Source
	PresentedPlayer *animation.Player // Presented as a standalone player, if it is one.
	Picture         picture.Status    // Presentation generations, readiness and errors.
}

// Controller belongs to one Bubble Tea event loop. Forward every message to
// every controller, including hidden ones, and execute every returned command.
// Message ownership is private; unrelated and stale results are ignored.
type Controller struct {
	pic                               *picture.Picture
	source                            animation.Source
	seeker                            animation.Seekable
	restart                           bool
	player                            *animation.Player
	wait                              Wait
	now                               func() time.Time
	nextDue                           time.Time
	job                               *job
	pending                           *int
	paused, finished, visible, closed bool
	rate                              float64
	err                               error
	presentationErr                   error
	presented                         animation.Source
	submitted                         *submission // Latest uncommitted submission.
	clearOnReplace                    bool
}

// Picture commits only its current generation and cancels superseded work, so
// only the latest submission can still become presented.
type submission struct {
	generation uint64
	source     animation.Source
}
type job struct {
	ctx    context.Context
	cancel context.CancelFunc
	due    time.Time
	source atomic.Pointer[animation.Source]
}

func (j *job) setSource(src animation.Source) {
	j.source.Store(&src)
}

func (j *job) getSource() animation.Source {
	if p := j.source.Load(); p != nil {
		return *p
	}
	return nil
}

type result struct {
	source animation.Source
	owner  *Controller
	job    *job
	more   bool
	err    error
}

// New preserves the standalone-player API, including frame seeking.
func New(player *animation.Player, options Options) *Controller {
	if player == nil {
		return NewSource(nil, options)
	}
	return NewSource(player, options)
}

// NewSource schedules immutable player or composite positions through a Picture.
// Custom sources must meet animation.Source's ownership/cancellation contract.
func NewSource(source animation.Source, options Options) *Controller {
	if options.Wait == nil {
		options.Wait = wait
	}
	options.Picture.StableID = !options.FreshIDs
	c := &Controller{now: time.Now, pic: picture.New(options.Picture), wait: options.Wait, visible: true, rate: 1, clearOnReplace: options.Picture.ClearOnReplace}
	c.setSource(source)
	return c
}
func (c *Controller) setSource(source animation.Source) {
	c.player, _ = source.(*animation.Player)
	if p, ok := source.(*animation.Player); ok && p == nil {
		source = nil
	}
	c.seeker, _ = source.(animation.Seekable)
	c.source = source
	c.finished = source != nil && source.Delay() <= 0
}
func wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (c *Controller) Init() tea.Cmd {
	var cmd tea.Cmd
	if c.source != nil {
		cmd = c.submit(c.source)
	}
	return tea.Batch(c.pic.Init(), cmd)
}
func (c *Controller) Status() Status {
	err := c.err
	if err == nil {
		err = c.presentationErr
	}
	presentedPlayer, _ := c.presented.(*animation.Player)
	return Status{Source: c.source, Player: c.player, Paused: c.paused, Finished: c.finished, Busy: c.job != nil, Rate: c.rate, Err: err,
		Presented: c.presented, PresentedPlayer: presentedPlayer, Picture: c.pic.Status()}
}

// submit hands source's image to Picture and records it for promotion.
func (c *Controller) submit(source animation.Source) tea.Cmd {
	if source == nil {
		// Clearing removes the front synchronously; nothing remains presented.
		c.presented, c.submitted = nil, nil
		return c.pic.SetImage(nil)
	}
	img := source.Image()
	cmd := c.pic.SetImageImmutable(img)
	s := c.pic.Status()
	if !validImage(img) {
		// A rejected image reverts Picture to its front (or clears it).
		c.submitted = nil
		if c.clearOnReplace {
			c.presented = nil
		}
	} else {
		// Even if schedule() reported a transient geometry or ID error,
		// Picture retained img as its current source for later recovery.
		c.submitted = &submission{s.Generation, source}
	}
	return cmd
}

// promote records a commit of the latest submission. Later generations that
// re-prepare the same image (resize, mode, visibility) also commit it.
func (c *Controller) promote() {
	if c.submitted != nil && c.pic.Status().Presented >= c.submitted.generation {
		c.presented, c.submitted = c.submitted.source, nil
	}
}
func (c *Controller) View() string { return c.pic.View() }
func (c *Controller) cancel() {
	c.nextDue = time.Time{}
	if c.job != nil {
		c.job.cancel()
	}
}
func (c *Controller) SetPlayer(p *animation.Player) tea.Cmd {
	if p == nil {
		return c.SetSource(nil)
	}
	return c.SetSource(p)
}

// SetSource replaces playback, cancelling/coalescing any off-loop work.
func (c *Controller) SetSource(source animation.Source) tea.Cmd {
	if c.closed {
		return nil
	}
	c.cancel()
	c.pending = nil
	c.restart = false
	c.err = nil
	c.presentationErr = nil
	c.setSource(source)
	return c.submit(c.source)
}

// Restart resets the entire source, including every composite region.
// The current paused state is preserved. Work runs off the event loop.
func (c *Controller) Restart() tea.Cmd {
	if c.closed || c.source == nil {
		return nil
	}
	c.cancel()
	c.pending = nil
	c.restart = true
	c.finished = false
	c.err = nil
	c.presentationErr = nil
	return c.schedule()
}
func (c *Controller) SetSize(cols, rows int) tea.Cmd {
	return c.presentation(c.pic.SetSize(cols, rows))
}
func (c *Controller) SetTerminal(kitty bool, width, height int) tea.Cmd {
	return c.presentation(c.pic.SetTerminal(kitty, width, height))
}
func (c *Controller) SetMode(mode picture.PictureMode) tea.Cmd {
	return c.presentation(c.pic.SetMode(mode))
}

// SetVisible hides or shows the picture. Hiding cancels pending work (including
// any in-flight or queued Seek or Restart) and clears terminal placements while
// retaining the current source position. Seeking or restarting while already
// hidden queues the operation to run when SetVisible(true) is called.
func (c *Controller) SetVisible(visible bool) tea.Cmd {
	c.visible = visible
	if !visible {
		c.cancel()
		c.pending, c.restart = nil, false
	}
	return tea.Batch(c.pic.SetVisible(visible), c.schedule())
}
func (c *Controller) Pause(paused bool) tea.Cmd {
	c.paused = paused
	c.cancel()
	if !paused && c.finished {
		c.restart = true
		c.finished = false
	}
	return c.schedule()
}

// SetRate clamps speed to Gloss's established 0.25x–4x range.
func (c *Controller) SetRate(rate float64) tea.Cmd {
	if rate != rate {
		return nil
	} // NaN is not a playback rate.
	c.rate = max(.25, min(4, rate))
	c.cancel()
	return c.schedule()
}

// Seek selects a seekable source's one-based frame, pauses, and resets loops.
// Players and custom animation.Seekable sources seek; other sources (such as
// composites) pause with ErrNotSeekable in Status.Err. Pending
// seeks coalesce, and a cancelled codec call must return before another starts.
func (c *Controller) Seek(frame int) tea.Cmd {
	if c.closed || c.source == nil {
		return nil
	}
	if c.seeker == nil {
		c.err = ErrNotSeekable
		c.paused = true
		c.cancel()
		return nil
	}
	c.restart = false
	c.paused = true
	c.finished = false
	c.err = nil
	c.presentationErr = nil
	c.cancel()
	c.pending = &frame
	return c.schedule()
}
func (c *Controller) schedule() tea.Cmd {
	if c.closed || !c.visible || c.source == nil || c.job != nil || c.err != nil || c.presentationErr != nil {
		return nil
	}
	target, restart := c.pending, c.restart
	if target == nil && !restart {
		s := c.pic.Status()
		if c.paused || c.finished || s.Busy || s.Presented == 0 || s.Generation != s.Presented {
			return nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{ctx: ctx, cancel: cancel}
	j.setSource(c.source)
	c.job = j
	seeker, source, waiter, now := c.seeker, c.source, c.wait, c.now
	if target == nil && !restart {
		current := now()
		if c.nextDue.IsZero() {
			c.nextDue = current.Add(time.Duration(float64(source.Delay()) / c.rate))
		} else if c.nextDue.Before(current) {
			c.nextDue = current
		}
		j.due = c.nextDue
	}
	return func() tea.Msg {
		out := result{owner: c, job: j, more: true}
		if restart {
			out.source, out.err = j.getSource().Reset(ctx)
		} else if target != nil {
			out.source, out.err = seeker.SeekFrame(ctx, *target)
		} else if waiter(ctx, max(0, j.due.Sub(now()))) {
			out.source, out.more, out.err = j.getSource().Advance(ctx)
		} else {
			out.err = context.Canceled
		}
		return out
	}
}
func (c *Controller) Update(msg tea.Msg) tea.Cmd {
	prev := c.pic.Status()
	cmds := []tea.Cmd{c.pic.Update(msg)}
	c.promote()
	now := c.pic.Status()
	if prev.Busy && now.Generation > prev.Generation && now.Err != nil {
		// An async preparation failure reverted Picture to its front (or cleared it).
		c.submitted = nil
		if c.clearOnReplace {
			c.presented = nil
		}
	}
	if now.Err == nil {
		c.presentationErr = nil
	}
	switch m := msg.(type) {
	case result:
		if m.owner != c || m.job != c.job {
			return tea.Batch(cmds...)
		}
		cancelled := m.job.ctx.Err() != nil
		m.job.cancel()
		c.job = nil
		if !c.closed && !cancelled {
			c.pending = nil
			c.restart = false
			if m.err != nil {
				c.err = m.err
				c.paused = true
			} else if m.source == nil {
				c.finished = !m.more
				if m.more {
					c.err = errors.New("playback: source returned no next position")
					c.paused = true
				}
			} else if m.source != nil {
				unchanged := c.source != nil && sameImage(c.source.Image(), m.source.Image())
				c.setSource(m.source)
				c.finished = !m.more || c.source.Delay() <= 0
				if m.more && !m.job.due.IsZero() {
					// Carry the timeline through composition and transmission.
					// If composition alone overruns, rebase rather than building
					// an unbounded catch-up debt. Never skip frames or overlap jobs.
					base := m.job.due
					delay := time.Duration(float64(c.source.Delay()) / c.rate)
					c.nextDue = base.Add(delay)
					if c.nextDue.Before(c.now()) {
						c.nextDue = c.now()
					}
				}
				switch {
				case !unchanged:
					cmds = append(cmds, c.submit(c.source))
				case c.submitted != nil:
					c.submitted.source = c.source // Same pixels as the pending image.
				case c.presented != nil && sameImage(c.presented.Image(), c.source.Image()):
					c.presented = c.source // Same pixels as the committed image.
				}
			}
		}
	case picture.ErrorMsg:
		if m.Owner == c.pic && m.Generation == c.pic.Status().Generation && errors.Is(c.pic.Status().Err, m.Err) {
			c.presentationErr = m.Err
			c.cancel()
		}
	}
	return tea.Batch(append(cmds, c.schedule())...)
}

// Close cancels work, releases the clip, and returns Picture cleanup commands.
func (c *Controller) Close() tea.Cmd {
	c.stop()
	c.presented = nil
	return c.pic.Close()
}

// Keep cancels playback and pending composition, releases the clip, and keeps
// the last presented frame. Run its command before quitting; the host must
// retain View in its final layout and choose the main screen to leave it there.
// Like Close, Keep is final. A later Close does not delete the kept frame.
func (c *Controller) Keep() tea.Cmd {
	c.stop()
	return c.pic.Keep()
}

func (c *Controller) stop() {
	c.closed = true
	c.cancel()
	c.pending = nil
	c.player = nil
	c.seeker = nil
	c.source = nil
	c.restart = false
	c.job = nil
	c.submitted = nil
}

// SetBackground re-prepares the current frame without seeking or restarting playback.
func (c *Controller) SetBackground(bg color.Color) tea.Cmd {
	return c.presentation(c.pic.SetBackground(bg))
}

// restyleSource updates the current source without cancelling an in-flight
// advance/seek/restart job or resetting nextDue. If the source's image is
// unchanged, it updates the tracked source in place without resubmitting to Picture.
func (c *Controller) restyleSource(source animation.Source) tea.Cmd {
	if c.closed || source == nil {
		return nil
	}
	unchanged := c.source != nil && sameImage(c.source.Image(), source.Image())
	c.setSource(source)
	if c.job != nil {
		c.job.setSource(source)
	}
	if unchanged {
		switch {
		case c.submitted != nil:
			c.submitted.source = c.source
		case c.presented != nil && sameImage(c.presented.Image(), c.source.Image()):
			c.presented = c.source
		}
		return nil
	}
	if c.pic.Status().Err == nil {
		c.presentationErr = nil
	}
	return tea.Batch(c.submit(c.source), c.schedule())
}

func (c *Controller) restyleBackground(bg color.Color, source animation.Source) tea.Cmd {
	if c.closed {
		return nil
	}
	if source == nil {
		return c.SetBackground(bg)
	}
	unchanged := c.source != nil && sameImage(c.source.Image(), source.Image())
	if unchanged {
		return tea.Batch(c.restyleSource(source), c.SetBackground(bg))
	}
	return c.restyleSource(source)
}

func (c *Controller) Fit() picture.FitMode { return c.pic.Fit() }

// SetFit re-prepares the current frame without seeking or restarting playback.
func (c *Controller) SetFit(fit picture.FitMode) tea.Cmd {
	return c.presentation(c.pic.SetFit(fit))
}

func (c *Controller) Anchor() picture.FitAnchor { return c.pic.Anchor() }

// SetAnchor re-prepares the current frame without seeking or restarting playback.
func (c *Controller) SetAnchor(anchor picture.FitAnchor) tea.Cmd {
	return c.presentation(c.pic.SetAnchor(anchor))
}

// SetViewport clips the prepared picture without seeking or restarting playback.
func (c *Controller) SetViewport(v picture.Viewport) { c.pic.SetViewport(v) }

// ResetViewport restores the complete picture view.
func (c *Controller) ResetViewport() { c.pic.ResetViewport() }

// Custom image implementations need not be comparable. Unknown identities are
// conservatively resubmitted; immutable sources may reuse an image to skip work.
func sameImage(a, b image.Image) bool {
	if a == nil || b == nil {
		return false
	}
	if eq, ok := imageview.Same(a, b); ok {
		return eq
	}
	return reflect.ValueOf(a).Comparable() && reflect.ValueOf(b).Comparable() && a == b
}

// RasterGeometry reports the picture's full preparation target before clipping.
func (c *Controller) RasterGeometry() picture.RasterGeometry { return c.pic.RasterGeometry() }

// SetResolution changes Kitty encoding resolution without changing cell layout.
func (c *Controller) SetResolution(factor float64) tea.Cmd {
	return c.presentation(c.pic.SetResolution(factor))
}

// A successful presentation correction must not clear a source/decoder failure.
func (c *Controller) presentation(cmd tea.Cmd) tea.Cmd {
	if c.pic.Status().Err == nil {
		c.presentationErr = nil
	}
	return tea.Batch(cmd, c.schedule())
}
