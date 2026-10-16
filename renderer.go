// Package ntpicture renders images in Bubble Tea with Kitty graphics or
// half-block glyphs. Picture owns asynchronous replacement and cleanup, and
// TerminalSession scopes terminal capability and cell-geometry discovery.
// The low-level compatibility adapter lives in nimbleterminal.dev/ntpicture/legacy.
// Decoding is opt-in through image format imports or ntpicture/decoders.
package ntpicture

import (
	"context"
	"image"
	"image/color"
	"math"

	"nimbleterminal.dev/ntpicture/internal/kittyshm"
)

// PictureMode selects how images are rendered.
type PictureMode int8

const (
	PictureGlyph PictureMode = iota // Universal half-block ANSI
	PictureKitty                    // High-res Kitty graphics protocol
	PictureAuto                     // Select PictureKitty when session supports Kitty, otherwise PictureGlyph
)

// FitMode controls how the source image is mapped onto the cell rectangle.
// The zero value is FitContain. Out-of-range values are treated as FitContain.
type FitMode int8

const (
	FitContain FitMode = iota // preserve aspect ratio, letterbox (default)
	FitFill                   // stretch to fill the cell rectangle
	FitCover                  // preserve aspect ratio, crop to fill
)

// FitAnchor controls which edge or center is preserved when a fit mode
// must crop overflow. Today only FitCover consults this; FitContain and
// FitFill never crop.
type FitAnchor int8

const (
	AnchorCenter FitAnchor = iota // center crop (default)
	AnchorTop
	AnchorBottom
	AnchorLeft
	AnchorRight
)

const DefaultKittyID = 43

// Sensible defaults for terminal cell pixel size. Most monospace fonts have
// ~1:2 cell ratios (8×16, 9×18, 10×20). Consumers can pre-set via Config or
// update at runtime via Picture.SetTerminal or TerminalSession when the
// terminal reports its actual cell dims (CSI 16 t).
const (
	defaultCellPixelW = 8
	defaultCellPixelH = 16
)

// Config configures rendering policy for Picture (via Options.Render) or
// legacy.Model at construction.
type Config struct {
	// KittyZ sets placement depth; negative values place graphics under text.
	KittyZ     int
	KittyID    int         // default 43 (ignored by Picture, which uses Options.NextID)
	Background color.Color // default color.Transparent (no compositing)

	// Fit controls how the source image is mapped onto the cell
	// rectangle. The zero value is FitContain (preserve aspect ratio,
	// letterbox). Use FitFill for games, boards, tile maps, and other
	// coordinate-aligned UIs where source pixels must map exactly to the
	// configured terminal-cell rectangle.
	Fit FitMode

	// Anchor controls which edge or center is preserved when Fit must
	// crop overflow. Only consulted by FitCover today. Zero value is
	// AnchorCenter, matching prior behavior.
	Anchor FitAnchor

	// Crop selects an optional source-pixel sub-rectangle of the image before
	// Fit scaling. An empty rectangle (the default) uses the full source image.
	Crop image.Rectangle

	// CellPixelWidth and CellPixelHeight are the terminal cell dimensions in
	// pixels. Used in Kitty mode to pre-scale the source image to the c×r
	// cell rectangle's pixel dimensions before encoding, so Kitty's
	// aspect-ratio-preserving placement fills the cell rectangle. Default
	// 8×16 (typical 1:2 font cell). Update at runtime via
	// Picture.SetCellSize, Picture.SetTerminal, or TerminalSession when the
	// terminal reports its real cell size.
	CellPixelWidth  int
	CellPixelHeight int

	// KittyResolutionFactor scales the encoded Kitty image's per-cell
	// pixel resolution. Values in (0, 1] shrink the transmitted bitmap;
	// the terminal upscales the image to fill the c×r cell rectangle on
	// display. Default 1.0 (full terminal-pixel resolution). Smaller
	// values produce a chunkier, lower-bandwidth image — useful when
	// the renderer's per-frame composite cost is the bottleneck (e.g.
	// browser-WASM with ghostty-web), or when matching the perceived
	// fidelity of Glyph mode is desirable. Out-of-range values clamp
	// to 1.0. Legacy Model rounds per-cell dimensions; Picture scales the
	// whole raster, rounding once per dimension. Picture.SetResolution changes
	// this factor at runtime; Picture.RasterGeometry reports the exact target.
	KittyResolutionFactor float64

	// MaxRasterPixels caps the total pixels (X * Y) of Picture's Kitty
	// preparation target in RasterGeometry().Pixels. Zero or negative means
	// no additional limit beyond MaxImagePixels. When the scaled Kitty raster
	// exceeds MaxRasterPixels, Picture scales both dimensions down to fit
	// while preserving aspect ratio (with a minimum of 1x1 pixel). Glyph mode
	// is unaffected because glyph resolution is fixed at cols x 2*rows.
	MaxRasterPixels int

	// KittyFormat selects how Kitty frames are transmitted: PNG (default;
	// small on the wire, one encode per frame), raw RGBA (no encode or
	// decode, about 5.3 bytes per pixel through the terminal stream), or
	// zlib-compressed RGBA (KittyFormatZlib).
	KittyFormat KittyFormat

	// KittyMedium chooses direct transmission (default) or shared memory.
	// Shared memory always uses raw RGBA; KittyFormat selects the direct fallback.
	KittyMedium KittyMedium
}

func normalizeFit(fit FitMode) FitMode {
	switch fit {
	case FitFill, FitCover:
		return fit
	}
	return FitContain
}

func normalizeAnchor(anchor FitAnchor) FitAnchor {
	switch anchor {
	case AnchorTop, AnchorBottom, AnchorLeft, AnchorRight:
		return anchor
	}
	return AnchorCenter
}

func normalizeCrop(crop image.Rectangle) image.Rectangle {
	crop = crop.Canon()
	if crop.Empty() {
		return image.Rectangle{}
	}
	return crop
}

func normalizeMaxRasterPixels(maxPixels int) int {
	if maxPixels <= 0 {
		return 0
	}
	return min(maxPixels, MaxImagePixels)
}

func normalizeConfig(cfg Config) Config {
	if cfg.KittyID <= 0 || cfg.KittyID >= 1<<24 {
		cfg.KittyID = DefaultKittyID
	}
	if cfg.Background == nil {
		cfg.Background = color.Transparent
	}
	cfg.Background = color.RGBA64Model.Convert(cfg.Background)
	cfg.Fit = normalizeFit(cfg.Fit)
	cfg.Anchor = normalizeAnchor(cfg.Anchor)
	cfg.Crop = normalizeCrop(cfg.Crop)
	if cfg.CellPixelWidth <= 0 {
		cfg.CellPixelWidth = defaultCellPixelW
	}
	if cfg.CellPixelHeight <= 0 {
		cfg.CellPixelHeight = defaultCellPixelH
	}
	if math.IsNaN(cfg.KittyResolutionFactor) || cfg.KittyResolutionFactor <= 0 || cfg.KittyResolutionFactor > 1 {
		cfg.KittyResolutionFactor = 1.0
	}
	cfg.MaxRasterPixels = normalizeMaxRasterPixels(cfg.MaxRasterPixels)
	cfg.KittyFormat = normalizeKittyFormat(cfg.KittyFormat)
	cfg.KittyMedium = normalizeKittyMedium(cfg.KittyMedium)
	return cfg
}

type kittyPrepareParams struct {
	modelID         uint64
	seq             uint64
	img             image.Image
	kittyZ, kittyID int
	background      color.Color
	fit             FitMode
	anchor          FitAnchor
	crop            image.Rectangle
	format          KittyFormat
	medium          KittyMedium
	cols, rows      int
	cpw, cph        int
	rasterSize      image.Point
	prevGeom        kittyGeom
	transport       kittyTransport
	shared          *kittySharedState
}

func prepareKittyFrame(ctx context.Context, p kittyPrepareParams) (kittyFrameMsg, bool) {
	if ctx.Err() != nil {
		return kittyFrameMsg{}, false
	}
	var prepared image.Image
	if p.rasterSize.X > 0 && p.rasterSize.Y > 0 {
		prepared = prepareSourceCrop(p.img, p.crop, p.fit, p.rasterSize.X, p.rasterSize.Y, 1, 1, p.background, p.anchor)
	} else {
		prepared = prepareSourceCrop(p.img, p.crop, p.fit, p.cols, p.rows, p.cpw, p.cph, p.background, p.anchor)
	}
	if prepared == nil || ctx.Err() != nil {
		return kittyFrameMsg{}, false
	}
	actualMedium, actualFormat := KittyMediumDirect, p.format
	var apc string
	var object *kittyshm.Object
	if p.medium == KittyMediumSharedMemory && p.transport.shared {
		var err error
		apc, object, err = p.transport.buildKittySharedMemoryAPC(prepared, p.kittyID, p.cols, p.rows, p.kittyZ)
		if err == nil {
			actualMedium, actualFormat = KittyMediumSharedMemory, KittyFormatRGBA
		}
	}
	if actualMedium == KittyMediumDirect {
		// Yield around expensive direct encoding, including shared-memory
		// fallback, so browser events can drain. Native yields are no-ops.
		yieldToJS()
		apc = p.transport.buildKittyAPC(prepared, p.kittyID, p.cols, p.rows, p.format, p.kittyZ)
		yieldToJS()
	}
	if ctx.Err() != nil {
		_ = object.Unlink()
		return kittyFrameMsg{}, false
	}
	if !p.shared.replace(p.seq, object) {
		return kittyFrameMsg{}, false
	}
	currGeom := kittyGeom{cols: p.cols, rows: p.rows, cellPixelW: p.cpw, cellPixelH: p.cph, fit: p.fit, anchor: p.anchor}
	if p.prevGeom != (kittyGeom{}) && p.prevGeom != currGeom {
		apc = p.transport.kittyDeleteImage(p.kittyID) + apc
	}
	grid := buildKittyGrid(p.cols, p.rows, p.kittyID)
	return kittyFrameMsg{
		modelID:      p.modelID,
		ID:           p.kittyID,
		Seq:          p.seq,
		APC:          apc,
		Grid:         grid,
		Format:       actualFormat,
		Medium:       actualMedium,
		sharedMemory: object,
	}, true
}
