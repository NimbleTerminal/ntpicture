//go:build js && wasm

package main

import (
	"bytes"
	"sync"
	"syscall/js"
)

// openTerminal connects to the page's terminal through the same JavaScript
// functions booba installs for Bubble Tea programs: the page calls
// bubbletea_write with input, bubbletea_read for pending output, and
// bubbletea_resize with the size, and watches bubbletea_exited.
func openTerminal() (*terminal, error) {
	var (
		mu         sync.Mutex
		pending    bytes.Buffer
		cols, rows = 80, 24
	)
	in := make(chan []byte, 256)
	resized := make(chan struct{}, 1)
	global := js.Global()
	global.Set("bubbletea_exited", false)
	global.Set("bubbletea_final", "")
	global.Set("bubbletea_write", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 {
			select {
			case in <- []byte(args[0].String()):
			default: // input is arriving faster than the loop reads it
			}
		}
		return nil
	}))
	read := func() string {
		mu.Lock()
		defer mu.Unlock()
		out := pending.String()
		pending.Reset()
		return out
	}
	global.Set("bubbletea_read", js.FuncOf(func(js.Value, []js.Value) any { return read() }))
	global.Set("bubbletea_resize", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) >= 2 && args[0].Int() > 0 && args[1].Int() > 1 {
			mu.Lock()
			cols, rows = args[0].Int(), args[1].Int()
			mu.Unlock()
			select {
			case resized <- struct{}{}:
			default:
			}
		}
		return nil
	}))
	return &terminal{
		in:      in,
		out:     writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return pending.Write(p) }),
		resized: resized,
		size:    func() (int, int) { mu.Lock(); defer mu.Unlock(); return cols, rows },
		close: func() {
			// Nothing in Go is callable after main returns: hand over the rest.
			global.Set("bubbletea_final", read())
			global.Set("bubbletea_exited", true)
		},
	}, nil
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
