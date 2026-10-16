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
	// Session shares discovery, cell geometry, and transport settings. Nil uses
	// the legacy default. Explicit sessions supply initial cell geometry.
	Session *TerminalSession
	Render  Config
	NextID  func() (int, error)
	// ClearOnReplace removes old content immediately, as required for QR codes.
	// Otherwise the displayed picture remains until its replacement is ready.
	ClearOnReplace bool
	// StableID reuses the displayed Kitty image ID while cell geometry and
	// raster dimensions are unchanged, keeping View identical. Pixels update
	// on transmission, before PresentedMsg; output already sent cannot be
	// recalled. Resize, mode changes, hiding and clearing start a fresh ID.
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
type Picture struct {
	opts                  Options
	mode                  PictureMode
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
}

type picturePlacement struct {
	cfg         Config
	mode        PictureMode
	cols, rows  int
	rasterSize  image.Point
	transport   kittyTransport
	kittyShared *kittySharedState
	image       image.Image
	imgID       uint64
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

func (id *pictureImageID) loadOutputDone() <-chan struct{} {
	if id == nil {
		return nil
	}
	id.outputMu.Lock()
	defer id.outputMu.Unlock()
	return id.outputDone
}

type pictureJob struct {
	placement  *picturePlacement
	ctx        context.Context
	cancel     context.CancelFunc
	outputDone chan struct{} // Closed by accepted transmission completion.
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
// select Kitty after affirmative detection, or call SetTerminal with a known
// host capability and cell geometry. Application UI is never rendered here.
func New(opts Options) *Picture {
	if opts.NextID == nil {
		opts.NextID = NextImageID
	}
	if opts.RetireDelay == 0 {
		opts.RetireDelay = 50 * time.Millisecond
	}
	if opts.Session != nil {
		state := opts.Session.Snapshot()
		opts.Render.CellPixelWidth, opts.Render.CellPixelHeight = state.CellPixelWidth, state.CellPixelHeight
	}
	// Normalize all renderer defaults once and preserve them across replacements.
	opts.Render = normalizeConfig(opts.Render)
	opts.Render.Background = color.RGBA64Model.Convert(opts.Render.Background)
	ctx, cancel := context.WithCancel(context.Background())
	return &Picture{opts: opts, visible: true, lifetime: ctx, closeCancel: cancel}
}

func (p *Picture) Init() tea.Cmd {
	if p.closed {
		return nil
	}
	return p.session().Init()
}
func (p *Picture) Status() Status {
	return Status{p.generation, p.presented, !p.closed && (p.job != nil || p.dirty), p.err, p.format, p.medium}
}
func (p *Picture) Mode() PictureMode { return p.mode }
func (p *Picture) CellPixelSize() (int, int) {
	return p.opts.Render.CellPixelWidth, p.opts.Render.CellPixelHeight
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
	snapshot := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(snapshot, snapshot.Bounds(), imageview.Unwrap(img), b.Min, draw.Src)
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
		p.front.cfg.Background != cfg.Background {
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
		p.job.placement.active.Store(false)
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
		if (failedImgID != p.front.imgID || (p.front.lease != nil && p.front.lease.overwritten.Load())) && p.frontNeedsPrepare() {
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
	return p.changed()
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
	mode := PictureGlyph
	if kitty {
		mode = PictureKitty
	}
	c := &p.opts.Render
	if mode == p.mode && cellWidth == c.CellPixelWidth && cellHeight == c.CellPixelHeight {
		return nil
	}
	oldMode, oldConfig := p.mode, *c
	p.mode = mode
	c.CellPixelWidth, c.CellPixelHeight = cellWidth, cellHeight
	if p.cols > 0 && p.rows > 0 {
		if err := p.geometryError(); err != nil {
			p.mode = oldMode
			*c = oldConfig
			return p.reportError(err)
		}
	}
	return p.changed()
}

// SetMode uses the picture’s terminal session capability. Hosts with known capability can
// use SetTerminal instead. An unavailable Kitty mode leaves the mode unchanged.
func (p *Picture) SetMode(mode PictureMode) tea.Cmd {
	if p.closed || mode == p.mode {
		return nil
	}
	if mode == PictureKitty {
		if err := p.session().unavailable(); err != nil {
			return p.reportError(err)
		}
	}
	cw, ch := p.CellPixelSize()
	return p.SetTerminal(mode == PictureKitty, cw, ch)
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
	p.generation++
	p.err = nil
	p.dirty = true
	if p.job != nil {
		p.job.cancel()
		p.job.placement.active.Store(false)
	}
	var cleanup tea.Cmd
	if !p.visible || p.img == nil || p.cols == 0 || p.rows == 0 || p.opts.ClearOnReplace {
		cleanup = p.clearFront()
	}
	return tea.Batch(cleanup, p.schedule())
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
	var lease *pictureImageID
	if p.opts.StableID && !p.opts.ClearOnReplace && p.mode == PictureKitty && p.front != nil &&
		p.front.mode == PictureKitty && p.front.cols == p.cols && p.front.rows == p.rows &&
		p.front.cfg.CellPixelWidth == cfg.CellPixelWidth && p.front.cfg.CellPixelHeight == cfg.CellPixelHeight && p.front.rasterSize == raster {
		lease = p.front.lease
		cfg.KittyID = p.front.cfg.KittyID
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
	transport := p.session().transport()
	placement := &picturePlacement{
		cfg:         cfg,
		mode:        p.mode,
		cols:        p.cols,
		rows:        p.rows,
		rasterSize:  raster,
		transport:   transport,
		kittyShared: &kittySharedState{},
		image:       p.img,
		imgID:       p.imgID,
		generation:  p.generation,
	}
	placement.active.Store(true)
	if p.mode == PictureKitty {
		placement.lease = lease
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
	var params kittyPrepareParams
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
		params.shared.begin(params.seq)
	}
	return func() tea.Msg {
		var frame tea.Msg
		if ctx.Err() == nil {
			if mode == PictureKitty {
				if msg, ok := prepareKittyFrame(ctx, params); ok {
					frame = msg
				}
			} else {
				var scratch fitScratch
				rendered := prepareSourceWithScratch(img, cfg.Fit, cols, rows, 1, 2, cfg.Background, cfg.Anchor, &scratch)
				frame = string(appendGlyph(nil, rendered))
			}
		}
		return picturePrepared{p, job, frame}
	}
}

// Update routes all internal messages by owner and job identity. Superseded
// work is drained before another preparation starts: cancellation never opens
// an unbounded second worker. Unknown and foreign messages are ignored.
func (p *Picture) Update(msg tea.Msg) tea.Cmd {
	if p.opts.HostGeometry {
		switch msg.(type) {
		case uv.CellSizeEvent, uv.KittyGraphicsEvent, terminalSessionTick, kittyProbeTickMsg, TerminalSessionMsg:
			return nil
		}
	}
	if p.opts.Session != nil {
		terminal := false
		switch v := msg.(type) {
		case uv.CellSizeEvent, uv.KittyGraphicsEvent, terminalSessionTick, kittyProbeTickMsg:
			terminal = true
		case TerminalSessionMsg:
			if v.Session != p.opts.Session {
				return nil
			}
			terminal = true
		}
		if terminal {
			cmd := p.opts.Session.Update(msg)
			state := p.opts.Session.Snapshot()
			return tea.Batch(cmd, p.SetTerminal(p.mode == PictureKitty, state.CellPixelWidth, state.CellPixelHeight))
		}
	}
	switch v := msg.(type) {
	case picturePrepared:
		if v.owner != p || v.job != p.job {
			return nil
		}
		j := v.job
		if p.closed || j.ctx.Err() != nil || j.placement.generation != p.generation {
			cleanup := p.dispose(j.placement)
			j.cancel()
			p.job = nil
			if !p.closed && !p.dirty && p.frontNeedsPrepare() {
				p.dirty = true
			}
			return tea.Batch(cleanup, p.schedule())
		}
		if text, ok := v.frame.(string); ok {
			j.placement.grid = text
			return p.commit(j, p.opts.Render.KittyFormat, KittyMediumDirect)
		}
		f, ok := v.frame.(kittyFrameMsg)
		if !ok || f.APC == "" {
			return p.failPreparedJob(j, errors.New("ntpicture: image preparation failed"))
		}
		if f.sharedMemory != nil && !j.placement.kittyShared.submit(f.Seq, f.sharedMemory) {
			return p.failPreparedJob(j, errors.New("ntpicture: shared image was retired before transmission"))
		}
		j.outputDone = make(chan struct{})
		if !j.placement.ownsID && j.placement.lease != nil {
			j.placement.lease.setOutputDone(j.outputDone)
		}
		ready := func() tea.Msg { return pictureReady{p, j, f.Grid, f.Format, f.Medium} }
		return tea.Sequence(func() tea.Msg {
			if !j.placement.active.Load() {
				return nil
			}
			if !j.placement.ownsID && j.placement.lease != nil {
				j.placement.lease.overwritten.Store(true)
			}
			return tea.RawMsg{Msg: f.APC}
		}, func() tea.Msg {
			defer close(j.outputDone)
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
	case uv.CellSizeEvent:
		// Some terminals return zero when pixel dimensions are unavailable.
		// Ignore malformed reports without discarding an in-flight source.
		if v.Width <= 0 || v.Height <= 0 {
			return nil
		}
		defaultTerminalSession.setCellSize(v.Width, v.Height)
		return p.SetTerminal(p.mode == PictureKitty, v.Width, v.Height)
	case uv.KittyGraphicsEvent:
		return recordKittyResponse(v)
	case kittyProbeTickMsg:
		return recordKittyTimeout()
	}
	return nil
}

func (p *Picture) commit(j *pictureJob, format KittyFormat, medium KittyMedium) tea.Cmd {
	var cleanup tea.Cmd
	if old := p.front; old != nil {
		if old.lease != nil && old.lease == j.placement.lease {
			old.ownsID = false
			j.placement.ownsID = true
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
	f.active.Store(false)
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
	f.active.Store(false)
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

// Keep permanently stops work while preserving the last presented View and
// terminal image. Pending replacements and retired placements are discarded.
// Run the returned command before quitting. Shared-memory fronts are sent
// directly to survive buffer release; direct fronts need no retransmission
// unless an accepted stable-ID replacement may have overwritten their pixels.
// The host owns final layout and main-screen selection; an alternate screen
// normally disappears on exit. Subsequent Keep, Close and setters are no-ops.
// Keep cancels pending preparation. In StableID mode its command drains any
// already-accepted transmission before restoring the last committed frame.
func (p *Picture) Keep() tea.Cmd {
	if p.closed {
		return nil
	}
	var outputDone <-chan struct{}
	var overwritten bool
	if p.front != nil && p.front.lease != nil {
		outputDone = p.front.lease.loadOutputDone()
		overwritten = p.front.lease.overwritten.Load()
	}
	if outputDone == nil && p.job != nil && p.front != nil && p.job.placement.lease != nil && p.job.placement.lease == p.front.lease {
		outputDone = p.job.outputDone
	}
	p.keptView = p.View()
	front := p.front
	p.front = nil // Close must never delete the kept terminal ID.
	cleanup := p.Close()
	if front == nil || front.mode != PictureKitty {
		return cleanup
	}
	if p.medium == KittyMediumDirect && outputDone == nil && !overwritten {
		// The terminal already owns these pixels. Only shared-memory fronts or
		// an accepted borrower of this ID need an exit-time retransmission.
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
		return tea.RawMsg{Msg: frame.APC}
	}
	return tea.Sequence(cleanup, keep)
}

// Close cancels work and releases producer resources immediately. After Keep
// it is a no-op, including for the retained View. Run the
// returned command before removing the component or quitting to delete its
// terminal placements. Already-accepted transmissions also carry late cleanup.
func (p *Picture) Close() tea.Cmd {
	if p.closed {
		return nil
	}
	p.closed = true
	p.dirty = false
	p.img = nil
	p.closeCancel()
	var pending tea.Cmd
	if p.job != nil {
		p.job.cancel()
		pending = p.dispose(p.job.placement)
		p.job = nil
	}
	return tea.Batch(pending, p.clearFront(), p.clearRetired())
}

func (p *Picture) session() *TerminalSession {
	if p.opts.Session != nil {
		return p.opts.Session
	}
	return defaultSession()
}
