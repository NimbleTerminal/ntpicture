package ntpicture

import (
	"errors"
	"fmt"
	"image"

	tea "charm.land/bubbletea/v2"
)

// Frame is a complete submission: source pixels with the cell layout and
// terminal geometry they were produced for. Cell dimensions must be positive.
// A nil Image clears the picture at the new geometry.
type Frame struct {
	Image                 image.Image
	Cols, Rows            int
	Mode                  PictureMode
	CellWidth, CellHeight int
	// Exact requires Image bounds to equal the resulting RasterGeometry().Pixels,
	// so the image is never resampled to fit. Use it with FitFill for QR modules
	// and framebuffers rendered at the target resolution.
	Exact bool
	// Immutable skips the pixel copy, with the same promise as SetImageImmutable.
	Immutable bool
}

// Submit validates the image and its geometry together, then applies both as
// one generation. A rejected submission reports ErrorMsg and changes no size,
// mode or cell geometry; like a rejected SetImage it drops any pending source
// and retains the front unless ClearOnReplace is set. Submit does not probe:
// Mode is applied as given, as with SetTerminal.
func (p *Picture) Submit(f Frame) tea.Cmd {
	if p.closed {
		return nil
	}
	if f.Cols < 0 || f.Rows < 0 || f.CellWidth <= 0 || f.CellHeight <= 0 {
		return p.failSubmission(errors.New("ntpicture: invalid frame geometry"))
	}
	if f.Mode != PictureGlyph && f.Mode != PictureKitty {
		return p.failSubmission(errors.New("ntpicture: invalid picture mode"))
	}
	if f.Image != nil {
		if err := checkImage(f.Image); err != nil {
			return p.failSubmission(err)
		}
	}
	oldCols, oldRows, oldMode, oldConfig := p.cols, p.rows, p.mode, p.opts.Render
	p.cols, p.rows, p.mode = f.Cols, f.Rows, f.Mode
	p.opts.Render.CellPixelWidth, p.opts.Render.CellPixelHeight = f.CellWidth, f.CellHeight
	var err error
	if f.Cols > 0 && f.Rows > 0 {
		err = p.geometryError()
	}
	if err == nil && f.Exact && f.Image != nil {
		if got, want := f.Image.Bounds().Size(), p.RasterGeometry().Pixels; got != want {
			err = fmt.Errorf("ntpicture: frame is %dx%d pixels, raster needs %dx%d", got.X, got.Y, want.X, want.Y)
		}
	}
	if err != nil {
		p.cols, p.rows, p.mode, p.opts.Render = oldCols, oldRows, oldMode, oldConfig
		return p.failSubmission(err)
	}
	img := f.Image
	if img != nil && !f.Immutable {
		img = snapshotImage(img)
	}
	p.img = img
	p.imgID++
	return p.changed()
}
