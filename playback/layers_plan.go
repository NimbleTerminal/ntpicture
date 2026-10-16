package playback

import (
	"image"
	"math"
	"slices"

	picture "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"
)

func scaleOverlaySubRect(subPx image.Rectangle, cellPxSize, rasterSize image.Point) image.Rectangle {
	fullCell := image.Rectangle{Max: cellPxSize}
	fullRaster := image.Rectangle{Max: rasterSize}
	if subPx.Empty() || cellPxSize.X <= 0 || cellPxSize.Y <= 0 || rasterSize.X <= 0 || rasterSize.Y <= 0 || subPx == fullCell {
		return fullRaster
	}
	if cellPxSize == rasterSize {
		return subPx.Intersect(fullRaster)
	}
	x0 := int(math.Round(float64(subPx.Min.X) * float64(rasterSize.X) / float64(cellPxSize.X)))
	y0 := int(math.Round(float64(subPx.Min.Y) * float64(rasterSize.Y) / float64(cellPxSize.Y)))
	x1 := int(math.Round(float64(subPx.Max.X) * float64(rasterSize.X) / float64(cellPxSize.X)))
	y1 := int(math.Round(float64(subPx.Max.Y) * float64(rasterSize.Y) / float64(cellPxSize.Y)))
	x0 = max(0, min(x0, rasterSize.X-1))
	y0 = max(0, min(y0, rasterSize.Y-1))
	x1 = max(x0+1, min(x1, rasterSize.X))
	y1 = max(y0+1, min(y1, rasterSize.Y))
	return image.Rect(x0, y0, x1, y1)
}

// planLayers converts composite pixel regions into cell-aligned windows clipped
// to the visible cell grid using the base picture's Fit and Anchor mapping,
// merging overlapping cell windows (and partially overlapping key sets) at the
// finer of their resolutions.
func planLayers(comp *animation.Composite, cols, rows, cw, ch int, fit picture.FitMode, anchor picture.FitAnchor) []layerGroup {
	if comp == nil || cols <= 0 || rows <= 0 {
		return nil
	}
	cw, ch = max(1, cw), max(1, ch)
	bounds := comp.Image().Bounds()
	if bounds.Empty() {
		return nil
	}
	sw, sh := bounds.Dx(), bounds.Dy()
	tw, th := cols*cw, rows*ch
	dst := fitRect(bounds, tw, th, fit, anchor)
	if dst.Empty() {
		return nil
	}
	visTarget := image.Rect(0, 0, tw, th)
	grid := image.Rect(0, 0, cols, rows)
	regions := comp.Regions()
	var groups []layerGroup
	for _, r := range regions {
		px := r.Rect.Intersect(bounds)
		if px.Empty() || r.Player == nil {
			continue
		}
		rel := px.Sub(bounds.Min)
		tx0 := dst.Min.X + int((int64(rel.Min.X)*int64(dst.Dx()))/int64(sw))
		ty0 := dst.Min.Y + int((int64(rel.Min.Y)*int64(dst.Dy()))/int64(sh))
		tx1 := dst.Min.X + int((int64(rel.Max.X)*int64(dst.Dx())+int64(sw)-1)/int64(sw))
		ty1 := dst.Min.Y + int((int64(rel.Max.Y)*int64(dst.Dy())+int64(sh)-1)/int64(sh))
		tpx := image.Rect(tx0, ty0, tx1, ty1).Intersect(visTarget)
		if tpx.Empty() {
			continue
		}
		cells := image.Rect(
			tpx.Min.X/cw,
			tpx.Min.Y/ch,
			(tpx.Max.X+cw-1)/cw,
			(tpx.Max.Y+ch-1)/ch,
		).Intersect(grid)
		if cells.Empty() {
			continue
		}
		dispW := float64(r.Rect.Dx()) * float64(dst.Dx()) / float64(sw)
		dispH := float64(r.Rect.Dy()) * float64(dst.Dy()) / float64(sh)
		own := r.Player.Image().Bounds().Size()
		factor := min(
			1.0,
			float64(max(1, own.X))/max(1.0, dispW),
			float64(max(1, own.Y))/max(1.0, dispH),
		)
		groups = mergeLayerGroup(groups, layerGroup{cells: cells, resolution: factor})
	}
	populateGroupWindowsAndKeys := func(gs []layerGroup) []layerGroup {
		out := gs[:0]
		for _, g := range gs {
			c := g.cells
			cellPx := image.Rect(c.Min.X*cw, c.Min.Y*ch, c.Max.X*cw, c.Max.Y*ch)
			tWin := cellPx.Intersect(dst)
			if tWin.Empty() {
				continue
			}
			window := image.Rect(
				bounds.Min.X+int((int64(tWin.Min.X-dst.Min.X)*int64(sw))/int64(dst.Dx())),
				bounds.Min.Y+int((int64(tWin.Min.Y-dst.Min.Y)*int64(sh))/int64(dst.Dy())),
				bounds.Min.X+int((int64(tWin.Max.X-dst.Min.X)*int64(sw)+int64(dst.Dx())-1)/int64(dst.Dx())),
				bounds.Min.Y+int((int64(tWin.Max.Y-dst.Min.Y)*int64(sh)+int64(dst.Dy())-1)/int64(dst.Dy())),
			).Intersect(bounds)
			if window.Empty() {
				continue
			}
			var keys []string
			for _, r := range regions {
				if r.Player != nil && r.Rect.Overlaps(window) && !slices.Contains(keys, r.Key) {
					keys = append(keys, r.Key)
				}
			}
			slices.Sort(keys)
			g.window = window
			g.subPx = tWin.Sub(cellPx.Min)
			g.keys = keys
			out = append(out, g)
		}
		return out
	}
	groups = populateGroupWindowsAndKeys(groups)

	// If two groups share at least one key but have different key sets, merge
	// them so all co-scheduled keys advance on a single Composite clock.
	for {
		merged := false
		for i := 0; i < len(groups) && !merged; i++ {
			for j := i + 1; j < len(groups); j++ {
				if sharesKey(groups[i].keys, groups[j].keys) && !slices.Equal(groups[i].keys, groups[j].keys) {
					combined := layerGroup{
						cells:      groups[i].cells.Union(groups[j].cells),
						resolution: max(groups[i].resolution, groups[j].resolution),
					}
					groups = append(groups[:j], groups[j+1:]...)
					groups = append(groups[:i], groups[i+1:]...)
					groups = mergeLayerGroup(groups, combined)
					groups = populateGroupWindowsAndKeys(groups)
					merged = true
					break
				}
			}
		}
		if !merged {
			break
		}
	}
	return groups
}

func fitRect(bounds image.Rectangle, tw, th int, fit picture.FitMode, anchor picture.FitAnchor) image.Rectangle {
	sw, sh := int64(bounds.Dx()), int64(bounds.Dy())
	cw, ch := int64(tw), int64(th)
	if sw <= 0 || sh <= 0 || cw <= 0 || ch <= 0 {
		return image.Rectangle{}
	}
	switch fit {
	case picture.FitFill:
		return image.Rect(0, 0, tw, th)
	case picture.FitCover:
		var w, h int64
		if sw*ch > sh*cw {
			h = ch
			w = sw * ch / sh
		} else {
			w = cw
			h = sh * cw / sw
		}
		w, h = max(w, 1), max(h, 1)
		var ox, oy int64
		switch anchor {
		case picture.AnchorTop:
			ox, oy = (cw-w)/2, 0
		case picture.AnchorBottom:
			ox, oy = (cw-w)/2, ch-h
		case picture.AnchorLeft:
			ox, oy = 0, (ch-h)/2
		case picture.AnchorRight:
			ox, oy = cw-w, (ch-h)/2
		default:
			ox, oy = (cw-w)/2, (ch-h)/2
		}
		return image.Rect(int(ox), int(oy), int(ox+w), int(oy+h))
	default: // FitContain
		var w, h int64
		if sw*ch >= sh*cw {
			w, h = cw, sh*cw/sw
		} else {
			w, h = sw*ch/sh, ch
		}
		w, h = max(w, 1), max(h, 1)
		ox, oy := (cw-w)/2, (ch-h)/2
		return image.Rect(int(ox), int(oy), int(ox+w), int(oy+h))
	}
}

func sharesKey(a, b []string) bool {
	for _, k := range a {
		if slices.Contains(b, k) {
			return true
		}
	}
	return false
}

func mergeLayerGroup(groups []layerGroup, g layerGroup) []layerGroup {
	for {
		merged := false
		for i, o := range groups {
			if o.cells.Overlaps(g.cells) {
				g = layerGroup{
					cells:      o.cells.Union(g.cells),
					resolution: max(o.resolution, g.resolution),
				}
				groups = append(groups[:i], groups[i+1:]...)
				merged = true
				break
			}
		}
		if !merged {
			return append(groups, g)
		}
	}
}
