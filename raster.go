package ntpicture

import (
	"errors"
	"image"
	"image/color"
	"math"

	tea "charm.land/bubbletea/v2"
)

// RasterGeometry describes the full picture, before Viewport clipping.
// Pixels is the preparation/encoding target, not the current retained front.
// DisplayAspect is physical width/height from cells and terminal cell geometry;
// use it for a mesh camera even when Pixels has a different ratio (glyphs or
// integer rounding). With no cell area, Pixels and DisplayAspect are zero.
type RasterGeometry struct {
	Cells         image.Point
	Pixels        image.Point
	DisplayAspect float64
	Mode          PictureMode
}

// RasterGeometry lets producers render directly at the target resolution.
// Requery after size, mode, cell-size/session, or resolution changes. Submit an
// opaque image at Pixels with FitFill to avoid additional scaling or compositing.
func (p *Picture) RasterGeometry() RasterGeometry {
	g := RasterGeometry{Cells: image.Pt(p.cols, p.rows), Mode: p.mode}
	if p.cols <= 0 || p.rows <= 0 {
		return g
	}
	cw, ch := p.CellPixelSize()
	width, height := float64(p.cols)*float64(cw), float64(p.rows)*float64(ch)
	g.DisplayAspect = width / height
	g.Pixels = image.Pt(p.cols, 2*p.rows)
	if p.mode == PictureKitty {
		g.Pixels = kittyRasterPixels(width, height, p.opts.Render.KittyResolutionFactor, p.opts.Render.MaxRasterPixels)
	}
	return g
}

func kittyRasterPixels(width, height, factor float64, maxPixels int) image.Point {
	w, h := width*factor, height*factor
	pxW, pxH := max(1, int(math.Round(w))), max(1, int(math.Round(h)))
	if maxPixels <= 0 || pxW <= maxPixels/pxH {
		return image.Pt(pxW, pxH)
	}
	if maxPixels == 1 {
		return image.Pt(1, 1)
	}
	scale := math.Sqrt(float64(maxPixels) / (w * h))
	sw, sh := w*scale, h*scale
	pxW, pxH = max(1, int(math.Round(sw))), max(1, int(math.Round(sh)))
	if pxW <= maxPixels/pxH {
		return image.Pt(pxW, pxH)
	}
	pxW, pxH = max(1, int(math.Floor(sw))), max(1, int(math.Floor(sh)))
	if pxW == 1 && pxH > maxPixels {
		pxH = maxPixels
	} else if pxH == 1 && pxW > maxPixels {
		pxW = maxPixels
	}
	for pxW > maxPixels/pxH && (pxW > 1 || pxH > 1) {
		if pxW >= pxH && pxW > 1 {
			pxW--
		} else if pxH > 1 {
			pxH--
		}
	}
	return image.Pt(pxW, pxH)
}

// MaxRasterPixels reports the configured Kitty raster pixel cap (0 when uncapped).
func (p *Picture) MaxRasterPixels() int { return p.opts.Render.MaxRasterPixels }

// SetMaxRasterPixels sets the Kitty raster pixel cap. Zero or negative removes
// the cap (bounded by MaxImagePixels). When the resulting Kitty target Pixels
// change, a new placement is prepared while retaining the old front.
func (p *Picture) SetMaxRasterPixels(maxPixels int) tea.Cmd {
	if p.closed {
		return nil
	}
	maxPixels = normalizeMaxRasterPixels(maxPixels)
	before := p.RasterGeometry().Pixels
	p.opts.Render.MaxRasterPixels = maxPixels
	if p.mode != PictureKitty || p.RasterGeometry().Pixels == before {
		return nil
	}
	return p.changedConfig()
}

// SetImageFor submits img only if g still matches p.RasterGeometry() (with
// positive pixel dimensions when img is non-nil). If the picture's size, mode,
// cell pixel dimensions, or resolution changed while img was being rendered,
// the stale frame is dropped and SetImageFor returns nil without reporting an
// error or cancelling in-flight work.
func (p *Picture) SetImageFor(img image.Image, g RasterGeometry) tea.Cmd {
	if p.closed || g != p.RasterGeometry() {
		return nil
	}
	if img != nil && (g.Pixels.X <= 0 || g.Pixels.Y <= 0) {
		return nil
	}
	return p.SetImage(img)
}

// SetImageImmutableFor is like SetImageFor, but avoids a pixel copy with the
// same immutability promise as SetImageImmutable.
func (p *Picture) SetImageImmutableFor(img image.Image, g RasterGeometry) tea.Cmd {
	if p.closed || g != p.RasterGeometry() {
		return nil
	}
	if img != nil && (g.Pixels.X <= 0 || g.Pixels.Y <= 0) {
		return nil
	}
	return p.SetImageImmutable(img)
}

// Resolution reports the configured Kitty resolution factor in (0, 1].
func (p *Picture) Resolution() float64 {
	if p == nil {
		return 0
	}
	return p.opts.Render.KittyResolutionFactor
}

// SetResolution sets the Kitty raster scale in (0,1], relative to terminal
// pixels. It preserves cell footprint and physical display aspect. Whole-image
// dimensions are rounded once, with a minimum of one pixel per dimension.
// A changed raster uses a fresh placement, retaining the old front until ready.
// Glyph resolution stays fixed; the factor is remembered for switching to Kitty.
// Invalid values report ErrorMsg without discarding pixels or cancelling work.
// Run the returned command even if submitting a new producer frame immediately.
func (p *Picture) SetResolution(factor float64) tea.Cmd {
	if p.closed {
		return nil
	}
	if math.IsNaN(factor) || math.IsInf(factor, 0) || factor <= 0 || factor > 1 {
		return p.reportError(errors.New("ntpicture: resolution factor must be finite and in (0,1]"))
	}
	before := p.RasterGeometry().Pixels
	p.opts.Render.KittyResolutionFactor = factor
	p.err = nil
	if p.mode != PictureKitty || p.RasterGeometry().Pixels == before {
		return nil
	}
	return p.changedConfig()
}

// Background reports the configured background color.
func (p *Picture) Background() color.Color {
	if p == nil {
		return nil
	}
	return p.opts.Render.Background
}

// SetBackground changes the color composited behind translucent pixels and
// letterbox areas. Nil means transparent. A changed color prepares a new
// generation from the current image, retaining the old front until ready;
// without an image it applies to the next submission.
func (p *Picture) SetBackground(c color.Color) tea.Cmd {
	if p.closed {
		return nil
	}
	if c == nil {
		c = color.Transparent
	}
	c = color.RGBA64Model.Convert(c)
	if c == p.opts.Render.Background {
		return nil
	}
	p.opts.Render.Background = c
	if p.img == nil {
		return nil
	}
	return p.changedConfig()
}

// Fit reports how source images are mapped onto the cell rectangle.
func (p *Picture) Fit() FitMode { return p.opts.Render.Fit }

// SetFit changes how source images are mapped onto the cell rectangle.
// Out-of-range values normalize to FitContain. A changed fit mode prepares a
// new generation from the current image, retaining the old front until ready;
// without an image it applies to the next submission.
func (p *Picture) SetFit(fit FitMode) tea.Cmd {
	if p.closed {
		return nil
	}
	fit = normalizeFit(fit)
	if fit == p.opts.Render.Fit {
		return nil
	}
	p.opts.Render.Fit = fit
	if p.img == nil {
		return nil
	}
	return p.changedConfig()
}

// Anchor reports the crop anchor used by FitCover.
func (p *Picture) Anchor() FitAnchor { return p.opts.Render.Anchor }

// SetAnchor changes the crop anchor used by FitCover. Out-of-range values
// normalize to AnchorCenter. A changed anchor prepares a new generation from
// the current image, retaining the old front until ready; without an image it
// applies to the next submission.
func (p *Picture) SetAnchor(anchor FitAnchor) tea.Cmd {
	if p.closed {
		return nil
	}
	anchor = normalizeAnchor(anchor)
	if anchor == p.opts.Render.Anchor {
		return nil
	}
	p.opts.Render.Anchor = anchor
	if p.img == nil {
		return nil
	}
	return p.changedConfig()
}

// Crop reports the configured source-pixel crop rectangle (empty when uncropped).
func (p *Picture) Crop() image.Rectangle {
	if p == nil {
		return image.Rectangle{}
	}
	return p.opts.Render.Crop
}

// SetCrop sets an optional source-pixel crop rectangle applied before Fit
// scaling. Coordinates are in the source image's bounds coordinate space and
// are intersected with the source image bounds during preparation. Passing an
// empty rectangle clears the crop (equivalent to ResetCrop). A changed crop
// prepares a new generation from the retained image, keeping the current front
// visible until ready; without an image it applies to the next submission.
func (p *Picture) SetCrop(r image.Rectangle) tea.Cmd {
	if p == nil || p.closed {
		return nil
	}
	r = normalizeCrop(r)
	if r == p.opts.Render.Crop {
		return nil
	}
	p.opts.Render.Crop = r
	if p.img == nil {
		return nil
	}
	return p.changedConfig()
}

// ResetCrop clears any source-pixel crop rectangle so the full source image is
// prepared.
func (p *Picture) ResetCrop() tea.Cmd {
	return p.SetCrop(image.Rectangle{})
}
