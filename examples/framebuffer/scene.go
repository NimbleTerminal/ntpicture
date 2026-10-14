package main

import (
	"context"
	"image"
	"image/color"
	"math"
)

// renderScene draws a rotating wireframe cube above a perspective floor grid.
// Every call returns a fresh framebuffer, so the result can be submitted with
// SetImageImmutable and never touched again. aspect is the physical display
// aspect ratio, which differs from width/height for glyph rasters; the camera
// uses it so the cube stays cubic regardless of pixel shape. The context is
// checked between stages and returns early when the frame has become obsolete.
func renderScene(ctx context.Context, width, height int, aspect float64, phase float64) (*image.RGBA, error) {
	if width <= 0 || height <= 0 {
		return nil, context.Canceled
	}
	if aspect <= 0 {
		aspect = float64(width) / float64(height)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	fillBackground(img)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cam := camera{width: width, height: height, aspect: aspect, focal: 1.6}
	thickness := max(1, min(width, height)/220)
	drawFloor(img, cam, phase, thickness)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	drawCube(img, cam, phase, thickness)
	drawBorder(img)
	return img, ctx.Err()
}

type vec3 struct{ x, y, z float64 }

type camera struct {
	width, height int
	aspect, focal float64
}

// project maps a camera-space point to pixel coordinates. Points behind the
// near plane are reported as not visible.
func (c camera) project(p vec3) (float64, float64, bool) {
	if p.z < 0.2 {
		return 0, 0, false
	}
	nx := p.x * c.focal / (c.aspect * p.z)
	ny := p.y * c.focal / p.z
	return (nx + 1) / 2 * float64(c.width), (1 - ny) / 2 * float64(c.height), true
}

func fillBackground(img *image.RGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		t := float64(y-b.Min.Y) / float64(max(1, b.Dy()-1))
		c := color.RGBA{uint8(10 + 14*t), uint8(14 + 18*t), uint8(30 + 26*t), 255}
		row := img.Pix[img.PixOffset(b.Min.X, y):img.PixOffset(b.Max.X, y)]
		for i := 0; i < len(row); i += 4 {
			row[i], row[i+1], row[i+2], row[i+3] = c.R, c.G, c.B, c.A
		}
	}
}

func rotate(p vec3, ax, ay float64) vec3 {
	// Rotate around Y, then X.
	cy, sy := math.Cos(ay), math.Sin(ay)
	x, z := p.x*cy+p.z*sy, -p.x*sy+p.z*cy
	cx, sx := math.Cos(ax), math.Sin(ax)
	y, z := p.y*cx-z*sx, p.y*sx+z*cx
	return vec3{x, y, z}
}

func drawFloor(img *image.RGBA, cam camera, phase float64, thickness int) {
	const floorY = -1.1
	const depth = 9.0
	// Slide the depth lines toward the viewer so the floor appears to move.
	offset := math.Mod(phase*0.8, 1)
	for i := 0; i <= 8; i++ {
		z := 1.5 + float64(i) + (1 - offset)
		if z > 1.5+depth {
			continue
		}
		fade := 1 - (z-1.5)/depth
		c := shade(color.RGBA{40, 170, 160, 255}, 0.15+0.6*fade)
		drawSegment(img, cam, vec3{-5, floorY, z}, vec3{5, floorY, z}, c, thickness)
	}
	for i := -5; i <= 5; i++ {
		x := float64(i)
		drawSegment(img, cam, vec3{x, floorY, 1.5}, vec3{x, floorY, 1.5 + depth}, shade(color.RGBA{40, 170, 160, 255}, 0.35), thickness)
	}
}

func drawCube(img *image.RGBA, cam camera, phase float64, thickness int) {
	var corners [8]vec3
	for i := range corners {
		corners[i] = vec3{float64(i&1)*2 - 1, float64(i>>1&1)*2 - 1, float64(i>>2&1)*2 - 1}
		p := rotate(corners[i], phase*0.7, phase)
		p.x *= 0.85
		p.y = p.y*0.85 + 0.15*math.Sin(phase*1.3)
		p.z = p.z*0.85 + 3.4
		corners[i] = p
	}
	edges := [12][2]int{{0, 1}, {2, 3}, {4, 5}, {6, 7}, {0, 2}, {1, 3}, {4, 6}, {5, 7}, {0, 4}, {1, 5}, {2, 6}, {3, 7}}
	palette := [3]color.RGBA{{255, 120, 90, 255}, {120, 220, 110, 255}, {110, 160, 255, 255}}
	for i, e := range edges {
		a, b := corners[e[0]], corners[e[1]]
		// Farther edges are dimmer, which conveys depth without hidden-line removal.
		depth := (a.z + b.z) / 2
		c := shade(palette[i/4], 1.15-0.22*(depth-2.5))
		drawSegment(img, cam, a, b, c, thickness+1)
	}
}

// drawBorder outlines the full raster so that panning the viewport makes the
// picture bounds visible.
func drawBorder(img *image.RGBA) {
	b := img.Bounds()
	c := color.RGBA{200, 200, 90, 255}
	for x := b.Min.X; x < b.Max.X; x++ {
		img.SetRGBA(x, b.Min.Y, c)
		img.SetRGBA(x, b.Max.Y-1, c)
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		img.SetRGBA(b.Min.X, y, c)
		img.SetRGBA(b.Max.X-1, y, c)
	}
}

func shade(c color.RGBA, k float64) color.RGBA {
	k = min(1.3, max(0, k))
	scale := func(v uint8) uint8 { return uint8(min(255, float64(v)*k)) }
	return color.RGBA{scale(c.R), scale(c.G), scale(c.B), 255}
}

func drawSegment(img *image.RGBA, cam camera, a, b vec3, c color.RGBA, thickness int) {
	x0, y0, ok0 := cam.project(a)
	x1, y1, ok1 := cam.project(b)
	if !ok0 || !ok1 {
		return
	}
	drawLine(img, int(math.Round(x0)), int(math.Round(y0)), int(math.Round(x1)), int(math.Round(y1)), c, thickness)
}

// drawLine is an integer Bresenham line clipped to the image, thickened by
// painting a small square at each step.
func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA, thickness int) {
	b := img.Bounds()
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	// Bound the loop for segments that extend far outside the raster.
	limit := 4 * (b.Dx() + b.Dy() + dx - dy)
	err := dx + dy
	for range limit {
		for ty := range thickness {
			for tx := range thickness {
				p := image.Pt(x0+tx, y0+ty)
				if p.In(b) {
					img.SetRGBA(p.X, p.Y, c)
				}
			}
		}
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
