package playback

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"time"

	xdraw "golang.org/x/image/draw"
	picture "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"
	"nimbleterminal.dev/ntpicture/internal/imageview"
)

type layerGroup struct {
	cells      image.Rectangle
	window     image.Rectangle
	subPx      image.Rectangle
	target     image.Point
	content    image.Rectangle
	resolution float64
	keys       []string
}

type layerOverlay struct {
	cells      image.Rectangle
	window     image.Rectangle
	target     image.Point
	content    image.Rectangle
	resolution float64
	keys       []string
	ctrl       *Controller
	isFollower bool
	followers  []*layerOverlay
}

func (o *layerOverlay) presented() bool {
	return o != nil && o.ctrl != nil && o.ctrl.Status().Presented != nil
}

func (o *layerOverlay) layerStatus() Status {
	st := o.ctrl.Status()
	st.Source = unwrapOverlaySource(st.Source)
	st.Presented = unwrapOverlaySource(st.Presented)
	return st
}

func unwrapOverlaySource(src animation.Source) animation.Source {
	if ls, ok := src.(*linkedWindowSource); ok && ls != nil {
		return ls.main
	}
	return src
}

func compositeFromSource(src animation.Source) *animation.Composite {
	src = unwrapOverlaySource(src)
	switch s := src.(type) {
	case *animation.Composite:
		return s
	case *paddedWindowSource:
		if s != nil {
			return s.comp
		}
	}
	return nil
}

func bgIsTransparentColor(bg color.Color) bool {
	if bg == nil {
		return true
	}
	_, _, _, a := bg.RGBA()
	return a == 0
}

// paddedWindowSource places a content-sized composite window inside a full
// cell-raster frame when a FitContain overlay cell straddles a letterbox bar,
// filling the letterbox area with the configured picture background.
type paddedWindowSource struct {
	comp    *animation.Composite
	target  image.Point
	content image.Rectangle
	bg      color.Color
	img     image.Image
}

func composePaddedWindow(comp *animation.Composite, target image.Point, content image.Rectangle, bg color.Color) image.Image {
	if comp == nil {
		return nil
	}
	full := image.Rectangle{Max: target}
	out := image.NewRGBA(full)
	if !bgIsTransparentColor(bg) {
		draw.Draw(out, full, &image.Uniform{C: bg}, image.Point{}, draw.Src)
	}
	if frame := comp.Image(); frame != nil {
		src := imageview.Unwrap(frame)
		sb := src.Bounds()
		dst := content.Intersect(full)
		if !dst.Empty() && !sb.Empty() {
			if dst.Size() == sb.Size() {
				draw.Draw(out, dst, src, sb.Min, draw.Over)
			} else {
				xdraw.CatmullRom.Scale(out, dst, src, sb, draw.Over, nil)
			}
		}
	}
	return imageview.New(out)
}

func wrapWindowSource(comp *animation.Composite, target image.Point, content image.Rectangle, bg color.Color) animation.Source {
	if comp == nil {
		return nil
	}
	full := image.Rectangle{Max: target}
	if content == full {
		return comp
	}
	return &paddedWindowSource{
		comp:    comp,
		target:  target,
		content: content,
		bg:      bg,
		img:     composePaddedWindow(comp, target, content, bg),
	}
}

func (s *paddedWindowSource) Image() image.Image   { return s.img }
func (s *paddedWindowSource) Delay() time.Duration { return s.comp.Delay() }

func (s *paddedWindowSource) Advance(ctx context.Context) (animation.Source, bool, error) {
	next, more, err := s.comp.Advance(ctx)
	if err != nil {
		return s, false, err
	}
	nextComp, ok := next.(*animation.Composite)
	if !ok || nextComp == nil {
		return s, false, nil
	}
	if sameImage(s.comp.Image(), nextComp.Image()) {
		return &paddedWindowSource{
			comp:    nextComp,
			target:  s.target,
			content: s.content,
			bg:      s.bg,
			img:     s.img,
		}, more, nil
	}
	return wrapWindowSource(nextComp, s.target, s.content, s.bg), more, nil
}

func (s *paddedWindowSource) Reset(ctx context.Context) (animation.Source, error) {
	next, err := s.comp.Reset(ctx)
	if err != nil {
		return s, err
	}
	nextComp, _ := next.(*animation.Composite)
	if nextComp == nil {
		return s, nil
	}
	return wrapWindowSource(nextComp, s.target, s.content, s.bg), nil
}

type linkedFollower struct {
	comp    *animation.Composite
	target  image.Point
	content image.Rectangle
	bg      color.Color
	source  animation.Source
}

// linkedWindowSource ties follower overlay windows to a leader window so that
// disjoint placements of the same composite key(s) share one clock and compose
// off the event loop inside the leader controller's advance job.
type linkedWindowSource struct {
	main      animation.Source
	followers []linkedFollower
}

func (s *linkedWindowSource) Image() image.Image   { return s.main.Image() }
func (s *linkedWindowSource) Delay() time.Duration { return s.main.Delay() }

func (s *linkedWindowSource) Advance(ctx context.Context) (animation.Source, bool, error) {
	next, more, err := s.main.Advance(ctx)
	if err != nil {
		return s, false, err
	}
	nextMainComp := compositeFromSource(next)
	if nextMainComp == nil {
		return s, false, nil
	}
	nextFollowers := make([]linkedFollower, len(s.followers))
	for i, f := range s.followers {
		nf, err := f.comp.WithPositions(ctx, nextMainComp)
		if err != nil {
			return s, false, err
		}
		var nextSrc animation.Source
		if ps, ok := f.source.(*paddedWindowSource); ok && ps != nil && sameImage(f.comp.Image(), nf.Image()) && ps.bg == f.bg {
			nextSrc = &paddedWindowSource{
				comp:    nf,
				target:  f.target,
				content: f.content,
				bg:      f.bg,
				img:     ps.img,
			}
		} else {
			nextSrc = wrapWindowSource(nf, f.target, f.content, f.bg)
		}
		nextFollowers[i] = linkedFollower{
			comp:    nf,
			target:  f.target,
			content: f.content,
			bg:      f.bg,
			source:  nextSrc,
		}
	}
	return &linkedWindowSource{main: next, followers: nextFollowers}, more, nil
}

func (s *linkedWindowSource) Reset(ctx context.Context) (animation.Source, error) {
	next, err := s.main.Reset(ctx)
	if err != nil {
		return s, err
	}
	nextMainComp := compositeFromSource(next)
	if nextMainComp == nil {
		return s, nil
	}
	nextFollowers := make([]linkedFollower, len(s.followers))
	for i, f := range s.followers {
		nf, err := f.comp.WithPositions(ctx, nextMainComp)
		if err != nil {
			return s, err
		}
		nextFollowers[i] = linkedFollower{
			comp:    nf,
			target:  f.target,
			content: f.content,
			bg:      f.bg,
			source:  wrapWindowSource(nf, f.target, f.content, f.bg),
		}
	}
	return &linkedWindowSource{main: next, followers: nextFollowers}, nil
}

func withOverlayBackground(src animation.Source, bg color.Color) (animation.Source, bool) {
	switch s := src.(type) {
	case *paddedWindowSource:
		if s == nil || s.bg == bg {
			return src, false
		}
		return wrapWindowSource(s.comp, s.target, s.content, bg), true
	case *linkedWindowSource:
		if s == nil {
			return nil, false
		}
		changed := false
		main := s.main
		if nm, ok := withOverlayBackground(main, bg); ok {
			main = nm
			changed = true
		}
		followers := make([]linkedFollower, len(s.followers))
		for i, f := range s.followers {
			if f.bg != bg {
				f.bg = bg
				changed = true
			}
			if nf, ok := withOverlayBackground(f.source, bg); ok {
				f.source = nf
				changed = true
			}
			followers[i] = f
		}
		if !changed {
			return src, false
		}
		return &linkedWindowSource{main: main, followers: followers}, true
	}
	return src, false
}

// imageSource wraps an immutable static image with zero delay.
type imageSource struct{ img image.Image }

func (s *imageSource) Image() image.Image                                      { return s.img }
func (s *imageSource) Delay() time.Duration                                    { return 0 }
func (s *imageSource) Advance(context.Context) (animation.Source, bool, error) { return s, false, nil }
func (s *imageSource) Reset(context.Context) (animation.Source, error)         { return s, nil }

// layerSnapshotSource pairs the committed flattened Snapshot image with the
// committed composite playback position for Status().Presented.
// Pointer receivers ensure animation.Source values are comparable with ==.
type layerSnapshotSource struct {
	comp      *animation.Composite
	positions animation.Positions
	img       image.Image
}

func (s *layerSnapshotSource) Image() image.Image { return s.img }
func (s *layerSnapshotSource) Delay() time.Duration {
	if s.comp == nil {
		return 0
	}
	return s.positions.Delay()
}
func (s *layerSnapshotSource) Advance(ctx context.Context) (animation.Source, bool, error) {
	if s.comp == nil {
		return s, false, nil
	}
	curr, err := s.comp.WithPositions(ctx, s.positions)
	if err != nil {
		return s, false, err
	}
	next, more, err := curr.Advance(ctx)
	if err != nil || !more {
		return s, more, err
	}
	return next, true, nil
}
func (s *layerSnapshotSource) Reset(ctx context.Context) (animation.Source, error) {
	if s.comp == nil {
		return s, nil
	}
	next, err := s.comp.Reset(ctx)
	if err != nil {
		return s, err
	}
	return next, nil
}

type layerLayoutJob struct {
	ctx           context.Context
	cancel        context.CancelFunc
	prevComp      *animation.Composite
	prevPositions animation.Positions
	submitBase    bool
}

type layerLayoutResult struct {
	owner      *Layers
	job        *layerLayoutJob
	origComp   *animation.Composite
	comp       *animation.Composite
	submitBase bool
	windows    []*animation.Composite
	sources    []animation.Source
	bg         color.Color
	err        error
}

type layerRestartJob struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type layerRestartResult struct {
	owner *Layers
	job   *layerRestartJob
	comp  *animation.Composite
	err   error
}

func validImageBounds(img image.Image) bool {
	if img == nil {
		return false
	}
	b := img.Bounds()
	return !b.Empty() && b.Dx() > 0 && b.Dy() > 0 && b.Dx() <= picture.MaxImagePixels/b.Dy()
}

func validImage(img image.Image) bool {
	return validImageBounds(img)
}
