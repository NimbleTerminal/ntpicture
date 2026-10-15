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
		factor := p.opts.Render.KittyResolutionFactor
		g.Pixels = image.Pt(max(1, int(math.Round(width*factor))), max(1, int(math.Round(height*factor))))
	}
	return g
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
	return p.changed()
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
	return p.changed()
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
	return p.changed()
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
	return p.changed()
}
