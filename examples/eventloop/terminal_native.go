//go:build !js

package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/x/term"
)

// openTerminal puts the real terminal in raw mode and reads stdin.
func openTerminal() (*terminal, error) {
	fd := os.Stdin.Fd()
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("stdin is not a terminal: %w", err)
	}
	in := make(chan []byte)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				in <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				close(in)
				return
			}
		}
	}()
	return &terminal{
		in:  in,
		out: os.Stdout,
		size: func() (int, int) {
			cols, rows, err := term.GetSize(fd)
			if err != nil || cols <= 0 || rows <= 1 {
				return 80, 24
			}
			return cols, rows
		},
		close: func() { _ = term.Restore(fd, state) },
	}, nil
}
