// Command eventloop shows an image from a hand-written terminal event loop,
// with no Bubble Tea program:
//
//	go run ./eventloop [image]
//
// The program owns raw mode and reads stdin itself, so it detects Kitty
// graphics with ntpicture.Probe, which does no I/O, and then draws with
// ntpicture.Render. Press m to switch between Kitty graphics and glyphs, and
// q to quit.
package main

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	picture "nimbleterminal.dev/ntpicture"
	_ "nimbleterminal.dev/ntpicture/decoders" // PNG, JPEG, GIF, WebP, BMP, TIFF
)

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

	// This program, not ntpicture, owns the terminal.
	fd := os.Stdin.Fd()
	state, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("stdin is not a terminal: %w", err)
	}
	defer func() { _ = term.Restore(fd, state) }()
	fmt.Print("\x1b[?1049h\x1b[?25l") // alternate screen, hide cursor
	defer fmt.Print("\x1b[?25h\x1b[?1049l")

	input := make(chan []byte)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				input <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				close(input)
				return
			}
		}
	}()

	// 1. Write the probe's query.
	probe := picture.NewProbe(picture.ProbeOptions{})
	if _, err := io.WriteString(os.Stdout, probe.Query()); err != nil {
		return err
	}
	timeout := time.After(time.Second)

	v := viewer{img: img, fd: fd}
	defer v.clear()
	for {
		var keys []byte
		select {
		case b, ok := <-input:
			if !ok {
				return nil
			}
			// 2. Pass input through the probe. It removes the terminal's
			// replies and returns everything else, such as keys typed early.
			keys, _ = probe.Feed(b)
		case <-timeout:
			// 3. A terminal that never answered: stop waiting.
			keys = probe.Timeout()
		}
		if probe.Done() && !v.ready {
			// 4. The result feeds RenderOptions, so Render never probes.
			v.ready, v.terminal, timeout = true, probe.Result(), nil
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
	fd       uintptr
	ready    bool
	glyphs   bool
	terminal picture.ProbeResult
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
	cols, rows, err := term.GetSize(v.fd)
	if err != nil || cols <= 0 || rows <= 1 {
		cols, rows = 80, 24
	}
	opts := picture.RenderOptions{
		Width:           cols,
		Height:          rows - 1, // the last line is the status line
		Kitty:           v.terminal.Kitty,
		CellPixelWidth:  v.terminal.CellPixelWidth,
		CellPixelHeight: v.terminal.CellPixelHeight,
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
	if !v.glyphs && v.terminal.Kitty == picture.KittyCapabilitySupported {
		mode = "Kitty graphics"
	}
	// Raw mode does not translate newlines.
	fmt.Print("\x1b[2J\x1b[H", strings.ReplaceAll(out, "\n", "\r\n"), "\r\n", mode, " | m: switch mode | q: quit")
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
