package ntpicture_test

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"strings"

	"nimbleterminal.dev/ntpicture"
)

func gradient() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 160, A: 255})
		}
	}
	return img
}

// Print an image from any Go program; no Bubble Tea program is needed.
// The zero RenderOptions detects Kitty graphics and sizes to the terminal.
func ExamplePrint() {
	if err := ntpicture.Print(os.Stdout, gradient(), ntpicture.RenderOptions{}); err != nil {
		fmt.Println(err)
	}
}

// Render returns the image as a string, for composing with other output, and
// a cleanup function that removes a Kitty image from the terminal.
func ExampleRender() {
	out, cleanup, err := ntpicture.Render(gradient(), ntpicture.RenderOptions{
		Width:  16,
		Height: 8,
		Mode:   ntpicture.RenderGlyph, // omit to use Kitty graphics where supported
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer cleanup()
	fmt.Println(len(strings.Split(out, "\n")), "lines")
	// Output: 8 lines
}
