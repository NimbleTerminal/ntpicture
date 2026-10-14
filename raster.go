package ntpicture

import (
	"errors"
	"image"
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
		err := errors.New("ntpicture: resolution factor must be finite and in (0,1]")
		p.err = err
		generation := p.generation
		return func() tea.Msg { return ErrorMsg{p, generation, err} }
	}
	before := p.RasterGeometry().Pixels
	p.opts.Render.KittyResolutionFactor = factor
	p.err = nil
	if p.mode != PictureKitty || p.RasterGeometry().Pixels == before {
		return nil
	}
	return p.changed()
}
