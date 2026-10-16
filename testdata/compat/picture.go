// Proposed NTCharts picture compatibility adapter. Consumer checks install
// this fixture into a temporary NTCharts copy; it is not a second renderer.
package picture

import (
	tea "charm.land/bubbletea/v2"
	nt "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/legacy"
)

type Model = legacy.Model
type Config = nt.Config
type PictureMode = nt.PictureMode
type FitMode = nt.FitMode
type FitAnchor = nt.FitAnchor
type KittyCapability = nt.KittyCapability
type KittyCapabilityMsg = legacy.KittyCapabilityMsg
type KittyFormat = nt.KittyFormat
type KittyMedium = nt.KittyMedium
type KittyFrameMsg = legacy.KittyFrameMsg
type GridImage = nt.GridImage
type GridImageConfig = nt.GridImageConfig

const (
	DefaultKittyID             = nt.DefaultKittyID
	PictureGlyph               = nt.PictureGlyph
	PictureKitty               = nt.PictureKitty
	FitContain                 = nt.FitContain
	FitFill                    = nt.FitFill
	FitCover                   = nt.FitCover
	AnchorCenter               = nt.AnchorCenter
	AnchorTop                  = nt.AnchorTop
	AnchorBottom               = nt.AnchorBottom
	AnchorLeft                 = nt.AnchorLeft
	AnchorRight                = nt.AnchorRight
	KittyCapabilityUnknown     = nt.KittyCapabilityUnknown
	KittyCapabilitySupported   = nt.KittyCapabilitySupported
	KittyCapabilityUnsupported = nt.KittyCapabilityUnsupported
	KittyFormatPNG             = nt.KittyFormatPNG
	KittyFormatRGBA            = nt.KittyFormatRGBA
	KittyFormatZlib            = nt.KittyFormatZlib
	KittyMediumDirect          = nt.KittyMediumDirect
	KittyMediumSharedMemory    = nt.KittyMediumSharedMemory
)

var (
	ErrKittyProbePending = nt.ErrKittyProbePending
	ErrKittyNotDetected  = nt.ErrKittyNotDetected
	ErrKittyProbeTimeout = nt.ErrKittyProbeTimeout
)

func New() Model                                  { return legacy.NewModel() }
func NewWithConfig(cfg Config) Model              { return legacy.NewModelWithConfig(cfg) }
func NewGridImage(cfg GridImageConfig) *GridImage { return nt.NewGridImage(cfg) }
func IsPictureMsg(msg tea.Msg) bool               { return legacy.IsPictureMsg(msg) }
func RequestCellSize() tea.Cmd                    { return legacy.RequestCellSize() }
func QueryKittySupport() tea.Cmd                  { return legacy.QueryKittySupport() }
func KittyEnvSignalled() bool                     { return legacy.KittyEnvSignalled() }
func KittySupported() KittyCapability             { return legacy.KittySupported() }
func KittyUnavailable() error                     { return legacy.KittyUnavailable() }
func ForceKittyCapability(c KittyCapability)      { legacy.ForceKittyCapability(c) }
func SetTmuxPassthrough(enabled bool)             { legacy.SetTmuxPassthrough(enabled) }
func KittySharedMemorySupported() bool            { return nt.KittySharedMemorySupported() }
