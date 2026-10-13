package ntpicture

import (
	"fmt"
	"image"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

// Viewport selects a cell rectangle within a Picture's full SetSize dimensions.
// X/Y are source-cell offsets, not screen coordinates. The host positions the
// returned View. Out-of-bounds portions are clipped; nonpositive sizes are empty.
// A viewport does not rescale pixels, change image IDs, or release resources.
// Use SetVisible for resource/visibility policy and SetSize for image layout.
type Viewport struct{ X, Y, Width, Height int }

// SetViewport changes only View, including while a replacement is pending.
// It emits no commands, does no image preparation, and does not cancel playback.
// Subsequent frames use this viewport until ResetViewport is called.
func (p *Picture) SetViewport(v Viewport) {
	if !p.closed {
		p.viewport, p.viewportSet = v, true
	}
}

func (p *Picture) ResetViewport() {
	if !p.closed {
		p.viewportSet = false
	}
}

func (v Viewport) bounds(cols, rows int) image.Rectangle {
	// Clamp before adding sizes, avoiding overflow for arbitrary caller inputs.
	if v.Width <= 0 || v.Height <= 0 || v.X >= cols || v.Y >= rows {
		return image.Rectangle{}
	}
	if v.X < 0 {
		if v.X <= -v.Width {
			return image.Rectangle{}
		}
		v.Width += v.X
		v.X = 0
	}
	if v.Y < 0 {
		if v.Y <= -v.Height {
			return image.Rectangle{}
		}
		v.Height += v.Y
		v.Y = 0
	}
	return image.Rect(v.X, v.Y, v.X+min(v.Width, cols-v.X), v.Y+min(v.Height, rows-v.Y))
}

func (p *Picture) viewportView() string {
	rect := p.viewport.bounds(p.cols, p.rows)
	if rect.Empty() {
		return ""
	}
	front := p.front
	var lines []string
	if front.model.mode != PictureKitty {
		lines = strings.Split(front.grid, "\n")
	}
	var out strings.Builder
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		width := max(0, min(rect.Max.X, front.model.cols)-rect.Min.X)
		if y >= front.model.rows {
			width = 0
		}
		if width > 0 {
			if front.model.mode == PictureKitty {
				// Explicit source row/column diacritics make a clipped grid refer to the
				// original virtual placement; no bitmap slice or new transmission is needed.
				id := front.model.kittyID
				fmt.Fprintf(&out, "\x1b[38;2;%d;%d;%dm", id>>16&255, id>>8&255, id&255)
				for x := rect.Min.X; x < rect.Min.X+width; x++ {
					out.WriteRune(kitty.Placeholder)
					out.WriteRune(kitty.Diacritic(y))
					out.WriteRune(kitty.Diacritic(x))
				}
				out.WriteString("\x1b[39m")
			} else if y < len(lines) {
				out.WriteString(ansi.Cut(lines[y], rect.Min.X, rect.Min.X+width))
				out.WriteString("\x1b[0m")
			}
		}
		out.WriteString(strings.Repeat(" ", rect.Dx()-width))
		if y+1 < rect.Max.Y {
			out.WriteByte('\n')
		}
	}
	return out.String()
}
