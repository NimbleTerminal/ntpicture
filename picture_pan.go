package ntpicture

import (
	"fmt"
	"image"
	"os"
	"runtime"
	"strings"

	"nimbleterminal.dev/ntpicture/internal/framecache"
)

// MaxKittyPanPixels bounds the whole source uploaded for source-rectangle
// panning. Larger sources use ordinary per-crop preparation instead.
const MaxKittyPanPixels = 16 << 20

const kittyPanPlacementID = 1

func (p *Picture) canKittyPan(img image.Image, cfg Config, transport kittyTransport) (image.Rectangle, bool) {
	if p.mode != PictureKitty || img == nil || transport.disableVirtualCrop || cfg.Crop.Empty() {
		return image.Rectangle{}, false
	}
	if _, tagged := img.(*framecache.Image); tagged {
		return image.Rectangle{}, false
	}
	if cfg.Fit != FitContain && cfg.Fit != FitFill {
		return image.Rectangle{}, false
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || w > MaxKittyPanPixels/h {
		return image.Rectangle{}, false
	}
	if cfg.MaxRasterPixels > 0 && w > cfg.MaxRasterPixels/h {
		return image.Rectangle{}, false
	}
	crop := cfg.Crop.Intersect(b)
	if crop.Empty() {
		return image.Rectangle{}, false
	}
	return crop.Sub(b.Min), true
}

// Ghostty's virtual placements need per-crop images. Explicit capability
// overrides remain available for hosts that know their terminal supports crops.
func ghosttyEnvSignal() bool {
	return runtime.GOOS == "js" || strings.Contains(strings.ToLower(os.Getenv("TERM")), "ghostty") ||
		strings.Contains(strings.ToLower(os.Getenv("TERM_PROGRAM")), "ghostty") || os.Getenv("GHOSTTY_RESOURCES_DIR") != ""
}

func (transport kittyTransport) buildKittyPutAPC(id, placement, cols, rows int, crop image.Rectangle, z int) string {
	return transport.wrap(fmt.Sprintf("\x1b_Ga=p,U=1,i=%d,p=%d,c=%d,r=%d,x=%d,y=%d,w=%d,h=%d,z=%d,q=2\x1b\\",
		id, placement, cols, rows, crop.Min.X, crop.Min.Y, crop.Dx(), crop.Dy(), z))
}
