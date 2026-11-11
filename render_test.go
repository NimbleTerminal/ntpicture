package ntpicture

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"
)

// stubRenderTerminal keeps Render away from the terminal running the tests
// and returns a pointer to the number of probes sent.
func stubRenderTerminal(t *testing.T, probe terminalProbe, geom terminalGeometry) *int {
	t.Helper()
	t.Setenv("NTPICTURE_KITTY", "")
	t.Setenv("NTPICTURE_TMUX_PASSTHROUGH", "0")
	probes := new(int)
	oldProbe, oldGeom := probeTerminalFn, terminalGeometryFn
	probeTerminalFn = func(bool, time.Duration) terminalProbe { *probes++; return probe }
	terminalGeometryFn = func() terminalGeometry { return geom }
	reset := func() {
		renderProbe.Lock()
		renderProbe.done, renderProbe.result = false, terminalProbe{}
		renderProbe.Unlock()
	}
	reset()
	t.Cleanup(func() {
		probeTerminalFn, terminalGeometryFn = oldProbe, oldGeom
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
	probes := stubRenderTerminal(t, terminalProbe{kitty: true}, terminalGeometry{})
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
	probes := stubRenderTerminal(t, terminalProbe{}, terminalGeometry{})
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
	stubRenderTerminal(t, terminalProbe{kitty: true, cellW: 10, cellH: 20}, terminalGeometry{})
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
	probes := stubRenderTerminal(t, terminalProbe{}, terminalGeometry{})
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
	t.Setenv("NTPICTURE_KITTY", "supported")
	if out, _, err = Render(img, RenderOptions{Width: 2, Height: 1}); err != nil || !strings.HasPrefix(out, "\x1b_G") {
		t.Fatalf("environment override: out=%q err=%v", out, err)
	}
	if *probes != 0 {
		t.Fatalf("overrides probed %d times", *probes)
	}
}

func TestRenderSize(t *testing.T) {
	square, wide := image.Rect(0, 0, 100, 100), image.Rect(0, 0, 4000, 1000)
	for _, tc := range []struct {
		name               string
		w, h               int
		b                  image.Rectangle
		mode               PictureMode
		termCols, termRows int
		wantCols, wantRows int
	}{
		{"explicit", 7, 3, square, PictureGlyph, 0, 0, 7, 3},
		{"width only", 40, 0, square, PictureGlyph, 0, 0, 40, 20},
		{"height only", 0, 10, square, PictureGlyph, 0, 0, 20, 10},
		{"glyph natural size", 0, 0, image.Rect(0, 0, 20, 20), PictureGlyph, 0, 0, 20, 10},
		{"kitty natural size", 0, 0, square, PictureKitty, 0, 0, 13, 7},
		{"shrinks to default width", 0, 0, wide, PictureGlyph, 0, 0, 80, 10},
		{"shrinks to terminal width", 0, 0, wide, PictureGlyph, 120, 50, 120, 15},
		{"shrinks to terminal height", 0, 0, image.Rect(0, 0, 100, 400), PictureGlyph, 80, 11, 5, 10},
		{"never empty", 1, 0, image.Rect(0, 0, 1000, 1), PictureGlyph, 0, 0, 1, 1},
	} {
		cols, rows := renderSize(tc.w, tc.h, tc.b, tc.mode, 8, 16, tc.termCols, tc.termRows)
		if cols != tc.wantCols || rows != tc.wantRows {
			t.Errorf("%s: got %dx%d, want %dx%d", tc.name, cols, rows, tc.wantCols, tc.wantRows)
		}
	}
}

func TestRenderRejectsBadInput(t *testing.T) {
	stubRenderTerminal(t, terminalProbe{}, terminalGeometry{})
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

func TestPrint(t *testing.T) {
	stubRenderTerminal(t, terminalProbe{}, terminalGeometry{})
	var buf bytes.Buffer
	if err := Print(&buf, renderTestImage(4, 4), RenderOptions{Width: 4}); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.HasSuffix(out, "\x1b[0m\n") || strings.Count(out, "\n") != 2 {
		t.Fatalf("want two glyph lines ending in a newline, got %q", out)
	}
}

func TestParseProbeReply(t *testing.T) {
	ok := fmt.Sprintf("\x1b_Gi=%d;OK\x1b\\", kittyProbeID)
	for _, tc := range []struct {
		reply string
		want  terminalProbe
		done  bool
	}{
		{"", terminalProbe{}, false},
		{ok, terminalProbe{kitty: true}, false},
		{ok + "\x1b[6;20;10t\x1b[?62;4c", terminalProbe{kitty: true, cellW: 10, cellH: 20}, true},
		{"\x1b[?1;2c", terminalProbe{}, true},
		{"\x1b_Gi=7;OK\x1b\\\x1b[6;0;0t\x1b[?6", terminalProbe{}, false},
	} {
		got, done := parseProbeReply([]byte(tc.reply))
		if got != tc.want || done != tc.done {
			t.Errorf("%q: got %+v done=%v, want %+v done=%v", tc.reply, got, done, tc.want, tc.done)
		}
	}
	if q := renderProbeQuery(false); !strings.HasSuffix(q, "\x1b[16t\x1b[c") || !strings.HasPrefix(q, "\x1b_Ga=q") {
		t.Errorf("unexpected probe query %q", q)
	}
}
