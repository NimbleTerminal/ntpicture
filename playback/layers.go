package playback

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"slices"

	tea "charm.land/bubbletea/v2"
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
	overlayCols      int
	overlayRows      int
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
	cachedPresented   animation.Source
	snapshotValid     bool

	layoutJob      *layerLayoutJob
	restartJob     *layerRestartJob
	restartPending bool
	err            error
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
	} else if options.Picture.AutoMode && !options.Picture.HostGeometry {
		if (*picture.Picture)(nil).KittySupported() == picture.KittyCapabilitySupported {
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
		if !options.Picture.AutoMode {
			_ = base.SetTerminal(true, cw, ch)
		}
		_ = base.Pause(true)
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

func (l *Layers) syncBasePause() tea.Cmd {
	if want := l.basePaused(); l.base.Status().Paused != want {
		return l.base.Pause(want)
	}
	return nil
}

func (l *Layers) invalidateSnapshot() {
	l.snapshotValid = false
	l.cachedPresented = nil
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
			if l.mode == picture.PictureGlyph && l.visible && l.layoutJob == nil {
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
	if !l.visible || l.cols <= 0 || l.rows <= 0 {
		return nil
	}
	if l.presentedLayout != l.layoutGen || l.layoutGen == 0 || l.basePending {
		if l.retainedPresented != nil {
			return l.retainedPresented
		}
		if l.retainedSnapshot != nil {
			l.retainedPresented = &imageSource{img: l.retainedSnapshot}
			return l.retainedPresented
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
	if l.cachedPresented != nil {
		return l.cachedPresented
	}
	if l.presentedComp != nil {
		l.cachedPresented = &layerSnapshotSource{
			comp:      l.presentedComp,
			positions: l.presentedPositions(),
			img:       snap,
		}
		return l.cachedPresented
	}
	l.cachedPresented = &imageSource{img: snap}
	return l.cachedPresented
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

// Closed reports whether Close or Keep has permanently stopped the layers.
// A nil receiver reports true.
func (l *Layers) Closed() bool {
	return l == nil || l.closed
}

// TerminalState returns a snapshot of the base picture's terminal session.
func (l *Layers) TerminalState() picture.TerminalState {
	if l == nil {
		return (*picture.Picture)(nil).TerminalState()
	}
	return l.base.TerminalState()
}

// KittySupported reports the Kitty graphics capability of the base picture's
// terminal session.
func (l *Layers) KittySupported() picture.KittyCapability {
	return l.TerminalState().Kitty
}

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
		return l.syncBasePause()
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
	l.restartPending = false
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
			l.restartPending = false
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
	l.restartPending = false
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
	if l.submitted && cols > 0 && rows > 0 {
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
		l.harvestOverlayPositions()
		closeCmd := l.closeAllOverlays()
		l.cancelLayout()
		l.clearSnapshots()
		l.layoutGen++
		l.baseTargetGen = l.base.Status().Picture.Generation
		return tea.Batch(baseCmd, closeCmd)
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
	if !modeChanged && !geomChanged && cellWidth > 0 && cellHeight > 0 && l.base.Status().Picture.Err == nil && l.err == nil {
		l.explicitTerminal = true
		return l.syncBasePause()
	}
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
	pauseCmd := l.syncBasePause()
	if !l.submitted {
		return tea.Batch(baseCmd, pauseCmd)
	}
	if modeChanged || geomChanged {
		return tea.Batch(baseCmd, pauseCmd, l.syncGeometryAndMode(modeChanged))
	}
	return tea.Batch(baseCmd, pauseCmd)
}

// SetMode switches between PictureGlyph and PictureKitty using the configured
// session, or follows the session automatically when PictureAuto is passed.
func (l *Layers) SetMode(mode picture.PictureMode) tea.Cmd {
	if l.closed {
		return nil
	}
	if mode != picture.PictureAuto && mode == l.mode {
		return nil
	}
	prevMode := l.mode
	prevCW, prevCH := l.cw, l.ch
	prevRetained, prevRetainedPres := l.retainedSnapshot, l.retainedPresented
	if l.submitted {
		l.captureRetainedSnapshot()
	}
	baseCmd := l.base.SetMode(mode)
	if l.base.Status().Picture.Err != nil || (mode != picture.PictureAuto && l.base.pic.Mode() != mode) {
		l.retainedSnapshot, l.retainedPresented = prevRetained, prevRetainedPres
		return baseCmd
	}
	l.err = nil
	l.mode = l.base.pic.Mode()
	l.cw, l.ch = l.base.pic.CellPixelSize()
	modeChanged := l.mode != prevMode
	geomChanged := l.cw != prevCW || l.ch != prevCH
	pauseCmd := l.syncBasePause()
	if !l.submitted || (!modeChanged && !geomChanged) {
		l.retainedSnapshot, l.retainedPresented = prevRetained, prevRetainedPres
		return tea.Batch(baseCmd, pauseCmd)
	}
	return tea.Batch(baseCmd, pauseCmd, l.syncGeometryAndMode(modeChanged))
}

// SetVisible hides or shows all layers. Hiding cancels pending work and clears
// terminal placements while retaining the composite and its playback positions.
// Calling Restart while already hidden queues the reset to run when SetVisible(true) is called.
func (l *Layers) SetVisible(visible bool) tea.Cmd {
	if l.closed || l.visible == visible {
		return nil
	}
	l.visible = visible
	if !visible {
		l.restartPending = false
		l.cancelRestart()
		l.cancelLayout()
		l.retainedSnapshot = nil
		l.retainedPresented = nil
		l.cachedSnapshot = nil
		l.invalidateSnapshot()
		l.harvestOverlayPositions()
		return tea.Batch(l.closeAllOverlays(), l.base.SetVisible(false))
	}
	baseCmd := l.base.SetVisible(true)
	if l.restartPending && l.comp != nil {
		l.restartPending = false
		return tea.Batch(baseCmd, l.Restart())
	}
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
	if !l.visible {
		l.restartPending = true
		return nil
	}
	l.restartPending = false
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
	if bg == l.opts.Picture.Render.Background && l.base.Status().Picture.Err == nil && l.err == nil {
		return nil
	}
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
	prevMode := l.base.pic.Mode()
	prevBaseSource := l.base.Status().Source
	prevBasePresented := l.base.Status().Presented

	// If a terminal event may change cell pixel size or mode on the base picture,
	// preserve the last committed snapshot before base.Update mutates base state.
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
		l.invalidateSnapshot()
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
			l.invalidateSnapshot()
		}
	}
	for _, o := range l.retainedOverlays {
		cmds = append(cmds, o.ctrl.Update(msg))
	}

	nw, nh := l.base.pic.CellPixelSize()
	newMode := l.base.pic.Mode()
	modeChanged := newMode != prevMode && newMode != l.mode
	geomChanged := (nw != prevCW || nh != prevCH) && nw > 0 && nh > 0
	if modeChanged || geomChanged {
		if preEventSnap != nil {
			l.retainedSnapshot = preEventSnap
			l.retainedPresented = preEventPres
		}
		l.mode = newMode
		if nw > 0 && nh > 0 {
			l.cw, l.ch = nw, nh
		}
		cmds = append(cmds, l.syncBasePause())
		if l.submitted {
			cmds = append(cmds, l.syncGeometryAndMode(modeChanged))
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
					if m.submitBase || l.base.Status().Source != l.comp || (l.presentedLayout != l.layoutGen && m.comp != m.origComp) {
						cmds = append(cmds, l.syncBasePause())
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
	if !l.visible || l.cols <= 0 || l.rows <= 0 {
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
	l.cachedPresented = nil
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
	l.restartPending = false
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
	l.restartPending = false
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
	l.invalidateSnapshot()
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
	l.invalidateSnapshot()
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

	if !l.visible {
		l.cancelLayout()
		l.clearSnapshots()
		if l.still != nil {
			cmds = append(cmds, l.base.SetSource(&imageSource{img: l.still}))
		}
		return tea.Batch(cmds...)
	}

	l.layoutGen++
	l.cachedSnapshot = nil
	l.invalidateSnapshot()
	cmds = append(cmds, l.syncBasePause())

	if l.comp != nil {
		cmds = append(cmds, l.startLayout(l.comp, true))
	} else {
		l.cancelLayout()
		l.basePending = false
		cmds = append(cmds, l.base.SetSource(&imageSource{img: l.still}))
		l.baseTargetGen = l.base.Status().Picture.Generation
		cmds = append(cmds, l.promoteBase())
	}
	return tea.Batch(cmds...)
}

func (l *Layers) syncGeometryAndMode(modeChanged bool) tea.Cmd {
	if !l.visible {
		l.cancelLayout()
		l.clearSnapshots()
		return l.closeAllOverlays()
	}
	var cmds []tea.Cmd
	cmds = append(cmds, l.retireOverlays())
	l.layoutGen++
	l.baseTargetGen = l.base.Status().Picture.Generation
	l.cachedSnapshot = nil
	l.invalidateSnapshot()
	if l.comp != nil && (l.mode == picture.PictureKitty || modeChanged || l.layoutJob != nil) {
		cmds = append(cmds, l.startLayout(l.comp, false))
	} else {
		l.cancelLayout()
		l.basePending = false
	}
	if modeChanged && l.mode == picture.PictureGlyph {
		cmds = append(cmds, l.syncBasePause())
	}
	cmds = append(cmds, l.promoteBase())
	return tea.Batch(cmds...)
}

func (l *Layers) startLayout(comp *animation.Composite, submitBase bool) tea.Cmd {
	prevComp := l.comp
	prevPositions := l.positions
	if l.layoutJob != nil {
		prevComp = l.layoutJob.prevComp
		prevPositions = l.layoutJob.prevPositions
		submitBase = submitBase || l.layoutJob.submitBase
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
		l.overlayCols, l.overlayRows = l.cols, l.rows
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
		submitBase:    submitBase,
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
			l.invalidateSnapshot()
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
	if len(l.retainedOverlays) > 0 && l.presentedLayout == l.layoutGen && !l.basePending && l.retainedLayout == l.presentedLayout && len(l.overlays) > 0 && l.allOverlaysPresented() {
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
	if l.presentedLayout != l.layoutGen || l.basePending || l.layoutGen == 0 {
		return l.closeActiveOverlays()
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
		l.retainedCols = l.overlayCols
		l.retainedRows = l.overlayRows
	}
	l.invalidateSnapshot()
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
	l.invalidateSnapshot()
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
	l.invalidateSnapshot()
	return tea.Batch(cmds...)
}

func (l *Layers) closeAllOverlays() tea.Cmd {
	return tea.Batch(l.closeRetainedOverlays(), l.closeActiveOverlays())
}
