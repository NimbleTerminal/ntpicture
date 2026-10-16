package playback

import (
	"image"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	picture "nimbleterminal.dev/ntpicture"
)

func (l *Layers) spliceOverlays(grid string) string {
	var toSplice []*layerOverlay
	if l.retainedLayout == l.presentedLayout && l.cols == l.retainedCols && l.rows == l.retainedRows {
		for _, o := range l.retainedOverlays {
			if o.presented() {
				toSplice = append(toSplice, o)
			}
		}
	}
	if l.presentedLayout == l.layoutGen && !l.basePending {
		for _, o := range l.overlays {
			if o.presented() {
				toSplice = append(toSplice, o)
			}
		}
	}
	if len(toSplice) == 0 {
		return grid
	}
	lines := strings.Split(grid, "\n")
	for _, o := range toSplice {
		overView := o.ctrl.View()
		if overView == "" {
			continue
		}
		overRows := strings.Split(overView, "\n")
		if len(overRows) != o.cells.Dy() {
			continue
		}
		for i, row := range overRows {
			y := o.cells.Min.Y + i
			if y >= 0 && y < len(lines) {
				lines[y] = spliceCells(lines[y], o.cells.Min.X, o.cells.Max.X, row)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// spliceCells replaces cells [x0, x1) of a Kitty placeholder row with repl.
// It closes any preceding SGR before repl and reopens the SGR that covered x1
// after repl, supporting edge-aligned and multiple disjoint splices per row.
func spliceCells(line string, x0, x1 int, repl string) string {
	var starts []int
	for i, r := range line {
		if r == kitty.Placeholder {
			starts = append(starts, i)
		}
	}
	n := len(starts)
	if x0 < 0 || x0 >= x1 || x1 > n || repl == "" {
		return line
	}
	starts = append(starts, len(line))
	var prefix, suffix string
	if x0 > 0 {
		end := starts[x0]
		if i := strings.IndexByte(line[starts[x0-1]:end], '\x1b'); i >= 0 {
			end = starts[x0-1] + i
		}
		prefix = line[:end] + "\x1b[39m"
	}
	if x1 < n {
		suffix = lastSGR(line[:starts[x1]]) + line[starts[x1]:]
	}
	return prefix + repl + suffix
}

func lastSGR(s string) string {
	i := strings.LastIndex(s, "\x1b[38;2;")
	if i < 0 {
		return ""
	}
	end := strings.IndexByte(s[i:], 'm')
	if end < 0 {
		return ""
	}
	return s[i : i+end+1]
}

func (l *Layers) clipViewport(grid string) string {
	rect := viewportBounds(l.viewport, l.cols, l.rows)
	if rect.Empty() {
		return ""
	}
	lines := strings.Split(grid, "\n")
	var out strings.Builder
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		width := 0
		if y >= 0 && y < len(lines) {
			if l.mode == picture.PictureKitty {
				var row string
				row, width = sliceKittyCells(lines[y], rect.Min.X, rect.Max.X)
				out.WriteString(row)
			} else {
				width = max(0, min(rect.Max.X, l.cols)-rect.Min.X)
				if width > 0 {
					if rect.Min.X == 0 && width == l.cols {
						out.WriteString(lines[y])
					} else {
						out.WriteString(ansi.Cut(lines[y], rect.Min.X, rect.Min.X+width))
						out.WriteString("\x1b[0m")
					}
				}
			}
		}
		if pad := rect.Dx() - width; pad > 0 {
			out.WriteString(strings.Repeat(" ", pad))
		}
		if y+1 < rect.Max.Y {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func sliceKittyCells(line string, x0, x1 int) (string, int) {
	var starts []int
	for i, r := range line {
		if r == kitty.Placeholder {
			starts = append(starts, i)
		}
	}
	n := len(starts)
	if x0 < 0 || x0 >= x1 || x0 >= n {
		return "", 0
	}
	x1 = min(x1, n)
	starts = append(starts, len(line))
	end := starts[x1]
	if i := strings.IndexByte(line[starts[x1-1]:end], '\x1b'); i >= 0 {
		end = starts[x1-1] + i
	}
	return lastSGR(line[:starts[x0]]) + line[starts[x0]:end] + "\x1b[39m", x1 - x0
}

func viewportBounds(v picture.Viewport, cols, rows int) image.Rectangle {
	if cols <= 0 || rows <= 0 || v.Width <= 0 || v.Height <= 0 || v.X >= cols || v.Y >= rows {
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
