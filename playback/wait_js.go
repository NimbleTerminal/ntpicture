//go:build js && wasm

package playback

import (
	"context"
	"syscall/js"
	"time"
)

// Under single-threaded js/wasm, Go's internal time.NewTimer is polled by
// checkTimers() inside findRunnable(). When multiple tracks or overlays run
// concurrently and CPU work per frame exceeds a track's remaining delay,
// one Go timer is always expired whenever another track parks, so _resume()
// ping-pongs between tracks and never yields to the JavaScript event loop.
// Scheduling frame waits through the host JS event loop (setTimeout) parks
// goroutines on a channel rather than Go's timer heap, forcing Go to return
// to JavaScript between bursts of work so input and render callbacks run.
func wait(ctx context.Context, d time.Duration) bool {
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
