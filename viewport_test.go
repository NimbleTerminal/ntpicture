package ntpicture

import (
	"image"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

func TestViewportScrollDoesNotPrepareOrTransmit(t *testing.T) {
	for _, mode := range []bool{false, true} {
		p := testPicture(mode)
		drivePicture(t, p, p.SetSize(5, 4))
		drivePicture(t, p, p.SetImage(randomNRGBA(5, 8)))
		original, front, generation := p.View(), p.front, p.Status().Generation
		for _, v := range []Viewport{{1, 1, 3, 2}, {2, 2, 3, 2}, {0, 0, 1, 1}} {
			p.SetViewport(v)
			view := p.View()
			if len(strings.Split(view, "\n")) != v.Height {
				t.Fatal("incorrect viewport height")
			}
			if mode {
				expected := string([]rune{kitty.Placeholder, kitty.Diacritic(v.Y), kitty.Diacritic(v.X)})
				if !strings.Contains(view, expected) {
					t.Fatal("Kitty coordinates were rebased")
				}
			} else {
				lines := strings.Split(original, "\n")
				for i, row := range strings.Split(view, "\n") {
					want := ansi.Cut(lines[v.Y+i], v.X, v.X+v.Width) + "\x1b[0m"
					if row != want {
						t.Fatal("glyph viewport changed cells or colors")
					}
				}
			}
			if p.front != front || p.job != nil || p.Status().Generation != generation {
				t.Fatal("scroll scheduled rendering")
			}
		}
		p.ResetViewport()
		if p.View() != original {
			t.Fatal("reset did not restore original view")
		}
		drivePicture(t, p, p.Close())
	}
}

func TestViewportPendingReplacementResizeAndKeep(t *testing.T) {
	p := testPicture(true)
	drivePicture(t, p, p.SetSize(4, 4))
	drivePicture(t, p, p.SetImage(randomNRGBA(4, 8)))
	pending := p.SetImage(randomNRGBA(4, 8))
	generation := p.Status().Generation
	p.SetViewport(Viewport{1, 1, 2, 2})
	before := p.View()
	if before == "" || p.Status().Generation != generation || p.job.ctx.Err() != nil {
		t.Fatal("viewport cancelled replacement")
	}
	drivePicture(t, p, pending)
	p.SetViewport(Viewport{3, 3, 3, 3})
	if strings.Count(p.View(), "\n") != 0 {
		t.Fatal("viewport not clipped to layout")
	}
	pending = p.SetSize(6, 6)
	if strings.Count(p.View(), "\n") != 2 {
		t.Fatal("retained front not padded during resize")
	}
	drivePicture(t, p, pending)
	before = p.View()
	out := output(drivePicture(t, p, p.Keep()))
	if out != "" || p.View() != before {
		t.Fatal("keep retransmitted direct pixels or changed viewport")
	}
	p.ResetViewport()
	p.SetViewport(Viewport{})
	if p.View() != before {
		t.Fatal("closed viewport changed")
	}
}

func TestViewportBounds(t *testing.T) {
	for _, tc := range []struct {
		cols, rows int
		v          Viewport
		want       image.Rectangle
	}{
		{10, 10, Viewport{1, 2, 3, 4}, image.Rect(1, 2, 4, 6)},
		{10, 10, Viewport{-1, -2, 3, 4}, image.Rect(0, 0, 2, 2)},
		{10, 10, Viewport{10, 0, 3, 4}, image.Rectangle{}},
		{10, 10, Viewport{0, 0, 0, 4}, image.Rectangle{}},
		{10, 10, Viewport{-10, -10, 2, 2}, image.Rectangle{}},
		{10, 10, Viewport{1, 1, int(^uint(0) >> 1), int(^uint(0) >> 1)}, image.Rect(1, 1, 10, 10)},
		{0, 0, Viewport{-2, -1, 6, 5}, image.Rectangle{}},
		{5, -85, Viewport{-2, -127, 6, 137}, image.Rectangle{}},
	} {
		if got := tc.v.bounds(tc.cols, tc.rows); got != tc.want {
			t.Fatalf("(%d,%d) %+v: %v != %v", tc.cols, tc.rows, tc.v, got, tc.want)
		}
	}
}

func FuzzViewportAndGeometry(f *testing.F) {
	f.Add(10, 10, 1, 2, 3, 4, 8, 16, uint8(0), uint8(0), false)
	f.Add(5, 4, -2, -1, 6, 5, 3, 7, uint8(1), uint8(2), true)
	f.Add(0, 0, -100, 100, 50, 50, 1, 1, uint8(2), uint8(4), true)
	f.Add(5, -85, -2, -127, 6, 137, 1, 1, uint8(0), uint8(0), false)
	f.Fuzz(func(t *testing.T, cols, rows, vx, vy, vw, vh, srcW, srcH int, fit, anchor uint8, kittyMode bool) {
		v := Viewport{X: vx, Y: vy, Width: vw, Height: vh}
		rect := v.bounds(cols, rows)
		if cols <= 0 || rows <= 0 || vw <= 0 || vh <= 0 {
			if !rect.Empty() {
				t.Fatalf("non-empty bounds %v for cols=%d rows=%d v=%+v", rect, cols, rows, v)
			}
		} else if !rect.Empty() {
			if !rect.In(image.Rect(0, 0, cols, rows)) || rect.Dx() > vw || rect.Dy() > vh {
				t.Fatalf("bounds %v out of range for cols=%d rows=%d v=%+v", rect, cols, rows, v)
			}
		}

		c := cols % 9
		r := rows % 9
		sw := (srcW & 7) + 1
		sh := (srcH & 7) + 1
		p := New(Options{
			Render: Config{
				Fit:    FitMode(fit % 4),
				Anchor: FitAnchor(anchor % 6),
			},
			RetireDelay: -1,
		})
		if kittyMode {
			drivePicture(t, p, p.SetMode(PictureKitty))
		}
		drivePicture(t, p, p.SetSize(c, r))
		drivePicture(t, p, p.SetImage(randomNRGBA(sw, sh)))
		p.SetViewport(v)
		view := p.View()
		expected := v.bounds(c, r)
		if c <= 0 || r <= 0 || expected.Empty() {
			if view != "" {
				t.Fatalf("expected empty view, got %q", view)
			}
		} else if gotLines := strings.Count(view, "\n") + 1; gotLines != expected.Dy() {
			t.Fatalf("lines %d != %d", gotLines, expected.Dy())
		}
		geom := p.RasterGeometry()
		if c > 0 && r > 0 {
			if geom.Pixels.X <= 0 || geom.Pixels.Y <= 0 || geom.DisplayAspect <= 0 {
				t.Fatalf("invalid geometry: %+v", geom)
			}
		}
		drivePicture(t, p, p.Close())
	})
}
