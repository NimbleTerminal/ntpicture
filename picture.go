package ntpicture

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"
	"nimbleterminal.dev/ntpicture/internal/imageview"
	"nimbleterminal.dev/ntpicture/internal/kittyshm"
)

// MaxImagePixels bounds snapshots and prepared images in Picture.
const MaxImagePixels = 32 << 20

var ErrImageIDExhausted = errors.New("ntpicture: positive 24-bit image IDs exhausted")
var pictureIDs = func() *atomic.Uint32 {
	ids := new(atomic.Uint32)
	// Leave at least half the range for this process. Randomization reduces
	// collisions with images kept by earlier processes; it is not a lease.
	ids.Store(rand.Uint32N(1 << 23))
	return ids
}()

// NextImageID allocates one fresh positive 24-bit ID, never a block of IDs.
// Hosts mixing Picture with other image components must use the same allocator
// everywhere. IDs are not reused; exhaustion is reported rather than wrapping.
// The starting offset is randomized to reduce cross-process keep-screen
// collisions. Hosts requiring collision-free terminal history must supply NextID.
func NextImageID() (int, error) {
	for {
		old := pictureIDs.Load()
		if old >= (1<<24)-1 {
			return 0, ErrImageIDExhausted
		}
		if pictureIDs.CompareAndSwap(old, old+1) {
			return int(old + 1), nil
		}
	}
}

// Options configures a Picture. Render.KittyID is ignored: new placements
// use NextID. The zero value renders glyphs until SetMode selects Kitty.
type Options struct {
	// Session shares discovery, cell geometry, and transport settings. When nil,
	// New allocates an owned TerminalSession initialized from Render cell
	// dimensions and closes it when Close or Keep is called.
	Session *TerminalSession
	Render  Config
	NextID  func() (int, error)
	// AutoMode selects PictureKitty automatically when the picture's terminal
	// session reports KittyCapabilitySupported (at construction or during
	// Update) and PictureGlyph otherwise. Explicit SetMode(PictureGlyph),
	// SetMode(PictureKitty), ToggleMode, SetTerminal, or Submit calls override
	// automatic selection until SetMode(PictureAuto) is called. Ignored when
	// HostGeometry is true.
	AutoMode bool
	// ClearOnReplace removes old content immediately, as required for QR codes.
	// Otherwise the displayed picture remains until its replacement is ready.
	ClearOnReplace bool
	// StableID reuses the displayed Kitty image ID while cell geometry and
	// raster dimensions are unchanged, keeping View identical. Pixels update
	// on transmission, before PresentedMsg; output already sent cannot be
	// recalled. Requires Kitty animation-frame support (Ghostty 1.4+).
	// Resize, mode changes, hiding and clearing start a fresh ID.
	// ClearOnReplace takes precedence. Glyph rendering is unaffected.
	StableID bool
	// RetireDelay defaults to 50ms. A negative duration means immediate retirement.
	// This is a Bubble Tea frame-ordering grace period, not a terminal acknowledgement.
	RetireDelay time.Duration
	// HostGeometry makes Update ignore terminal events: cell-size reports, Kitty
	// replies, probe ticks and session notifications. The host forwards them to
	// its TerminalSession (or legacy.DefaultTerminalSession) and applies geometry
	// with SetTerminal or Submit, so a stale raster is never re-prepared first.
	HostGeometry bool
}

// Status is an event-loop snapshot. Presented identifies the last committed
// generation, which can lag Generation while an image is being prepared.
type Status struct {
	Generation, Presented uint64
	Busy                  bool
	Err                   error
	Format                KittyFormat
	Medium                KittyMedium
}

// PresentedMsg reports ordered output/grid completion, not terminal display
// acknowledgement. Owner and Generation let independent producers pace work.
type PresentedMsg struct {
	Owner      *Picture
	Generation uint64
}

// ErrorMsg reports a failed submission. A retained front remains visible.
type ErrorMsg struct {
	Owner      *Picture
	Generation uint64
	Err        error
}

// Picture renders still or continuously supplied images. It owns replacement,
// transmission pacing and placement cleanup. All methods belong to one Bubble
// Tea event loop; do not copy it. Run every returned command, including Close.
// Forward messages to Update even while hidden. The host owns keys and layout.
//
// Picture, playback.Controller, and playback.Layers share the same presentation
// methods (Init, Update, View, geometry/mode, Viewport, SetVisible, Stop, Keep,
// KeepOnMainScreen, and Close) so hosts can manage mixed still and animated
// images through a small local interface.
type Picture struct {
	frameEpoch uint64

	opts                  Options
	ownsSession           bool
	mode                  PictureMode
	autoMode              bool
	cols, rows            int
	visible, closed       bool
	keptView              string
	viewport              Viewport
	viewportSet           bool
	img                   image.Image
	imgID                 uint64
	generation, presented uint64
	front                 *picturePlacement
	job                   *pictureJob
	dirty                 bool
	err                   error
	format                KittyFormat
	medium                KittyMedium
	retired               []*picturePlacement
	retiredSHM            []*kittyshm.Object
	retirementPending     bool
	lifetime              context.Context
	closeCancel           context.CancelFunc
	restoreCancel         context.CancelFunc
}

type picturePlacement struct {
	epoch  uint64
	raster *image.NRGBA
	frames *pictureFrames

	cfg         Config
	mode        PictureMode
	cols, rows  int
	rasterSize  image.Point
	transport   kittyTransport
	kittyShared *kittySharedState
	image       image.Image
	imgID       uint64
	panSource   bool
	panPutOnly  bool
	panCrop     image.Rectangle
	grid        string
	generation  uint64
	retireAt    time.Time
	active      atomic.Bool
	lease       *pictureImageID
	ownsID      bool
	delete      tea.Cmd // Immutable; safe to use after the owner has been removed.
}

// A streaming replacement borrows its front's ID until commit. Cleanup of a
// cancelled borrower must never delete the still-displayed image.
type pictureImageID struct {
	active      atomic.Bool
	overwritten atomic.Bool
	outputMu    sync.Mutex
	outputDone  <-chan struct{}
}

func (id *pictureImageID) setOutputDone(ch <-chan struct{}) {
	if id == nil {
		return
	}
	id.outputMu.Lock()
	id.outputDone = ch
	id.outputMu.Unlock()
}

func (id *pictureImageID) clearOutputDone(ch <-chan struct{}) {
	if id == nil {
		return
	}
	id.outputMu.Lock()
	if id.outputDone == ch {
		id.outputDone = nil
	}
	id.outputMu.Unlock()
}

func (id *pictureImageID) loadOutputDone() <-chan struct{} {
	if id == nil {
		return nil
	}
	id.outputMu.Lock()
	defer id.outputMu.Unlock()
	return id.outputDone
}

func (f *picturePlacement) deactivate() {
	if f == nil {
		return
	}
	if f.lease == nil {
		f.active.Store(false)
		return
	}
	f.lease.outputMu.Lock()
	f.active.Store(false)
	if !f.ownsID && !f.lease.overwritten.Load() {
		f.lease.outputDone = nil
	}
	f.lease.outputMu.Unlock()
}

type pictureJob struct {
	placement  *picturePlacement
	ctx        context.Context
	cancel     context.CancelFunc
	outputDone chan struct{} // Closed by accepted transmission completion.
	fromConfig bool
	started    atomic.Bool
	run        func() tea.Msg
	replacedBy atomic.Pointer[pictureJob]
}

func (j *pictureJob) execute() tea.Msg {
	cur := j
	for {
		next := cur.replacedBy.Load()
		if next == nil {
			break
		}
		cur = next
	}
	if !cur.started.CompareAndSwap(false, true) {
		return nil
	}
	return cur.run()
}

type picturePrepared struct {
	owner *Picture
	job   *pictureJob
	frame tea.Msg
}
type pictureReady struct {
	owner  *Picture
	job    *pictureJob
	grid   string
	format KittyFormat
	medium KittyMedium
}
type pictureRetire struct{ owner *Picture }

// New creates a Picture. Terminal probing remains explicit: run Init and
// select Kitty after affirmative detection (or enable Options.AutoMode), or
// call SetTerminal with a known host capability and cell geometry. Application
// UI is never rendered here.
func New(opts Options) *Picture {
	if opts.NextID == nil {
		opts.NextID = NextImageID
	}
	if opts.RetireDelay == 0 {
		opts.RetireDelay = 50 * time.Millisecond
	}
	autoMode := opts.AutoMode && !opts.HostGeometry
	mode := PictureGlyph
	ownsSession := opts.Session == nil
	if ownsSession {
		opts.Session = NewTerminalSession(TerminalOptions{
			CellPixelWidth:  opts.Render.CellPixelWidth,
			CellPixelHeight: opts.Render.CellPixelHeight,
		})
	}
	state := opts.Session.Snapshot()
	opts.Render.CellPixelWidth, opts.Render.CellPixelHeight = state.CellPixelWidth, state.CellPixelHeight
	if autoMode && state.Kitty == KittyCapabilitySupported {
		mode = PictureKitty
	}
	// Normalize all renderer defaults once and preserve them across replacements.
	opts.Render = normalizeConfig(opts.Render)
	opts.Render.Background = color.RGBA64Model.Convert(opts.Render.Background)
	ctx, cancel := context.WithCancel(context.Background())
	return &Picture{opts: opts, ownsSession: ownsSession, mode: mode, autoMode: autoMode, visible: true, lifetime: ctx, closeCancel: cancel}
}

func (p *Picture) Init() tea.Cmd {
	if p.closed {
		return nil
	}
	sess := p.session()
	cmd := sess.Init()
	if p.ownsSession {
		if kitty := sess.Snapshot().Kitty; kitty != KittyCapabilityUnknown {
			return tea.Batch(cmd, kittyCapabilityCommand(kitty))
		}
	}
	return cmd
}
func (p *Picture) Status() Status {
	return Status{p.generation, p.presented, !p.closed && (p.job != nil || p.dirty), p.err, p.format, p.medium}
}
func (p *Picture) Mode() PictureMode { return p.mode }

// AutoMode reports whether the picture automatically selects PictureKitty or
// PictureGlyph from its terminal session capability.
func (p *Picture) AutoMode() bool {
	return p != nil && p.autoMode
}

// Size reports the configured cell dimensions (cols, rows).
func (p *Picture) Size() (cols, rows int) {
	if p == nil {
		return 0, 0
	}
	return p.cols, p.rows
}

// Visible reports whether the picture is currently visible.
func (p *Picture) Visible() bool {
	return p != nil && p.visible
}

func (p *Picture) CellPixelSize() (int, int) {
	return p.opts.Render.CellPixelWidth, p.opts.Render.CellPixelHeight
}

// Closed reports whether Close or Keep has permanently stopped the picture.
// A nil receiver reports true.
func (p *Picture) Closed() bool {
	return p == nil || p.closed
}

// Session returns the picture's terminal session.
func (p *Picture) Session() *TerminalSession {
	return p.session()
}

// TerminalState returns a snapshot of the picture's terminal session.
func (p *Picture) TerminalState() TerminalState {
	return p.session().Snapshot()
}

// KittySupported reports the Kitty graphics capability of the picture's
// terminal session.
func (p *Picture) KittySupported() KittyCapability {
	return p.TerminalState().Kitty
}

// SetImage copies pixels before returning. The caller may then reuse its image.
// Pass nil to clear. Failed validation reports ErrorMsg and retains the front
// unless ClearOnReplace is set.
func (p *Picture) SetImage(img image.Image) tea.Cmd {
	if p.closed {
		return nil
	}
	if img == nil {
		return p.SetImageImmutable(nil)
	}
	if err := checkImage(img); err != nil {
		return p.failSubmission(err)
	}
	return p.SetImageImmutable(snapshotImage(img))
}
func snapshotImage(img image.Image) *image.RGBA {
	b := img.Bounds()
	snapshot := image.NewRGBA(b)
	draw.Draw(snapshot, b, imageview.Unwrap(img), b.Min, draw.Src)
	return snapshot
}

// SetImageImmutable avoids a pixel copy. The caller promises never to mutate
// the supplied pixels while Picture or an in-flight command can retain them.
// A producer reusing a framebuffer must use SetImage instead.
func (p *Picture) SetImageImmutable(img image.Image) tea.Cmd {
	if p.closed {
		return nil
	}
	if img != nil {
		if err := checkImage(img); err != nil {
			return p.failSubmission(err)
		}
	}
	p.img = img
	p.imgID++
	return p.changed()
}

// SetImageCropped atomically updates the source-pixel crop rectangle and copies
// pixels from img in a single generation without scheduling an intermediate job
// against the previous image. Pass an empty crop for the full source image.
func (p *Picture) SetImageCropped(img image.Image, crop image.Rectangle) tea.Cmd {
	if p == nil || p.closed {
		return nil
	}
	if img == nil {
		return p.SetImageImmutableCropped(nil, crop)
	}
	if err := checkImage(img); err != nil {
		return p.failSubmission(err)
	}
	return p.SetImageImmutableCropped(snapshotImage(img), crop)
}

// SetImageImmutableCropped atomically updates the source-pixel crop rectangle
// and sets an immutable source image in a single generation without scheduling
// an intermediate job against the previous image. Pass an empty crop for the
// full source image.
func (p *Picture) SetImageImmutableCropped(img image.Image, crop image.Rectangle) tea.Cmd {
	if p == nil || p.closed {
		return nil
	}
	if img != nil {
		if err := checkImage(img); err != nil {
			return p.failSubmission(err)
		}
	}
	p.opts.Render.Crop = normalizeCrop(crop)
	p.img = img
	p.imgID++
	return p.changed()
}
func checkImage(img image.Image) error {
	b := img.Bounds()
	if b.Empty() || b.Dx() <= 0 || b.Dy() <= 0 || b.Dx() > MaxImagePixels/b.Dy() {
		return fmt.Errorf("ntpicture: image exceeds %d pixels or is empty", MaxImagePixels)
	}
	return nil
}
func (p *Picture) reportError(err error) tea.Cmd {
	p.err = err
	generation := p.generation
	return func() tea.Msg { return ErrorMsg{p, generation, err} }
}
func (p *Picture) frontNeedsPrepare() bool {
	if p.front == nil || p.opts.ClearOnReplace || !p.visible || p.img == nil || p.cols <= 0 || p.rows <= 0 {
		return false
	}
	if p.front.lease != nil && (p.front.lease.overwritten.Load() || p.front.lease.loadOutputDone() != nil) {
		return true
	}
	cfg := p.opts.Render
	if p.front.mode != p.mode || p.front.cols != p.cols || p.front.rows != p.rows ||
		p.front.cfg.Fit != cfg.Fit || p.front.cfg.Anchor != cfg.Anchor ||
		p.front.cfg.Crop != cfg.Crop || p.front.cfg.Background != cfg.Background {
		return true
	}
	if p.mode == PictureKitty {
		if p.front.cfg.CellPixelWidth != cfg.CellPixelWidth ||
			p.front.cfg.CellPixelHeight != cfg.CellPixelHeight ||
			p.front.rasterSize != p.RasterGeometry().Pixels ||
			p.front.cfg.KittyFormat != cfg.KittyFormat ||
			p.front.cfg.KittyMedium != cfg.KittyMedium ||
			p.front.cfg.KittyZ != cfg.KittyZ {
			return true
		}
	}
	return false
}
func (p *Picture) failSubmission(err error) tea.Cmd {
	p.generation++
	p.err = err
	p.dirty = false
	if p.opts.ClearOnReplace && p.visible {
		p.img, p.imgID = nil, 0
	} else if p.front != nil {
		p.img, p.imgID = p.front.image, p.front.imgID
	} else if p.visible && p.cols > 0 && p.rows > 0 {
		p.img, p.imgID = nil, 0
	}
	if p.job != nil {
		p.job.cancel()
		p.job.placement.deactivate()
	}
	var clear tea.Cmd
	if p.opts.ClearOnReplace {
		clear = p.clearFront()
	} else if p.frontNeedsPrepare() {
		p.dirty = true
	}
	return tea.Batch(clear, p.reportError(err), p.schedule())
}
func (p *Picture) failPreparedJob(j *pictureJob, err error) tea.Cmd {
	p.generation++
	p.err = err
	p.dirty = false
	j.cancel()
	p.job = nil
	cleanup := p.dispose(j.placement)
	if p.opts.ClearOnReplace && p.visible {
		p.img, p.imgID = nil, 0
		return tea.Batch(cleanup, p.clearFront(), p.reportError(err))
	}
	if p.front != nil {
		failedImgID := j.placement.imgID
		p.img, p.imgID = p.front.image, p.front.imgID
		if failedImgID != p.front.imgID && p.frontNeedsPrepare() {
			p.dirty = true
		}
	} else if p.visible && p.cols > 0 && p.rows > 0 {
		p.img, p.imgID = nil, 0
	}
	return tea.Batch(cleanup, p.reportError(err), p.schedule())
}

func (p *Picture) SetSize(cols, rows int) tea.Cmd {
	if p.closed {
		return nil
	}
	cols, rows = max(0, cols), max(0, rows)
	if cols == p.cols && rows == p.rows {
		return nil
	}
	oldCols, oldRows := p.cols, p.rows
	p.cols, p.rows = cols, rows
	if cols > 0 && rows > 0 {
		if err := p.geometryError(); err != nil {
			p.cols, p.rows = oldCols, oldRows
			return p.reportError(err)
		}
	}
	return p.changedConfig()
}

// SetTerminal applies an explicit host graphics decision without probing or
// consulting process-global capability state. Cell dimensions must be positive.
func (p *Picture) SetTerminal(kitty bool, cellWidth, cellHeight int) tea.Cmd {
	if p.closed {
		return nil
	}
	if cellWidth <= 0 || cellHeight <= 0 {
		return p.reportError(errors.New("ntpicture: invalid cell geometry"))
	}
	oldAuto := p.autoMode
	p.autoMode = false
	cmd, ok := p.applyTerminalStatus(kitty, cellWidth, cellHeight)
	if !ok {
		p.autoMode = oldAuto
		return cmd
	}
	if p.ownsSession && p.opts.Session != nil {
		p.opts.Session.setTerminalKitty(kitty)
	}
	return cmd
}

// SetCellSize updates cell pixel dimensions while preserving the current
// rendering mode and AutoMode setting. Cell dimensions must be positive.
func (p *Picture) SetCellSize(cellWidth, cellHeight int) tea.Cmd {
	if p == nil || p.closed {
		return nil
	}
	if cellWidth <= 0 || cellHeight <= 0 {
		return p.reportError(errors.New("ntpicture: invalid cell geometry"))
	}
	return p.applyTerminal(p.mode == PictureKitty, cellWidth, cellHeight)
}

// SetKittyCapability updates the picture's terminal session capability while
// preserving AutoMode. When AutoMode is active, the picture switches between
// PictureKitty and PictureGlyph to match the updated capability.
func (p *Picture) SetKittyCapability(c KittyCapability) tea.Cmd {
	if p == nil || p.closed {
		return nil
	}
	sess := p.session()
	beforeKitty := sess.Snapshot().Kitty
	cmd := sess.SetKittyCapability(c)
	state := sess.Snapshot()
	if p.ownsSession && state.Kitty != beforeKitty && state.Kitty != KittyCapabilityUnknown {
		cmd = tea.Batch(cmd, kittyCapabilityCommand(state.Kitty))
	}
	if !p.autoMode {
		return cmd
	}
	cw, ch := p.CellPixelSize()
	return tea.Batch(cmd, p.applyTerminal(state.Kitty == KittyCapabilitySupported, cw, ch))
}

func (p *Picture) applyTerminal(kitty bool, cellWidth, cellHeight int) tea.Cmd {
	cmd, _ := p.applyTerminalStatus(kitty, cellWidth, cellHeight)
	return cmd
}

func (p *Picture) applyTerminalStatus(kitty bool, cellWidth, cellHeight int) (tea.Cmd, bool) {
	if p.closed {
		return nil, false
	}
	if cellWidth <= 0 || cellHeight <= 0 {
		return p.reportError(errors.New("ntpicture: invalid cell geometry")), false
	}
	mode := PictureGlyph
	if kitty {
		mode = PictureKitty
	}
	c := &p.opts.Render
	if mode == p.mode && cellWidth == c.CellPixelWidth && cellHeight == c.CellPixelHeight {
		if p.ownsSession && p.opts.Session != nil {
			p.opts.Session.setCellSize(cellWidth, cellHeight)
		}
		return nil, true
	}
	oldMode, oldConfig := p.mode, *c
	p.mode = mode
	c.CellPixelWidth, c.CellPixelHeight = cellWidth, cellHeight
	if p.cols > 0 && p.rows > 0 {
		if err := p.geometryError(); err != nil {
			p.mode = oldMode
			*c = oldConfig
			return p.reportError(err), false
		}
	}
	if p.ownsSession && p.opts.Session != nil {
		p.opts.Session.setCellSize(cellWidth, cellHeight)
	}
	return p.changedConfig(), true
}

// SetMode uses the picture’s terminal session capability. Pass PictureAuto to
// follow the session's Kitty capability automatically. Hosts with known
// capability can use SetTerminal instead. An unavailable Kitty mode leaves the
// mode unchanged.
func (p *Picture) SetMode(mode PictureMode) tea.Cmd {
	if p.closed {
		return nil
	}
	if mode == PictureAuto {
		oldAuto := p.autoMode
		p.autoMode = true
		cw, ch := p.CellPixelSize()
		if p.opts.Session != nil {
			st := p.opts.Session.Snapshot()
			cw, ch = st.CellPixelWidth, st.CellPixelHeight
		}
		cmd, ok := p.applyTerminalStatus(p.KittySupported() == KittyCapabilitySupported, cw, ch)
		if !ok {
			p.autoMode = oldAuto
		}
		return cmd
	}
	if mode != PictureGlyph && mode != PictureKitty {
		return p.reportError(errors.New("ntpicture: invalid picture mode"))
	}
	if mode == PictureKitty {
		if err := p.session().unavailable(); err != nil {
			return p.reportError(err)
		}
	}
	if mode == p.mode {
		p.autoMode = false
		return nil
	}
	cw, ch := p.CellPixelSize()
	oldAuto := p.autoMode
	p.autoMode = false
	cmd, ok := p.applyTerminalStatus(mode == PictureKitty, cw, ch)
	if !ok {
		p.autoMode = oldAuto
	}
	return cmd
}

// ToggleMode switches between PictureGlyph and PictureKitty. When currently in
// PictureGlyph, it switches to PictureKitty only if KittySupported() reports
// KittyCapabilitySupported; otherwise it is a no-op and does not report an
// error. Explicit toggling disables AutoMode until SetMode(PictureAuto) is called.
func (p *Picture) ToggleMode() tea.Cmd {
	if p == nil || p.closed {
		return nil
	}
	if p.mode == PictureGlyph {
		if p.KittySupported() != KittyCapabilitySupported {
			p.autoMode = false
			return nil
		}
		return p.SetMode(PictureKitty)
	}
	return p.SetMode(PictureGlyph)
}

// SetVisible hides placements and cancels pending work while retaining source
// pixels. Showing the component prepares its latest source again.
func (p *Picture) SetVisible(visible bool) tea.Cmd {
	if p.closed || p.visible == visible {
		return nil
	}
	p.visible = visible
	return p.changed()
}

func (p *Picture) changed() tea.Cmd {
	return p.changedWith(false)
}

func (p *Picture) changedConfig() tea.Cmd {
	p.frameEpoch++
	return p.changedWith(true)
}

func (p *Picture) changedWith(fromConfig bool) tea.Cmd {
	p.cancelRestore()
	p.generation++
	p.err = nil
	p.dirty = true
	var aborted *pictureJob
	if p.job != nil {
		p.job.cancel()
		p.job.placement.deactivate()
		if p.job.fromConfig && p.job.started.CompareAndSwap(false, true) {
			if p.job.placement.ownsID && p.job.placement.lease != nil {
				p.job.placement.lease.active.Store(false)
			}
			p.job.placement.kittyShared.release()
			aborted = p.job
			p.job = nil
		}
	}
	var cleanup tea.Cmd
	if !p.visible || p.img == nil || p.cols == 0 || p.rows == 0 || p.opts.ClearOnReplace {
		cleanup = p.clearFront()
	}
	sched := p.scheduleWithAborted(aborted)
	if p.job != nil && fromConfig {
		p.job.fromConfig = true
	}
	if aborted != nil && p.job != nil {
		aborted.replacedBy.Store(p.job)
	}
	return tea.Batch(cleanup, sched)
}

func (p *Picture) geometryError() error {
	c := p.opts.Render
	if p.cols > MaxImagePixels/c.CellPixelWidth || p.rows > MaxImagePixels/c.CellPixelHeight {
		return errors.New("ntpicture: cell geometry exceeds pixel limit")
	}
	w, h := p.cols*c.CellPixelWidth, p.rows*c.CellPixelHeight
	if h <= 0 || w > MaxImagePixels/h {
		return errors.New("ntpicture: prepared image exceeds pixel limit")
	}
	// Glyph preparation uses two pixel rows per cell regardless of terminal
	// geometry. Check before multiplication, including on 32-bit platforms.
	if p.mode == PictureGlyph && (p.rows > MaxImagePixels/2 || p.cols > MaxImagePixels/(2*p.rows)) {
		return errors.New("ntpicture: glyph raster exceeds pixel limit")
	}
	// Every placeholder needs a distinct row/column diacritic. The low-level
	// legacy model leaves this to its caller; Picture validates before allocating.
	if p.mode == PictureKitty && (kitty.Diacritic(p.cols-1) == kitty.Diacritic(0) && p.cols > 1 || kitty.Diacritic(p.rows-1) == kitty.Diacritic(0) && p.rows > 1) {
		return errors.New("ntpicture: placement exceeds Kitty placeholder grid")
	}
	return nil
}

func (p *Picture) schedule() tea.Cmd {
	return p.scheduleWithAborted(nil)
}

func (p *Picture) scheduleWithAborted(aborted *pictureJob) tea.Cmd {
	if p.closed || p.job != nil || !p.dirty {
		return nil
	}
	p.dirty = false
	if !p.visible || p.img == nil || p.cols == 0 || p.rows == 0 {
		return nil
	}
	if err := p.geometryError(); err != nil {
		return p.reportError(err)
	}
	cfg := p.opts.Render
	raster := p.RasterGeometry().Pixels
	transport := p.session().transport()
	panCrop, canPan := p.canKittyPan(p.img, cfg, transport)
	var lease *pictureImageID
	sameGrid := !p.opts.ClearOnReplace && p.mode == PictureKitty && p.front != nil &&
		p.front.mode == PictureKitty && p.front.cols == p.cols && p.front.rows == p.rows &&
		p.front.cfg.CellPixelWidth == cfg.CellPixelWidth && p.front.cfg.CellPixelHeight == cfg.CellPixelHeight && p.front.rasterSize == raster
	panPutOnly := sameGrid && canPan && p.front.panSource && p.front.imgID == p.imgID &&
		p.front.cfg.KittyFormat == cfg.KittyFormat && p.front.cfg.KittyMedium == cfg.KittyMedium &&
		p.front.cfg.KittyZ == cfg.KittyZ && p.front.cfg.Transient == cfg.Transient &&
		p.front.lease != nil && !p.front.lease.overwritten.Load() && p.front.lease.loadOutputDone() == nil
	if sameGrid && p.front.panSource == canPan && (p.opts.StableID || (canPan && p.front.imgID == p.imgID)) {
		lease = p.front.lease
		cfg.KittyID = p.front.cfg.KittyID
	}
	ownsID := false
	if p.mode == PictureKitty && lease == nil && aborted != nil && aborted.placement != nil &&
		aborted.placement.mode == PictureKitty && aborted.placement.ownsID && aborted.placement.lease != nil && aborted.placement.cfg.KittyID > 0 {
		aborted.placement.ownsID = false
		aborted.placement.kittyShared.release()
		lease, ownsID = aborted.placement.lease, true
		lease.active.Store(true)
		cfg.KittyID = aborted.placement.cfg.KittyID
	}
	if p.mode == PictureKitty && lease == nil {
		id, err := p.opts.NextID()
		if err == nil && (id <= 0 || id >= 1<<24) {
			err = ErrImageIDExhausted
		}
		if err != nil {
			return p.reportError(err)
		}
		cfg.KittyID = id
	}
	transport.transient = cfg.Transient
	placement := &picturePlacement{
		epoch:       p.frameEpoch,
		cfg:         cfg,
		mode:        p.mode,
		cols:        p.cols,
		rows:        p.rows,
		rasterSize:  raster,
		transport:   transport,
		kittyShared: &kittySharedState{},
		image:       p.img,
		imgID:       p.imgID,
		panSource:   canPan,
		panPutOnly:  panPutOnly,
		panCrop:     panCrop,
		generation:  p.generation,
	}
	placement.active.Store(true)
	if p.mode == PictureKitty {
		placement.lease, placement.ownsID = lease, ownsID
		if lease == nil {
			lease = new(pictureImageID)
			lease.active.Store(true)
			placement.lease, placement.ownsID = lease, true
		}
		deleteImage := transport.kittyDeleteImage(cfg.KittyID)
		placement.delete = func() tea.Msg {
			if lease.active.Load() {
				return nil
			}
			return tea.RawMsg{Msg: deleteImage}
		}
	}
	ctx, cancel := context.WithCancel(p.lifetime)
	job := &pictureJob{placement: placement, ctx: ctx, cancel: cancel}
	p.job = job
	img, mode, cols, rows := p.img, p.mode, p.cols, p.rows
	if panPutOnly {
		frontGrid, frontFormat, frontMedium := p.front.grid, p.format, p.medium
		putAPC := transport.buildKittyPutAPC(cfg.KittyID, kittyPanPlacementID, cols, rows, panCrop, cfg.KittyZ)
		job.run = func() tea.Msg {
			var frame tea.Msg
			if ctx.Err() == nil {
				frame = kittyFrameMsg{ID: cfg.KittyID, Seq: 1, APC: putAPC, Grid: frontGrid, Format: frontFormat, Medium: frontMedium}
			}
			return picturePrepared{p, job, frame}
		}
		return job.execute
	}
	var params kittyPrepareParams
	var selectFrame string
	var cached bool
	if mode == PictureKitty {
		cpw := max(1, int(float64(cfg.CellPixelWidth)*cfg.KittyResolutionFactor))
		cph := max(1, int(float64(cfg.CellPixelHeight)*cfg.KittyResolutionFactor))
		params = kittyPrepareParams{
			seq:        1,
			img:        img,
			kittyZ:     cfg.KittyZ,
			kittyID:    cfg.KittyID,
			background: cfg.Background,
			fit:        cfg.Fit,
			anchor:     cfg.Anchor,
			crop:       cfg.Crop,
			panSource:  canPan,
			panCrop:    panCrop,
			format:     cfg.KittyFormat,
			medium:     cfg.KittyMedium,
			cols:       cols,
			rows:       rows,
			cpw:        cpw,
			cph:        cph,
			rasterSize: raster,
			transport:  transport,
			shared:     placement.kittyShared,
		}
		// Whole-source pan uploads and animation-frame rasters have different
		// geometry and ownership. Keep their preparation paths separate.
		if !canPan {
			params.retain = p.opts.StableID && !p.opts.ClearOnReplace
			selectFrame, cached = p.configureFrames(&params, placement)
		}
		params.shared.begin(params.seq)
	}
	job.run = func() tea.Msg {
		var frame tea.Msg
		if ctx.Err() == nil {
			if mode == PictureKitty {
				if cached {
					frame = kittyFrameMsg{APC: selectFrame, Grid: buildKittyGrid(cols, rows, cfg.KittyID), Format: cfg.KittyFormat, Medium: KittyMediumDirect}
				} else if msg, ok := prepareKittyFrame(ctx, params); ok {
					msg.APC += selectFrame
					frame = msg
				}
			} else {
				var scratch fitScratch
				rendered := prepareSourceCropWithScratch(img, cfg.Crop, cfg.Fit, cols, rows, 1, 2, cfg.Background, cfg.Anchor, &scratch)
				frame = string(appendGlyph(nil, rendered))
			}
		}
		return picturePrepared{p, job, frame}
	}
	return job.execute
}

// Update routes all internal messages by owner and job identity. Superseded
// work is drained before another preparation starts: cancellation never opens
// an unbounded second worker. Unknown and foreign messages are ignored.
func (p *Picture) Update(msg tea.Msg) tea.Cmd {
	if p.opts.HostGeometry {
		switch msg.(type) {
		case uv.CellSizeEvent, uv.KittyGraphicsEvent, terminalSessionTick, kittyProbeTickMsg, TerminalSessionMsg, KittyCapabilityMsg:
			return nil
		}
	}
	sess := p.session()
	terminal := false
	switch v := msg.(type) {
	case uv.CellSizeEvent, uv.KittyGraphicsEvent, terminalSessionTick, kittyProbeTickMsg:
		terminal = true
	case TerminalSessionMsg:
		if v.Session != sess {
			return nil
		}
		terminal = true
	case KittyCapabilityMsg:
		if !p.ownsSession {
			return nil
		}
		if v.Capability != KittyCapabilityUnknown && v.Capability != KittyCapabilityUnsupported && v.Capability != KittyCapabilitySupported {
			return nil
		}
		sess.setKitty(v.Capability)
		state := sess.Snapshot()
		kitty := p.mode == PictureKitty
		if p.autoMode {
			kitty = state.Kitty == KittyCapabilitySupported
		}
		return p.applyTerminal(kitty, state.CellPixelWidth, state.CellPixelHeight)
	}
	if terminal {
		beforeKitty := sess.Snapshot().Kitty
		cmd := sess.Update(msg)
		state := sess.Snapshot()
		if p.ownsSession && state.Kitty != beforeKitty && state.Kitty != KittyCapabilityUnknown {
			cmd = tea.Batch(cmd, kittyCapabilityCommand(state.Kitty))
		}
		kitty := p.mode == PictureKitty
		if p.autoMode {
			kitty = state.Kitty == KittyCapabilitySupported
		}
		return tea.Batch(cmd, p.applyTerminal(kitty, state.CellPixelWidth, state.CellPixelHeight))
	}
	switch v := msg.(type) {
	case picturePrepared:
		if v.owner != p || v.job != p.job {
			return nil
		}
		j := v.job
		if p.closed || j.ctx.Err() != nil || j.placement.generation != p.generation {
			j.cancel()
			p.job = nil
			if !p.closed && !p.dirty && p.frontNeedsPrepare() {
				p.dirty = true
			}
			sched := p.scheduleWithAborted(j)
			var cleanup tea.Cmd
			if p.closed || j.placement.ownsID {
				cleanup = p.dispose(j.placement)
			} else {
				j.placement.deactivate()
				j.placement.kittyShared.release()
			}
			return tea.Batch(cleanup, sched)
		}
		if text, ok := v.frame.(string); ok {
			j.placement.grid = text
			return p.commit(j, p.opts.Render.KittyFormat, KittyMediumDirect)
		}
		f, ok := v.frame.(kittyFrameMsg)
		if !ok || (f.APC == "" && !f.unchanged) {
			return p.failPreparedJob(j, errors.New("ntpicture: image preparation failed"))
		}
		j.placement.raster = f.raster
		if f.unchanged {
			// No new transfer consumed the old shared object. Move ownership so
			// retiring the previous placement cannot release it prematurely.
			j.placement.kittyShared = p.front.kittyShared
			p.front.kittyShared = &kittySharedState{}
			j.placement.grid = f.Grid
			return p.commit(j, p.format, p.medium)
		}
		if f.sharedMemory != nil && !j.placement.kittyShared.submit(f.Seq, f.sharedMemory) {
			return p.failPreparedJob(j, errors.New("ntpicture: shared image was retired before transmission"))
		}
		p.cancelRestore()
		j.outputDone = make(chan struct{})
		if !j.placement.ownsID && j.placement.lease != nil {
			j.placement.lease.setOutputDone(j.outputDone)
		}
		ready := func() tea.Msg { return pictureReady{p, j, f.Grid, f.Format, f.Medium} }
		return tea.Sequence(func() tea.Msg {
			if j.placement.lease != nil {
				j.placement.lease.outputMu.Lock()
				if !j.placement.active.Load() {
					if !j.placement.ownsID && j.placement.lease.outputDone == j.outputDone {
						j.placement.lease.outputDone = nil
					}
					j.placement.lease.outputMu.Unlock()
					return nil
				}
				if !j.placement.ownsID {
					j.placement.lease.overwritten.Store(true)
				}
				j.placement.lease.outputMu.Unlock()
			} else if !j.placement.active.Load() {
				return nil
			}
			return tea.RawMsg{Msg: f.APC}
		}, func() tea.Msg {
			defer func() {
				j.placement.lease.clearOutputDone(j.outputDone)
				close(j.outputDone)
			}()
			// A Close/replacement can race with the raw output after acceptance.
			// Cleanup survives removal of this Picture from the host's routing table.
			if !j.placement.active.Load() {
				return tea.Sequence(j.placement.delete, ready)()
			}
			return ready()
		})
	case pictureReady:
		if v.owner != p || v.job != p.job {
			return nil
		}
		if p.closed || v.job.ctx.Err() != nil || v.job.placement.generation != p.generation {
			cleanup := p.dispose(v.job.placement)
			v.job.cancel()
			p.job = nil
			if !p.closed && !p.dirty && p.frontNeedsPrepare() {
				p.dirty = true
			}
			return tea.Batch(cleanup, p.schedule())
		}
		v.job.placement.grid = v.grid
		return p.commit(v.job, v.format, v.medium)
	case pictureRetire:
		if v.owner != p {
			return nil
		}
		p.retirementPending = false
		var cmds []tea.Cmd
		now := time.Now()
		kept := p.retired[:0]
		for _, f := range p.retired {
			if !now.Before(f.retireAt) {
				cmds = append(cmds, p.retirePlacement(f))
			} else {
				kept = append(kept, f)
			}
		}
		clear(p.retired[len(kept):])
		p.retired = kept
		return tea.Batch(append(cmds, p.retireLater())...)
	}
	return nil
}

func (p *Picture) cancelRestore() {
	if p.restoreCancel != nil {
		p.restoreCancel()
		p.restoreCancel = nil
	}
}

func (p *Picture) commit(j *pictureJob, format KittyFormat, medium KittyMedium) tea.Cmd {
	p.cancelRestore()
	var cleanup tea.Cmd
	if old := p.front; old != nil {
		if old.lease != nil && old.lease == j.placement.lease {
			old.ownsID = false
			j.placement.ownsID = true
		}
		if j.placement.panPutOnly {
			j.placement.kittyShared = old.kittyShared
			old.kittyShared = &kittySharedState{}
		}
		if p.opts.RetireDelay < 0 || p.mode == PictureGlyph {
			cleanup = p.retirePlacement(old)
		} else {
			old.retireAt = time.Now().Add(p.opts.RetireDelay)
			p.retired = append(p.retired, old)
			// Bound retained images even when the event loop outruns its timer.
			if len(p.retired) > 2 {
				cleanup = p.retirePlacement(p.retired[0])
				p.retired = p.retired[1:]
			}
		}
	}
	if j.placement.lease != nil {
		j.placement.lease.overwritten.Store(false)
		j.placement.lease.setOutputDone(nil)
	}
	p.front = j.placement
	p.presented = j.placement.generation
	p.format, p.medium = format, medium
	j.cancel()
	p.job = nil
	generation := p.presented
	return tea.Batch(cleanup, p.retireLater(), func() tea.Msg { return PresentedMsg{p, generation} }, p.schedule())
}
func (p *Picture) retireLater() tea.Cmd {
	if p.retirementPending || len(p.retired) == 0 {
		return nil
	}
	p.retirementPending = true
	ctx, delay := p.lifetime, time.Until(p.retired[0].retireAt)
	return func() tea.Msg {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return pictureRetire{p}
		}
	}
}
func (p *Picture) trackRetiredSHM(obj *kittyshm.Object) {
	kept := p.retiredSHM[:0]
	for _, o := range p.retiredSHM {
		if !o.Done() {
			kept = append(kept, o)
		}
	}
	clear(p.retiredSHM[len(kept):])
	p.retiredSHM = kept
	if obj != nil && !obj.Done() {
		p.retiredSHM = append(p.retiredSHM, obj)
	}
}
func (p *Picture) clearRetiredSHM() {
	for _, o := range p.retiredSHM {
		_ = o.Unlink()
	}
	clear(p.retiredSHM)
	p.retiredSHM = nil
}
func (p *Picture) retirePlacement(f *picturePlacement) tea.Cmd {
	if f == nil {
		return nil
	}
	f.deactivate()
	if f.ownsID && f.lease != nil {
		f.lease.active.Store(false)
	}
	if f.mode == PictureKitty {
		p.trackRetiredSHM(f.kittyShared.retire())
	}
	return f.delete
}
func (p *Picture) dispose(f *picturePlacement) tea.Cmd {
	if f == nil {
		return nil
	}
	f.deactivate()
	if f.ownsID && f.lease != nil {
		f.lease.active.Store(false)
	}
	if f.mode == PictureKitty {
		if !f.ownsID && f.lease != nil && f.lease.active.Load() {
			p.trackRetiredSHM(f.kittyShared.retire())
		} else {
			f.kittyShared.release()
		}
	}
	return f.delete
}
func (p *Picture) clearFront() tea.Cmd {
	p.cancelRestore()
	p.clearRetiredSHM()
	cmd := p.dispose(p.front)
	p.front = nil
	return cmd
}
func (p *Picture) clearRetired() tea.Cmd {
	p.clearRetiredSHM()
	var cmds []tea.Cmd
	for _, f := range p.retired {
		cmds = append(cmds, p.dispose(f))
	}
	p.retired = nil
	return tea.Batch(cmds...)
}

// View returns only component content. While a replacement is prepared, its
// front is clipped/padded to current cell dimensions. The host owns loading UI.
func (p *Picture) View() string {
	if p.closed {
		return p.keptView
	}
	if !p.visible || p.front == nil || p.cols == 0 || p.rows == 0 {
		return ""
	}
	if p.viewportSet {
		return p.renderViewport(p.viewport)
	}
	if p.front.cols == p.cols && p.front.rows == p.rows {
		return p.front.grid
	}
	return p.renderViewport(Viewport{Width: p.cols, Height: p.rows})
}

// Stop cancels pending preparation or transmission while preserving the last
// committed View and terminal placement. Late results from cancelled work are
// ignored. In StableID mode, if a cancelled replacement already began raw
// terminal output, Stop's returned command drains that transmission and
// restores the committed front's pixels. Unlike Keep or Close, Stop is not
// final: Close still deletes the retained terminal placement, Keep or
// KeepOnMainScreen still preserves it on exit, and subsequent setters can
// submit new images or configuration changes.
func (p *Picture) Stop() tea.Cmd {
	if p != nil {
		p.frameEpoch++
	}
	if p == nil || p.closed {
		return nil
	}
	p.cancelRestore()
	p.generation++
	p.dirty = false
	p.err = nil
	if p.front != nil {
		p.img, p.imgID = p.front.image, p.front.imgID
		p.front.generation = p.generation
		p.presented = p.generation
	} else {
		p.img, p.imgID = nil, 0
	}
	var pending tea.Cmd
	if p.job != nil {
		p.job.cancel()
		pending = p.dispose(p.job.placement)
		p.job = nil
	}
	front := p.front
	if front == nil || front.mode != PictureKitty || front.lease == nil {
		return pending
	}
	outputDone := front.lease.loadOutputDone()
	overwritten := front.lease.overwritten.Load()
	if outputDone == nil && !overwritten {
		return pending
	}
	cfg := front.cfg
	cpw := max(1, int(float64(cfg.CellPixelWidth)*cfg.KittyResolutionFactor))
	cph := max(1, int(float64(cfg.CellPixelHeight)*cfg.KittyResolutionFactor))
	params := kittyPrepareParams{
		seq:        1,
		img:        front.image,
		kittyZ:     cfg.KittyZ,
		kittyID:    cfg.KittyID,
		background: cfg.Background,
		fit:        cfg.Fit,
		anchor:     cfg.Anchor,
		crop:       cfg.Crop,
		panSource:  front.panSource,
		panCrop:    front.panCrop,
		format:     cfg.KittyFormat,
		medium:     KittyMediumDirect,
		cols:       front.cols,
		rows:       front.rows,
		cpw:        cpw,
		cph:        cph,
		rasterSize: front.rasterSize,
		transport:  front.transport,
		shared:     &kittySharedState{},
	}
	params.shared.begin(params.seq)
	restoreCtx, restoreCancel := context.WithCancel(p.lifetime)
	p.restoreCancel = restoreCancel
	restore := func() tea.Msg {
		defer restoreCancel()
		if outputDone != nil {
			select {
			case <-outputDone:
			case <-restoreCtx.Done():
				return nil
			}
		}
		if restoreCtx.Err() != nil || !front.active.Load() {
			return nil
		}
		frame, ok := prepareKittyFrame(restoreCtx, params)
		if !ok || frame.APC == "" || restoreCtx.Err() != nil || !front.active.Load() {
			return nil
		}
		front.lease.outputMu.Lock()
		if restoreCtx.Err() != nil || !front.active.Load() || front.lease.outputDone != nil {
			front.lease.outputMu.Unlock()
			return nil
		}
		front.lease.overwritten.Store(false)
		front.lease.outputMu.Unlock()
		return tea.RawMsg{Msg: frame.APC}
	}
	return tea.Sequence(pending, restore)
}

const keepScreenSwitchDelay = 35 * time.Millisecond

// Keep permanently stops work while preserving the last presented View and
// terminal image on the current screen. Pending replacements and retired
// placements are discarded. Run the returned command before quitting.
// Shared-memory fronts are sent directly to survive buffer release; direct
// fronts need no retransmission unless an accepted stable-ID replacement may
// have overwritten their pixels. When switching Bubble Tea's View().AltScreen
// from true to false on exit, use KeepOnMainScreen instead so the retained
// Kitty placement is recreated after leaving the alternate screen. Subsequent
// Keep, KeepOnMainScreen, Close and setters are no-ops.
func (p *Picture) Keep() tea.Cmd {
	return p.keep(false)
}

// KeepOnMainScreen permanently stops work like Keep, but always retransmits and
// recreates the retained Kitty placement directly so it survives a terminal
// screen switch from the alternate screen (ESC[?1049l) to the main screen.
//
// In Bubble Tea v2, update model state so View().AltScreen is false and
// View().Content includes p.View(), then return tea.Sequence(p.KeepOnMainScreen(), tea.Quit).
// KeepOnMainScreen paces its raw Kitty transfer behind the renderer's screen
// switch flush so the placement is registered on the main screen after ESC[?1049l.
func (p *Picture) KeepOnMainScreen() tea.Cmd {
	return p.keep(true)
}

func (p *Picture) keep(retransmit bool) tea.Cmd {
	if p == nil || p.closed {
		return nil
	}
	p.cancelRestore()
	p.keptView = p.View()
	front := p.front
	p.front = nil // Close must never delete the kept terminal ID.
	cleanup := p.Close()
	if front == nil || front.mode != PictureKitty {
		return cleanup
	}
	var outputDone <-chan struct{}
	var overwritten bool
	if front.lease != nil {
		outputDone = front.lease.loadOutputDone()
		overwritten = front.lease.overwritten.Load()
	}
	if !retransmit && p.medium == KittyMediumDirect && outputDone == nil && !overwritten {
		// The terminal already owns these pixels on the current screen. Only
		// shared-memory fronts, an accepted borrower of this ID, or a screen
		// switch need an exit-time retransmission.
		front.kittyShared.release()
		return cleanup
	}
	// Capture the committed geometry and source, including during a resize.
	// Fresh parameters avoid geometry-change deletion of the retained ID.
	cfg := front.cfg
	cpw := max(1, int(float64(cfg.CellPixelWidth)*cfg.KittyResolutionFactor))
	cph := max(1, int(float64(cfg.CellPixelHeight)*cfg.KittyResolutionFactor))
	params := kittyPrepareParams{
		seq:        1,
		img:        front.image,
		kittyZ:     cfg.KittyZ,
		kittyID:    cfg.KittyID,
		background: cfg.Background,
		fit:        cfg.Fit,
		anchor:     cfg.Anchor,
		crop:       cfg.Crop,
		panSource:  front.panSource,
		panCrop:    front.panCrop,
		format:     cfg.KittyFormat,
		medium:     KittyMediumDirect,
		cols:       front.cols,
		rows:       front.rows,
		cpw:        cpw,
		cph:        cph,
		rasterSize: front.rasterSize,
		transport:  front.transport,
		shared:     &kittySharedState{},
	}
	params.shared.begin(params.seq)
	generation := p.presented
	startedAt := time.Now()
	keep := func() tea.Msg {
		// Drain accepted output before restoring the committed frame under an
		// ID that a cancelled streaming replacement may already have updated.
		if outputDone != nil {
			<-outputDone
		}
		defer front.kittyShared.release()
		frame, ok := prepareKittyFrame(context.Background(), params)
		if !ok || frame.APC == "" {
			return ErrorMsg{p, generation, errors.New("ntpicture: keep-screen preparation failed")}
		}
		if retransmit {
			if rem := keepScreenSwitchDelay - time.Since(startedAt); rem > 0 {
				time.Sleep(rem)
			}
		}
		return tea.RawMsg{Msg: frame.APC}
	}
	return tea.Sequence(cleanup, keep)
}

// Close cancels work and releases producer resources immediately. After Keep or
// KeepOnMainScreen it is a no-op, including for the retained View. Run the
// returned command before removing the component or quitting to delete its
// terminal placements. Already-accepted transmissions also carry late cleanup.
func (p *Picture) Close() tea.Cmd {
	if p == nil || p.closed {
		return nil
	}
	p.cancelRestore()
	p.closed = true
	p.dirty = false
	p.img = nil
	p.closeCancel()
	if p.ownsSession && p.opts.Session != nil {
		p.opts.Session.Close()
	}
	var pending tea.Cmd
	if p.job != nil {
		p.job.cancel()
		pending = p.dispose(p.job.placement)
		p.job = nil
	}
	return tea.Batch(pending, p.clearFront(), p.clearRetired())
}

func (p *Picture) session() *TerminalSession {
	if p != nil && p.opts.Session != nil {
		return p.opts.Session
	}
	return defaultSession()
}
