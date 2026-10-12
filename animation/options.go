package animation

import (
	"context"
	"fmt"
	"image"
	"io"
)

// Limits may tighten the decoder's hard resource ceilings. Zero fields select
// the defaults, never unlimited operation. Storage excludes working canvases.
type Limits struct {
	MaxInputBytes    int // Default 128 MiB.
	MaxCanvasPixels  int // Default 32 Mi pixels.
	MaxFrames        int // Default 1,000.
	MaxGIFFrameBytes int // Default 64 MiB of paletted pixels.
}

func (l Limits) normalized() (Limits, error) {
	pairs := []struct {
		value   *int
		ceiling int
	}{
		{&l.MaxInputBytes, MaxFileBytes}, {&l.MaxCanvasPixels, MaxPixels},
		{&l.MaxFrames, MaxGIFFrames}, {&l.MaxGIFFrameBytes, MaxGIFPixels},
	}
	for _, p := range pairs {
		if *p.value == 0 {
			*p.value = p.ceiling
		}
		if *p.value < 0 || *p.value > p.ceiling {
			return l, fmt.Errorf("animation limit must be positive and no greater than %d", p.ceiling)
		}
	}
	return l, nil
}

// Options controls decoding. Animate=false returns only the first composited
// image, with no player. Even previews preflight the complete container.
type Options struct {
	Animate bool
	Limits  Limits
}

// PreflightWithLimits checks metadata and storage bounds before pixel allocation.
func PreflightWithLimits(ctx context.Context, data []byte, limits Limits) (Stats, error) {
	l, err := limits.normalized()
	if err != nil {
		return Stats{}, err
	}
	if err := ctx.Err(); err != nil {
		return Stats{}, err
	}
	if len(data) > l.MaxInputBytes {
		return Stats{}, ErrTooLarge
	}
	var s Stats
	if isGIF(data) {
		s, err = preflightGIFContext(ctx, data)
	} else {
		s, err = PreflightWebP(data)
	}
	if err != nil {
		return s, err
	}
	if s.Bounds.Dx() > l.MaxCanvasPixels/s.Bounds.Dy() || s.Frames > l.MaxFrames || (isGIF(data) && s.Bytes > l.MaxGIFFrameBytes) {
		return s, fmt.Errorf("animation exceeds configured limits")
	}
	return s, ctx.Err()
}

// DecodeContext snapshots input so lazy WebP frames remain valid after the caller
// reuses its buffer. Cancellation is checked during codec reads and composition;
// a codec's current computation between reads cannot be forcibly interrupted.
func DecodeContext(ctx context.Context, data []byte, options Options) (image.Image, *Player, error) {
	if _, err := PreflightWithLimits(ctx, data, options.Limits); err != nil {
		return nil, nil, err
	}
	if isGIF(data) {
		return decodeGIF(ctx, data, options.Animate)
	}
	return decodeWebP(ctx, data, options.Animate)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// FrameError identifies a failed lazy frame using the player's one-based index.
type FrameError struct {
	Frame int
	Err   error
}

func (e *FrameError) Error() string { return fmt.Sprintf("animation frame %d: %v", e.Frame, e.Err) }
func (e *FrameError) Unwrap() error { return e.Err }
