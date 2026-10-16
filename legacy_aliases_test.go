package ntpicture

type Model = legacyModel
type KittyFrameMsg = kittyFrameMsg

var (
	NewModel               = newLegacyModel
	NewModelWithConfig     = newLegacyModelWithConfig
	DefaultTerminalSession = defaultSession
	ForceKittyCapability   = forceKittyCapability
	SetTmuxPassthrough     = setTmuxPassthrough
	KittySupported         = kittySupported
	KittyEnvSignalled      = kittyEnvSignalledFn
	QueryKittySupport      = queryKittySupport
	KittyUnavailable       = kittyUnavailable
	RequestCellSize        = requestCellSize
	IsPictureMsg           = isPictureMsg
)
