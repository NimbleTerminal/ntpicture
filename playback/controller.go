// Package playback schedules animation.Player positions through ntpicture.Picture.
// Hosts own key bindings, layout, visibility decisions, and aggregate budgets.
package playback

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	picture "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"
)

// Wait must honor cancellation. Nil uses a cancellable timer.
type Wait func(context.Context, time.Duration) bool
type Options struct {
	Picture picture.Options
	Wait    Wait
}
type Status struct {
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
	player                            *animation.Player
	wait                              Wait
	job                               *job
	pending                           *int
	paused, finished, visible, closed bool
	rate                              float64
	err                               error
}
type job struct {
	ctx    context.Context
	cancel context.CancelFunc
}
type result struct {
	owner  *Controller
	job    *job
	player *animation.Player
	more   bool
	err    error
}

func New(player *animation.Player, options Options) *Controller {
	if options.Wait == nil {
		options.Wait = wait
	}
	return &Controller{pic: picture.New(options.Picture), player: player, wait: options.Wait, visible: true, rate: 1}
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
	if c.player != nil {
		cmd = c.pic.SetImageImmutable(c.player.Image())
	}
	return tea.Batch(c.pic.Init(), cmd)
}
func (c *Controller) Status() Status {
	return Status{Player: c.player, Paused: c.paused, Finished: c.finished, Busy: c.job != nil, Rate: c.rate, Err: c.err}
}
func (c *Controller) View() string { return c.pic.View() }
func (c *Controller) cancel() {
	if c.job != nil {
		c.job.cancel()
	}
}
func (c *Controller) SetPlayer(p *animation.Player) tea.Cmd {
	if c.closed {
		return nil
	}
	c.cancel()
	c.pending = nil
	c.player = p
	c.finished = false
	c.err = nil
	if p == nil {
		return c.pic.SetImage(nil)
	}
	return c.pic.SetImageImmutable(p.Image())
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
		frame := 1
		c.pending = &frame
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

// Seek selects a one-based frame, pauses, and resets loop counting. Pending
// seeks coalesce, and a cancelled codec call must return before another starts.
func (c *Controller) Seek(frame int) tea.Cmd {
	if c.closed || c.player == nil {
		return nil
	}
	c.paused = true
	c.finished = false
	c.err = nil
	c.cancel()
	c.pending = &frame
	return c.schedule()
}
func (c *Controller) schedule() tea.Cmd {
	if c.closed || !c.visible || c.player == nil || c.job != nil || c.err != nil {
		return nil
	}
	target := c.pending
	if target == nil {
		s := c.pic.Status()
		if c.paused || c.finished || s.Busy || s.Presented == 0 || s.Generation != s.Presented {
			return nil
		}
	}
	c.pending = nil
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{ctx, cancel}
	c.job = j
	p, waiter, delay := c.player, c.wait, time.Duration(float64(c.player.Delay())/c.rate)
	return func() tea.Msg {
		out := result{owner: c, job: j, more: true}
		if target != nil {
			out.player, out.err = p.Seek(ctx, *target)
		} else if waiter(ctx, delay) {
			out.player, out.more, out.err = p.NextContext(ctx)
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
			} else if m.player != nil {
				c.player = m.player
				c.finished = !m.more
				if m.more {
					cmds = append(cmds, c.pic.SetImageImmutable(m.player.Image()))
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
	c.job = nil
}
