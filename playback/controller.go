// Package playback schedules immutable animation sources through ntpicture.Picture.
// Hosts own key bindings, layout, visibility decisions, and aggregate budgets.
package playback

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	picture "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"
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

// ErrNotSeekable reports frame seeking on a source without one frame sequence.
var ErrNotSeekable = errors.New("playback: source does not support frame seeking")

type Status struct {
	Source                 animation.Source  // Immutable current source position; may lead presentation.
	Player                 *animation.Player // Immutable current position; presentation may still be pending.
	Paused, Finished, Busy bool
	Rate                   float64
	Err                    error
}

// Controller belongs to one Bubble Tea event loop. Forward every message to
// every controller, including hidden ones, and execute every returned command.
// Message ownership is private; unrelated and stale results are ignored.
type Controller struct {
	pic                               *picture.Picture
	source                            animation.Source
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
}
type job struct {
	ctx    context.Context
	cancel context.CancelFunc
	due    time.Time
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
	c := &Controller{now: time.Now, pic: picture.New(options.Picture), wait: options.Wait, visible: true, rate: 1}
	c.setSource(source)
	return c
}
func (c *Controller) setSource(source animation.Source) {
	c.player, _ = source.(*animation.Player)
	if p, ok := source.(*animation.Player); ok && p == nil {
		source = nil
	}
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
		cmd = c.pic.SetImageImmutable(c.source.Image())
	}
	return tea.Batch(c.pic.Init(), cmd)
}
func (c *Controller) Status() Status {
	return Status{Source: c.source, Player: c.player, Paused: c.paused, Finished: c.finished, Busy: c.job != nil, Rate: c.rate, Err: c.err}
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
	c.setSource(source)
	if c.source == nil {
		return c.pic.SetImage(nil)
	}
	return c.pic.SetImageImmutable(c.source.Image())
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
	return c.schedule()
}
func (c *Controller) SetSize(cols, rows int) tea.Cmd {
	c.err = nil
	return c.pic.SetSize(cols, rows)
}
func (c *Controller) SetTerminal(kitty bool, width, height int) tea.Cmd {
	return c.pic.SetTerminal(kitty, width, height)
}
func (c *Controller) SetMode(mode picture.PictureMode) tea.Cmd { return c.pic.SetMode(mode) }
func (c *Controller) SetVisible(visible bool) tea.Cmd {
	c.visible = visible
	if !visible {
		c.cancel()
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

// Seek selects a standalone player’s one-based frame, pauses, and resets loops.
// Composite/custom sources pause with ErrNotSeekable in Status.Err. Pending
// seeks coalesce, and a cancelled codec call must return before another starts.
func (c *Controller) Seek(frame int) tea.Cmd {
	if c.closed || c.source == nil {
		return nil
	}
	if c.player == nil {
		c.err = ErrNotSeekable
		c.paused = true
		c.cancel()
		return nil
	}
	c.restart = false
	c.paused = true
	c.finished = false
	c.err = nil
	c.cancel()
	c.pending = &frame
	return c.schedule()
}
func (c *Controller) schedule() tea.Cmd {
	if c.closed || !c.visible || c.source == nil || c.job != nil || c.err != nil {
		return nil
	}
	target, restart := c.pending, c.restart
	if target == nil && !restart {
		s := c.pic.Status()
		if c.paused || c.finished || s.Busy || s.Presented == 0 || s.Generation != s.Presented {
			return nil
		}
	}
	c.pending = nil
	c.restart = false
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{ctx: ctx, cancel: cancel}
	c.job = j
	p, source, waiter, now := c.player, c.source, c.wait, c.now
	if target == nil && !restart {
		if c.nextDue.IsZero() {
			c.nextDue = now().Add(time.Duration(float64(source.Delay()) / c.rate))
		}
		j.due = c.nextDue
	}
	return func() tea.Msg {
		out := result{owner: c, job: j, more: true}
		if restart {
			out.source, out.err = source.Reset(ctx)
		} else if target != nil {
			out.source, out.err = p.Seek(ctx, *target)
		} else if waiter(ctx, max(0, j.due.Sub(now()))) {
			out.source, out.more, out.err = source.Advance(ctx)
		} else {
			out.err = context.Canceled
		}
		return out
	}
}
func (c *Controller) Update(msg tea.Msg) tea.Cmd {
	cmds := []tea.Cmd{c.pic.Update(msg)}
	switch m := msg.(type) {
	case result:
		if m.owner != c || m.job != c.job {
			return tea.Batch(cmds...)
		}
		cancelled := m.job.ctx.Err() != nil
		m.job.cancel()
		c.job = nil
		if !c.closed && !cancelled {
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
				c.setSource(m.source)
				c.finished = !m.more || c.source.Delay() <= 0
				if m.more {
					if !m.job.due.IsZero() {
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
					cmds = append(cmds, c.pic.SetImageImmutable(c.source.Image()))
				}
			}
		}
	case picture.ErrorMsg:
		if m.Owner == c.pic && m.Generation == c.pic.Status().Generation {
			c.err = m.Err
			c.cancel()
		}
	}
	return tea.Batch(append(cmds, c.schedule())...)
}

// Close cancels work, releases the clip, and returns Picture cleanup commands.
func (c *Controller) Close() tea.Cmd {
	c.stop()
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
	c.source = nil
	c.restart = false
	c.job = nil
}

// SetViewport clips the prepared picture without seeking or restarting playback.
func (c *Controller) SetViewport(v picture.Viewport) { c.pic.SetViewport(v) }

// ResetViewport restores the complete picture view.
func (c *Controller) ResetViewport() { c.pic.ResetViewport() }
