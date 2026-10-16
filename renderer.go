// Package ntpicture renders images in Bubble Tea with Kitty graphics or
// half-block glyphs. Picture owns asynchronous replacement and cleanup, and
// TerminalSession scopes terminal capability and cell-geometry discovery.
// The low-level compatibility adapter lives in nimbleterminal.dev/ntpicture/legacy.
// Decoding is opt-in through image format imports or ntpicture/decoders.
package ntpicture

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"nimbleterminal.dev/ntpicture/internal/kittyshm"
	"nimbleterminal.dev/ntpicture/internal/legacybridge"
)

// PictureMode selects how images are rendered.
type PictureMode int8

const (
	PictureGlyph PictureMode = iota // Universal half-block ANSI
	PictureKitty                    // High-res Kitty graphics protocol
)

// FitMode controls how the source image is mapped onto the cell rectangle.
// The zero value is FitContain. Out-of-range values are treated as FitContain.
type FitMode int8

const (
	FitContain FitMode = iota // preserve aspect ratio, letterbox (default)
	FitFill                   // stretch to fill the cell rectangle
	FitCover                  // preserve aspect ratio, crop to fill
)

// FitAnchor controls which edge or center is preserved when a fit mode
// must crop overflow. Today only FitCover consults this; FitContain and
// FitFill never crop.
type FitAnchor int8

const (
	AnchorCenter FitAnchor = iota // center crop (default)
	AnchorTop
	AnchorBottom
	AnchorLeft
	AnchorRight
)

const DefaultKittyID = 43

// Sensible defaults for terminal cell pixel size. Most monospace fonts have
// ~1:2 cell ratios (8×16, 9×18, 10×20). Consumers can pre-set via Config or
// update at runtime via Picture.SetTerminal or TerminalSession when the
// terminal reports its actual cell dims (CSI 16 t).
const (
	defaultCellPixelW = 8
	defaultCellPixelH = 16
)

// Config configures rendering policy for Picture (via Options.Render) or
// legacy.Model at construction.
type Config struct {
	// KittyZ sets placement depth; negative values place graphics under text.
	KittyZ     int
	KittyID    int         // default 43 (ignored by Picture, which uses Options.NextID)
	Background color.Color // default color.Transparent (no compositing)

	// Fit controls how the source image is mapped onto the cell
	// rectangle. The zero value is FitContain (preserve aspect ratio,
	// letterbox). Use FitFill for games, boards, tile maps, and other
	// coordinate-aligned UIs where source pixels must map exactly to the
	// configured terminal-cell rectangle.
	Fit FitMode

	// Anchor controls which edge or center is preserved when Fit must
	// crop overflow. Only consulted by FitCover today. Zero value is
	// AnchorCenter, matching prior behavior.
	Anchor FitAnchor

	// CellPixelWidth and CellPixelHeight are the terminal cell dimensions in
	// pixels. Used in Kitty mode to pre-scale the source image to the c×r
	// cell rectangle's pixel dimensions before encoding, so Kitty's
	// aspect-ratio-preserving placement fills the cell rectangle. Default
	// 8×16 (typical 1:2 font cell). Update at runtime via
	// Picture.SetTerminal or TerminalSession when the terminal reports its real cell size.
	CellPixelWidth  int
	CellPixelHeight int

	// KittyResolutionFactor scales the encoded Kitty image's per-cell
	// pixel resolution. Values in (0, 1] shrink the transmitted bitmap;
	// the terminal upscales the image to fill the c×r cell rectangle on
	// display. Default 1.0 (full terminal-pixel resolution). Smaller
	// values produce a chunkier, lower-bandwidth image — useful when
	// the renderer's per-frame composite cost is the bottleneck (e.g.
	// browser-WASM with ghostty-web), or when matching the perceived
	// fidelity of Glyph mode is desirable. Out-of-range values clamp
	// to 1.0. Legacy Model rounds per-cell dimensions; Picture scales the
	// whole raster, rounding once per dimension. Picture.SetResolution changes
	// this factor at runtime; Picture.RasterGeometry reports the exact target.
	KittyResolutionFactor float64

	// KittyFormat selects how Kitty frames are transmitted: PNG (default;
	// small on the wire, one encode per frame) or raw RGBA (no encode or
	// decode, about 5.3 bytes per pixel through the terminal stream).
	KittyFormat KittyFormat

	// KittyMedium chooses direct transmission (default) or shared memory.
	// Shared memory always uses raw RGBA; KittyFormat selects the direct fallback.
	KittyMedium KittyMedium
}

// legacyModel renders an image.Image as half-blocks or Kitty graphics inside a
// Bubble Tea program. Exported as legacy.Model in nimbleterminal.dev/ntpicture/legacy.
type legacyModel struct {
	kittyZ     int
	modelID    uint64
	mode       PictureMode
	cols, rows int

	img image.Image
	seq uint64

	glyphCache string
	glyphKey   string
	glyphFit   fitScratch
	glyphBuf   []byte

	kittyGrid string

	kittyID    int
	background color.Color
	fit        FitMode
	anchor     FitAnchor

	cellPixelW, cellPixelH int

	// kittyResolutionFactor scales the effective per-cell pixel
	// resolution used for Kitty encoding (see Config.KittyResolutionFactor).
	kittyResolutionFactor float64

	// kittyFormat selects PNG or raw RGBA transmission (see Config.KittyFormat).
	kittyFormat KittyFormat
	kittyMedium KittyMedium
	kittyShared *kittySharedState

	// lastRenderedGeom records the (cols, rows, cellPixelW, cellPixelH)
	// of the most recently *applied* KittyFrame — i.e. the geometry
	// currently placed on the terminal. renderCmd compares the current
	// geometry against this snapshot and prepends a kittyDeleteImage to
	// the APC when they differ; some Kitty-protocol terminals (Ghostty,
	// confirmed) don't honor a c/r change for an already-on-screen
	// virtual placement, leaving the previous geometry stuck. Resets to
	// the zero value whenever we explicitly emit kittyDeleteImage
	// (Toggle Kitty→Glyph, SetImage(nil)).
	lastRenderedGeom kittyGeom
}

// kittyGeom is the five-tuple of "what dimensions and fit mode does the Kitty
// image currently on screen occupy". Zero value means "no image currently
// placed at this model's kittyID" — used as a sentinel by renderCmd to
// skip the delete-prepend on first render.
type kittyGeom struct {
	cols, rows             int
	cellPixelW, cellPixelH int
	fit                    FitMode
	anchor                 FitAnchor
}

var nextModelID atomic.Uint64

func normalizeFit(fit FitMode) FitMode {
	switch fit {
	case FitFill, FitCover:
		return fit
	}
	return FitContain
}

func normalizeAnchor(anchor FitAnchor) FitAnchor {
	switch anchor {
	case AnchorTop, AnchorBottom, AnchorLeft, AnchorRight:
		return anchor
	}
	return AnchorCenter
}

func normalizeConfig(cfg Config) Config {
	if cfg.KittyID <= 0 || cfg.KittyID >= 1<<24 {
		cfg.KittyID = DefaultKittyID
	}
	if cfg.Background == nil {
		cfg.Background = color.Transparent
	}
	cfg.Fit = normalizeFit(cfg.Fit)
	cfg.Anchor = normalizeAnchor(cfg.Anchor)
	if cfg.CellPixelWidth <= 0 {
		cfg.CellPixelWidth = defaultCellPixelW
	}
	if cfg.CellPixelHeight <= 0 {
		cfg.CellPixelHeight = defaultCellPixelH
	}
	if math.IsNaN(cfg.KittyResolutionFactor) || cfg.KittyResolutionFactor <= 0 || cfg.KittyResolutionFactor > 1 {
		cfg.KittyResolutionFactor = 1.0
	}
	cfg.KittyFormat = normalizeKittyFormat(cfg.KittyFormat)
	cfg.KittyMedium = normalizeKittyMedium(cfg.KittyMedium)
	return cfg
}

func newLegacyModel() legacyModel {
	return newLegacyModelWithConfig(Config{})
}

func newLegacyModelWithConfig(cfg Config) legacyModel {
	cfg = normalizeConfig(cfg)
	return legacyModel{
		modelID:               nextModelID.Add(1),
		kittyZ:                cfg.KittyZ,
		mode:                  PictureGlyph,
		kittyID:               cfg.KittyID,
		background:            cfg.Background,
		cellPixelW:            cfg.CellPixelWidth,
		cellPixelH:            cfg.CellPixelHeight,
		kittyResolutionFactor: cfg.KittyResolutionFactor,
		kittyFormat:           cfg.KittyFormat,
		kittyMedium:           cfg.KittyMedium,
		kittyShared:           &kittySharedState{},
		fit:                   cfg.Fit,
		anchor:                cfg.Anchor,
	}
}

func (m *legacyModel) SetImage(img image.Image) tea.Cmd {
	prev := m.img
	m.img = img
	m.seq++
	m.invalidateGlyph()

	if img == nil {
		m.kittyShared.clear(m.seq)
		m.invalidateKitty()
		if m.mode == PictureKitty && prev != nil {
			// Image gone from the terminal: clear the geom snapshot so
			// the next render is treated as a fresh placement (no
			// spurious delete prepended on first re-render).
			m.lastRenderedGeom = kittyGeom{}
			return tea.Raw(kittyDeleteImage(m.kittyID))
		}
		return nil
	}
	return m.renderCmd()
}

func (m *legacyModel) SetSize(cols, rows int) tea.Cmd {
	if cols < 0 {
		cols = 0
	}
	if rows < 0 {
		rows = 0
	}
	if cols == m.cols && rows == m.rows {
		return nil
	}
	m.cols = cols
	m.rows = rows
	m.seq++
	m.invalidateGlyph()
	m.invalidateKitty()
	return m.renderCmd()
}

func (m *legacyModel) ToggleBlocked() error {
	if m.mode == PictureGlyph {
		return kittyUnavailable()
	}
	return nil
}

func (m *legacyModel) Toggle() tea.Cmd {
	prev := m.mode
	if m.mode == PictureGlyph {
		if kittySupported() != KittyCapabilitySupported {
			return nil
		}
		m.mode = PictureKitty
	} else {
		m.mode = PictureGlyph
	}
	m.seq++

	if prev == PictureKitty {
		m.kittyShared.clear(m.seq)
	}
	if prev == PictureKitty && m.img != nil {
		m.invalidateKitty()
		m.lastRenderedGeom = kittyGeom{}
		return tea.Raw(kittyDeleteImage(m.kittyID))
	}
	return m.renderCmd()
}

func (m *legacyModel) Mode() PictureMode { return m.mode }

func (m *legacyModel) Fit() FitMode { return m.fit }

func (m *legacyModel) Anchor() FitAnchor { return m.anchor }

func (m *legacyModel) SetFit(fit FitMode) tea.Cmd {
	fit = normalizeFit(fit)
	if fit == m.fit {
		return nil
	}
	m.fit = fit
	m.seq++
	m.invalidateGlyph()
	m.invalidateKitty()
	return m.renderCmd()
}

func (m *legacyModel) SetAnchor(anchor FitAnchor) tea.Cmd {
	anchor = normalizeAnchor(anchor)
	if anchor == m.anchor {
		return nil
	}
	m.anchor = anchor
	m.seq++
	m.invalidateGlyph()
	m.invalidateKitty()
	return m.renderCmd()
}

func (m *legacyModel) Init() tea.Cmd {
	return tea.Batch(requestCellSize(), queryKittySupport())
}

func (m *legacyModel) KittySupported() KittyCapability { return kittySupported() }

func (m *legacyModel) CellPixelSize() (w, h int) {
	return m.cellPixelW, m.cellPixelH
}

func (m *legacyModel) SetCellPixelSize(w, h int) tea.Cmd {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	if w == m.cellPixelW && h == m.cellPixelH {
		return nil
	}
	m.cellPixelW = w
	m.cellPixelH = h
	m.seq++
	m.invalidateKitty()
	return m.renderCmd()
}

func (m *legacyModel) KittyResolutionFactor() float64 { return m.kittyResolutionFactor }

func (m *legacyModel) SetKittyResolutionFactor(f float64) tea.Cmd {
	if math.IsNaN(f) || f <= 0 || f > 1 {
		f = 1.0
	}
	if f == m.kittyResolutionFactor {
		return nil
	}
	m.kittyResolutionFactor = f
	m.seq++
	m.invalidateKitty()
	return m.renderCmd()
}

func (m *legacyModel) KittyFormat() KittyFormat { return m.kittyFormat }

func (m *legacyModel) SetKittyFormat(f KittyFormat) tea.Cmd {
	f = normalizeKittyFormat(f)
	if f == m.kittyFormat {
		return nil
	}
	m.kittyFormat = f
	m.seq++
	return m.renderCmd()
}

func (m *legacyModel) KittyMedium() KittyMedium { return m.kittyMedium }

func (m *legacyModel) SetKittyMedium(medium KittyMedium) tea.Cmd {
	medium = normalizeKittyMedium(medium)
	if medium == m.kittyMedium {
		return nil
	}
	m.kittyMedium = medium
	m.seq++
	return m.renderCmd()
}

func (m *legacyModel) String() string { return m.View().Content }

// applyKittyGridMsg is the second half of the kittyFrameMsg pipeline.
type applyKittyGridMsg struct {
	modelID             uint64
	cols, rows, kittyID int
	grid                string
}

func isPictureMsg(msg tea.Msg) bool {
	if _, ok := unwrapKittyFrameMsg(msg); ok {
		return true
	}
	if legacybridge.IsCapabilityMsg != nil && legacybridge.IsCapabilityMsg(msg) {
		return true
	}
	switch msg.(type) {
	case applyKittyGridMsg, uv.CellSizeEvent, uv.KittyGraphicsEvent, kittyProbeTickMsg,
		picturePrepared, pictureReady, pictureRetire, PresentedMsg, ErrorMsg, kittyCapabilityMsg, TerminalSessionMsg, terminalSessionTick:
		return true
	}
	return false
}

func (m *legacyModel) Update(msg tea.Msg) tea.Cmd {
	if frame, ok := unwrapKittyFrameMsg(msg); ok {
		if frame.modelID != m.modelID {
			return nil
		}
		if frame.Seq != m.seq || (frame.sharedMemory != nil && !m.kittyShared.submit(frame.Seq, frame.sharedMemory)) {
			_ = frame.sharedMemory.Unlink()
			return nil
		}
		cpw, cph := m.kittyCellPixelSize()
		m.lastRenderedGeom = kittyGeom{
			cols:       m.cols,
			rows:       m.rows,
			cellPixelW: cpw,
			cellPixelH: cph,
			fit:        m.fit,
			anchor:     m.anchor,
		}
		modelID, grid := m.modelID, frame.Grid
		gridCols, gridRows, gridID := m.cols, m.rows, m.kittyID
		return tea.Sequence(
			tea.Raw(frame.APC),
			func() tea.Msg {
				return applyKittyGridMsg{modelID: modelID, cols: gridCols, rows: gridRows, kittyID: gridID, grid: grid}
			},
		)
	}
	switch msg := msg.(type) {
	case applyKittyGridMsg:
		if msg.modelID != m.modelID {
			return nil
		}
		if msg.cols != m.cols || msg.rows != m.rows || msg.kittyID != m.kittyID {
			return nil
		}
		m.kittyGrid = msg.grid
		return nil
	case uv.CellSizeEvent:
		if msg.Width <= 0 || msg.Height <= 0 {
			return nil
		}
		defaultTerminalSession.setCellSize(msg.Width, msg.Height)
		return m.SetCellPixelSize(msg.Width, msg.Height)
	case uv.KittyGraphicsEvent:
		return recordKittyResponse(msg)
	case kittyProbeTickMsg:
		return recordKittyTimeout()
	}
	return nil
}

func requestCellSize() tea.Cmd {
	return tea.Raw(ansi.WindowOp(16))
}

func (m *legacyModel) View() tea.View {
	if m.img == nil || m.cols <= 0 || m.rows <= 0 {
		return tea.NewView("")
	}

	if m.mode == PictureKitty && m.kittyGrid != "" {
		return tea.NewView(m.kittyGrid)
	}

	key := fmt.Sprintf("%d|%d|%d|%d|%d", m.seq, m.cols, m.rows, m.fit, m.anchor)
	if m.glyphKey == key && m.glyphCache != "" {
		return tea.NewView(m.glyphCache)
	}

	rendered := prepareSourceWithScratch(m.img, m.fit, m.cols, m.rows, 1, 2, m.background, m.anchor, &m.glyphFit)
	m.glyphBuf = appendGlyph(m.glyphBuf[:0], rendered)
	out := string(m.glyphBuf)
	m.glyphCache = out
	m.glyphKey = key
	return tea.NewView(out)
}

func (m *legacyModel) invalidateGlyph() {
	m.glyphCache = ""
	m.glyphKey = ""
}

func (m *legacyModel) invalidateKitty() {
	m.kittyGrid = ""
}

func (m *legacyModel) kittyCellPixelSize() (int, int) {
	return max(1, int(float64(m.cellPixelW)*m.kittyResolutionFactor)),
		max(1, int(float64(m.cellPixelH)*m.kittyResolutionFactor))
}

type kittyPrepareParams struct {
	modelID         uint64
	seq             uint64
	img             image.Image
	kittyZ, kittyID int
	background      color.Color
	fit             FitMode
	anchor          FitAnchor
	format          KittyFormat
	medium          KittyMedium
	cols, rows      int
	cpw, cph        int
	rasterSize      image.Point
	prevGeom        kittyGeom
	transport       kittyTransport
	shared          *kittySharedState
}

func prepareKittyFrame(ctx context.Context, p kittyPrepareParams) (kittyFrameMsg, bool) {
	if ctx.Err() != nil {
		return kittyFrameMsg{}, false
	}
	var prepared image.Image
	if p.rasterSize.X > 0 && p.rasterSize.Y > 0 {
		prepared = prepareSource(p.img, p.fit, p.rasterSize.X, p.rasterSize.Y, 1, 1, p.background, p.anchor)
	} else {
		prepared = prepareSource(p.img, p.fit, p.cols, p.rows, p.cpw, p.cph, p.background, p.anchor)
	}
	if prepared == nil || ctx.Err() != nil {
		return kittyFrameMsg{}, false
	}
	actualMedium, actualFormat := KittyMediumDirect, p.format
	var apc string
	var object *kittyshm.Object
	if p.medium == KittyMediumSharedMemory && p.transport.shared {
		var err error
		apc, object, err = p.transport.buildKittySharedMemoryAPC(prepared, p.kittyID, p.cols, p.rows, p.kittyZ)
		if err == nil {
			actualMedium, actualFormat = KittyMediumSharedMemory, KittyFormatRGBA
		}
	}
	if actualMedium == KittyMediumDirect {
		// Yield around expensive direct encoding, including shared-memory
		// fallback, so browser events can drain. Native yields are no-ops.
		yieldToJS()
		apc = p.transport.buildKittyAPC(prepared, p.kittyID, p.cols, p.rows, p.format, p.kittyZ)
		yieldToJS()
	}
	if ctx.Err() != nil {
		_ = object.Unlink()
		return kittyFrameMsg{}, false
	}
	if !p.shared.replace(p.seq, object) {
		return kittyFrameMsg{}, false
	}
	currGeom := kittyGeom{cols: p.cols, rows: p.rows, cellPixelW: p.cpw, cellPixelH: p.cph, fit: p.fit, anchor: p.anchor}
	if p.prevGeom != (kittyGeom{}) && p.prevGeom != currGeom {
		apc = p.transport.kittyDeleteImage(p.kittyID) + apc
	}
	grid := buildKittyGrid(p.cols, p.rows, p.kittyID)
	return kittyFrameMsg{
		modelID:      p.modelID,
		ID:           p.kittyID,
		Seq:          p.seq,
		APC:          apc,
		Grid:         grid,
		Format:       actualFormat,
		Medium:       actualMedium,
		sharedMemory: object,
	}, true
}

func (m *legacyModel) renderCmd() tea.Cmd {
	return m.renderCmdContext(context.Background())
}

// Cancellation is cooperative between preparation stages. Standard-library
// encoders/scalers already running must finish; Picture bounds concurrency.
func (m *legacyModel) renderCmdContext(ctx context.Context) tea.Cmd {
	if m.kittyShared == nil {
		m.kittyShared = &kittySharedState{}
	}
	if m.mode != PictureKitty || m.img == nil || m.cols <= 0 || m.rows <= 0 {
		m.kittyShared.clear(m.seq)
		return nil
	}
	cpw, cph := m.kittyCellPixelSize()
	params := kittyPrepareParams{
		modelID:    m.modelID,
		seq:        m.seq,
		img:        m.img,
		kittyZ:     m.kittyZ,
		kittyID:    m.kittyID,
		background: m.background,
		fit:        m.fit,
		anchor:     m.anchor,
		format:     m.kittyFormat,
		medium:     m.kittyMedium,
		cols:       m.cols,
		rows:       m.rows,
		cpw:        cpw,
		cph:        cph,
		prevGeom:   m.lastRenderedGeom,
		transport:  legacyTransport(),
		shared:     m.kittyShared,
	}
	params.shared.begin(params.seq)
	return func() tea.Msg {
		msg, ok := prepareKittyFrame(ctx, params)
		if !ok {
			return nil
		}
		return wrapKittyFrameMsg(msg)
	}
}

func init() {
	legacybridge.EnsureLegacyEnv = ensureLegacyEnv
	legacybridge.DefaultTerminalSession = func() any { return defaultSession() }
	legacybridge.ForceKittyCapability = func(c int8) { forceKittyCapability(KittyCapability(c)) }
	legacybridge.SetTmuxPassthrough = setTmuxPassthrough
	legacybridge.KittySupported = func() int8 { return int8(kittySupported()) }
	legacybridge.KittyEnvSignalled = kittyEnvSignalledFn
	legacybridge.QueryKittySupport = queryKittySupport
	legacybridge.KittyUnavailable = kittyUnavailable
	legacybridge.RequestCellSize = requestCellSize
	legacybridge.IsPictureMsg = isPictureMsg
	legacybridge.NewModel = func() any { return newLegacyModel() }
	legacybridge.NewModelWithConfig = func(c legacybridge.ConfigFields) any {
		return newLegacyModelWithConfig(Config{
			KittyZ:                c.KittyZ,
			KittyID:               c.KittyID,
			Background:            c.Background,
			Fit:                   FitMode(c.Fit),
			Anchor:                FitAnchor(c.Anchor),
			CellPixelWidth:        c.CellPixelWidth,
			CellPixelHeight:       c.CellPixelHeight,
			KittyResolutionFactor: c.KittyResolutionFactor,
			KittyFormat:           KittyFormat(c.KittyFormat),
			KittyMedium:           KittyMedium(c.KittyMedium),
		})
	}
	legacybridge.ModelSetImage = func(v any, img image.Image) (any, tea.Cmd) {
		m := v.(legacyModel)
		cmd := m.SetImage(img)
		return m, cmd
	}
	legacybridge.ModelSetSize = func(v any, cols, rows int) (any, tea.Cmd) {
		m := v.(legacyModel)
		cmd := m.SetSize(cols, rows)
		return m, cmd
	}
	legacybridge.ModelToggleBlocked = func(v any) error {
		m := v.(legacyModel)
		return m.ToggleBlocked()
	}
	legacybridge.ModelToggle = func(v any) (any, tea.Cmd) {
		m := v.(legacyModel)
		cmd := m.Toggle()
		return m, cmd
	}
	legacybridge.ModelMode = func(v any) int8 {
		m := v.(legacyModel)
		return int8(m.Mode())
	}
	legacybridge.ModelFit = func(v any) int8 {
		m := v.(legacyModel)
		return int8(m.Fit())
	}
	legacybridge.ModelAnchor = func(v any) int8 {
		m := v.(legacyModel)
		return int8(m.Anchor())
	}
	legacybridge.ModelSetFit = func(v any, fit int8) (any, tea.Cmd) {
		m := v.(legacyModel)
		cmd := m.SetFit(FitMode(fit))
		return m, cmd
	}
	legacybridge.ModelSetAnchor = func(v any, anchor int8) (any, tea.Cmd) {
		m := v.(legacyModel)
		cmd := m.SetAnchor(FitAnchor(anchor))
		return m, cmd
	}
	legacybridge.ModelInit = func(v any) tea.Cmd {
		m := v.(legacyModel)
		return m.Init()
	}
	legacybridge.ModelKittySupported = func(v any) int8 {
		m := v.(legacyModel)
		return int8(m.KittySupported())
	}
	legacybridge.ModelCellPixelSize = func(v any) (int, int) {
		m := v.(legacyModel)
		return m.CellPixelSize()
	}
	legacybridge.ModelSetCellPixelSize = func(v any, w, h int) (any, tea.Cmd) {
		m := v.(legacyModel)
		cmd := m.SetCellPixelSize(w, h)
		return m, cmd
	}
	legacybridge.ModelKittyResolutionFactor = func(v any) float64 {
		m := v.(legacyModel)
		return m.KittyResolutionFactor()
	}
	legacybridge.ModelSetKittyResolutionFactor = func(v any, f float64) (any, tea.Cmd) {
		m := v.(legacyModel)
		cmd := m.SetKittyResolutionFactor(f)
		return m, cmd
	}
	legacybridge.ModelKittyFormat = func(v any) int8 {
		m := v.(legacyModel)
		return int8(m.KittyFormat())
	}
	legacybridge.ModelSetKittyFormat = func(v any, f int8) (any, tea.Cmd) {
		m := v.(legacyModel)
		cmd := m.SetKittyFormat(KittyFormat(f))
		return m, cmd
	}
	legacybridge.ModelKittyMedium = func(v any) int8 {
		m := v.(legacyModel)
		return int8(m.KittyMedium())
	}
	legacybridge.ModelSetKittyMedium = func(v any, medium int8) (any, tea.Cmd) {
		m := v.(legacyModel)
		cmd := m.SetKittyMedium(KittyMedium(medium))
		return m, cmd
	}
	legacybridge.ModelString = func(v any) string {
		m := v.(legacyModel)
		return m.String()
	}
	legacybridge.ModelUpdate = func(v any, msg tea.Msg) (any, tea.Cmd) {
		m := v.(legacyModel)
		cmd := m.Update(msg)
		return m, cmd
	}
	legacybridge.ModelView = func(v any) tea.View {
		m := v.(legacyModel)
		return m.View()
	}
	legacybridge.ModelRenderCmdContext = func(v any, ctx context.Context) (any, tea.Cmd) {
		m := v.(legacyModel)
		cmd := m.renderCmdContext(ctx)
		return m, cmd
	}
}
