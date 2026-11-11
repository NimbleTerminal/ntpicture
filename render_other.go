//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package ntpicture

import "time"

// Synchronous detection is not implemented here: RenderAuto uses glyphs unless
// RenderOptions.Kitty or NTPICTURE_KITTY says otherwise.
func readTerminalGeometry() terminalGeometry { return terminalGeometry{} }

func probeTerminal(bool, time.Duration) ProbeResult {
	return ProbeResult{Kitty: KittyCapabilityUnsupported}
}
