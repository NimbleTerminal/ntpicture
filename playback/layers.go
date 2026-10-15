package playback

import (
	"context"
	"image"
	"image/color"
	"image/draw"
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
	resolution float64
}

type layerOverlay struct {
	cells      image.Rectangle
	window     image.Rectangle
	resolution float64
	ctrl       *Controller
}

func (o *layerOverlay) presented() bool {
	return o != nil && o.ctrl != nil && o.ctrl.Status().Presented != nil
}

// imageSource wraps an immutable static image with zero delay.
type imageSource struct{ img image.Image }

func (s imageSource) Image() image.Image                                      { return s.img }
func (s imageSource) Delay() time.Duration                                    { return 0 }
func (s imageSource) Advance(context.Context) (animation.Source, bool, error) { return s, false, nil }
func (s imageSource) Reset(context.Context) (animation.Source, error)         { return s, nil }

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
// All methods belong to one Bubble Tea event loop; forward every message to
// Update and execute every returned command.
type Layers struct {
	opts             Options
	base             *Controller
	comp             *animation.Composite
	still            image.Image
	positions        animation.Positions
	overlays         []*layerOverlay
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

	layoutGen        uint64
	baseTargetGen    uint64
	presentedLayout  uint64
	presentedBase    image.Image
	retainedSnapshot image.Image
	cachedSnapshot   image.Image
	snapshotValid    bool

	restartJob *layerRestartJob
	err        error
}

// NewLayers creates a layered picture component sharing options.Picture.Session
// and options.Picture.NextID across the base picture and all animated overlays.
func NewLayers(composite *animation.Composite, options Options) *Layers {
	if options.Wait == nil {
		options.Wait = wait
	}
	if options.Picture.NextID == nil {
		options.Picture.NextID = picture.NextImageID
	}
	cw, ch := options.Picture.Render.CellPixelWidth, options.Picture.Render.CellPixelHeight
	if options.Picture.Session != nil {
		state := options.Picture.Session.Snapshot()
		cw, ch = state.CellPixelWidth, state.CellPixelHeight
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
	return &Layers{
		opts:      options,
		base:      NewSource(nil, baseOpts),
		comp:      composite,
		positions: composite.Positions(),
		cw:        cw,
		ch:        ch,
		active:    true,
		visible:   true,
		rate:      1,
	}
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

// Status reports the combined playback and base presentation status.
func (l *Layers) Status() Status {
	baseStatus := l.base.Status()
	err := l.err
	if err == nil {
		err = baseStatus.Err
	}
	busy := l.restartJob != nil || baseStatus.Busy || baseStatus.Picture.Busy
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
			if !st.Finished {
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
	var presented animation.Source
	if l.presentedLayout == l.layoutGen && l.layoutGen > 0 && baseStatus.Presented != nil {
		presented = baseStatus.Presented
	}
	return Status{
		Source:    source,
		Paused:    l.paused,
		Finished:  finished,
		Busy:      busy,
		Rate:      l.rate,
		Err:       err,
		Presented: presented,
		Picture:   baseStatus.Picture,
	}
}

// Layers returns a snapshot of the active Kitty overlay layers in cell-group order.
// Under glyph rendering or while playback activation is suspended, it returns nil.
func (l *Layers) Layers() []Layer {
	if len(l.overlays) == 0 {
		return nil
	}
	out := make([]Layer, len(l.overlays))
	for i, o := range l.overlays {
		out[i] = Layer{
			Cells:      o.cells,
			Window:     o.window,
			Resolution: o.resolution,
			Status:     o.ctrl.Status(),
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
// Hosts can set Active(false) during scroll or resize bursts so only the still
// base picture is prepared, then call SetActive(true) once the viewport settles
// to construct and start the Kitty overlays without re-preparing the base.
func (l *Layers) SetActive(active bool) tea.Cmd {
	if l.closed || l.active == active {
		return nil
	}
	l.active = active
	if l.mode == picture.PictureGlyph {
		return l.base.Pause(l.basePaused())
	}
	if !l.active {
		return l.stopOverlays()
	}
	if !l.submitted || l.comp == nil || !l.visible || l.cols <= 0 || l.rows <= 0 {
		return nil
	}
	return l.rebuildOverlays()
}

// SetComposite replaces the current composite, carrying matching region playback
// positions from previous overlays. Pass nil to clear.
func (l *Layers) SetComposite(composite *animation.Composite) tea.Cmd {
	if l.closed {
		return nil
	}
	l.cancelRestart()
	l.err = nil
	l.still = nil
	if composite == nil {
		l.comp = nil
		l.positions = animation.Positions{}
		l.clearSnapshots()
		return tea.Batch(l.closeOverlays(), l.base.SetSource(nil))
	}
	l.captureRetainedSnapshot()
	stopCmd := l.stopOverlays()
	if len(l.positions.Keys()) > 0 {
		if carried, err := composite.WithPositions(context.Background(), l.positions); err == nil {
			composite = carried
		}
	}
	l.comp = composite
	l.positions = l.positions.WithPositions(composite)
	return tea.Batch(stopCmd, l.applySource(false))
}

// SetImage copies img and displays it as a still base picture with no animated overlays.
// Pass nil to clear.
func (l *Layers) SetImage(img image.Image) tea.Cmd {
	if l.closed {
		return nil
	}
	if img == nil {
		return l.SetImageImmutable(nil)
	}
	b := img.Bounds()
	if b.Empty() || b.Dx() <= 0 || b.Dy() <= 0 || b.Dx() > picture.MaxImagePixels/b.Dy() {
		return l.SetImageImmutable(img)
	}
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
	l.cancelRestart()
	l.err = nil
	l.comp = nil
	l.positions = animation.Positions{}
	if img == nil {
		l.still = nil
		l.clearSnapshots()
		return tea.Batch(l.closeOverlays(), l.base.SetSource(nil))
	}
	l.captureRetainedSnapshot()
	stopCmd := l.closeOverlays()
	l.still = imageview.New(img)
	return tea.Batch(stopCmd, l.applySource(false))
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
	if l.submitted {
		l.captureRetainedSnapshot()
	}
	l.cols, l.rows = cols, rows
	baseCmd := l.base.SetSize(cols, rows)
	if l.base.Status().Picture.Err != nil {
		return baseCmd
	}
	if !l.submitted {
		return baseCmd
	}
	if cols == 0 || rows == 0 {
		l.layoutGen++
		l.baseTargetGen = l.base.Status().Picture.Generation
		return tea.Batch(baseCmd, l.stopOverlays())
	}
	return tea.Batch(baseCmd, l.syncGeometryAndMode(false))
}

// SetTerminal applies an explicit host graphics decision and cell pixel geometry
// without probing or consulting global state.
func (l *Layers) SetTerminal(kitty bool, cellWidth, cellHeight int) tea.Cmd {
	if l.closed {
		return nil
	}
	l.explicitTerminal = true
	if l.submitted {
		l.captureRetainedSnapshot()
	}
	baseCmd := l.base.SetTerminal(kitty, cellWidth, cellHeight)
	if l.base.Status().Picture.Err != nil {
		return baseCmd
	}
	mode := picture.PictureGlyph
	if kitty {
		mode = picture.PictureKitty
	}
	modeChanged := mode != l.mode
	geomChanged := cellWidth != l.cw || cellHeight != l.ch
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
	if l.submitted {
		l.captureRetainedSnapshot()
	}
	baseCmd := l.base.SetMode(mode)
	if l.base.Status().Picture.Err != nil || l.base.pic.Mode() != mode {
		return baseCmd
	}
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
		l.retainedSnapshot = nil
		l.cachedSnapshot = nil
		l.snapshotValid = false
		return tea.Batch(l.stopOverlays(), l.base.SetVisible(false))
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
		cmds = append(cmds, o.ctrl.Pause(paused))
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
	l.opts.Picture.Render.Background = bg
	cmds := []tea.Cmd{l.base.SetBackground(bg)}
	for _, o := range l.overlays {
		cmds = append(cmds, o.ctrl.SetBackground(bg))
	}
	return tea.Batch(cmds...)
}

// Fit reports the base picture's fit mode.
func (l *Layers) Fit() picture.FitMode { return l.base.Fit() }

// SetFit changes the base picture's fit mode.
func (l *Layers) SetFit(fit picture.FitMode) tea.Cmd {
	if l.closed {
		return nil
	}
	l.opts.Picture.Render.Fit = fit
	return l.base.SetFit(fit)
}

// Anchor reports the base picture's crop anchor.
func (l *Layers) Anchor() picture.FitAnchor { return l.base.Anchor() }

// SetAnchor changes the base picture's crop anchor.
func (l *Layers) SetAnchor(anchor picture.FitAnchor) tea.Cmd {
	if l.closed {
		return nil
	}
	l.opts.Picture.Render.Anchor = anchor
	return l.base.SetAnchor(anchor)
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

// Update routes messages to the base picture and active overlays, tracks region
// playback positions, and promotes committed presentations.
func (l *Layers) Update(msg tea.Msg) tea.Cmd {
	if l.closed {
		return l.base.Update(msg)
	}
	var cmds []tea.Cmd
	prevCW, prevCH := l.base.pic.CellPixelSize()
	prevBaseSource := l.base.Status().Source
	prevBasePresented := l.base.Status().Presented
	cmds = append(cmds, l.base.Update(msg))

	if afterBase := l.base.Status(); afterBase.Source != prevBaseSource {
		if comp, ok := afterBase.Source.(*animation.Composite); ok && comp != nil {
			l.comp = comp
			l.positions = l.positions.WithPositions(comp)
		}
	}
	if l.base.Status().Presented != prevBasePresented {
		l.snapshotValid = false
	}

	for _, o := range l.overlays {
		beforeSource := o.ctrl.Status().Source
		beforePresented := o.ctrl.Status().Presented
		cmds = append(cmds, o.ctrl.Update(msg))
		after := o.ctrl.Status()
		if after.Source != beforeSource {
			if comp, ok := after.Source.(*animation.Composite); ok && comp != nil {
				l.positions = l.positions.WithPositions(comp)
			}
		}
		if after.Presented != beforePresented {
			l.snapshotValid = false
		}
	}

	if nw, nh := l.base.pic.CellPixelSize(); (nw != prevCW || nh != prevCH) && nw > 0 && nh > 0 {
		l.cw, l.ch = nw, nh
		if l.submitted {
			cmds = append(cmds, l.syncGeometryAndMode(false))
		}
	}

	switch m := msg.(type) {
	case layerRestartResult:
		if m.owner == l && m.job == l.restartJob {
			cancelled := m.job.ctx.Err() != nil
			m.job.cancel()
			l.restartJob = nil
			if !cancelled {
				if m.err != nil {
					l.err = m.err
					l.paused = true
				} else if m.comp != nil {
					l.captureRetainedSnapshot()
					cmds = append(cmds, l.closeOverlays())
					l.comp = m.comp
					l.positions = m.comp.Positions()
					cmds = append(cmds, l.applySource(false))
				}
			}
		}
	}

	l.promoteBase()
	return tea.Batch(cmds...)
}

// View returns the composed terminal cell grid. Under Kitty graphics, each
// presented overlay's placeholder rows are spliced into the base picture's grid
// at its cell window; unpresented overlays retain the base picture's cells.
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
	if l.mode == picture.PictureKitty && l.presentedLayout == l.layoutGen && l.layoutGen > 0 && len(l.overlays) > 0 {
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
// overlays leave the committed base image's pixels intact. Returns nil before
// any frame is presented, while hidden, and after clearing or Close; Keep
// retains the last presented snapshot.
func (l *Layers) Snapshot() image.Image {
	if l.closed {
		return l.retainedSnapshot
	}
	if !l.visible {
		return nil
	}
	l.promoteBase()
	if l.presentedLayout != l.layoutGen || l.layoutGen == 0 {
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
	cmds := []tea.Cmd{l.closeOverlays(), l.base.Close()}
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
	l.keptView = l.View()
	l.retainedSnapshot = l.Snapshot()
	l.closed = true
	var cmds []tea.Cmd
	if l.presentedLayout == l.layoutGen && l.layoutGen > 0 {
		cmds = append(cmds, l.base.Keep())
		for _, o := range l.overlays {
			if o.presented() {
				cmds = append(cmds, o.ctrl.Keep())
			} else {
				cmds = append(cmds, o.ctrl.Close())
			}
		}
		l.overlays = nil
	} else {
		cmds = append(cmds, l.base.Keep(), l.closeOverlays())
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

func (l *Layers) clearSnapshots() {
	l.presentedBase = nil
	l.retainedSnapshot = nil
	l.cachedSnapshot = nil
	l.snapshotValid = false
	l.presentedLayout = 0
}

func (l *Layers) captureRetainedSnapshot() {
	if l.opts.Picture.ClearOnReplace {
		l.retainedSnapshot = nil
		return
	}
	if snap := l.Snapshot(); snap != nil {
		l.retainedSnapshot = snap
	}
}

func (l *Layers) applySource(newLayout bool) tea.Cmd {
	if newLayout {
		l.captureRetainedSnapshot()
	}
	l.submitted = true
	var cmds []tea.Cmd
	cmds = append(cmds, l.stopOverlays())

	if l.comp == nil && l.still == nil {
		l.clearSnapshots()
		cmds = append(cmds, l.base.SetSource(nil))
		return tea.Batch(cmds...)
	}

	l.layoutGen++
	l.cachedSnapshot = nil
	l.snapshotValid = false
	cmds = append(cmds, l.base.Pause(l.basePaused()))

	if l.comp != nil {
		cmds = append(cmds, l.base.SetSource(l.comp))
		if l.mode == picture.PictureKitty && l.active && l.visible && l.cols > 0 && l.rows > 0 {
			cmds = append(cmds, l.rebuildOverlays())
		}
	} else {
		cmds = append(cmds, l.base.SetSource(imageSource{img: l.still}))
	}

	l.baseTargetGen = l.base.Status().Picture.Generation
	l.promoteBase()
	return tea.Batch(cmds...)
}

func (l *Layers) syncGeometryAndMode(modeChanged bool) tea.Cmd {
	var cmds []tea.Cmd
	cmds = append(cmds, l.stopOverlays())
	if l.comp != nil && len(l.positions.Keys()) > 0 {
		if carried, err := l.comp.WithPositions(context.Background(), l.positions); err == nil && carried != l.comp {
			l.comp = carried
			cmds = append(cmds, l.base.SetSource(l.comp))
		}
	}
	l.layoutGen++
	l.baseTargetGen = l.base.Status().Picture.Generation
	l.cachedSnapshot = nil
	l.snapshotValid = false
	if l.mode == picture.PictureKitty && l.comp != nil && l.active && l.visible && l.cols > 0 && l.rows > 0 {
		cmds = append(cmds, l.rebuildOverlays())
	} else if modeChanged && l.mode == picture.PictureGlyph {
		cmds = append(cmds, l.base.Pause(l.basePaused()))
	}
	l.promoteBase()
	return tea.Batch(cmds...)
}

func (l *Layers) promoteBase() {
	if !l.submitted || l.layoutGen == 0 {
		return
	}
	picStatus := l.base.Status().Picture
	if l.baseTargetGen > 0 && picStatus.Presented >= l.baseTargetGen && l.base.Status().Presented != nil {
		if l.presentedLayout != l.layoutGen {
			l.presentedLayout = l.layoutGen
			l.retainedSnapshot = nil
			l.snapshotValid = false
		}
		l.presentedBase = l.base.Status().Presented.Image()
	}
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
	hasPresentedOverlay := false
	for _, o := range l.overlays {
		if o.presented() {
			hasPresentedOverlay = true
			break
		}
	}
	if !hasPresentedOverlay {
		return imageview.New(baseImg)
	}
	bounds := baseImg.Bounds()
	out := image.NewRGBA(bounds)
	draw.Draw(out, bounds, imageview.Unwrap(baseImg), bounds.Min, draw.Src)
	for _, o := range l.overlays {
		if !o.presented() {
			continue
		}
		frame := o.ctrl.Status().Presented.Image()
		if frame == nil {
			continue
		}
		dst := o.window.Intersect(bounds)
		if dst.Empty() {
			continue
		}
		src := imageview.Unwrap(frame)
		fb := src.Bounds()
		if dst.Size() == fb.Size() {
			draw.Draw(out, dst, src, fb.Min, draw.Src)
		} else {
			xdraw.CatmullRom.Scale(out, dst, src, fb, draw.Src, nil)
		}
	}
	return imageview.New(out)
}

func (l *Layers) stopOverlays() tea.Cmd {
	if len(l.overlays) == 0 {
		return nil
	}
	for _, o := range l.overlays {
		if comp, ok := o.ctrl.Status().Source.(*animation.Composite); ok && comp != nil {
			l.positions = l.positions.WithPositions(comp)
		}
	}
	return l.closeOverlays()
}

func (l *Layers) closeOverlays() tea.Cmd {
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

func (l *Layers) rebuildOverlays() tea.Cmd {
	cmds := []tea.Cmd{l.stopOverlays()}
	if l.comp == nil || l.mode != picture.PictureKitty || !l.active || !l.visible || l.cols <= 0 || l.rows <= 0 {
		return tea.Batch(cmds...)
	}
	if len(l.positions.Keys()) > 0 {
		if carried, err := l.comp.WithPositions(context.Background(), l.positions); err == nil {
			l.comp = carried
		}
	}
	groups := planLayers(l.comp, l.cols, l.rows, l.cw, l.ch)
	for _, g := range groups {
		overlayOpts := l.opts
		overlayOpts.Picture.HostGeometry = true
		overlayOpts.Picture.Render.Fit = picture.FitFill
		overlayOpts.Picture.Render.KittyResolutionFactor = g.resolution
		overlayOpts.Picture.Render.CellPixelWidth = l.cw
		overlayOpts.Picture.Render.CellPixelHeight = l.ch

		ctrl := NewSource(nil, overlayOpts)
		ctrl.SetSize(g.cells.Dx(), g.cells.Dy())
		ctrl.SetTerminal(true, l.cw, l.ch)
		ctrl.SetResolution(g.resolution)
		ctrl.SetRate(l.rate)
		ctrl.Pause(l.paused)
		ctrl.SetVisible(l.visible)

		target := ctrl.RasterGeometry().Pixels
		windowSource, err := l.comp.Window(context.Background(), g.window, target)
		if err != nil {
			_ = ctrl.Close()
			if l.err == nil {
				l.err = err
			}
			continue
		}
		cmds = append(cmds, ctrl.SetSource(windowSource))
		l.overlays = append(l.overlays, &layerOverlay{
			cells:      g.cells,
			window:     g.window,
			resolution: g.resolution,
			ctrl:       ctrl,
		})
	}
	return tea.Batch(cmds...)
}

// planLayers converts composite pixel regions into cell-aligned windows clipped
// to the visible cell grid, merging overlapping cell windows (including
// transitive and union-expanded overlaps) at the finer of their resolutions.
func planLayers(comp *animation.Composite, cols, rows, cw, ch int) []layerGroup {
	if comp == nil || cols <= 0 || rows <= 0 {
		return nil
	}
	cw, ch = max(1, cw), max(1, ch)
	bounds := comp.Image().Bounds()
	if bounds.Empty() {
		return nil
	}
	grid := image.Rect(0, 0, cols, rows)
	var groups []layerGroup
	for _, r := range comp.Regions() {
		px := r.Rect.Intersect(bounds)
		if px.Empty() || r.Player == nil {
			continue
		}
		rel := px.Sub(bounds.Min)
		cells := image.Rect(
			rel.Min.X/cw,
			rel.Min.Y/ch,
			(rel.Max.X+cw-1)/cw,
			(rel.Max.Y+ch-1)/ch,
		).Intersect(grid)
		if cells.Empty() {
			continue
		}
		own := r.Player.Image().Bounds().Size()
		factor := min(
			1.0,
			float64(max(1, own.X))/float64(max(1, r.Rect.Dx())),
			float64(max(1, own.Y))/float64(max(1, r.Rect.Dy())),
		)
		groups = mergeLayerGroup(groups, layerGroup{cells: cells, resolution: factor})
	}
	out := groups[:0]
	for _, g := range groups {
		c := g.cells
		window := image.Rect(
			bounds.Min.X+c.Min.X*cw,
			bounds.Min.Y+c.Min.Y*ch,
			bounds.Min.X+c.Max.X*cw,
			bounds.Min.Y+c.Max.Y*ch,
		).Intersect(bounds)
		if window.Empty() {
			continue
		}
		g.window = window
		out = append(out, g)
	}
	return out
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
	lines := strings.Split(grid, "\n")
	for _, o := range l.overlays {
		if !o.presented() {
			continue
		}
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
