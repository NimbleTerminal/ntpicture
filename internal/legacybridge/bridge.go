package legacybridge

import (
	"context"
	"image"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntpicture/internal/kittyshm"
)

// FrameFields carries the internal fields of a prepared Kitty frame across the
// root ntpicture and ntpicture/legacy packages without exporting them in root.
type FrameFields struct {
	ModelID      uint64
	ID           int
	Seq          uint64
	APC          string
	Grid         string
	Format       int8
	Medium       int8
	SharedMemory *kittyshm.Object
}

var (
	EnsureLegacyEnv        func()
	DefaultTerminalSession func() any
	ForceKittyCapability   func(int8)
	SetTmuxPassthrough     func(bool)
	KittySupported         func() int8
	KittyEnvSignalled      func() bool
	QueryKittySupport      func() tea.Cmd
	KittyUnavailable       func() error
	RequestCellSize        func() tea.Cmd
	IsPictureMsg           func(tea.Msg) bool

	WrapFrameMsg   func(FrameFields) tea.Msg
	UnwrapFrameMsg func(tea.Msg) (FrameFields, bool)

	NewModel                      func() any
	NewModelWithConfig            func(cfg any) any
	ModelSetImage                 func(m any, img image.Image) (any, tea.Cmd)
	ModelSetSize                  func(m any, cols, rows int) (any, tea.Cmd)
	ModelToggleBlocked            func(m any) error
	ModelToggle                   func(m any) (any, tea.Cmd)
	ModelMode                     func(m any) int8
	ModelFit                      func(m any) int8
	ModelAnchor                   func(m any) int8
	ModelSetFit                   func(m any, fit int8) (any, tea.Cmd)
	ModelSetAnchor                func(m any, anchor int8) (any, tea.Cmd)
	ModelInit                     func(m any) tea.Cmd
	ModelKittySupported           func(m any) int8
	ModelCellPixelSize            func(m any) (int, int)
	ModelSetCellPixelSize         func(m any, w, h int) (any, tea.Cmd)
	ModelKittyResolutionFactor    func(m any) float64
	ModelSetKittyResolutionFactor func(m any, f float64) (any, tea.Cmd)
	ModelKittyFormat              func(m any) int8
	ModelSetKittyFormat           func(m any, f int8) (any, tea.Cmd)
	ModelKittyMedium              func(m any) int8
	ModelSetKittyMedium           func(m any, medium int8) (any, tea.Cmd)
	ModelString                   func(m any) string
	ModelUpdate                   func(m any, msg tea.Msg) (any, tea.Cmd)
	ModelView                     func(m any) tea.View
	ModelRenderCmdContext         func(m any, ctx context.Context) (any, tea.Cmd)
)
