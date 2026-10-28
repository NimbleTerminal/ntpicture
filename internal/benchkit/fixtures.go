// Package benchkit supplies deterministic workloads and a synchronous command
// driver for benchmarks. It is not an alternative application event loop.
package benchkit

import (
	"fmt"
	"image"
	"image/color"
)

const FixtureVersion = 1

var Patterns = []string{"unchanged", "localized", "scattered", "full"}

// Fixtures constructs three immutable, opaque frames outside measured work.
// Localized edits occupy a 10x10 rectangle. Scattered edits touch four corners,
// deliberately making their bounding rectangle span the entire canvas.
func Fixtures(width, height int, pattern string) ([]image.Image, error) {
	if width < 10 || height < 10 || width > 4096 || height > 4096 {
		return nil, fmt.Errorf("fixture dimensions must be 10..4096")
	}
	valid := false
	for _, name := range Patterns {
		valid = valid || name == pattern
	}
	if !valid {
		return nil, fmt.Errorf("unknown pattern %q", pattern)
	}
	out := make([]image.Image, 3)
	for frame := range out {
		img := image.NewNRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				// A chart-like background with gradients, fine lines and repeated regions.
				c := color.NRGBA{R: byte(x * 255 / width), G: byte(y * 255 / height), B: 96, A: 255}
				if x%31 == 0 || y%23 == 0 {
					c = color.NRGBA{R: 32, G: 48, B: 64, A: 255}
				}
				changed := pattern == "full" || (pattern == "localized" && x >= width/2-5 && x < width/2+5 && y >= height/2-5 && y < height/2+5) ||
					(pattern == "scattered" && (x == 0 || x == width-1) && (y == 0 || y == height-1))
				if changed && frame > 0 {
					c.R ^= byte(frame * 71)
					c.G ^= byte(frame * 53)
					c.B ^= byte(frame * 97)
				}
				img.SetNRGBA(x, y, c)
			}
		}
		out[frame] = img
	}
	return out, nil
}
