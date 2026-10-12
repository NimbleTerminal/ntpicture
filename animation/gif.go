package animation

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"io"
	"time"
)

// Bound DecodeAll before it allocates: compressed GIFs can describe thousands
// of large frames with very little input. Frames stay paletted; only the
// current composition and a disposal-previous snapshot are RGBA canvases.
const MaxFileBytes = 128 << 20
const MaxPixels = 32 << 20

var ErrTooLarge = fmt.Errorf("file exceeds 128 MiB")

const MaxGIFFrames = 1000
const MaxGIFPixels = 64 << 20 // Sum of frame rectangles, one byte per pixel.

// Transparent animations use a transparent canvas, as image viewers do.
// Opaque GIFs use the logical-screen background color when one is defined.
func gifBackground(g *gif.GIF) color.Color {
	for _, c := range g.Image[0].Palette {
		if _, _, _, a := c.RGBA(); a == 0 {
			return color.Transparent
		}
	}
	if palette, ok := g.Config.ColorModel.(color.Palette); ok && int(g.BackgroundIndex) < len(palette) {
		return palette[g.BackgroundIndex]
	}
	return color.Transparent
}

func DecodeGIF(data []byte, animate bool) (image.Image, *Player, error) {
	if !isGIF(data) {
		return nil, nil, fmt.Errorf("not a GIF")
	}
	return DecodeContext(context.Background(), data, Options{Animate: animate})
}
func decodeGIF(ctx context.Context, data []byte, animate bool) (image.Image, *Player, error) {
	if len(data) > MaxFileBytes {
		return nil, nil, ErrTooLarge
	}
	cfg, err := gif.DecodeConfig(contextReader{ctx, bytes.NewReader(data)})
	if err != nil {
		return nil, nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxPixels/cfg.Height {
		return nil, nil, fmt.Errorf("GIF canvas exceeds %d pixels", MaxPixels)
	}
	var g *gif.GIF
	if animate {
		g, err = gif.DecodeAll(contextReader{ctx, bytes.NewReader(data)})
	} else {
		var first image.Image
		first, err = gif.Decode(contextReader{ctx, bytes.NewReader(data)})
		if err == nil {
			g = &gif.GIF{Image: []*image.Paletted{first.(*image.Paletted)}, Delay: []int{0}, Disposal: []byte{0}, Config: cfg, LoopCount: -1, BackgroundIndex: data[11]}
		}
	}
	if err != nil {
		return nil, nil, err
	}
	for _, frame := range g.Image {
		if frame.Bounds().Empty() || !frame.Bounds().In(image.Rect(0, 0, cfg.Width, cfg.Height)) {
			return nil, nil, fmt.Errorf("GIF has an empty or out-of-canvas frame")
		}
	}
	clip := &Clip{Bounds: image.Rect(0, 0, cfg.Width, cfg.Height), LoopCount: g.LoopCount, Background: gifBackground(g)}
	for i, frame := range g.Image {
		clip.Frames = append(clip.Frames, ClipFrame{Image: frame, Rect: frame.Bounds(), Delay: normalizedGIFDelay(g.Delay[i]), Disposal: g.Disposal[i], Blend: draw.Over})
	}
	p := newPlayerContext(ctx, clip)
	if p.err != nil {
		return nil, nil, p.err
	}
	if len(g.Image) == 1 {
		return p.Image(), nil, nil
	}
	return p.Image(), p, nil
}

// checkGIF walks block lengths and descriptors without decompressing pixels.
// The standard decoder remains responsible for LZW, palettes and extensions.
func checkGIF(data []byte) error { _, err := PreflightGIF(data); return err }

func PreflightGIF(data []byte) (Stats, error) { return preflightGIFContext(context.Background(), data) }
func preflightGIFContext(ctx context.Context, data []byte) (Stats, error) {
	var stats Stats
	err := inspectGIF(ctx, data, &stats)
	return stats, err
}

func inspectGIF(ctx context.Context, data []byte, stats *Stats) error {
	if len(data) > MaxFileBytes {
		return ErrTooLarge
	}
	if len(data) < 13 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return fmt.Errorf("invalid GIF header")
	}
	width, height := int(binary.LittleEndian.Uint16(data[6:8])), int(binary.LittleEndian.Uint16(data[8:10]))
	if width <= 0 || height <= 0 || width > MaxPixels/height {
		return fmt.Errorf("GIF canvas exceeds %d pixels", MaxPixels)
	}
	pos := 13
	take := func(n int) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if n > len(data)-pos {
			return nil, io.ErrUnexpectedEOF
		}
		part := data[pos : pos+n]
		pos += n
		return part, nil
	}
	table := func(packed byte) error {
		if packed&0x80 != 0 {
			_, err := take(3 << ((packed & 7) + 1))
			return err
		}
		return nil
	}
	blocks := func() error {
		for {
			size, err := take(1)
			if err != nil {
				return err
			}
			if size[0] == 0 {
				return nil
			}
			if _, err = take(int(size[0])); err != nil {
				return err
			}
		}
	}
	if err := table(data[10]); err != nil {
		return err
	}
	frames, pixels := 0, 0
	for {
		block, err := take(1)
		if err != nil {
			return err
		}
		switch block[0] {
		case 0x3b:
			if frames == 0 {
				return fmt.Errorf("GIF has no frames")
			}
			stats.Frames, stats.Bytes = frames, pixels
			stats.Bounds = image.Rect(0, 0, width, height)
			return nil
		case 0x21:
			if _, err := take(1); err != nil {
				return err
			}
			if err := blocks(); err != nil {
				return err
			}
		case 0x2c:
			descriptor, err := take(9)
			if err != nil {
				return err
			}
			x, y := int(binary.LittleEndian.Uint16(descriptor[:2])), int(binary.LittleEndian.Uint16(descriptor[2:4]))
			w, h := int(binary.LittleEndian.Uint16(descriptor[4:6])), int(binary.LittleEndian.Uint16(descriptor[6:8]))
			if w == 0 || h == 0 || x+w > width || y+h > height {
				return fmt.Errorf("GIF has an empty or out-of-canvas frame")
			}
			frames++
			if frames > MaxGIFFrames {
				return fmt.Errorf("GIF exceeds %d frames", MaxGIFFrames)
			}
			if w > (MaxGIFPixels-pixels)/h {
				return fmt.Errorf("GIF frames exceed %d decoded pixels", MaxGIFPixels)
			}
			pixels += w * h
			if err := table(descriptor[8]); err != nil {
				return err
			}
			if _, err := take(1); err != nil {
				return err
			} // LZW minimum code size.
			if err := blocks(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("invalid GIF block 0x%02x", block[0])
		}
	}
}

func normalizedGIFDelay(delay int) time.Duration {
	return normalizedDelay(time.Duration(delay) * 10 * time.Millisecond)
}

// Delays under 20 ms commonly mean an unspecified delay. Avoid spinning or
// saturating the terminal; all other delays are preserved.
func normalizedDelay(d time.Duration) time.Duration {
	if d < 20*time.Millisecond {
		return 100 * time.Millisecond
	}
	return d
}
