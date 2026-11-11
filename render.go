package ntpicture

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/x/term"
)

// RenderMode selects the output of Render and Print. The zero value detects.
type RenderMode int8

const (
	RenderAuto  RenderMode = iota // Kitty graphics when the terminal supports them, otherwise glyphs
	RenderGlyph                   // half-block glyphs; works in any true-color terminal
	RenderKitty                   // Kitty graphics, without checking for support
)

// RenderOptions configures Render and Print. The zero value fits the image to
// the terminal and detects Kitty graphics support.
type RenderOptions struct {
	// Width and Height are the size of the result in terminal cells. When one
	// is zero it follows from the other and the image's aspect ratio. When both
	// are zero the image is shown at its own size, shrunk to fit the terminal
	// (80x24 when the terminal size is unknown).
	Width, Height int

	// Fit maps the image onto Width x Height. The zero value is FitContain.
	Fit FitMode

	// Mode is RenderAuto unless set.
	Mode RenderMode

	// Background fills letterboxing and shows through transparent pixels.
	// Nil leaves them transparent under Kitty and black in glyphs.
	Background color.Color

	// CellPixelWidth and CellPixelHeight are the terminal's cell size in
	// pixels, if already known. Zero asks the terminal, then assumes 8x16.
	CellPixelWidth, CellPixelHeight int

	// Kitty states whether the terminal supports Kitty graphics and skips
	// detection. RenderAuto consults it; the NT_KITTY environment
	// variable is used when it is KittyCapabilityUnknown.
	Kitty KittyCapability

	// Output is where the cleanup function returned by Render writes. It
	// must reach the terminal the image was written to. Nil means os.Stdout.
	Output io.Writer
}

// renderProbeTimeout bounds synchronous detection. Terminals answer the
// trailing device-attributes query at once, so only a terminal that answers
// nothing at all waits this long.
const renderProbeTimeout = time.Second

// terminalGeometry is the controlling terminal's size. Zero fields are unknown.
type terminalGeometry struct {
	cols, rows   int
	cellW, cellH int
}

// Replaced in tests, which must not talk to the terminal running them.
var (
	probeTerminalFn    = probeTerminal
	terminalGeometryFn = readTerminalGeometry
)

// A process has one controlling terminal, so it is asked once.
var renderProbe struct {
	sync.Mutex
	done   bool
	result ProbeResult
}

func cachedTerminalProbe(tmux bool) ProbeResult {
	renderProbe.Lock()
	defer renderProbe.Unlock()
	if !renderProbe.done {
		renderProbe.result = probeTerminalFn(tmux, renderProbeTimeout)
		renderProbe.done = true
	}
	return renderProbe.result
}

// Render draws img as a string of Height lines, each Width cells wide, ready
// to write to a terminal: Kitty graphics where supported, half-block glyphs
// elsewhere. It needs no Bubble Tea program.
//
// With RenderAuto, the first call may briefly put the controlling terminal in
// raw mode to ask whether it supports Kitty graphics. Set Mode or
// RenderOptions.Kitty to avoid that. Detection reads terminal input, so inside
// a Bubble Tea program use Picture instead, and in another event loop use Probe. Detection is available on Linux,
// macOS, and the BSDs; other platforms use glyphs unless told otherwise.
//
// cleanup deletes the Kitty image from the terminal and is never nil. Call it
// when the image should disappear, typically with defer. It does nothing for
// glyph output and is safe to call more than once. To leave the image in the
// scrollback, as Print does, do not call it.
func Render(img image.Image, opts RenderOptions) (output string, cleanup func(), err error) {
	return render(img, opts)
}

// Print renders img and writes it to w, followed by a newline. The image stays
// on screen; use Render to delete it later. When w is a file that is not a
// terminal, RenderAuto writes glyphs.
func Print(w io.Writer, img image.Image, opts RenderOptions) error {
	if f, ok := w.(interface{ Fd() uintptr }); ok && opts.Mode == RenderAuto && !term.IsTerminal(f.Fd()) {
		opts.Mode = RenderGlyph
	}
	out, _, err := render(img, opts)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, out+"\n")
	return err
}

func render(img image.Image, opts RenderOptions) (string, func(), error) {
	noop := func() {}
	if img == nil {
		return "", noop, errors.New("ntpicture: nil image")
	}
	b := img.Bounds()
	if b.Empty() {
		return "", noop, errors.New("ntpicture: empty image")
	}
	if b.Dx() > MaxImagePixels/b.Dy() {
		return "", noop, errors.New("ntpicture: image exceeds pixel limit")
	}

	tmux, envKitty, _ := envTerminalConfig()
	geom := terminalGeometryFn()
	cellW, cellH := geom.cellW, geom.cellH

	mode := PictureGlyph
	switch opts.Mode {
	case RenderGlyph:
	case RenderKitty:
		mode = PictureKitty
	case RenderAuto:
		capability := opts.Kitty
		if capability == KittyCapabilityUnknown {
			capability = envKitty
		}
		if capability == KittyCapabilityUnknown {
			probe := cachedTerminalProbe(tmux)
			capability = probe.Kitty
			if probe.CellPixelWidth > 0 && probe.CellPixelHeight > 0 {
				cellW, cellH = probe.CellPixelWidth, probe.CellPixelHeight
			}
		}
		if capability == KittyCapabilitySupported {
			mode = PictureKitty
		}
	default:
		return "", noop, fmt.Errorf("ntpicture: unknown render mode %d", opts.Mode)
	}

	if opts.CellPixelWidth > 0 && opts.CellPixelHeight > 0 {
		cellW, cellH = opts.CellPixelWidth, opts.CellPixelHeight
	}
	cfg := normalizeConfig(Config{
		Background:      opts.Background,
		Fit:             opts.Fit,
		CellPixelWidth:  cellW,
		CellPixelHeight: cellH,
	})
	cellW, cellH = cfg.CellPixelWidth, cfg.CellPixelHeight

	cols, rows := renderSize(opts.Width, opts.Height, b, mode, cellW, cellH, geom.cols, geom.rows)
	if err := geometryError(cols, rows, cellW, cellH, mode); err != nil {
		return "", noop, err
	}

	if mode == PictureGlyph {
		prepared := prepareSourceCrop(img, cfg.Crop, cfg.Fit, cols, rows, 1, 2, cfg.Background, cfg.Anchor)
		if prepared == nil {
			return "", noop, errors.New("ntpicture: could not prepare image")
		}
		return string(appendGlyph(nil, prepared)), noop, nil
	}

	prepared := prepareSourceCrop(img, cfg.Crop, cfg.Fit, cols, rows, cellW, cellH, cfg.Background, cfg.Anchor)
	if prepared == nil {
		return "", noop, errors.New("ntpicture: could not prepare image")
	}
	id, err := NextImageID()
	if err != nil {
		return "", noop, err
	}
	transport := kittyTransport{tmux: tmux}
	apc := transport.buildKittyAPC(prepared, id, cols, rows, cfg.KittyFormat)
	if apc == "" {
		return "", noop, errors.New("ntpicture: could not encode image")
	}
	out := opts.Output
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			if out == nil {
				out = os.Stdout
			}
			_, _ = io.WriteString(out, transport.kittyDeleteImage(id))
		})
	}
	return apc + buildKittyGrid(cols, rows, id), cleanup, nil
}

// renderSize resolves the cell rectangle. Derived dimensions keep the image's
// aspect ratio on screen. With neither given, the image is shown at its own
// size (one image pixel per cell column in glyphs), shrunk to the terminal.
func renderSize(width, height int, b image.Rectangle, mode PictureMode, cellW, cellH, termCols, termRows int) (cols, rows int) {
	aspect := float64(b.Dy()) / float64(b.Dx()) * float64(cellW) / float64(cellH) // rows per column
	rowsFor := func(cols int) int { return clampCells(float64(cols) * aspect) }
	colsFor := func(rows int) int { return clampCells(float64(rows) / aspect) }
	switch {
	case width > 0 && height > 0:
		return width, height
	case width > 0:
		return width, rowsFor(width)
	case height > 0:
		return colsFor(height), height
	}
	maxCols, maxRows := 80, 24
	if termCols > 0 {
		maxCols = termCols
	}
	if termRows > 1 {
		maxRows = termRows - 1 // leave the prompt's line
	}
	cols = b.Dx()
	if mode == PictureKitty {
		cols = (b.Dx() + cellW - 1) / cellW
	}
	cols = min(cols, maxCols)
	rows = rowsFor(cols)
	if rows > maxRows {
		rows = maxRows
		cols = min(cols, colsFor(rows))
	}
	return cols, rows
}

func clampCells(v float64) int {
	return int(math.Max(1, math.Min(math.Round(v), 1<<20)))
}
