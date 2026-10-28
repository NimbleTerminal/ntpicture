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
	"nimbleterminal.dev/ntpicture/internal/legacybridge"
)

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
	transient   bool
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
		transient:             cfg.Transient,
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
	switch msg.(type) {
	case applyKittyGridMsg, uv.CellSizeEvent, uv.KittyGraphicsEvent, kittyProbeTickMsg,
		picturePrepared, pictureReady, pictureRetire, PresentedMsg, ErrorMsg, KittyCapabilityMsg, TerminalSessionMsg, terminalSessionTick, terminalFeatureTick:
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
	case terminalFeatureTick:
		return defaultTerminalSession.Update(msg)
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
	params.transport.transient = m.transient
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
	legacybridge.NewModelWithConfig = func(cfg any) any {
		return newLegacyModelWithConfig(cfg.(Config))
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
	legacybridge.ModelString = func(v any) (any, string) {
		next, view := legacybridge.ModelView(v)
		return next, view.Content
	}
	legacybridge.ModelUpdate = func(v any, msg tea.Msg) (any, tea.Cmd) {
		m := v.(legacyModel)
		cmd := m.Update(msg)
		return m, cmd
	}
	legacybridge.ModelView = func(v any) (any, tea.View) {
		m := v.(legacyModel)
		view := m.View()
		// Public Model copies retain independent value state. Keep the immutable
		// presentation, but do not publish mutable scratch that subsequent
		// renders of those copies could overwrite (or race on).
		m.glyphFit = fitScratch{}
		m.glyphBuf = nil
		return m, view
	}
	legacybridge.ModelRenderCmdContext = func(v any, ctx context.Context) (any, tea.Cmd) {
		m := v.(legacyModel)
		cmd := m.renderCmdContext(ctx)
		return m, cmd
	}
}
