package ntpicture

import (
	"fmt"
	"image"

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
	// Kitty placements preserve aspect ratio and cannot composite our background.
	// Preserve the normal renderer's pixel semantics when either is required.
	if !bgIsTransparent(cfg.Background) {
		return image.Rectangle{}, false
	}
	target := p.RasterGeometry().Pixels
	if cfg.Fit == FitFill && int64(crop.Dx())*int64(target.Y) != int64(crop.Dy())*int64(target.X) {
		return image.Rectangle{}, false
	}
	return crop.Sub(b.Min), true
}

func (transport kittyTransport) buildKittyPutAPC(id, placement, cols, rows int, crop image.Rectangle, z int) string {
	return transport.wrap(fmt.Sprintf("\x1b_Ga=p,U=1,i=%d,p=%d,c=%d,r=%d,x=%d,y=%d,w=%d,h=%d,z=%d,q=2\x1b\\",
		id, placement, cols, rows, crop.Min.X, crop.Min.Y, crop.Dx(), crop.Dy(), z))
}
