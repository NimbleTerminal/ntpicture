// Package imageview shares trusted read-only image views between ntpicture's
// renderer and animation packages without exposing mutable pixels to callers.
package imageview

import (
	"image"
	"image/color"
)

// Image exposes no mutation methods or backing buffers. Only internal code may
// unwrap it, and must treat the resulting image as immutable.
type Image struct{ img image.Image }

func New(img image.Image) Image         { return Image{Unwrap(img)} }
func (r Image) Bounds() image.Rectangle { return r.img.Bounds() }
func (r Image) ColorModel() color.Model { return r.img.ColorModel() }
func (r Image) At(x, y int) color.Color { return r.img.At(x, y) }

// RGBA64At preserves image/draw's allocation-free middle path for external
// consumers that cannot use the package-private concrete-image fast path.
func (r Image) RGBA64At(x, y int) color.RGBA64 {
	if img, ok := r.img.(image.RGBA64Image); ok {
		return img.RGBA64At(x, y)
	}
	red, green, blue, alpha := r.img.At(x, y).RGBA()
	return color.RGBA64{uint16(red), uint16(green), uint16(blue), uint16(alpha)}
}
func (r Image) Opaque() bool {
	img, ok := r.img.(interface{ Opaque() bool })
	return ok && img.Opaque()
}

// Unwrap accepts only views created here, never an arbitrary public unwrapping
// interface. The caller must not mutate or return the backing image publicly.
func Unwrap(img image.Image) image.Image {
	if view, ok := img.(Image); ok {
		return view.img
	}
	return img
}
