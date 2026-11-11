//go:build js && wasm

package ntpicture

import "syscall/js"

// browserDisplayScale reads the page's device pixel ratio. It is absent
// outside a browser (for example under Node), which reports as unknown.
func browserDisplayScale() float64 {
	v := js.Global().Get("devicePixelRatio")
	if v.Type() != js.TypeNumber {
		return 0
	}
	if scale := v.Float(); validDisplayScale(scale) {
		return scale
	}
	return 0
}
