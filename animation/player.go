package animation

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"nimbleterminal.dev/ntpicture/internal/imageview"
	"time"
)

// Player is an immutable playback position. Next composes a fresh canvas;
// render/export commands may retain any earlier Image without a data race.
// The decoded palettes and frame rectangles are shared between positions.
type Player struct {
	err            error
	data           *Clip
	canvas         *image.RGBA
	restore        *image.RGBA
	frame, repeats int
}

// Image is a read-only snapshot; later positions never mutate its pixels.
func (p *Player) Image() image.Image { return imageview.New(p.canvas) }

// Err reports a lazy failure from Next or Restart. Prefer context methods to
// receive errors directly. A failed frame is skipped; its disposal still applies.
func (p *Player) Err() error { return p.err }

func (p *Player) Frame() int  { return p.frame + 1 }
func (p *Player) Frames() int { return len(p.data.Frames) }
func (p *Player) Delay() time.Duration {
	return p.data.Frames[p.frame].Delay
}

func (p *Player) Restart() *Player { return newPlayer(p.data) }
func (p *Player) RestartContext(ctx context.Context) (*Player, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n := newPlayerContext(ctx, p.data)
	return n, n.err
}

// Seek composes a one-based frame and resets the loop count. It owns its
// canvases, leaving earlier playback/export positions immutable. Replaying
// disposal uses at most two canvases, not a cache of every composed frame.
func (p *Player) Seek(ctx context.Context, frame int) (*Player, error) {
	frame = max(1, min(frame, p.Frames()))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var n *Player
	if frame >= p.Frame() && p.err == nil {
		copy := *p
		n = &copy
		n.repeats = 0
		n.err = nil
		n.canvas = image.NewRGBA(p.canvas.Bounds())
		draw.Draw(n.canvas, n.canvas.Bounds(), p.canvas, image.Point{}, draw.Src)
		if p.restore != nil {
			n.restore = image.NewRGBA(p.restore.Bounds())
			draw.Draw(n.restore, n.restore.Bounds(), p.restore, image.Point{}, draw.Src)
		}
	} else {
		n = newPlayerContext(ctx, p.data)
		if n.err != nil {
			return nil, n.err
		}
	}
	snapshot := n.restore
	for n.Frame() < frame {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch n.data.Frames[n.frame].Disposal {
		case DisposalBackground:
			draw.Draw(n.canvas, n.data.Frames[n.frame].Rect, image.NewUniform(n.data.Background), image.Point{}, draw.Src)
		case DisposalPrevious:
			if n.restore != nil {
				draw.Draw(n.canvas, n.canvas.Bounds(), n.restore, image.Point{}, draw.Src)
			}
		}
		n.frame++
		n.restore = nil
		if n.data.Frames[n.frame].Disposal == DisposalPrevious {
			if snapshot == nil {
				snapshot = image.NewRGBA(n.canvas.Bounds())
			}
			draw.Draw(snapshot, snapshot.Bounds(), n.canvas, image.Point{}, draw.Src)
			n.restore = snapshot
		}
		if err := n.data.Frames[n.frame].paint(ctx, n.canvas); err != nil {
			return nil, &FrameError{n.frame + 1, err}
		}
	}
	return n, ctx.Err()
}

// Next honors Go's GIF loop semantics: -1 plays once, 0 repeats forever,
// and a positive count repeats that many times after the first pass.
func (p *Player) Next() (*Player, bool) {
	n, more, _ := p.NextContext(context.Background())
	return n, more
}

// NextContext advances once, returning the original position and more=false at
// the loop limit. A lazy error returns a skipped-frame position and FrameError;
// callers may stop or deliberately continue. Cancellation errors should discard
// the returned position. All previously published images remain unchanged.
func (p *Player) NextContext(ctx context.Context) (*Player, bool, error) {
	if err := ctx.Err(); err != nil {
		return p, false, err
	}
	n := *p
	n.err = nil
	n.frame++
	if n.frame == len(p.data.Frames) {
		if p.data.LoopCount < 0 || (p.data.LoopCount > 0 && p.repeats >= p.data.LoopCount) {
			return p, false, nil
		}
		n = *newPlayerContext(ctx, p.data)
		n.repeats = p.repeats + 1
		return &n, true, n.err
	}
	n.canvas = image.NewRGBA(p.canvas.Bounds())
	draw.Draw(n.canvas, n.canvas.Bounds(), p.canvas, image.Point{}, draw.Src)
	switch p.data.Frames[p.frame].Disposal {
	case DisposalBackground:
		draw.Draw(n.canvas, p.data.Frames[p.frame].Rect, image.NewUniform(p.data.Background), image.Point{}, draw.Src)
	case DisposalPrevious:
		if p.restore != nil {
			draw.Draw(n.canvas, n.canvas.Bounds(), p.restore, image.Point{}, draw.Src)
		}
	}
	n.paint(ctx)
	return &n, true, n.err
}

func newPlayer(data *Clip) *Player { return newPlayerContext(context.Background(), data) }
func newPlayerContext(ctx context.Context, data *Clip) *Player {
	p := &Player{data: data, canvas: image.NewRGBA(data.Bounds)}
	draw.Draw(p.canvas, p.canvas.Bounds(), image.NewUniform(data.Background), image.Point{}, draw.Src)
	p.paint(ctx)
	return p
}

func (p *Player) paint(ctx context.Context) {
	p.restore = nil
	if p.data.Frames[p.frame].Disposal == DisposalPrevious {
		p.restore = image.NewRGBA(p.canvas.Bounds())
		draw.Draw(p.restore, p.restore.Bounds(), p.canvas, image.Point{}, draw.Src)
	}
	if err := p.data.Frames[p.frame].paint(ctx, p.canvas); err != nil {
		p.err = &FrameError{p.frame + 1, err}
	}
}

// paint draws the frame at its placement, decoding it first when it is held
// compressed. A frame that fails to decode, or decodes to the wrong size,
// leaves the canvas as it was; its delay and disposal still apply.
func (f ClipFrame) paint(ctx context.Context, canvas draw.Image) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	img := f.Image
	if img == nil {
		var decoded image.Image
		var err error
		if f.LoadContext != nil {
			decoded, err = f.LoadContext(ctx)
		} else {
			decoded, err = f.Load()
		}
		if err != nil {
			return err
		}
		if decoded == nil || decoded.Bounds().Size() != f.Rect.Size() {
			return fmt.Errorf("decoded frame dimensions do not match placement")
		}
		img = decoded
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	draw.Draw(canvas, f.Rect, imageview.Unwrap(img), img.Bounds().Min, f.Blend)
	return ctx.Err()
}
