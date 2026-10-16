//go:build !js || !wasm

package demohelp

import (
	"context"
	"time"
)

// Wait pauses for d or until ctx is cancelled.
func Wait(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
