//go:build js && wasm

package demohelp

import (
	"context"
	"syscall/js"
	"time"
)

// Wait pauses for d through the JavaScript event loop so continuous producer
// loops under single-threaded WASM always yield to host input/render callbacks.
func Wait(ctx context.Context, d time.Duration) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	setTimeout := js.Global().Get("setTimeout")
	clearTimeout := js.Global().Get("clearTimeout")
	if setTimeout.Type() != js.TypeFunction {
		timer := time.NewTimer(max(time.Millisecond, d))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	ch := make(chan struct{}, 1)
	cb := js.FuncOf(func(js.Value, []js.Value) any {
		select {
		case ch <- struct{}{}:
		default:
		}
		return nil
	})
	defer cb.Release()
	ms := max(0, int(d/time.Millisecond))
	id := setTimeout.Invoke(cb, ms)
	select {
	case <-ctx.Done():
		if clearTimeout.Type() == js.TypeFunction {
			clearTimeout.Invoke(id)
		}
		return false
	case <-ch:
		return ctx.Err() == nil
	}
}
