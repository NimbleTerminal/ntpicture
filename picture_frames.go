package ntpicture

import (
	"bytes"
	"fmt"
	"image"

	"nimbleterminal.dev/ntpicture/internal/framecache"
)

func dirtyPixels(a, b *image.NRGBA) image.Rectangle {
	var dirty image.Rectangle
	for y := 0; y < b.Rect.Dy(); y++ {
		ar, br := a.Pix[y*a.Stride:][:4*b.Rect.Dx()], b.Pix[y*b.Stride:][:4*b.Rect.Dx()]
		if bytes.Equal(ar, br) {
			continue
		}
		for x := 0; x < b.Rect.Dx(); x++ {
			if !bytes.Equal(ar[x*4:][:4], br[x*4:][:4]) {
				dirty = dirty.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return dirty
}

type pictureFrames struct {
	source *framecache.Source
	frames map[int]int // source position -> terminal append order
}

func (p *Picture) configureFrames(params *kittyPrepareParams, placement *picturePlacement) (string, bool) {
	front := p.front
	compatible := front != nil && front.lease == placement.lease && !placement.ownsID &&
		front.epoch == p.frameEpoch && front.cfg == placement.cfg &&
		!front.lease.overwritten.Load() && front.lease.loadOutputDone() == nil
	if compatible && placement.transport.frameEdits {
		params.baseline = front.raster
	}
	tagged, ok := p.img.(*framecache.Image)
	if !ok || !p.opts.StableID || p.opts.ClearOnReplace {
		return "", false
	}
	params.img = tagged.Image
	if !placement.transport.frameCache {
		return "", false
	}
	size := placement.rasterSize
	src := tagged.Source
	if src == nil || tagged.Frame < 1 || tagged.Frame > src.Frames || src.Frames <= 1 || size.X <= 0 || size.Y <= 0 || src.Bytes <= 0 ||
		int64(src.Frames) > int64(src.Bytes)/(int64(size.X)*int64(size.Y)*4) {
		return "", false
	}
	params.baseline = nil // Cached animation root frames must remain immutable.
	params.retain = false
	frames := &pictureFrames{source: src, frames: map[int]int{}}
	if compatible && front.frames != nil && front.frames.source == src {
		for k, v := range front.frames.frames {
			frames.frames[k] = v
		}
	}
	placement.frames = frames
	if n, found := frames.frames[tagged.Frame]; found {
		return placement.transport.wrap(fmt.Sprintf("\x1b_Ga=a,i=%d,c=%d,q=2\x1b\\", placement.cfg.KittyID, n)), true
	}
	n := len(frames.frames) + 1
	frames.frames[tagged.Frame] = n
	if n == 1 {
		return "", false
	}
	params.transport.frame, params.transport.appendFrame = true, true
	return placement.transport.wrap(fmt.Sprintf("\x1b_Ga=a,i=%d,c=%d,q=2\x1b\\", placement.cfg.KittyID, n)), false
}
