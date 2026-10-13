// Proposed NTCharts picture compatibility adapter. Consumer checks install
// this fixture into a temporary NTCharts copy; it is not a second renderer.
package picture

import (
	tea "charm.land/bubbletea/v2"
	nt "nimbleterminal.dev/ntpicture"
)

type Model = nt.Model
type Config = nt.Config
type PictureMode = nt.PictureMode
type FitMode = nt.FitMode
type FitAnchor = nt.FitAnchor
type KittyCapability = nt.KittyCapability
type KittyCapabilityMsg = nt.KittyCapabilityMsg
type KittyFormat = nt.KittyFormat
type KittyMedium = nt.KittyMedium
type KittyFrameMsg = nt.KittyFrameMsg
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

func New() Model                                  { return nt.NewModel() }
func NewWithConfig(cfg Config) Model              { return nt.NewModelWithConfig(cfg) }
func NewGridImage(cfg GridImageConfig) *GridImage { return nt.NewGridImage(cfg) }
func IsPictureMsg(msg tea.Msg) bool               { return nt.IsPictureMsg(msg) }
func RequestCellSize() tea.Cmd                    { return nt.RequestCellSize() }
func QueryKittySupport() tea.Cmd                  { return nt.QueryKittySupport() }
func KittyEnvSignalled() bool                     { return nt.KittyEnvSignalled() }
func KittySupported() KittyCapability             { return nt.KittySupported() }
func KittyUnavailable() error                     { return nt.KittyUnavailable() }
func ForceKittyCapability(c KittyCapability)      { nt.ForceKittyCapability(c) }
func SetTmuxPassthrough(enabled bool)             { nt.SetTmuxPassthrough(enabled) }
func KittySharedMemorySupported() bool            { return nt.KittySharedMemorySupported() }
