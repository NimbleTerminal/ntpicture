package ntpicture

import (
	"bytes"
	"image"
	"image/color"
	"io"
	"strings"
	"testing"
	"time"
)

// stubRenderTerminal keeps Render away from the terminal running the tests
// and returns a pointer to the number of probes sent.
func stubRenderTerminal(t *testing.T, probe ProbeResult, geom terminalGeometry) *int {
	t.Helper()
	t.Setenv("NT_KITTY", "")
	t.Setenv("NT_TMUX", "0")
	probes := new(int)
	oldProbe, oldGeom, oldIsTerminal := probeTerminalFn, terminalGeometryFn, isTerminalFn
	probeTerminalFn = func(bool, time.Duration) ProbeResult { *probes++; return probe }
	terminalGeometryFn = func() terminalGeometry { return geom }
	reset := func() {
		renderProbe.Lock()
		renderProbe.done, renderProbe.result = false, ProbeResult{}
		renderProbe.Unlock()
	}
	reset()
	t.Cleanup(func() {
		probeTerminalFn, terminalGeometryFn, isTerminalFn = oldProbe, oldGeom, oldIsTerminal
		reset()
	})
	return probes
}

func renderTestImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: 128, A: 255})
		}
	}
	return img
}

func TestRenderGlyph(t *testing.T) {
	probes := stubRenderTerminal(t, ProbeResult{Kitty: KittyCapabilitySupported}, terminalGeometry{})
	img := renderTestImage(8, 8)
	out, cleanup, err := Render(img, RenderOptions{Width: 4, Height: 2, Mode: RenderGlyph, Fit: FitFill})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup == nil {
		t.Fatal("cleanup is nil")
	}
	cleanup()
	cleanup()
	if *probes != 0 {
		t.Fatalf("explicit glyph mode probed %d times", *probes)
	}
	want := string(appendGlyph(nil, prepareSource(img, FitFill, 4, 2, 1, 2, color.Transparent, AnchorCenter)))
	if out != want {
		t.Fatalf("glyph output differs from the shared encoder:\n%q\n%q", out, want)
	}
	if lines := strings.Split(out, "\n"); len(lines) != 2 || strings.Count(lines[0], "▄") != 4 {
		t.Fatalf("want 2 lines of 4 cells, got %q", out)
	}
}

func TestRenderAutoFallsBackToGlyph(t *testing.T) {
	probes := stubRenderTerminal(t, ProbeResult{Kitty: KittyCapabilityUnsupported}, terminalGeometry{})
	for range 2 {
		out, cleanup, err := Render(renderTestImage(4, 4), RenderOptions{Width: 4, Height: 2})
		if err != nil {
			t.Fatal(err)
		}
		cleanup()
		if out == "" || strings.Contains(out, "\x1b_G") || !strings.Contains(out, "▄") {
			t.Fatalf("want glyph output, got %q", out)
		}
	}
	if *probes != 1 {
		t.Fatalf("want one cached probe, got %d", *probes)
	}
}

func TestRenderAutoKittyAndCleanup(t *testing.T) {
	stubRenderTerminal(t, ProbeResult{Kitty: KittyCapabilitySupported, CellPixelWidth: 10, CellPixelHeight: 20}, terminalGeometry{})
	var term bytes.Buffer
	out, cleanup, err := Render(renderTestImage(40, 40), RenderOptions{Width: 4, Height: 2, Output: &term})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "\x1b_G") || !strings.Contains(out, "c=4,r=2") || !strings.Contains(out, "U=1") {
		t.Fatalf("want a Kitty virtual placement of 4x2 cells, got %q", out[:min(len(out), 80)])
	}
	grid := out[strings.LastIndex(out, "\x1b\\")+2:]
	if strings.Count(grid, "\n") != 1 || strings.Count(grid, "\U0010EEEE") != 8 {
		t.Fatalf("want a 4x2 placeholder grid, got %q", grid)
	}
	if term.Len() != 0 {
		t.Fatalf("Render wrote %q before cleanup", term.String())
	}
	cleanup()
	deleted := term.String()
	if !strings.HasPrefix(deleted, "\x1b_Ga=d,d=I,i=") {
		t.Fatalf("want a delete sequence, got %q", deleted)
	}
	cleanup()
	if term.String() != deleted {
		t.Fatal("second cleanup wrote again")
	}
}

func TestRenderKittyOverrides(t *testing.T) {
	probes := stubRenderTerminal(t, ProbeResult{Kitty: KittyCapabilityUnsupported}, terminalGeometry{})
	img := renderTestImage(16, 16)
	var sink bytes.Buffer
	for name, opts := range map[string]RenderOptions{
		"mode":       {Width: 2, Height: 1, Mode: RenderKitty, Output: &sink},
		"capability": {Width: 2, Height: 1, Kitty: KittyCapabilitySupported, Output: &sink},
	} {
		out, cleanup, err := Render(img, opts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cleanup()
		if !strings.HasPrefix(out, "\x1b_G") {
			t.Fatalf("%s: want Kitty output, got %q", name, out)
		}
	}
	out, _, err := Render(img, RenderOptions{Width: 2, Height: 1, Kitty: KittyCapabilityUnsupported})
	if err != nil || strings.Contains(out, "\x1b_G") {
		t.Fatalf("unsupported override: out=%q err=%v", out, err)
	}
	t.Setenv("NT_KITTY", "supported")
	if out, _, err = Render(img, RenderOptions{Width: 2, Height: 1}); err != nil || !strings.HasPrefix(out, "\x1b_G") {
		t.Fatalf("environment override: out=%q err=%v", out, err)
	}
	if *probes != 0 {
		t.Fatalf("overrides probed %d times", *probes)
	}
}

func TestRenderSize(t *testing.T) {
	square, wide, page := image.Rect(0, 0, 100, 100), image.Rect(0, 0, 4000, 1000), image.Rect(0, 0, 520, 1600)
	for _, tc := range []struct {
		name               string
		w, h               int
		b                  image.Rectangle
		mode               PictureMode
		maxCols, maxRows   int
		wantCols, wantRows int
	}{
		{"explicit", 7, 3, square, PictureGlyph, 80, 24, 7, 3},
		{"explicit ignores bounds", 90, 30, square, PictureGlyph, 80, 24, 90, 30},
		{"width only", 40, 0, square, PictureGlyph, 80, 24, 40, 20},
		{"width only, height bounded", 80, 0, square, PictureGlyph, 80, 24, 80, 24},
		{"height only", 0, 10, square, PictureGlyph, 80, 24, 20, 10},
		{"height only, width bounded", 0, 10, wide, PictureGlyph, 30, 24, 30, 10},
		{"glyph natural size", 0, 0, image.Rect(0, 0, 20, 20), PictureGlyph, 80, 24, 20, 10},
		{"kitty natural size", 0, 0, square, PictureKitty, 80, 24, 13, 7},
		{"shrinks to width", 0, 0, wide, PictureGlyph, 80, 24, 80, 10},
		{"shrinks to height", 0, 0, image.Rect(0, 0, 100, 400), PictureGlyph, 80, 10, 5, 10},
		{"tall page in a box", 0, 0, page, PictureGlyph, 56, 22, 14, 22},
		{"never empty", 1, 0, image.Rect(0, 0, 1000, 1), PictureGlyph, 80, 24, 1, 1},
	} {
		cols, rows := renderSize(tc.w, tc.h, tc.b, tc.mode, 8, 16, tc.maxCols, tc.maxRows)
		if cols != tc.wantCols || rows != tc.wantRows {
			t.Errorf("%s: got %dx%d, want %dx%d", tc.name, cols, rows, tc.wantCols, tc.wantRows)
		}
	}
}

func TestRenderBounds(t *testing.T) {
	for name, tc := range map[string]struct {
		geom       terminalGeometry
		opts       RenderOptions
		cols, rows int
	}{
		"default box":       {terminalGeometry{}, RenderOptions{}, 48, 24},
		"terminal box":      {terminalGeometry{cols: 120, rows: 41}, RenderOptions{}, 80, 40},
		"max box":           {terminalGeometry{cols: 120, rows: 41}, RenderOptions{MaxWidth: 56, MaxHeight: 22}, 44, 22},
		"max width only":    {terminalGeometry{cols: 120, rows: 41}, RenderOptions{MaxWidth: 30}, 30, 15},
		"width, max height": {terminalGeometry{}, RenderOptions{Width: 60, MaxHeight: 9}, 60, 9},
	} {
		stubRenderTerminal(t, ProbeResult{Kitty: KittyCapabilityUnsupported}, tc.geom)
		out, _, err := Render(renderTestImage(400, 400), tc.opts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		lines := strings.Split(out, "\n")
		if cols := strings.Count(lines[0], "▄"); cols != tc.cols || len(lines) != tc.rows {
			t.Errorf("%s: got %dx%d, want %dx%d", name, cols, len(lines), tc.cols, tc.rows)
		}
	}
}

func TestRenderRejectsBadInput(t *testing.T) {
	stubRenderTerminal(t, ProbeResult{Kitty: KittyCapabilityUnsupported}, terminalGeometry{})
	for name, tc := range map[string]struct {
		img  image.Image
		opts RenderOptions
	}{
		"nil image":    {nil, RenderOptions{}},
		"empty image":  {image.NewRGBA(image.Rectangle{}), RenderOptions{}},
		"unknown mode": {renderTestImage(2, 2), RenderOptions{Mode: 9}},
		"too large":    {renderTestImage(2, 2), RenderOptions{Width: 1 << 20, Height: 1 << 20}},
	} {
		out, cleanup, err := Render(tc.img, tc.opts)
		if err == nil || out != "" {
			t.Errorf("%s: want an error, got %q", name, out)
		}
		if cleanup == nil {
			t.Fatalf("%s: cleanup is nil", name)
		}
		cleanup()
	}
}

// fdWriter is a writer with a file descriptor, like *os.File.
type fdWriter struct{ bytes.Buffer }

func (*fdWriter) Fd() uintptr { return 3 }

func TestPrint(t *testing.T) {
	img := renderTestImage(4, 4)
	probes := stubRenderTerminal(t, ProbeResult{Kitty: KittyCapabilitySupported}, terminalGeometry{})

	// Not a terminal: glyphs, and the terminal is never asked.
	isTerminalFn = func(uintptr) bool { return false }
	var buf bytes.Buffer
	var file fdWriter
	for name, w := range map[string]interface {
		io.Writer
		String() string
	}{"buffer": &buf, "file": &file} {
		if err := Print(w, img, RenderOptions{Width: 4}); err != nil {
			t.Fatal(err)
		}
		if out := w.String(); !strings.HasSuffix(out, "\x1b[0m\n") || strings.Count(out, "\n") != 2 || strings.Contains(out, "\x1b_G") {
			t.Fatalf("%s: want two glyph lines ending in a newline, got %q", name, out)
		}
	}
	if *probes != 0 {
		t.Fatalf("Print to a non-terminal probed %d times", *probes)
	}
	t.Setenv("NT_KITTY", "supported")
	buf.Reset()
	if err := Print(&buf, img, RenderOptions{Width: 4}); err != nil || strings.Contains(buf.String(), "\x1b_G") {
		t.Fatalf("NT_KITTY applied to a non-terminal writer: %q %v", buf.String(), err)
	}
	t.Setenv("NT_KITTY", "")

	// The caller vouches for the destination.
	buf.Reset()
	if err := Print(&buf, img, RenderOptions{Width: 4, Kitty: KittyCapabilitySupported}); err != nil || !strings.HasPrefix(buf.String(), "\x1b_G") {
		t.Fatalf("explicit capability: %q %v", buf.String(), err)
	}

	// A terminal: detection runs.
	isTerminalFn = func(uintptr) bool { return true }
	var tty fdWriter
	if err := Print(&tty, img, RenderOptions{Width: 4}); err != nil || !strings.HasPrefix(tty.String(), "\x1b_G") {
		t.Fatalf("terminal writer: %q %v", tty.String(), err)
	}
	if *probes != 1 {
		t.Fatalf("want one probe for a terminal writer, got %d", *probes)
	}
}
