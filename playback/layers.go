package playback

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"math"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	xdraw "golang.org/x/image/draw"
	picture "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"
	"nimbleterminal.dev/ntpicture/internal/imageview"
)

// Layer describes one cell-aligned animated overlay window under Kitty graphics.
type Layer struct {
	Cells      image.Rectangle
	Window     image.Rectangle
	Resolution float64
	Status     Status
}

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

func (s imageSource) Image() image.Image                                      { return s.img }
func (s imageSource) Delay() time.Duration                                    { return 0 }
func (s imageSource) Advance(context.Context) (animation.Source, bool, error) { return s, false, nil }
func (s imageSource) Reset(context.Context) (animation.Source, error)         { return s, nil }

// layerSnapshotSource pairs the committed flattened Snapshot image with the
// committed composite playback position for Status().Presented.
type layerSnapshotSource struct {
	comp      *animation.Composite
	positions animation.Positions
	img       image.Image
}

func (s layerSnapshotSource) Image() image.Image { return s.img }
func (s layerSnapshotSource) Delay() time.Duration {
	if s.comp == nil {
		return 0
	}
	return s.positions.Delay()
}
func (s layerSnapshotSource) Advance(ctx context.Context) (animation.Source, bool, error) {
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
func (s layerSnapshotSource) Reset(ctx context.Context) (animation.Source, error) {
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

// Layers renders a composite or still base picture with cell-aligned animated
// region overlays under Kitty graphics, and falls back to animating the
// flattened composite when glyph rendering is active.
//
// Composite bounds are mapped onto the configured cell grid using FitFill;
// supply a composite rendered at RasterGeometry().Pixels for 1:1 pixel
// alignment without resampling. All methods belong to one Bubble Tea event
// loop; forward every message to Update and execute every returned command.
type Layers struct {
	opts             Options
	base             *Controller
	comp             *animation.Composite
	still            image.Image
	positions        animation.Positions
	overlays         []*layerOverlay
	retainedOverlays []*layerOverlay
	retainedLayout   uint64
	retainedCols     int
	retainedRows     int
	mode             picture.PictureMode
	cols, rows       int
	cw, ch           int
	active           bool
	paused           bool
	visible          bool
	closed           bool
	submitted        bool
	explicitTerminal bool
	rate             float64
	viewport         picture.Viewport
	viewportOn       bool
	keptView         string

	layoutGen         uint64
	baseTargetGen     uint64
	basePending       bool
	presentedLayout   uint64
	presentedBase     image.Image
	presentedComp     *animation.Composite
	retainedSnapshot  image.Image
	retainedPresented animation.Source
	cachedSnapshot    image.Image
	snapshotValid     bool

	layoutJob  *layerLayoutJob
	restartJob *layerRestartJob
	err        error
}

// NewLayers creates a layered picture component sharing options.Picture.Session
// and options.Picture.NextID across the base picture and all animated overlays.
// The base picture uses FitFill so composite coordinates map onto the cell grid.
func NewLayers(composite *animation.Composite, options Options) *Layers {
	if options.Wait == nil {
		options.Wait = wait
	}
	if options.Picture.NextID == nil {
		options.Picture.NextID = picture.NextImageID
	}
	if options.Picture.Render.Background == nil {
		options.Picture.Render.Background = color.RGBA64Model.Convert(color.Transparent)
	} else {
		options.Picture.Render.Background = color.RGBA64Model.Convert(options.Picture.Render.Background)
	}
	options.Picture.Render.Fit = picture.FitFill
	cw, ch := options.Picture.Render.CellPixelWidth, options.Picture.Render.CellPixelHeight
	var mode picture.PictureMode
	if options.Picture.Session != nil {
		state := options.Picture.Session.Snapshot()
		cw, ch = state.CellPixelWidth, state.CellPixelHeight
		if state.Kitty == picture.KittyCapabilitySupported {
			mode = picture.PictureKitty
		}
	}
	if cw <= 0 {
		cw = 8
	}
	if ch <= 0 {
		ch = 16
	}
	baseOpts := options
	// The base picture is replaced only on viewport/document changes under Kitty
	// graphics; using a fresh ID retains the displayed base until its replacement commits.
	baseOpts.FreshIDs = true
	base := NewSource(nil, baseOpts)
	if mode == picture.PictureKitty {
		_ = base.SetTerminal(true, cw, ch)
	}
	return &Layers{
		opts:      options,
		base:      base,
		comp:      composite,
		positions: composite.Positions(),
		mode:      mode,
		cw:        cw,
		ch:        ch,
		active:    true,
		visible:   true,
		rate:      1,
	}
}

func (l *Layers) backgroundColor() color.Color {
	if l.opts.Picture.Render.Background == nil {
		return color.RGBA64Model.Convert(color.Transparent)
	}
	return color.RGBA64Model.Convert(l.opts.Picture.Render.Background)
}

func (l *Layers) basePaused() bool {
	return l.mode == picture.PictureKitty || l.paused || !l.active
}

// Init starts terminal session discovery (unless HostGeometry or SetTerminal
// already configured geometry) and submits any initial composite or image.
func (l *Layers) Init() tea.Cmd {
	if l.closed {
		return nil
	}
	var initCmd tea.Cmd
	if !l.opts.Picture.HostGeometry && !l.explicitTerminal {
		initCmd = l.base.pic.Init()
	}
	var submitCmd tea.Cmd
	if !l.submitted && (l.comp != nil || l.still != nil) {
		submitCmd = l.applySource(true)
	}
	return tea.Batch(initCmd, submitCmd)
}

// Status reports the combined playback and presentation status. Status.Presented
// reflects the same committed image that Snapshot exports.
func (l *Layers) Status() Status {
	baseStatus := l.base.Status()
	err := l.err
	if err == nil {
		err = baseStatus.Err
	}
	busy := l.layoutJob != nil || l.restartJob != nil || baseStatus.Busy || baseStatus.Picture.Busy
	finished := true
	if l.comp == nil {
		finished = l.still != nil
	} else if l.mode == picture.PictureGlyph {
		finished = baseStatus.Finished
	} else if len(l.overlays) > 0 {
		for _, o := range l.overlays {
			st := o.ctrl.Status()
			if err == nil && st.Err != nil {
				err = st.Err
			}
			if st.Busy || st.Picture.Busy {
				busy = true
			}
			if !o.isFollower && !st.Finished {
				finished = false
			}
		}
	} else {
		finished = l.comp.Delay() <= 0
	}
	var source animation.Source
	if !l.closed {
		if l.comp != nil {
			source = l.comp
			if l.mode == picture.PictureGlyph {
				if comp, ok := baseStatus.Source.(*animation.Composite); ok && comp != nil {
					source = comp
				}
			}
		} else if l.still != nil {
			source = baseStatus.Source
		}
	}
	return Status{
		Source:    source,
		Paused:    l.paused,
		Finished:  finished,
		Busy:      busy,
		Rate:      l.rate,
		Err:       err,
		Presented: l.presentedSource(),
		Picture:   baseStatus.Picture,
	}
}

func (l *Layers) presentedSource() animation.Source {
	if l.closed {
		return l.retainedPresented
	}
	if !l.visible {
		return nil
	}
	if l.presentedLayout != l.layoutGen || l.layoutGen == 0 || l.basePending {
		if l.retainedPresented != nil {
			return l.retainedPresented
		}
		if l.retainedSnapshot != nil {
			return imageSource{img: l.retainedSnapshot}
		}
		return nil
	}
	snap := l.Snapshot()
	if snap == nil {
		return nil
	}
	if l.mode == picture.PictureGlyph {
		if basePres := l.base.Status().Presented; basePres != nil {
			return basePres
		}
	}
	if l.presentedComp != nil {
		return layerSnapshotSource{
			comp:      l.presentedComp,
			positions: l.presentedPositions(),
			img:       snap,
		}
	}
	return imageSource{img: snap}
}

func (l *Layers) presentedPositions() animation.Positions {
	if l.presentedComp == nil {
		return animation.Positions{}
	}
	pos := l.presentedComp.Positions()
	var activePresented []*layerOverlay
	if l.retainedLayout == l.presentedLayout {
		for _, o := range l.retainedOverlays {
			if o.presented() {
				activePresented = append(activePresented, o)
			}
		}
	}
	for _, o := range l.overlays {
		if o.presented() {
			activePresented = append(activePresented, o)
		}
	}
	for _, o := range activePresented {
		if !o.isFollower {
			continue
		}
		if comp := compositeFromSource(o.ctrl.Status().Presented); comp != nil {
			pos = pos.WithPositions(comp)
		}
	}
	for _, o := range activePresented {
		if o.isFollower {
			continue
		}
		if comp := compositeFromSource(o.ctrl.Status().Presented); comp != nil {
			pos = pos.WithPositions(comp)
		}
	}
	return pos
}

// Layers returns a snapshot of the active Kitty overlay layers in cell-group order.
// Under glyph rendering or while playback activation is suspended, it returns nil.
func (l *Layers) Layers() []Layer {
	if !l.active || l.mode != picture.PictureKitty || len(l.overlays) == 0 {
		return nil
	}
	out := make([]Layer, len(l.overlays))
	for i, o := range l.overlays {
		out[i] = Layer{
			Cells:      o.cells,
			Window:     o.window,
			Resolution: o.resolution,
			Status:     o.layerStatus(),
		}
	}
	return out
}

// Positions returns an immutable snapshot of all tracked region playback positions
// without copying or recomposing pixel buffers. Pass it to animation.NewComposite
// or animation.NewCompositeImmutable when laying out a new viewport.
func (l *Layers) Positions() animation.Positions {
	return l.positions
}

// Mode reports the current rendering mode (PictureGlyph or PictureKitty).
func (l *Layers) Mode() picture.PictureMode { return l.mode }

// CellPixelSize reports the current terminal cell dimensions in pixels.
func (l *Layers) CellPixelSize() (int, int) { return l.cw, l.ch }

// RasterGeometry reports the base picture's full preparation target before clipping.
func (l *Layers) RasterGeometry() picture.RasterGeometry { return l.base.RasterGeometry() }

// Active reports whether animated playback/overlay activation is enabled.
func (l *Layers) Active() bool { return l.active }

// SetActive enables or suspends animated overlay creation and advancement.
// Hosts can set Active(false) during scroll or resize bursts so presented
// overlays freeze in place while new composites prepare only the base picture,
// then call SetActive(true) once the viewport settles to construct and start
// the Kitty overlays without re-preparing the base.
func (l *Layers) SetActive(active bool) tea.Cmd {
	if l.closed || l.active == active {
		return nil
	}
	l.active = active
	if l.mode == picture.PictureGlyph {
		return l.base.Pause(l.basePaused())
	}
	if !l.active {
		if l.layoutJob != nil && !l.basePending {
			l.cancelLayout()
		}
		return l.retireOverlays()
	}
	if !l.submitted || l.comp == nil || !l.visible || l.cols <= 0 || l.rows <= 0 {
		return nil
	}
	if l.layoutJob != nil && l.basePending {
		// Replan overlays into the pending base layout job.
		return l.startLayout(l.comp, true)
	}
	return l.startLayout(l.comp, false)
}

// SetComposite replaces the current composite, carrying matching region playback
// positions from previous overlays off the event loop. Pass nil to clear.
func (l *Layers) SetComposite(composite *animation.Composite) tea.Cmd {
	if l.closed {
		return nil
	}
	prevComp := l.comp
	if l.layoutJob != nil {
		prevComp = l.layoutJob.prevComp
	}
	l.cancelRestart()
	l.cancelLayout()
	l.err = nil
	l.still = nil
	if composite == nil {
		l.comp = nil
		l.positions = animation.Positions{}
		l.clearSnapshots()
		return tea.Batch(l.closeAllOverlays(), l.base.SetSource(nil))
	}
	l.captureRetainedSnapshot()
	retireCmd := l.retireOverlays()
	prevPositions := l.positions
	if len(l.positions.Keys()) == 0 {
		l.positions = composite.Positions()
	}
	l.comp = composite
	cmd := l.applySource(false)
	if l.layoutJob != nil {
		l.layoutJob.prevComp = prevComp
		l.layoutJob.prevPositions = prevPositions
	}
	return tea.Batch(retireCmd, cmd)
}

func validImageBounds(img image.Image) bool {
	if img == nil {
		return false
	}
	b := img.Bounds()
	return !b.Empty() && b.Dx() > 0 && b.Dy() > 0 && b.Dx() <= picture.MaxImagePixels/b.Dy()
}

// SetImage copies img and displays it as a still base picture with no animated overlays.
// Pass nil to clear. Invalid images report ErrorMsg and leave existing composite/positions intact.
func (l *Layers) SetImage(img image.Image) tea.Cmd {
	if l.closed {
		return nil
	}
	if img == nil {
		return l.SetImageImmutable(nil)
	}
	if !validImageBounds(img) {
		return l.SetImageImmutable(img)
	}
	b := img.Bounds()
	cp := image.NewRGBA(b)
	draw.Draw(cp, b, imageview.Unwrap(img), b.Min, draw.Src)
	return l.SetImageImmutable(imageview.New(cp))
}

// SetImageImmutable displays an immutable still image with no animated overlays.
// The caller must not mutate img while Layers or an in-flight command retains it.
func (l *Layers) SetImageImmutable(img image.Image) tea.Cmd {
	if l.closed {
		return nil
	}
	if img != nil && !validImageBounds(img) {
		var clearCmd tea.Cmd
		if l.opts.Picture.ClearOnReplace {
			l.cancelRestart()
			l.cancelLayout()
			l.comp = nil
			l.still = nil
			l.positions = animation.Positions{}
			l.clearSnapshots()
			clearCmd = l.closeAllOverlays()
		}
		errCmd := l.base.pic.SetImageImmutable(img)
		l.err = l.base.Status().Picture.Err
		return tea.Batch(clearCmd, errCmd)
	}
	l.cancelRestart()
	l.cancelLayout()
	l.err = nil
	l.comp = nil
	l.positions = animation.Positions{}
	if img == nil {
		l.still = nil
		l.clearSnapshots()
		return tea.Batch(l.closeAllOverlays(), l.base.SetSource(nil))
	}
	l.captureRetainedSnapshot()
	retireCmd := l.retireOverlays()
	l.still = imageview.New(img)
	return tea.Batch(retireCmd, l.applySource(false))
}

// SetSize sets the full cell dimensions of the layered picture.
func (l *Layers) SetSize(cols, rows int) tea.Cmd {
	if l.closed {
		return nil
	}
	cols, rows = max(0, cols), max(0, rows)
	if cols == l.cols && rows == l.rows {
		return nil
	}
	// Capture before base.SetSize mutates base generation, but restore if rejected.
	prevRetained, prevRetainedPres := l.retainedSnapshot, l.retainedPresented
	if l.submitted {
		l.captureRetainedSnapshot()
	}
	baseCmd := l.base.SetSize(cols, rows)
	if l.base.Status().Picture.Err != nil {
		l.retainedSnapshot, l.retainedPresented = prevRetained, prevRetainedPres
		return baseCmd
	}
	l.err = nil
	l.cols, l.rows = cols, rows
	if !l.submitted {
		return baseCmd
	}
	if cols == 0 || rows == 0 {
		l.cancelLayout()
		l.layoutGen++
		l.baseTargetGen = l.base.Status().Picture.Generation
		l.basePending = false
		return tea.Batch(baseCmd, l.retireOverlays())
	}
	return tea.Batch(baseCmd, l.syncGeometryAndMode(false))
}

// SetTerminal applies an explicit host graphics decision and cell pixel geometry
// without probing or consulting global state.
func (l *Layers) SetTerminal(kitty bool, cellWidth, cellHeight int) tea.Cmd {
	if l.closed {
		return nil
	}
	mode := picture.PictureGlyph
	if kitty {
		mode = picture.PictureKitty
	}
	modeChanged := mode != l.mode
	geomChanged := cellWidth != l.cw || cellHeight != l.ch
	prevRetained, prevRetainedPres := l.retainedSnapshot, l.retainedPresented
	if l.submitted && (modeChanged || geomChanged) {
		l.captureRetainedSnapshot()
	}
	baseCmd := l.base.SetTerminal(kitty, cellWidth, cellHeight)
	if l.base.Status().Picture.Err != nil {
		l.retainedSnapshot, l.retainedPresented = prevRetained, prevRetainedPres
		return baseCmd
	}
	l.err = nil
	l.explicitTerminal = true
	l.mode = mode
	l.cw, l.ch = cellWidth, cellHeight
	pauseCmd := l.base.Pause(l.basePaused())
	if !l.submitted {
		return tea.Batch(baseCmd, pauseCmd)
	}
	if modeChanged || geomChanged {
		return tea.Batch(baseCmd, pauseCmd, l.syncGeometryAndMode(modeChanged))
	}
	return tea.Batch(baseCmd, pauseCmd)
}

// SetMode switches between PictureGlyph and PictureKitty using the configured session.
func (l *Layers) SetMode(mode picture.PictureMode) tea.Cmd {
	if l.closed || mode == l.mode {
		return nil
	}
	prevRetained, prevRetainedPres := l.retainedSnapshot, l.retainedPresented
	if l.submitted {
		l.captureRetainedSnapshot()
	}
	baseCmd := l.base.SetMode(mode)
	if l.base.Status().Picture.Err != nil || l.base.pic.Mode() != mode {
		l.retainedSnapshot, l.retainedPresented = prevRetained, prevRetainedPres
		return baseCmd
	}
	l.err = nil
	l.mode = mode
	l.cw, l.ch = l.base.pic.CellPixelSize()
	pauseCmd := l.base.Pause(l.basePaused())
	if !l.submitted {
		return tea.Batch(baseCmd, pauseCmd)
	}
	return tea.Batch(baseCmd, pauseCmd, l.syncGeometryAndMode(true))
}

// SetVisible hides or shows all layers. Hiding cancels pending work and clears
// terminal placements while retaining the composite and its playback positions.
func (l *Layers) SetVisible(visible bool) tea.Cmd {
	if l.closed || l.visible == visible {
		return nil
	}
	l.visible = visible
	if !visible {
		l.cancelRestart()
		l.cancelLayout()
		l.retainedSnapshot = nil
		l.retainedPresented = nil
		l.cachedSnapshot = nil
		l.snapshotValid = false
		l.harvestOverlayPositions()
		return tea.Batch(l.closeAllOverlays(), l.base.SetVisible(false))
	}
	baseCmd := l.base.SetVisible(true)
	if !l.submitted {
		return baseCmd
	}
	return tea.Batch(baseCmd, l.applySource(false))
}

// Pause pauses or resumes frame advancement across the composite and all overlays
// without removing presented Kitty overlay placements.
func (l *Layers) Pause(paused bool) tea.Cmd {
	if l.closed {
		return nil
	}
	l.paused = paused
	cmds := []tea.Cmd{l.base.Pause(l.basePaused())}
	for _, o := range l.overlays {
		if o.isFollower {
			cmds = append(cmds, o.ctrl.Pause(true))
		} else {
			cmds = append(cmds, o.ctrl.Pause(paused))
		}
	}
	return tea.Batch(cmds...)
}

// SetRate clamps playback speed to 0.25x–4x across all layers.
func (l *Layers) SetRate(rate float64) tea.Cmd {
	if l.closed || rate != rate {
		return nil
	}
	l.rate = max(0.25, min(4, rate))
	cmds := []tea.Cmd{l.base.SetRate(l.rate)}
	for _, o := range l.overlays {
		cmds = append(cmds, o.ctrl.SetRate(l.rate))
	}
	return tea.Batch(cmds...)
}

// Restart resets all regions in the composite to their first frame off the
// event loop while preserving the current paused state.
func (l *Layers) Restart() tea.Cmd {
	if l.closed || l.comp == nil {
		return nil
	}
	l.cancelRestart()
	l.err = nil
	ctx, cancel := context.WithCancel(context.Background())
	job := &layerRestartJob{ctx: ctx, cancel: cancel}
	l.restartJob = job
	comp := l.comp
	return func() tea.Msg {
		src, err := comp.Reset(ctx)
		resetComp, _ := src.(*animation.Composite)
		return layerRestartResult{owner: l, job: job, comp: resetComp, err: err}
	}
}

// SetBackground changes the background color on the base picture and all overlays.
func (l *Layers) SetBackground(bg color.Color) tea.Cmd {
	if l.closed {
		return nil
	}
	if bg == nil {
		bg = color.Transparent
	}
	bg = color.RGBA64Model.Convert(bg)
	l.opts.Picture.Render.Background = bg
	cmds := []tea.Cmd{l.base.SetBackground(bg)}
	for _, o := range l.overlays {
		if o.isFollower {
			continue
		}
		if updated, ok := withOverlayBackground(o.ctrl.Status().Source, bg); ok {
			cmds = append(cmds, o.ctrl.restyleBackground(bg, updated))
			if ls, ok := updated.(*linkedWindowSource); ok {
				for i, f := range o.followers {
					var fSrc animation.Source
					if i < len(ls.followers) {
						fSrc = ls.followers[i].source
					}
					cmds = append(cmds, f.ctrl.restyleBackground(bg, fSrc))
				}
			} else {
				for _, f := range o.followers {
					if f.content == (image.Rectangle{Max: f.target}) {
						cmds = append(cmds, f.ctrl.SetBackground(bg))
					}
				}
			}
			continue
		}
		if o.content == (image.Rectangle{Max: o.target}) {
			cmds = append(cmds, o.ctrl.SetBackground(bg))
		}
		for _, f := range o.followers {
			if f.content == (image.Rectangle{Max: f.target}) {
				cmds = append(cmds, f.ctrl.SetBackground(bg))
			}
		}
	}
	return tea.Batch(cmds...)
}

// Fit reports the base picture's fit mode (defaults to FitFill).
func (l *Layers) Fit() picture.FitMode { return l.base.Fit() }

// SetFit changes the base picture's fit mode and replans overlays for the new mapping.
func (l *Layers) SetFit(fit picture.FitMode) tea.Cmd {
	if l.closed {
		return nil
	}
	prev := l.base.Fit()
	prevRetained, prevRetainedPres := l.retainedSnapshot, l.retainedPresented
	if l.submitted {
		l.captureRetainedSnapshot()
	}
	baseCmd := l.base.SetFit(fit)
	l.opts.Picture.Render.Fit = l.base.Fit()
	if !l.submitted || l.base.Fit() == prev {
		l.retainedSnapshot, l.retainedPresented = prevRetained, prevRetainedPres
		return baseCmd
	}
	return tea.Batch(baseCmd, l.syncGeometryAndMode(false))
}

// Anchor reports the base picture's crop anchor.
func (l *Layers) Anchor() picture.FitAnchor { return l.base.Anchor() }

// SetAnchor changes the base picture's crop anchor and replans overlays for the new mapping.
func (l *Layers) SetAnchor(anchor picture.FitAnchor) tea.Cmd {
	if l.closed {
		return nil
	}
	prev := l.base.Anchor()
	prevRetained, prevRetainedPres := l.retainedSnapshot, l.retainedPresented
	if l.submitted {
		l.captureRetainedSnapshot()
	}
	baseCmd := l.base.SetAnchor(anchor)
	l.opts.Picture.Render.Anchor = l.base.Anchor()
	if !l.submitted || l.base.Anchor() == prev {
		l.retainedSnapshot, l.retainedPresented = prevRetained, prevRetainedPres
		return baseCmd
	}
	return tea.Batch(baseCmd, l.syncGeometryAndMode(false))
}

// SetViewport clips the composed cell view without rescaling or retransmitting pixels.
func (l *Layers) SetViewport(v picture.Viewport) {
	if !l.closed {
		l.viewport, l.viewportOn = v, true
	}
}

// ResetViewport restores the full cell view.
func (l *Layers) ResetViewport() {
	if !l.closed {
		l.viewportOn = false
	}
}

// Update routes messages to the base picture and overlays, tracks region
// playback positions, and promotes committed presentations.
func (l *Layers) Update(msg tea.Msg) tea.Cmd {
	if l.closed {
		var cmds []tea.Cmd
		cmds = append(cmds, l.base.Update(msg))
		for _, o := range l.overlays {
			cmds = append(cmds, o.ctrl.Update(msg))
		}
		for _, o := range l.retainedOverlays {
			cmds = append(cmds, o.ctrl.Update(msg))
		}
		return tea.Batch(cmds...)
	}
	var cmds []tea.Cmd
	prevCW, prevCH := l.base.pic.CellPixelSize()
	prevBaseSource := l.base.Status().Source
	prevBasePresented := l.base.Status().Presented

	// If a terminal event may change cell pixel size on the base picture, preserve
	// the last committed snapshot before base.Update mutates base state.
	var preEventSnap image.Image
	var preEventPres animation.Source
	if !l.opts.Picture.HostGeometry && l.submitted && !l.opts.Picture.ClearOnReplace {
		preEventSnap = l.Snapshot()
		preEventPres = l.presentedSource()
	}

	cmds = append(cmds, l.base.Update(msg))

	if afterBase := l.base.Status(); afterBase.Source != prevBaseSource {
		if comp, ok := afterBase.Source.(*animation.Composite); ok && comp != nil {
			l.comp = comp
			l.positions = l.positions.WithPositions(comp)
		}
	}
	if l.base.Status().Presented != prevBasePresented {
		l.snapshotValid = false
		if comp, ok := l.base.Status().Presented.(*animation.Composite); ok && comp != nil && l.presentedLayout == l.layoutGen && !l.basePending {
			l.presentedComp = comp
		}
	}

	for _, o := range l.overlays {
		beforeSource := o.ctrl.Status().Source
		beforePresented := o.ctrl.Status().Presented
		oMsg := msg
		if m, ok := msg.(result); ok && m.owner == o.ctrl && m.source != nil {
			if updated, ok := withOverlayBackground(m.source, l.backgroundColor()); ok {
				m.source = updated
				oMsg = m
			}
		}
		cmds = append(cmds, o.ctrl.Update(oMsg))
		after := o.ctrl.Status()
		if after.Source != beforeSource {
			if comp := compositeFromSource(after.Source); comp != nil {
				l.positions = l.positions.WithPositions(comp)
			}
			if src, ok := after.Source.(*linkedWindowSource); ok && src != nil {
				for i, f := range o.followers {
					if i < len(src.followers) && src.followers[i].source != nil && src.followers[i].source != f.ctrl.Status().Source {
						cmds = append(cmds, f.ctrl.restyleSource(src.followers[i].source))
					}
				}
			}
		}
		if after.Presented != beforePresented {
			l.snapshotValid = false
		}
	}
	for _, o := range l.retainedOverlays {
		cmds = append(cmds, o.ctrl.Update(msg))
	}

	if nw, nh := l.base.pic.CellPixelSize(); (nw != prevCW || nh != prevCH) && nw > 0 && nh > 0 {
		if preEventSnap != nil {
			l.retainedSnapshot = preEventSnap
			l.retainedPresented = preEventPres
		}
		l.cw, l.ch = nw, nh
		if l.submitted {
			cmds = append(cmds, l.syncGeometryAndMode(false))
		}
	}

	switch m := msg.(type) {
	case layerLayoutResult:
		if m.owner == l && m.job == l.layoutJob {
			cancelled := m.job.ctx.Err() != nil
			m.job.cancel()
			l.layoutJob = nil
			if !cancelled {
				if m.err != nil {
					l.err = m.err
					l.comp = m.job.prevComp
					l.positions = m.job.prevPositions
					l.basePending = false
					cmds = append(cmds, l.closeActiveOverlays(), l.Pause(true))
				} else if m.comp != nil {
					l.comp = m.comp
					l.positions = m.job.prevPositions.WithPositions(m.comp)
					if m.submitBase || (l.presentedLayout != l.layoutGen && m.comp != m.origComp) {
						cmds = append(cmds, l.base.Pause(l.basePaused()))
						cmds = append(cmds, l.base.SetSource(l.comp))
						l.baseTargetGen = l.base.Status().Picture.Generation
					} else if l.presentedLayout == l.layoutGen {
						l.presentedComp = l.comp
					}
					l.basePending = false
					cmds = append(cmds, l.submitOverlayWindows(m.windows, m.sources, m.bg))
				}
			}
		}
	case layerRestartResult:
		if m.owner == l && m.job == l.restartJob {
			cancelled := m.job.ctx.Err() != nil
			m.job.cancel()
			l.restartJob = nil
			if !cancelled {
				if m.err != nil {
					l.err = m.err
					cmds = append(cmds, l.Pause(true))
				} else if m.comp != nil {
					l.captureRetainedSnapshot()
					cmds = append(cmds, l.retireOverlaysWithoutHarvest())
					l.comp = m.comp
					l.positions = m.comp.Positions()
					cmds = append(cmds, l.applySource(false))
				}
			}
		}
	}

	cmds = append(cmds, l.promoteBase())
	return tea.Batch(cmds...)
}

// View returns the composed terminal cell grid. Under Kitty graphics, each
// presented overlay's placeholder rows are spliced into the base picture's grid
// at its cell window; unpresented overlays retain the previously presented cells.
func (l *Layers) View() string {
	if l.closed {
		return l.keptView
	}
	if !l.visible || l.cols <= 0 || l.rows <= 0 {
		return ""
	}
	grid := l.base.View()
	if grid == "" {
		return ""
	}
	if l.mode == picture.PictureKitty && l.layoutGen > 0 {
		grid = l.spliceOverlays(grid)
	}
	if l.viewportOn {
		return l.clipViewport(grid)
	}
	return grid
}

// Snapshot returns an immutable flattened image matching the frames actually
// committed to output, suitable for export.
//
// While a base replacement is pending (submitted but not yet committed via
// PresentedMsg), Snapshot returns the last committed snapshot from the previous
// layout rather than silently exporting the unpresented replacement. Unpresented
// overlays leave the committed pixels intact. Returns nil before any frame is
// presented, while hidden, and after clearing or Close; Keep retains the last
// presented snapshot.
func (l *Layers) Snapshot() image.Image {
	if l.closed {
		return l.retainedSnapshot
	}
	if !l.visible {
		return nil
	}
	if l.presentedLayout != l.layoutGen || l.layoutGen == 0 || l.basePending {
		return l.retainedSnapshot
	}
	if l.snapshotValid && l.cachedSnapshot != nil {
		return l.cachedSnapshot
	}
	snap := l.composePresentedSnapshot()
	l.cachedSnapshot = snap
	l.snapshotValid = snap != nil
	return snap
}

// Close cancels all pending work across the base picture and all overlays,
// releases retained images, and returns terminal placement cleanup commands.
// The host-owned TerminalSession is never closed.
func (l *Layers) Close() tea.Cmd {
	if l.closed {
		return nil
	}
	l.closed = true
	l.cancelRestart()
	l.cancelLayout()
	cmds := []tea.Cmd{l.closeAllOverlays(), l.base.Close()}
	l.comp = nil
	l.still = nil
	l.positions = animation.Positions{}
	l.clearSnapshots()
	return tea.Batch(cmds...)
}

// Keep permanently stops playback and pending preparation while preserving the
// last presented View, Snapshot, and terminal placements across the base picture
// and all presented overlays. Unpresented overlays are closed. Like Picture.Keep,
// Keep is final and a subsequent Close is a no-op. The host-owned TerminalSession
// is not closed.
func (l *Layers) Keep() tea.Cmd {
	if l.closed {
		return nil
	}
	l.cancelRestart()
	l.cancelLayout()
	l.keptView = l.View()
	l.retainedSnapshot = l.Snapshot()
	l.retainedPresented = l.presentedSource()
	l.closed = true
	cmds := []tea.Cmd{l.base.Keep()}
	if l.presentedLayout == l.layoutGen && !l.basePending && l.layoutGen > 0 {
		for _, o := range l.retainedOverlays {
			if l.retainedLayout == l.presentedLayout && o.presented() {
				cmds = append(cmds, o.ctrl.Keep())
			} else {
				cmds = append(cmds, o.ctrl.Close())
			}
		}
		l.retainedOverlays = nil
		for _, o := range l.overlays {
			if o.presented() {
				cmds = append(cmds, o.ctrl.Keep())
			} else {
				cmds = append(cmds, o.ctrl.Close())
			}
		}
		l.overlays = nil
	} else {
		for _, o := range l.retainedOverlays {
			if l.retainedLayout == l.presentedLayout && o.presented() {
				cmds = append(cmds, o.ctrl.Keep())
			} else {
				cmds = append(cmds, o.ctrl.Close())
			}
		}
		l.retainedOverlays = nil
		cmds = append(cmds, l.closeActiveOverlays())
	}
	l.comp = nil
	l.still = nil
	l.cachedSnapshot = nil
	l.snapshotValid = false
	return tea.Batch(cmds...)
}

func (l *Layers) cancelRestart() {
	if l.restartJob != nil {
		l.restartJob.cancel()
		l.restartJob = nil
	}
}

func (l *Layers) cancelLayout() {
	if l.layoutJob != nil {
		l.layoutJob.cancel()
		l.layoutJob = nil
	}
}

func (l *Layers) clearSnapshots() {
	l.presentedBase = nil
	l.presentedComp = nil
	l.retainedSnapshot = nil
	l.retainedPresented = nil
	l.cachedSnapshot = nil
	l.snapshotValid = false
	l.presentedLayout = 0
	l.basePending = false
}

func (l *Layers) captureRetainedSnapshot() {
	if l.opts.Picture.ClearOnReplace {
		l.retainedSnapshot = nil
		l.retainedPresented = nil
		return
	}
	if snap := l.Snapshot(); snap != nil {
		l.retainedSnapshot = snap
		l.retainedPresented = l.presentedSource()
	}
}

func (l *Layers) applySource(newLayout bool) tea.Cmd {
	if newLayout {
		l.captureRetainedSnapshot()
	}
	l.submitted = true
	var cmds []tea.Cmd
	cmds = append(cmds, l.retireOverlays())

	if l.comp == nil && l.still == nil {
		l.cancelLayout()
		l.clearSnapshots()
		cmds = append(cmds, l.closeRetainedOverlays(), l.base.SetSource(nil))
		return tea.Batch(cmds...)
	}

	l.layoutGen++
	l.cachedSnapshot = nil
	l.snapshotValid = false
	cmds = append(cmds, l.base.Pause(l.basePaused()))

	if l.comp != nil {
		cmds = append(cmds, l.startLayout(l.comp, true))
	} else {
		l.cancelLayout()
		l.basePending = false
		cmds = append(cmds, l.base.SetSource(imageSource{img: l.still}))
		l.baseTargetGen = l.base.Status().Picture.Generation
		cmds = append(cmds, l.promoteBase())
	}
	return tea.Batch(cmds...)
}

func (l *Layers) syncGeometryAndMode(modeChanged bool) tea.Cmd {
	var cmds []tea.Cmd
	cmds = append(cmds, l.retireOverlays())
	l.layoutGen++
	l.baseTargetGen = l.base.Status().Picture.Generation
	l.cachedSnapshot = nil
	l.snapshotValid = false
	if l.comp != nil {
		cmds = append(cmds, l.startLayout(l.comp, false))
	} else {
		l.cancelLayout()
		l.basePending = false
	}
	if modeChanged && l.mode == picture.PictureGlyph {
		cmds = append(cmds, l.base.Pause(l.basePaused()))
	}
	cmds = append(cmds, l.promoteBase())
	return tea.Batch(cmds...)
}

func scaleOverlaySubRect(subPx image.Rectangle, cellPxSize, rasterSize image.Point) image.Rectangle {
	fullCell := image.Rectangle{Max: cellPxSize}
	fullRaster := image.Rectangle{Max: rasterSize}
	if subPx.Empty() || cellPxSize.X <= 0 || cellPxSize.Y <= 0 || rasterSize.X <= 0 || rasterSize.Y <= 0 || subPx == fullCell {
		return fullRaster
	}
	if cellPxSize == rasterSize {
		return subPx.Intersect(fullRaster)
	}
	x0 := int(math.Round(float64(subPx.Min.X) * float64(rasterSize.X) / float64(cellPxSize.X)))
	y0 := int(math.Round(float64(subPx.Min.Y) * float64(rasterSize.Y) / float64(cellPxSize.Y)))
	x1 := int(math.Round(float64(subPx.Max.X) * float64(rasterSize.X) / float64(cellPxSize.X)))
	y1 := int(math.Round(float64(subPx.Max.Y) * float64(rasterSize.Y) / float64(cellPxSize.Y)))
	x0 = max(0, min(x0, rasterSize.X-1))
	y0 = max(0, min(y0, rasterSize.Y-1))
	x1 = max(x0+1, min(x1, rasterSize.X))
	y1 = max(y0+1, min(y1, rasterSize.Y))
	return image.Rect(x0, y0, x1, y1)
}

func (l *Layers) startLayout(comp *animation.Composite, submitBase bool) tea.Cmd {
	prevComp := l.comp
	prevPositions := l.positions
	if l.layoutJob != nil {
		prevComp = l.layoutJob.prevComp
		prevPositions = l.layoutJob.prevPositions
	}
	l.cancelLayout()
	var cmds []tea.Cmd
	cmds = append(cmds, l.closeActiveOverlays())
	if comp == nil {
		l.basePending = false
		return tea.Batch(cmds...)
	}

	var groups []layerGroup
	if l.mode == picture.PictureKitty && l.active && l.visible && l.cols > 0 && l.rows > 0 {
		groups = planLayers(comp, l.cols, l.rows, l.cw, l.ch, l.Fit(), l.Anchor())
		for i := range groups {
			g := &groups[i]
			cellPxSize := image.Pt(g.cells.Dx()*l.cw, g.cells.Dy()*l.ch)
			overlayOpts := l.opts
			overlayOpts.Picture.HostGeometry = true
			overlayOpts.Picture.Render.Fit = picture.FitFill
			overlayOpts.Picture.Render.KittyResolutionFactor = g.resolution
			overlayOpts.Picture.Render.CellPixelWidth = l.cw
			overlayOpts.Picture.Render.CellPixelHeight = l.ch
			if g.subPx != (image.Rectangle{Max: cellPxSize}) {
				overlayOpts.Picture.Render.Background = color.RGBA64Model.Convert(color.Transparent)
			}

			ctrl := NewSource(nil, overlayOpts)
			ctrl.SetSize(g.cells.Dx(), g.cells.Dy())
			ctrl.SetTerminal(true, l.cw, l.ch)
			ctrl.SetResolution(g.resolution)
			ctrl.SetRate(l.rate)
			ctrl.Pause(l.paused)
			ctrl.SetVisible(l.visible)
			g.target = ctrl.RasterGeometry().Pixels
			g.content = scaleOverlaySubRect(g.subPx, cellPxSize, g.target)

			l.overlays = append(l.overlays, &layerOverlay{
				cells:      g.cells,
				window:     g.window,
				target:     g.target,
				content:    g.content,
				resolution: g.resolution,
				keys:       g.keys,
				ctrl:       ctrl,
			})
		}
		// Link disjoint overlays that share the same key set so they share a single clock.
		for i := range l.overlays {
			lead := l.overlays[i]
			if lead.isFollower || len(lead.keys) == 0 {
				continue
			}
			for j := i + 1; j < len(l.overlays); j++ {
				cand := l.overlays[j]
				if !cand.isFollower && slices.Equal(lead.keys, cand.keys) {
					cand.isFollower = true
					_ = cand.ctrl.Pause(true)
					lead.followers = append(lead.followers, cand)
				}
			}
		}
	}

	if submitBase || l.presentedLayout != l.layoutGen {
		l.basePending = true
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &layerLayoutJob{
		ctx:           ctx,
		cancel:        cancel,
		prevComp:      prevComp,
		prevPositions: prevPositions,
	}
	l.layoutJob = job
	positions := prevPositions
	plan := append([]layerGroup(nil), groups...)
	bg := l.backgroundColor()
	jobCmd := func() tea.Msg {
		carried := comp
		if len(positions.Keys()) > 0 {
			var err error
			carried, err = comp.WithPositions(ctx, positions)
			if err != nil {
				return layerLayoutResult{owner: l, job: job, origComp: comp, err: err}
			}
		}
		var windows []*animation.Composite
		var sources []animation.Source
		if len(plan) > 0 {
			windows = make([]*animation.Composite, len(plan))
			sources = make([]animation.Source, len(plan))
			for i, g := range plan {
				w, err := carried.Window(ctx, g.window, g.content.Size())
				if err != nil {
					return layerLayoutResult{owner: l, job: job, origComp: comp, err: err}
				}
				if err := ctx.Err(); err != nil {
					return layerLayoutResult{owner: l, job: job, origComp: comp, err: err}
				}
				windows[i] = w
				sources[i] = wrapWindowSource(w, g.target, g.content, bg)
			}
		}
		return layerLayoutResult{
			owner:      l,
			job:        job,
			origComp:   comp,
			comp:       carried,
			submitBase: submitBase,
			windows:    windows,
			sources:    sources,
			bg:         bg,
		}
	}
	cmds = append(cmds, jobCmd)
	return tea.Batch(cmds...)
}

func (l *Layers) submitOverlayWindows(windows []*animation.Composite, sources []animation.Source, bg color.Color) tea.Cmd {
	if len(windows) == 0 || len(windows) != len(l.overlays) || len(sources) != len(l.overlays) {
		return nil
	}
	currBG := l.backgroundColor()
	if currBG != bg {
		for i, o := range l.overlays {
			sources[i] = wrapWindowSource(windows[i], o.target, o.content, currBG)
		}
		bg = currBG
	}
	indexOf := make(map[*layerOverlay]int, len(l.overlays))
	for i, o := range l.overlays {
		indexOf[o] = i
	}
	var cmds []tea.Cmd
	for i, o := range l.overlays {
		if o.isFollower {
			continue
		}
		if len(o.followers) == 0 {
			cmds = append(cmds, o.ctrl.SetSource(sources[i]))
			continue
		}
		followerWins := make([]linkedFollower, len(o.followers))
		for j, f := range o.followers {
			fIdx := indexOf[f]
			followerWins[j] = linkedFollower{
				comp:    windows[fIdx],
				target:  f.target,
				content: f.content,
				bg:      bg,
				source:  sources[fIdx],
			}
			cmds = append(cmds, f.ctrl.SetSource(sources[fIdx]))
		}
		cmds = append(cmds, o.ctrl.SetSource(&linkedWindowSource{
			main:      sources[i],
			followers: followerWins,
		}))
	}
	return tea.Batch(cmds...)
}

func (l *Layers) promoteBase() tea.Cmd {
	if !l.submitted || l.layoutGen == 0 || l.basePending {
		return nil
	}
	var cmds []tea.Cmd
	picStatus := l.base.Status().Picture
	if l.baseTargetGen > 0 && picStatus.Presented >= l.baseTargetGen && l.base.Status().Presented != nil {
		if l.presentedLayout != l.layoutGen {
			l.presentedLayout = l.layoutGen
			l.retainedSnapshot = nil
			l.retainedPresented = nil
			l.snapshotValid = false
			if l.retainedLayout != l.presentedLayout {
				cmds = append(cmds, l.closeRetainedOverlays())
			}
		}
		l.presentedBase = l.base.Status().Presented.Image()
		if comp, ok := l.base.Status().Presented.(*animation.Composite); ok && comp != nil {
			l.presentedComp = comp
		} else {
			l.presentedComp = l.comp
		}
	}
	if len(l.retainedOverlays) > 0 && l.retainedLayout == l.presentedLayout && len(l.overlays) > 0 && l.allOverlaysPresented() {
		cmds = append(cmds, l.closeRetainedOverlays())
	}
	return tea.Batch(cmds...)
}

func (l *Layers) allOverlaysPresented() bool {
	if len(l.overlays) == 0 {
		return false
	}
	for _, o := range l.overlays {
		if !o.presented() {
			return false
		}
	}
	return true
}

func (l *Layers) composePresentedSnapshot() image.Image {
	if l.mode == picture.PictureGlyph {
		if p := l.base.Status().Presented; p != nil {
			return imageview.New(p.Image())
		}
		return l.retainedSnapshot
	}
	baseImg := l.presentedBase
	if baseImg == nil {
		if p := l.base.Status().Presented; p != nil {
			baseImg = p.Image()
		}
	}
	if baseImg == nil {
		return nil
	}
	var activePresented []*layerOverlay
	if l.retainedLayout == l.presentedLayout {
		for _, o := range l.retainedOverlays {
			if o.presented() {
				activePresented = append(activePresented, o)
			}
		}
	}
	for _, o := range l.overlays {
		if o.presented() {
			activePresented = append(activePresented, o)
		}
	}
	if len(activePresented) == 0 {
		return imageview.New(baseImg)
	}
	bounds := baseImg.Bounds()
	out := image.NewRGBA(bounds)
	draw.Draw(out, bounds, imageview.Unwrap(baseImg), bounds.Min, draw.Src)
	for _, o := range activePresented {
		pres := o.ctrl.Status().Presented
		if pres == nil {
			continue
		}
		dst := o.window.Intersect(bounds)
		if dst.Empty() {
			continue
		}
		var src image.Image
		var fb image.Rectangle
		if comp := compositeFromSource(pres); comp != nil && comp.Image() != nil {
			src = imageview.Unwrap(comp.Image())
			fb = src.Bounds()
		} else if frame := pres.Image(); frame != nil {
			src = imageview.Unwrap(frame)
			fb = o.content.Intersect(src.Bounds())
		}
		if src == nil || fb.Empty() {
			continue
		}
		if dst.Size() == fb.Size() {
			draw.Draw(out, dst, src, fb.Min, draw.Src)
		} else {
			xdraw.CatmullRom.Scale(out, dst, src, fb, draw.Src, nil)
		}
	}
	return imageview.New(out)
}

func (l *Layers) harvestOverlayPositions() {
	for _, o := range l.overlays {
		if o.isFollower {
			continue
		}
		if comp := compositeFromSource(o.ctrl.Status().Source); comp != nil {
			l.positions = l.positions.WithPositions(comp)
		}
	}
}

func (l *Layers) retireOverlays() tea.Cmd {
	l.harvestOverlayPositions()
	return l.retireOverlaysWithoutHarvest()
}

func (l *Layers) retireOverlaysWithoutHarvest() tea.Cmd {
	if len(l.overlays) == 0 {
		if l.opts.Picture.ClearOnReplace {
			return l.closeRetainedOverlays()
		}
		return nil
	}
	var cmds []tea.Cmd
	if l.opts.Picture.ClearOnReplace {
		cmds = append(cmds, l.closeRetainedOverlays(), l.closeActiveOverlays())
		return tea.Batch(cmds...)
	}
	var newlyRetained []*layerOverlay
	for _, o := range l.overlays {
		if o.presented() {
			cmds = append(cmds, o.ctrl.Pause(true))
			newlyRetained = append(newlyRetained, o)
		} else {
			cmds = append(cmds, o.ctrl.Close())
		}
	}
	l.overlays = nil
	if len(newlyRetained) > 0 {
		cmds = append(cmds, l.closeRetainedOverlays())
		l.retainedOverlays = newlyRetained
		l.retainedLayout = l.presentedLayout
		l.retainedCols = l.cols
		l.retainedRows = l.rows
	}
	l.snapshotValid = false
	return tea.Batch(cmds...)
}

func (l *Layers) closeActiveOverlays() tea.Cmd {
	if len(l.overlays) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(l.overlays))
	for _, o := range l.overlays {
		cmds = append(cmds, o.ctrl.Close())
	}
	l.overlays = nil
	l.snapshotValid = false
	return tea.Batch(cmds...)
}

func (l *Layers) closeRetainedOverlays() tea.Cmd {
	if len(l.retainedOverlays) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(l.retainedOverlays))
	for _, o := range l.retainedOverlays {
		cmds = append(cmds, o.ctrl.Close())
	}
	l.retainedOverlays = nil
	l.retainedLayout = 0
	l.snapshotValid = false
	return tea.Batch(cmds...)
}

func (l *Layers) closeAllOverlays() tea.Cmd {
	return tea.Batch(l.closeRetainedOverlays(), l.closeActiveOverlays())
}

// planLayers converts composite pixel regions into cell-aligned windows clipped
// to the visible cell grid using the base picture's Fit and Anchor mapping,
// merging overlapping cell windows (and partially overlapping key sets) at the
// finer of their resolutions.
func planLayers(comp *animation.Composite, cols, rows, cw, ch int, fit picture.FitMode, anchor picture.FitAnchor) []layerGroup {
	if comp == nil || cols <= 0 || rows <= 0 {
		return nil
	}
	cw, ch = max(1, cw), max(1, ch)
	bounds := comp.Image().Bounds()
	if bounds.Empty() {
		return nil
	}
	sw, sh := bounds.Dx(), bounds.Dy()
	tw, th := cols*cw, rows*ch
	dst := fitRect(bounds, tw, th, fit, anchor)
	if dst.Empty() {
		return nil
	}
	visTarget := image.Rect(0, 0, tw, th)
	grid := image.Rect(0, 0, cols, rows)
	regions := comp.Regions()
	var groups []layerGroup
	for _, r := range regions {
		px := r.Rect.Intersect(bounds)
		if px.Empty() || r.Player == nil {
			continue
		}
		rel := px.Sub(bounds.Min)
		tx0 := dst.Min.X + int((int64(rel.Min.X)*int64(dst.Dx()))/int64(sw))
		ty0 := dst.Min.Y + int((int64(rel.Min.Y)*int64(dst.Dy()))/int64(sh))
		tx1 := dst.Min.X + int((int64(rel.Max.X)*int64(dst.Dx())+int64(sw)-1)/int64(sw))
		ty1 := dst.Min.Y + int((int64(rel.Max.Y)*int64(dst.Dy())+int64(sh)-1)/int64(sh))
		tpx := image.Rect(tx0, ty0, tx1, ty1).Intersect(visTarget)
		if tpx.Empty() {
			continue
		}
		cells := image.Rect(
			tpx.Min.X/cw,
			tpx.Min.Y/ch,
			(tpx.Max.X+cw-1)/cw,
			(tpx.Max.Y+ch-1)/ch,
		).Intersect(grid)
		if cells.Empty() {
			continue
		}
		dispW := float64(r.Rect.Dx()) * float64(dst.Dx()) / float64(sw)
		dispH := float64(r.Rect.Dy()) * float64(dst.Dy()) / float64(sh)
		own := r.Player.Image().Bounds().Size()
		factor := min(
			1.0,
			float64(max(1, own.X))/max(1.0, dispW),
			float64(max(1, own.Y))/max(1.0, dispH),
		)
		groups = mergeLayerGroup(groups, layerGroup{cells: cells, resolution: factor})
	}
	populateGroupWindowsAndKeys := func(gs []layerGroup) []layerGroup {
		out := gs[:0]
		for _, g := range gs {
			c := g.cells
			cellPx := image.Rect(c.Min.X*cw, c.Min.Y*ch, c.Max.X*cw, c.Max.Y*ch)
			tWin := cellPx.Intersect(dst)
			if tWin.Empty() {
				continue
			}
			window := image.Rect(
				bounds.Min.X+int((int64(tWin.Min.X-dst.Min.X)*int64(sw))/int64(dst.Dx())),
				bounds.Min.Y+int((int64(tWin.Min.Y-dst.Min.Y)*int64(sh))/int64(dst.Dy())),
				bounds.Min.X+int((int64(tWin.Max.X-dst.Min.X)*int64(sw)+int64(dst.Dx())-1)/int64(dst.Dx())),
				bounds.Min.Y+int((int64(tWin.Max.Y-dst.Min.Y)*int64(sh)+int64(dst.Dy())-1)/int64(dst.Dy())),
			).Intersect(bounds)
			if window.Empty() {
				continue
			}
			var keys []string
			for _, r := range regions {
				if r.Player != nil && r.Rect.Overlaps(window) && !slices.Contains(keys, r.Key) {
					keys = append(keys, r.Key)
				}
			}
			slices.Sort(keys)
			g.window = window
			g.subPx = tWin.Sub(cellPx.Min)
			g.keys = keys
			out = append(out, g)
		}
		return out
	}
	groups = populateGroupWindowsAndKeys(groups)

	// If two groups share at least one key but have different key sets, merge
	// them so all co-scheduled keys advance on a single Composite clock.
	for {
		merged := false
		for i := 0; i < len(groups) && !merged; i++ {
			for j := i + 1; j < len(groups); j++ {
				if sharesKey(groups[i].keys, groups[j].keys) && !slices.Equal(groups[i].keys, groups[j].keys) {
					combined := layerGroup{
						cells:      groups[i].cells.Union(groups[j].cells),
						resolution: max(groups[i].resolution, groups[j].resolution),
					}
					groups = append(groups[:j], groups[j+1:]...)
					groups = append(groups[:i], groups[i+1:]...)
					groups = mergeLayerGroup(groups, combined)
					groups = populateGroupWindowsAndKeys(groups)
					merged = true
					break
				}
			}
		}
		if !merged {
			break
		}
	}
	return groups
}

func fitRect(bounds image.Rectangle, tw, th int, fit picture.FitMode, anchor picture.FitAnchor) image.Rectangle {
	sw, sh := int64(bounds.Dx()), int64(bounds.Dy())
	cw, ch := int64(tw), int64(th)
	if sw <= 0 || sh <= 0 || cw <= 0 || ch <= 0 {
		return image.Rectangle{}
	}
	switch fit {
	case picture.FitFill:
		return image.Rect(0, 0, tw, th)
	case picture.FitCover:
		var w, h int64
		if sw*ch > sh*cw {
			h = ch
			w = sw * ch / sh
		} else {
			w = cw
			h = sh * cw / sw
		}
		w, h = max(w, 1), max(h, 1)
		var ox, oy int64
		switch anchor {
		case picture.AnchorTop:
			ox, oy = (cw-w)/2, 0
		case picture.AnchorBottom:
			ox, oy = (cw-w)/2, ch-h
		case picture.AnchorLeft:
			ox, oy = 0, (ch-h)/2
		case picture.AnchorRight:
			ox, oy = cw-w, (ch-h)/2
		default:
			ox, oy = (cw-w)/2, (ch-h)/2
		}
		return image.Rect(int(ox), int(oy), int(ox+w), int(oy+h))
	default: // FitContain
		var w, h int64
		if sw*ch >= sh*cw {
			w, h = cw, sh*cw/sw
		} else {
			w, h = sw*ch/sh, ch
		}
		w, h = max(w, 1), max(h, 1)
		ox, oy := (cw-w)/2, (ch-h)/2
		return image.Rect(int(ox), int(oy), int(ox+w), int(oy+h))
	}
}

func sharesKey(a, b []string) bool {
	for _, k := range a {
		if slices.Contains(b, k) {
			return true
		}
	}
	return false
}

func mergeLayerGroup(groups []layerGroup, g layerGroup) []layerGroup {
	for {
		merged := false
		for i, o := range groups {
			if o.cells.Overlaps(g.cells) {
				g = layerGroup{
					cells:      o.cells.Union(g.cells),
					resolution: max(o.resolution, g.resolution),
				}
				groups = append(groups[:i], groups[i+1:]...)
				merged = true
				break
			}
		}
		if !merged {
			return append(groups, g)
		}
	}
}

func (l *Layers) spliceOverlays(grid string) string {
	var toSplice []*layerOverlay
	if l.retainedLayout == l.presentedLayout && l.cols == l.retainedCols && l.rows == l.retainedRows {
		for _, o := range l.retainedOverlays {
			if o.presented() {
				toSplice = append(toSplice, o)
			}
		}
	}
	if l.presentedLayout == l.layoutGen && !l.basePending {
		for _, o := range l.overlays {
			if o.presented() {
				toSplice = append(toSplice, o)
			}
		}
	}
	if len(toSplice) == 0 {
		return grid
	}
	lines := strings.Split(grid, "\n")
	for _, o := range toSplice {
		overView := o.ctrl.View()
		if overView == "" {
			continue
		}
		overRows := strings.Split(overView, "\n")
		if len(overRows) != o.cells.Dy() {
			continue
		}
		for i, row := range overRows {
			y := o.cells.Min.Y + i
			if y >= 0 && y < len(lines) {
				lines[y] = spliceCells(lines[y], o.cells.Min.X, o.cells.Max.X, row)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// spliceCells replaces cells [x0, x1) of a Kitty placeholder row with repl.
// It closes any preceding SGR before repl and reopens the SGR that covered x1
// after repl, supporting edge-aligned and multiple disjoint splices per row.
func spliceCells(line string, x0, x1 int, repl string) string {
	var starts []int
	for i, r := range line {
		if r == kitty.Placeholder {
			starts = append(starts, i)
		}
	}
	n := len(starts)
	if x0 < 0 || x0 >= x1 || x1 > n || repl == "" {
		return line
	}
	starts = append(starts, len(line))
	var prefix, suffix string
	if x0 > 0 {
		end := starts[x0]
		if i := strings.IndexByte(line[starts[x0-1]:end], '\x1b'); i >= 0 {
			end = starts[x0-1] + i
		}
		prefix = line[:end] + "\x1b[39m"
	}
	if x1 < n {
		suffix = lastSGR(line[:starts[x1]]) + line[starts[x1]:]
	}
	return prefix + repl + suffix
}

func lastSGR(s string) string {
	i := strings.LastIndex(s, "\x1b[38;2;")
	if i < 0 {
		return ""
	}
	end := strings.IndexByte(s[i:], 'm')
	if end < 0 {
		return ""
	}
	return s[i : i+end+1]
}

func (l *Layers) clipViewport(grid string) string {
	rect := viewportBounds(l.viewport, l.cols, l.rows)
	if rect.Empty() {
		return ""
	}
	lines := strings.Split(grid, "\n")
	var out strings.Builder
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		width := 0
		if y >= 0 && y < len(lines) {
			if l.mode == picture.PictureKitty {
				var row string
				row, width = sliceKittyCells(lines[y], rect.Min.X, rect.Max.X)
				out.WriteString(row)
			} else {
				width = max(0, min(rect.Max.X, l.cols)-rect.Min.X)
				if width > 0 {
					if rect.Min.X == 0 && width == l.cols {
						out.WriteString(lines[y])
					} else {
						out.WriteString(ansi.Cut(lines[y], rect.Min.X, rect.Min.X+width))
						out.WriteString("\x1b[0m")
					}
				}
			}
		}
		if pad := rect.Dx() - width; pad > 0 {
			out.WriteString(strings.Repeat(" ", pad))
		}
		if y+1 < rect.Max.Y {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func sliceKittyCells(line string, x0, x1 int) (string, int) {
	var starts []int
	for i, r := range line {
		if r == kitty.Placeholder {
			starts = append(starts, i)
		}
	}
	n := len(starts)
	if x0 < 0 || x0 >= x1 || x0 >= n {
		return "", 0
	}
	x1 = min(x1, n)
	starts = append(starts, len(line))
	end := starts[x1]
	if i := strings.IndexByte(line[starts[x1-1]:end], '\x1b'); i >= 0 {
		end = starts[x1-1] + i
	}
	return lastSGR(line[:starts[x0]]) + line[starts[x0]:end] + "\x1b[39m", x1 - x0
}

func viewportBounds(v picture.Viewport, cols, rows int) image.Rectangle {
	if cols <= 0 || rows <= 0 || v.Width <= 0 || v.Height <= 0 || v.X >= cols || v.Y >= rows {
		return image.Rectangle{}
	}
	if v.X < 0 {
		if v.X <= -v.Width {
			return image.Rectangle{}
		}
		v.Width += v.X
		v.X = 0
	}
	if v.Y < 0 {
		if v.Y <= -v.Height {
			return image.Rectangle{}
		}
		v.Height += v.Y
		v.Y = 0
	}
	return image.Rect(v.X, v.Y, v.X+min(v.Width, cols-v.X), v.Y+min(v.Height, rows-v.Y))
}
