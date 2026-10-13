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
		v    Viewport
		want image.Rectangle
	}{
		{Viewport{1, 2, 3, 4}, image.Rect(1, 2, 4, 6)},
		{Viewport{-1, -2, 3, 4}, image.Rect(0, 0, 2, 2)},
		{Viewport{10, 0, 3, 4}, image.Rectangle{}},
		{Viewport{0, 0, 0, 4}, image.Rectangle{}},
		{Viewport{-10, -10, 2, 2}, image.Rectangle{}},
		{Viewport{1, 1, int(^uint(0) >> 1), int(^uint(0) >> 1)}, image.Rect(1, 1, 10, 10)},
	} {
		if got := tc.v.bounds(10, 10); got != tc.want {
			t.Fatalf("%+v: %v != %v", tc.v, got, tc.want)
		}
	}
}
