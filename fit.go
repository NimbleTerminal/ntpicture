package ntpicture

import (
	"image"
	"image/color"
	"nimbleterminal.dev/ntpicture/internal/imageview"

	"golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
)

// prepareSource returns an image.Image of bounds (cols*cellW × rows*cellH)
// with src mapped onto the target cell rectangle according to fit. All
// three modes composite over bg using draw.Over, so semi-transparent
// sources show the configured Background through their translucent areas
// (matching the behavior of the removed composite() helper). FitContain
// additionally fills the letterbox bars with bg. The returned bitmap is
// sized so both Glyph and Kitty backends can encode it without further AR
// math: each backend's native letterbox or stretch step is a no-op when
// source AR matches target AR.
//
// Returns nil for non-positive dims (defensive — callers should already
// short-circuit on cols/rows <= 0). Returns a bg-filled target when src
// has empty bounds.
func prepareSource(src image.Image, fit FitMode, cols, rows, cellW, cellH int, bg color.Color, anchor FitAnchor) image.Image {
	return prepareSourceWithScratch(src, fit, cols, rows, cellW, cellH, bg, anchor, nil)
}

func prepareSourceWithScratch(src image.Image, fit FitMode, cols, rows, cellW, cellH int, bg color.Color, anchor FitAnchor, scratch *fitScratch) image.Image {
	src = imageview.Unwrap(src)
	if cols <= 0 || rows <= 0 || cellW <= 0 || cellH <= 0 {
		return nil
	}
	tw := cols * cellW
	th := rows * cellH
	target := image.Rect(0, 0, tw, th)

	sb := src.Bounds()
	if sb.Dx() == 0 || sb.Dy() == 0 {
		out := scratch.target(target)
		draw.Draw(out, target, &image.Uniform{C: bg}, image.Point{}, draw.Src)
		return out
	}

	switch fit {
	case FitFill:
		return fillTo(src, sb, target, bg, scratch)
	case FitCover:
		return coverTo(src, sb, target, bg, anchor, scratch)
	default: // FitContain and any out-of-range value
		return containTo(src, sb, target, bg, scratch)
	}
}

// place draws src into dst within out, compositing with draw.Over. A
// destination of the source's own size is a plain copy: CatmullRom at 1:1
// changes no pixel but still allocates a float scratch buffer four times
// the source and runs both kernel passes, which dominated a terminal
// viewer's frame time.
func place(out *image.RGBA, dst image.Rectangle, src image.Image, sb image.Rectangle, scratch *fitScratch) {
	if dst.Dx() == sb.Dx() && dst.Dy() == sb.Dy() {
		draw.Draw(out, dst, src, sb.Min, draw.Over)
		return
	}
	if scratch == nil {
		draw.CatmullRom.Scale(out, dst, src, sb, draw.Over, nil)
		return
	}
	dims := [4]int{dst.Dx(), dst.Dy(), sb.Dx(), sb.Dy()}
	if scratch.scaler == nil || scratch.dims != dims {
		scratch.scaler = draw.CatmullRom.NewScaler(dims[0], dims[1], dims[2], dims[3])
		scratch.dims = dims
	}
	scratch.scaler.Scale(out, dst, src, sb, draw.Over, nil)
}

// coversTarget reports whether src at target size would hide bg entirely,
// so the fitted image is src itself and no copy is needed. Opacity is a
// scan of the source, cheaper than the allocation and copy it saves.
func coversTarget(src image.Image, sb, target image.Rectangle) bool {
	if sb.Dx() != target.Dx() || sb.Dy() != target.Dy() {
		return false
	}
	o, ok := src.(interface{ Opaque() bool })
	return ok && o.Opaque()
}

// bgIsTransparent reports whether bg has zero alpha — same predicate the
// removed composite helper used as its short-circuit.
func bgIsTransparent(bg color.Color) bool {
	_, _, _, a := bg.RGBA()
	return a == 0
}

// fillTo stretches src to exactly target, compositing over bg with
// draw.Over so semi-transparent sources show bg through their translucent
// areas. Fast path: when bg is transparent AND src is already at target
// size, return src unchanged (matches the kitty.go optimization the
// original composite() preserved for the transparent-bg case).
func fillTo(src image.Image, sb, target image.Rectangle, bg color.Color, scratch *fitScratch) image.Image {
	if (bgIsTransparent(bg) && sb.Dx() == target.Dx() && sb.Dy() == target.Dy()) || coversTarget(src, sb, target) {
		return src
	}
	out := scratch.target(target)
	draw.Draw(out, target, &image.Uniform{C: bg}, image.Point{}, draw.Src)
	place(out, target, src, sb, scratch)
	return out
}

// containTo computes the AR-preserving inscribed rect inside target, fills
// target with bg, then draws src into the inscribed rect with draw.Over so
// transparent bg yields literal transparency in the output PNG.
func containTo(src image.Image, sb, target image.Rectangle, bg color.Color, scratch *fitScratch) image.Image {
	if coversTarget(src, sb, target) {
		return src
	}
	out := scratch.target(target)
	draw.Draw(out, target, &image.Uniform{C: bg}, image.Point{}, draw.Src)

	tw, th := target.Dx(), target.Dy()
	sw, sh := sb.Dx(), sb.Dy()

	iw, ih := containSize(sw, sh, tw, th)
	ox := (tw - iw) / 2
	oy := (th - ih) / 2
	dst := image.Rect(ox, oy, ox+iw, oy+ih)
	place(out, dst, src, sb, scratch)
	return out
}

// containSize returns the AR-preserving inscribed size of an sw×sh source in
// a tw×th target, at least 1×1. Cross products are compared in int64 because
// products of bounded image dimensions overflow 32-bit int; the results never
// exceed the target, so they fit int.
func containSize(sw, sh, tw, th int) (iw, ih int) {
	w, h, cw, ch := int64(sw), int64(sh), int64(tw), int64(th)
	// Compare cross products to avoid floating point: sw/sh <=> tw/th.
	var iw64, ih64 int64
	if w*ch >= h*cw {
		// Source is "wider" relative to target; width-limited.
		iw64, ih64 = cw, h*cw/w
	} else {
		// Source is "taller" relative to target; height-limited.
		iw64, ih64 = w*ch/h, ch
	}
	return int(max(iw64, 1)), int(max(ih64, 1))
}

// coverOverflowLimit bounds the circumscribed rectangle, relative to the
// visible target, for which the separable scaler is used. Kernel.Scale
// allocates a float64 row buffer of cw×sh×32 bytes and weight tables for every
// destination column and row, and its horizontal pass visits every column of
// the overflowing rectangle, so its cost grows with the invisible area.
// Kernel.Transform visits only visible pixels but evaluates a two-dimensional
// kernel per pixel, so it wins once the overflow is large. At this limit,
// Scale's buffer is at most 4× the buffer an equal-size FitFill already needs.
const coverOverflowLimit = 4

// coverUsesScaler reports whether the separable scaler keeps its allocation
// within coverOverflowLimit times the visible target. Inputs are int64 and
// compared in float64 so enormous rectangles cannot overflow the comparison.
func coverUsesScaler(cw, ch, tw, th int64) bool {
	return float64(cw)*float64(ch) <= coverOverflowLimit*float64(tw)*float64(th)
}

// coverTo computes the AR-preserving circumscribed rect (one axis matches
// target, the other overflows), fills target with bg, then draws src into
// the circumscribed rect with draw.Over. Target-bound clipping crops the
// overflow; bg shows through translucent source pixels.
func coverTo(src image.Image, sb, target image.Rectangle, bg color.Color, anchor FitAnchor, scratch *fitScratch) image.Image {
	if coversTarget(src, sb, target) {
		return src
	}
	tw, th := int64(target.Dx()), int64(target.Dy())
	sw, sh := int64(sb.Dx()), int64(sb.Dy())

	// Circumscribed rect: scale source so it covers target preserving AR.
	// Compare cross products: if sw/sh > tw/th, source is wider — height-bind.
	// Products of bounded image dimensions fit int64 on every platform.
	var cw, ch int64
	if sw*th > sh*tw {
		// Width overflows: bind height to target, width grows past target.
		ch = th
		cw = sw * th / sh
	} else {
		// Height overflows (or equal): bind width.
		cw = tw
		ch = sh * tw / sw
	}
	cw, ch = max(cw, 1), max(ch, 1)
	var ox, oy int64
	switch anchor {
	case AnchorTop:
		ox = (tw - cw) / 2
		oy = 0
	case AnchorBottom:
		ox = (tw - cw) / 2
		oy = th - ch
	case AnchorLeft:
		ox = 0
		oy = (th - ch) / 2
	case AnchorRight:
		ox = tw - cw
		oy = (th - ch) / 2
	default: // AnchorCenter
		ox = (tw - cw) / 2
		oy = (th - ch) / 2
	}
	out := scratch.target(target)
	draw.Draw(out, target, &image.Uniform{C: bg}, image.Point{}, draw.Src)
	// A same-size rect is a clipped copy, and a bounded overflow uses the
	// separable scaler, whose rect dimensions then fit int. Larger overflows use
	// Transform, which visits only visible destination pixels and never allocates
	// for the invisible rectangle. Both keep the original fractional mapping and
	// kernel support at crop boundaries; only speed and scratch size differ.
	if (cw == sw && ch == sh) || coverUsesScaler(cw, ch, tw, th) {
		dst := image.Rect(int(ox), int(oy), int(ox+cw), int(oy+ch))
		place(out, dst, src, sb, scratch)
	} else {
		sx, sy := float64(cw)/float64(sw), float64(ch)/float64(sh)
		matrix := f64.Aff3{sx, 0, float64(ox) - sx*float64(sb.Min.X), 0, sy, float64(oy) - sy*float64(sb.Min.Y)}
		draw.CatmullRom.Transform(out, matrix, src, sb, draw.Over, nil)
	}
	return out
}

// fitScratch belongs only to the synchronous Glyph path. Kitty commands must
// keep their own targets because they can execute concurrently.
type fitScratch struct {
	rgba   *image.RGBA
	scaler draw.Scaler
	dims   [4]int
}

func (s *fitScratch) target(bounds image.Rectangle) *image.RGBA {
	if s == nil {
		return image.NewRGBA(bounds)
	}
	if s.rgba == nil || s.rgba.Bounds() != bounds {
		s.rgba = image.NewRGBA(bounds)
	}
	return s.rgba
}
