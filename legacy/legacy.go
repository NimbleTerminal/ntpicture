// Package legacy exposes the low-level Model and process-global terminal
// capability helpers extracted from NTCharts for compatibility adapters.
// New applications should use the root ntpicture.Picture and
// ntpicture.TerminalSession APIs instead.
package legacy

import (
	"image"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/internal/kittyshm"
	"nimbleterminal.dev/ntpicture/internal/legacybridge"
)

func init() {
	legacybridge.WrapFrameMsg = func(f legacybridge.FrameFields) tea.Msg {
		return KittyFrameMsg{
			modelID:      f.ModelID,
			ID:           f.ID,
			Seq:          f.Seq,
			APC:          f.APC,
			Grid:         f.Grid,
			Format:       ntpicture.KittyFormat(f.Format),
			Medium:       ntpicture.KittyMedium(f.Medium),
			sharedMemory: f.SharedMemory,
		}
	}
	legacybridge.UnwrapFrameMsg = func(msg tea.Msg) (legacybridge.FrameFields, bool) {
		f, ok := msg.(KittyFrameMsg)
		if !ok {
			return legacybridge.FrameFields{}, false
		}
		return legacybridge.FrameFields{
			ModelID:      f.modelID,
			ID:           f.ID,
			Seq:          f.Seq,
			APC:          f.APC,
			Grid:         f.Grid,
			Format:       int8(f.Format),
			Medium:       int8(f.Medium),
			SharedMemory: f.sharedMemory,
		}, true
	}
	if legacybridge.EnsureLegacyEnv != nil {
		legacybridge.EnsureLegacyEnv()
	}
}

// KittyFrameMsg carries the result of building a Kitty APC payload + grid for
// a specific generation of a legacy Model.
type KittyFrameMsg struct {
	modelID uint64
	ID      int
	Seq     uint64
	APC     string
	Grid    string
	// Format reports how this frame's pixels were transmitted (PNG, RGBA, or Zlib).
	Format ntpicture.KittyFormat
	// Medium reports the transport actually used, including any direct fallback.
	Medium       ntpicture.KittyMedium
	sharedMemory *kittyshm.Object
}

// KittyCapabilityMsg reports probe resolution on the legacy process-wide session.
type KittyCapabilityMsg = ntpicture.KittyCapabilityMsg

// Model renders an image.Image as half-blocks or Kitty graphics inside a
// Bubble Tea program using the extracted low-level NTCharts API. New consumers
// should normally use ntpicture.Picture.
type Model struct {
	inner any
}

func (m *Model) state() any {
	if m.inner == nil {
		m.inner = legacybridge.NewModel()
	}
	return m.inner
}

// NewModel returns a low-level Model with default Config.
func NewModel() Model {
	return Model{inner: legacybridge.NewModel()}
}

// NewModelWithConfig returns a Model with the supplied Config. Zero/nil fields
// are filled with defaults.
func NewModelWithConfig(cfg ntpicture.Config) Model {
	return Model{inner: legacybridge.NewModelWithConfig(cfg)}
}

// SetImage retains the image without copying; callers must keep its pixels immutable.
func (m *Model) SetImage(img image.Image) tea.Cmd {
	next, cmd := legacybridge.ModelSetImage(m.state(), img)
	m.inner = next
	return cmd
}

// SetSize updates the rendering dimensions in terminal cells.
func (m *Model) SetSize(cols, rows int) tea.Cmd {
	next, cmd := legacybridge.ModelSetSize(m.state(), cols, rows)
	m.inner = next
	return cmd
}

// ToggleBlocked reports why Toggle would refuse to switch modes.
func (m *Model) ToggleBlocked() error {
	return legacybridge.ModelToggleBlocked(m.state())
}

// Toggle switches between Glyph and Kitty modes.
func (m *Model) Toggle() tea.Cmd {
	next, cmd := legacybridge.ModelToggle(m.state())
	m.inner = next
	return cmd
}

// Mode returns the current rendering mode.
func (m *Model) Mode() ntpicture.PictureMode {
	return ntpicture.PictureMode(legacybridge.ModelMode(m.state()))
}

// Fit returns the current fit mode.
func (m *Model) Fit() ntpicture.FitMode {
	return ntpicture.FitMode(legacybridge.ModelFit(m.state()))
}

// Anchor returns the current fit anchor.
func (m *Model) Anchor() ntpicture.FitAnchor {
	return ntpicture.FitAnchor(legacybridge.ModelAnchor(m.state()))
}

// SetFit updates the fit mode.
func (m *Model) SetFit(fit ntpicture.FitMode) tea.Cmd {
	next, cmd := legacybridge.ModelSetFit(m.state(), int8(fit))
	m.inner = next
	return cmd
}

// SetAnchor updates the fit anchor.
func (m *Model) SetAnchor(anchor ntpicture.FitAnchor) tea.Cmd {
	next, cmd := legacybridge.ModelSetAnchor(m.state(), int8(anchor))
	m.inner = next
	return cmd
}

// Init returns a Cmd that asks the terminal for its cell pixel size and probes Kitty graphics support.
func (m *Model) Init() tea.Cmd {
	return legacybridge.ModelInit(m.state())
}

// KittySupported reports the process-wide Kitty graphics capability.
func (m *Model) KittySupported() ntpicture.KittyCapability {
	return ntpicture.KittyCapability(legacybridge.ModelKittySupported(m.state()))
}

// CellPixelSize returns the configured terminal cell pixel size.
func (m *Model) CellPixelSize() (w, h int) {
	return legacybridge.ModelCellPixelSize(m.state())
}

// SetCellPixelSize updates the terminal cell pixel size used for Kitty placements.
func (m *Model) SetCellPixelSize(w, h int) tea.Cmd {
	next, cmd := legacybridge.ModelSetCellPixelSize(m.state(), w, h)
	m.inner = next
	return cmd
}

// KittyResolutionFactor returns the current Kitty-image resolution multiplier.
func (m *Model) KittyResolutionFactor() float64 {
	return legacybridge.ModelKittyResolutionFactor(m.state())
}

// SetKittyResolutionFactor updates the multiplier applied to cell pixel dimensions.
func (m *Model) SetKittyResolutionFactor(f float64) tea.Cmd {
	next, cmd := legacybridge.ModelSetKittyResolutionFactor(m.state(), f)
	m.inner = next
	return cmd
}

// KittyFormat returns how Kitty frames are transmitted.
func (m *Model) KittyFormat() ntpicture.KittyFormat {
	return ntpicture.KittyFormat(legacybridge.ModelKittyFormat(m.state()))
}

// SetKittyFormat switches Kitty frames between PNG, raw RGBA, and zlib transmission.
func (m *Model) SetKittyFormat(f ntpicture.KittyFormat) tea.Cmd {
	next, cmd := legacybridge.ModelSetKittyFormat(m.state(), int8(f))
	m.inner = next
	return cmd
}

// KittyMedium returns the requested transport (frames report the actual medium).
func (m *Model) KittyMedium() ntpicture.KittyMedium {
	return ntpicture.KittyMedium(legacybridge.ModelKittyMedium(m.state()))
}

// SetKittyMedium changes the transport, normalizing unknown values to Direct.
func (m *Model) SetKittyMedium(medium ntpicture.KittyMedium) tea.Cmd {
	next, cmd := legacybridge.ModelSetKittyMedium(m.state(), int8(medium))
	m.inner = next
	return cmd
}

// String returns the rendered image content as a plain string.
func (m *Model) String() string {
	next, content := legacybridge.ModelString(m.state())
	m.inner = next
	return content
}

// Update processes the picture component's own messages.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	next, cmd := legacybridge.ModelUpdate(m.state(), msg)
	m.inner = next
	return cmd
}

// View returns the rendered image as a tea.View.
func (m *Model) View() tea.View {
	next, view := legacybridge.ModelView(m.state())
	m.inner = next
	return view
}

// DefaultTerminalSession returns the compatibility session used by process-wide
// capability helpers and legacy Model.
func DefaultTerminalSession() *ntpicture.TerminalSession {
	return legacybridge.DefaultTerminalSession().(*ntpicture.TerminalSession)
}

// ForceKittyCapability sets the process-wide Kitty graphics capability,
// bypassing terminal probing.
func ForceKittyCapability(c ntpicture.KittyCapability) {
	legacybridge.ForceKittyCapability(int8(c))
}

// SetTmuxPassthrough enables or disables process-wide tmux passthrough wrapping.
func SetTmuxPassthrough(enabled bool) {
	legacybridge.SetTmuxPassthrough(enabled)
}

// KittySupported reports the current process-wide Kitty graphics capability.
func KittySupported() ntpicture.KittyCapability {
	return ntpicture.KittyCapability(legacybridge.KittySupported())
}

// KittyEnvSignalled reports whether the env preflight saw a positive indicator
// when QueryKittySupport ran.
func KittyEnvSignalled() bool {
	return legacybridge.KittyEnvSignalled()
}

// QueryKittySupport returns a Cmd that probes terminal Kitty graphics support
// on the legacy process-wide session.
func QueryKittySupport() tea.Cmd {
	return legacybridge.QueryKittySupport()
}

// KittyUnavailable returns nil when the terminal is known to support Kitty
// graphics on the legacy process-wide session, or an explanatory error.
func KittyUnavailable() error {
	return legacybridge.KittyUnavailable()
}

// RequestCellSize returns a Cmd that asks the terminal for its cell pixel
// dimensions via CSI 16 t (XTWINOPS).
func RequestCellSize() tea.Cmd {
	return legacybridge.RequestCellSize()
}

// IsPictureMsg reports whether msg is a picture-related async update.
func IsPictureMsg(msg tea.Msg) bool {
	return legacybridge.IsPictureMsg(msg)
}
