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
	"golang.org/x/image/draw"
	"nimbleterminal.dev/ntpicture/internal/imageview"
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
	// are zero the image is shown at its own size, shrunk to fit MaxWidth x
	// MaxHeight.
	Width, Height int

	// MaxWidth and MaxHeight bound a dimension that Width or Height leaves
	// at zero, in cells. With both Width and Height zero they form a box the
	// image is shrunk to fit, keeping its aspect ratio, so nothing is
	// letterboxed. Zero means the terminal's size (its height less one line).
	MaxWidth, MaxHeight int

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

	// TmuxPassthrough wraps Kitty sequences for tmux. Nil reads TMUX and
	// NT_TMUX.
	TmuxPassthrough *bool

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
	isTerminalFn       = term.IsTerminal
	terminalGeometryFn = readTerminalGeometry
)

// A process has one controlling terminal, so it is asked once.
var renderProbe struct {
	sync.Mutex
	done   bool
	result ProbeResult
}

// cachedProbeCellSize returns the cell size an earlier probe learned, if any,
// without probing.
func cachedProbeCellSize() (w, h int) {
	renderProbe.Lock()
	defer renderProbe.Unlock()
	return renderProbe.result.CellPixelWidth, renderProbe.result.CellPixelHeight
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
// With RenderAuto, the first call may ask the controlling terminal whether it
// supports Kitty graphics, turning off echo, line buffering, and signal keys
// while it waits up to a second for the reply. A signal key such as Ctrl+C
// typed meanwhile takes effect once the terminal is restored; other keys are
// lost. A background job is never probed. Set Mode or RenderOptions.Kitty to
// avoid detection. Detection reads terminal input, so inside
// a Bubble Tea program use Picture instead, and in another event loop use Probe. Detection is available on Linux,
// macOS, and the BSDs; other platforms use glyphs unless told otherwise.
//
// cleanup deletes the Kitty image from the terminal and is never nil. Call it
// when the image should disappear, typically with defer. It does nothing for
// glyph output and is safe to call more than once. To leave the image in the
// scrollback, as Print does, do not call it.
func Render(img image.Image, opts RenderOptions) (output string, cleanup func(), err error) {
	return render(img, opts, true)
}

// Print renders img and writes it to w, followed by a newline. The image stays
// on screen; use Render to delete it later.
//
// RenderAuto detects Kitty graphics only when w is a terminal, meaning it has
// an Fd method, as *os.File does, that reports one. For any other writer,
// such as a file, pipe, bytes.Buffer, or bufio.Writer, it never opens the
// terminal: it writes glyphs, unless RenderOptions.Kitty says the destination
// supports Kitty graphics, and sizes them from RenderOptions alone, assuming
// an 80x24 terminal with 8x16 cells. To detect for a wrapped terminal writer,
// give the wrapper an Fd method or write the result of Render.
func Print(w io.Writer, img image.Image, opts RenderOptions) error {
	terminal := isTerminalWriter(w)
	if opts.Mode == RenderAuto && opts.Kitty != KittyCapabilitySupported && !terminal {
		opts.Mode = RenderGlyph
	}
	out, _, err := render(img, opts, terminal)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, out+"\n")
	return err
}

func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(interface{ Fd() uintptr })
	return ok && isTerminalFn(f.Fd())
}

// render does the work of Render and Print. terminal reports that the output
// goes to the controlling terminal, which may then be asked for its size and,
// in RenderAuto, its capabilities.
func render(img image.Image, opts RenderOptions, terminal bool) (string, func(), error) {
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
	if opts.TmuxPassthrough != nil {
		tmux = *opts.TmuxPassthrough
	}
	var geom terminalGeometry
	if terminal {
		geom = terminalGeometryFn()
	}
	cellW, cellH := geom.cellW, geom.cellH
	if terminal && (cellW <= 0 || cellH <= 0) {
		// Some terminals report their cell size only to the probe.
		cellW, cellH = cachedProbeCellSize()
	}

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

	maxCols, maxRows := 80, 24
	if geom.cols > 0 {
		maxCols = geom.cols
	}
	if geom.rows > 0 {
		maxRows = max(geom.rows-1, 1) // leave the prompt's line
	}
	if opts.MaxWidth > 0 {
		maxCols = opts.MaxWidth
	}
	if opts.MaxHeight > 0 {
		maxRows = opts.MaxHeight
	}
	cols, rows := renderSize(opts.Width, opts.Height, b, mode, cellW, cellH, maxCols, maxRows)
	if err := geometryError(cols, rows, cellW, cellH, mode); err != nil {
		return "", noop, err
	}

	if mode == PictureGlyph {
		prepared := prepareGlyphSource(img, cfg, cols, rows)
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

// prepareGlyphSource fits img into the two-pixels-per-cell glyph raster. A
// glyph pixel is square only when cells are twice as tall as wide. For other
// cells the fit is computed in the cell rectangle's real proportions, so what
// is displayed keeps its shape and agrees with the size renderSize derived.
// The image is resampled once, straight onto the cols x 2*rows raster.
func prepareGlyphSource(img image.Image, cfg Config, cols, rows int) image.Image {
	cellW, cellH := cfg.CellPixelWidth, cfg.CellPixelHeight
	if cfg.Fit == FitFill || cellH == 2*cellW {
		return prepareSourceCrop(img, cfg.Crop, cfg.Fit, cols, rows, 1, 2, cfg.Background, cfg.Anchor)
	}
	src := imageview.Unwrap(img)
	sb := src.Bounds()
	target := image.Rect(0, 0, cols, 2*rows)
	out := image.NewRGBA(target)
	draw.Draw(out, target, &image.Uniform{C: cfg.Background}, image.Point{}, draw.Src)

	// A raster pixel is cellW wide and cellH/2 tall on screen, so the image
	// measured in raster pixels is proportionally vw wide and vh tall.
	vw := float64(sb.Dx()) * float64(cellH)
	vh := float64(sb.Dy()) * 2 * float64(cellW)
	tw, th := float64(target.Dx()), float64(target.Dy())
	wider := vw*th > vh*tw // the image is wider than the raster, proportionally
	span := func(full int, fraction float64) int {
		return int(math.Max(1, math.Min(math.Round(float64(full)*fraction), float64(full))))
	}
	if cfg.Fit == FitCover {
		// Crop the source, centered, to the raster's shape.
		crop := sb
		if wider {
			keep := span(sb.Dx(), (vh*tw)/(vw*th))
			crop.Min.X += (sb.Dx() - keep) / 2
			crop.Max.X = crop.Min.X + keep
		} else {
			keep := span(sb.Dy(), (vw*th)/(vh*tw))
			crop.Min.Y += (sb.Dy() - keep) / 2
			crop.Max.Y = crop.Min.Y + keep
		}
		place(out, target, src, crop, nil)
		return out
	}
	dst := target
	if wider {
		keep := span(target.Dy(), (vh*tw)/(vw*th))
		dst.Min.Y = (target.Dy() - keep) / 2
		dst.Max.Y = dst.Min.Y + keep
	} else {
		keep := span(target.Dx(), (vw*th)/(vh*tw))
		dst.Min.X = (target.Dx() - keep) / 2
		dst.Max.X = dst.Min.X + keep
	}
	place(out, dst, src, sb, nil)
	return out
}

// renderSize resolves the cell rectangle. A dimension given as zero is
// derived from the image's aspect ratio on screen and bounded by maxCols or
// maxRows. With neither given, the image is shown at its own size (one image
// pixel per cell column in glyphs), shrunk to fit maxCols x maxRows.
func renderSize(width, height int, b image.Rectangle, mode PictureMode, cellW, cellH, maxCols, maxRows int) (cols, rows int) {
	aspect := float64(b.Dy()) / float64(b.Dx()) * float64(cellW) / float64(cellH) // rows per column
	rowsFor := func(cols int) int { return clampCells(float64(cols) * aspect) }
	colsFor := func(rows int) int { return clampCells(float64(rows) / aspect) }
	switch {
	case width > 0 && height > 0:
		return width, height
	case width > 0:
		return width, min(rowsFor(width), maxRows)
	case height > 0:
		return min(colsFor(height), maxCols), height
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
