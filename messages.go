package ntpicture

import (
	tea "charm.land/bubbletea/v2"
	"image"
	"nimbleterminal.dev/ntpicture/internal/kittyshm"
	"nimbleterminal.dev/ntpicture/internal/legacybridge"
)

// kittyFrameMsg carries the result of building a Kitty APC payload + grid for
// a specific generation of a legacyModel.
type kittyFrameMsg struct {
	raster    *image.NRGBA
	unchanged bool

	modelID uint64
	ID      int
	Seq     uint64
	APC     string
	Grid    string
	// Format reports how this frame's pixels were transmitted (PNG, RGBA, or Zlib).
	Format KittyFormat
	// Medium reports the transport actually used, including any direct fallback.
	Medium       KittyMedium
	sharedMemory *kittyshm.Object
}

func wrapKittyFrameMsg(f kittyFrameMsg) tea.Msg {
	if legacybridge.WrapFrameMsg != nil {
		return legacybridge.WrapFrameMsg(legacybridge.FrameFields{
			ModelID:      f.modelID,
			ID:           f.ID,
			Seq:          f.Seq,
			APC:          f.APC,
			Grid:         f.Grid,
			Format:       int8(f.Format),
			Medium:       int8(f.Medium),
			SharedMemory: f.sharedMemory,
		})
	}
	return f
}

func unwrapKittyFrameMsg(msg tea.Msg) (kittyFrameMsg, bool) {
	if f, ok := msg.(kittyFrameMsg); ok {
		return f, true
	}
	if legacybridge.UnwrapFrameMsg != nil {
		if f, ok := legacybridge.UnwrapFrameMsg(msg); ok {
			return kittyFrameMsg{
				modelID:      f.ModelID,
				ID:           f.ID,
				Seq:          f.Seq,
				APC:          f.APC,
				Grid:         f.Grid,
				Format:       KittyFormat(f.Format),
				Medium:       KittyMedium(f.Medium),
				sharedMemory: f.SharedMemory,
			}, true
		}
	}
	return kittyFrameMsg{}, false
}
