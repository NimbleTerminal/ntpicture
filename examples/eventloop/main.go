// Command eventloop shows an image from a hand-written terminal event loop,
// with no Bubble Tea program:
//
//	go run ./eventloop [image]
//
// The program owns the terminal and reads its input itself, so it detects
// Kitty graphics with ntpicture.Probe, which does no I/O, and then draws with
// ntpicture.Render. Press m to switch between Kitty graphics and glyphs, and
// q to quit.
//
// The loop below is the whole program. openTerminal, in terminal_native.go and
// terminal_js.go, only supplies raw input, output, and the size: a real
// terminal natively, the page's terminal in the browser.
package main

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"strings"
	"time"

	picture "nimbleterminal.dev/ntpicture"
	_ "nimbleterminal.dev/ntpicture/decoders" // PNG, JPEG, GIF, WebP, BMP, TIFF
)

// terminal is a raw terminal: this program, not ntpicture, owns it.
type terminal struct {
	in      <-chan []byte           // input as it arrives; closed at end of input
	out     io.Writer               // output to the terminal
	resized <-chan struct{}         // signals a new size, where the platform reports it
	size    func() (cols, rows int) // current size in cells
	close   func()                  // restores the terminal
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "eventloop:", err)
		os.Exit(1)
	}
}

func run() error {
	path := ""
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	img, err := load(path)
	if err != nil {
		return err
	}
	tty, err := openTerminal()
	if err != nil {
		return err
	}
	defer tty.close()
	if _, err := io.WriteString(tty.out, "\x1b[?1049h\x1b[?25l"); err != nil { // alternate screen, hide cursor
		return err
	}
	defer func() { _, _ = io.WriteString(tty.out, "\x1b[?25h\x1b[?1049l") }()

	// 1. Write the probe's query.
	probe := picture.NewProbe(picture.ProbeOptions{})
	if _, err := io.WriteString(tty.out, probe.Query()); err != nil {
		return err
	}
	timeout := time.After(time.Second)

	v := viewer{img: img, tty: tty}
	defer v.clear()
	for {
		var keys []byte
		select {
		case b, ok := <-tty.in:
			if !ok {
				return nil
			}
			// 2. Pass input through the probe. It removes the terminal's
			// replies and returns everything else, such as keys typed early.
			keys, _ = probe.Feed(b)
		case <-timeout:
			// 3. A terminal that never answered: stop waiting.
			keys = probe.Timeout()
		case <-tty.resized:
			if err := v.draw(); err != nil {
				return err
			}
		}
		if probe.Done() && !v.ready {
			// 4. The result feeds RenderOptions, so Render never probes.
			v.ready, v.detected, timeout = true, probe.Result(), nil
			if err := v.draw(); err != nil {
				return err
			}
		}
		for _, key := range keys {
			switch key {
			case 'q', 0x03: // q or ctrl+c
				return nil
			case 'm':
				v.glyphs = !v.glyphs
				if err := v.draw(); err != nil {
					return err
				}
			}
		}
	}
}

type viewer struct {
	img      image.Image
	tty      *terminal
	ready    bool
	glyphs   bool
	detected picture.ProbeResult
	cleanup  func()
}

// clear deletes the Kitty image on screen, if any.
func (v *viewer) clear() {
	if v.cleanup != nil {
		v.cleanup()
		v.cleanup = nil
	}
}

func (v *viewer) draw() error {
	if !v.ready {
		return nil
	}
	cols, rows := v.tty.size()
	opts := picture.RenderOptions{
		Width:           cols,
		Height:          rows - 1, // the last line is the status line
		Kitty:           v.detected.Kitty,
		CellPixelWidth:  v.detected.CellPixelWidth,
		CellPixelHeight: v.detected.CellPixelHeight,
		Output:          v.tty.out, // where cleanup deletes the image
	}
	if v.glyphs {
		opts.Mode = picture.RenderGlyph
	}
	out, cleanup, err := picture.Render(v.img, opts)
	if err != nil {
		return err
	}
	v.clear()
	v.cleanup = cleanup

	mode := "glyphs"
	if !v.glyphs && v.detected.Kitty == picture.KittyCapabilitySupported {
		mode = "Kitty graphics"
	}
	// A raw terminal does not translate newlines.
	_, err = fmt.Fprint(v.tty.out, "\x1b[2J\x1b[H", strings.ReplaceAll(out, "\n", "\r\n"), "\r\n", mode, " | m: switch mode | q: quit")
	return err
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
