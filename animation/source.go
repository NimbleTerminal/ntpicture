package animation

import (
	"context"
	"image"
	"time"
)

// Source is an immutable playback position. Advance and Reset return new
// positions; they must never mutate previously published images or state.
// Implementations must be safe for concurrent reads and honor cancellation.
// On error, callers discard the returned position. Delay is the interval before
// another advance; zero denotes a completed/static source. An Advance with
// more=false finishes playback; its image is not submitted by the controller.
// Successful advances with more=true and successful resets return a non-nil
// source (never a typed nil). Immutability replaces the need for a mutable source's Clone operation.
type Source interface {
	Image() image.Image
	Delay() time.Duration
	Advance(context.Context) (Source, bool, error)
	Reset(context.Context) (Source, error)
}

// Seekable is a Source with one frame sequence. SeekFrame composes a one-based
// frame (clamped to [1, Frames]), resets loop counting and returns a new
// position under the same ownership/cancellation contract; a successful seek
// returns a non-nil source. Wrappers that transform a Player's image (crop,
// zoom) can delegate these methods to keep frame seeking.
type Seekable interface {
	Source
	Frame() int
	Frames() int
	SeekFrame(ctx context.Context, frame int) (Source, error)
}

var _ Seekable = (*Player)(nil)

func (p *Player) Advance(ctx context.Context) (Source, bool, error) {
	return p.NextContext(ctx)
}
func (p *Player) Reset(ctx context.Context) (Source, error) {
	return p.RestartContext(ctx)
}
// SeekFrame adapts Seek to Seekable.
func (p *Player) SeekFrame(ctx context.Context, frame int) (Source, error) {
	n, err := p.Seek(ctx, frame)
	if err != nil {
		return nil, err
	}
	return n, nil
}
