// Command print shows an image without a Bubble Tea program, like imgcat:
//
//	go run ./print [-width N] [-height N] [-mode auto|glyph|kitty] [-transient] [image]
//
// Without an image argument it prints a generated gradient.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"os"
	"time"

	picture "nimbleterminal.dev/ntpicture"
	_ "nimbleterminal.dev/ntpicture/decoders" // PNG, JPEG, GIF, WebP, BMP, TIFF
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "print:", err)
		os.Exit(1)
	}
}

func run() error {
	width := flag.Int("width", 0, "width in cells (0: from height or the terminal)")
	height := flag.Int("height", 0, "height in cells (0: from width or the terminal)")
	modeName := flag.String("mode", "auto", "auto, glyph, or kitty")
	transient := flag.Bool("transient", false, "remove the image again after two seconds")
	flag.Parse()

	modes := map[string]picture.RenderMode{"auto": picture.RenderAuto, "glyph": picture.RenderGlyph, "kitty": picture.RenderKitty}
	mode, ok := modes[*modeName]
	if !ok {
		return fmt.Errorf("unknown mode %q", *modeName)
	}
	img, err := load(flag.Arg(0))
	if err != nil {
		return err
	}
	opts := picture.RenderOptions{Width: *width, Height: *height, Mode: mode}

	if !*transient {
		// Print leaves the image in the scrollback.
		return picture.Print(os.Stdout, img, opts)
	}

	// Render returns a string and a cleanup function that deletes a Kitty image.
	out, cleanup, err := picture.Render(img, opts)
	if err != nil {
		return err
	}
	defer cleanup()
	fmt.Println(out)
	time.Sleep(2 * time.Second)
	return nil
}

func load(path string) (image.Image, error) {
	if path == "" {
		return gradient(256, 160), nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	return img, err
}

func gradient(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: 160, A: 255})
		}
	}
	return img
}
