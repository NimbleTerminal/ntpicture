package ntpicture

import (
	"os"
	"strconv"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

// Display scale is device pixels per point. Values outside this range are
// treated as absent wherever they come from.
const (
	minDisplayScale = 0.5
	maxDisplayScale = 8
)

// displayScaleQuery is iTerm2's cell-size report request. Terminals that do
// not implement it ignore it, which is the expected case.
const displayScaleQuery = "\x1b]1337;ReportCellSize\x07"

const displayScaleReplyPrefix = "\x1b]1337;ReportCellSize="

func validDisplayScale(v float64) bool {
	return v >= minDisplayScale && v <= maxDisplayScale // false for NaN
}

// parsePlainDecimal accepts digits with an optional fraction only, so that
// strconv's exponents, hex floats, signs, Inf and NaN are all rejected.
func parsePlainDecimal(s string) (float64, bool) {
	if s == "" || len(s) > 16 {
		return 0, false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return 0, false
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

// parseDisplayScale returns 0 for empty, malformed or out-of-range text.
func parseDisplayScale(s string) float64 {
	if v, ok := parsePlainDecimal(strings.TrimSpace(s)); ok && validDisplayScale(v) {
		return v
	}
	return 0
}

func envDisplayScale() float64 { return parseDisplayScale(os.Getenv("NT_DISPLAY_SCALE")) }

// parseDisplayScaleReply reads "OSC 1337 ; ReportCellSize=<height>;<width>;<scale>".
// The sizes are in points and are only checked, never used: CSI 16 t remains
// the source of cell pixels. The older two-field reply carries no scale.
func parseDisplayScaleReply(e uv.UnknownOscEvent) (float64, bool) {
	body, ok := strings.CutPrefix(string(e), displayScaleReplyPrefix)
	if !ok {
		return 0, false
	}
	if rest, ok := strings.CutSuffix(body, "\x07"); ok {
		body = rest
	} else if rest, ok := strings.CutSuffix(body, "\x1b\\"); ok {
		body = rest
	}
	fields := strings.Split(body, ";")
	if len(fields) != 3 {
		return 0, false
	}
	for _, f := range fields[:2] {
		if v, ok := parsePlainDecimal(f); !ok || v <= 0 {
			return 0, false
		}
	}
	scale := parseDisplayScale(fields[2])
	return scale, scale != 0
}
