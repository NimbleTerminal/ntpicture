package ntpicture_test

import (
	"fmt"
	"io"
	"testing"

	nt "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/internal/benchkit"
)

// BenchmarkPresentation measures synchronous command execution through the
// public API to a discard writer, including raw output and changed View grids.
// It does not measure Bubble Tea scheduling or visible terminal frame rate.
func BenchmarkPresentation(b *testing.B) {
	for _, cols := range []int{40, 80} {
		for _, pattern := range benchkit.Patterns {
			for _, format := range []nt.KittyFormat{nt.KittyFormatPNG, nt.KittyFormatRGBA, nt.KittyFormatZlib} {
				for _, policy := range []string{"fresh", "stable"} {
					cfg := benchkit.Config{Cols: cols, Rows: cols / 4, CellWidth: 8, CellHeight: 16, Pattern: pattern, Format: format, Policy: policy}
					b.Run(fmt.Sprintf("%dx%d/%s/%s/%s", cols*8, cols*4, pattern, format, policy), func(b *testing.B) { benchmarkWorkload(b, cfg, false) })
				}
			}
			cfg := benchkit.Config{Cols: cols, Rows: cols / 4, CellWidth: 8, CellHeight: 16, Pattern: pattern, Policy: "fresh", Glyph: true}
			b.Run(fmt.Sprintf("%dx%d/%s/glyph", cols*8, cols*4, pattern), func(b *testing.B) { benchmarkWorkload(b, cfg, false) })
		}
	}
}

func BenchmarkPlaybackCache(b *testing.B) {
	for _, cols := range []int{40, 80} {
		for _, format := range []nt.KittyFormat{nt.KittyFormatPNG, nt.KittyFormatRGBA, nt.KittyFormatZlib} {
			for _, policy := range []string{"uncached", "cache"} {
				for _, phase := range []string{"cold-pass", "warm-frame"} {
					cfg := benchkit.Config{Cols: cols, Rows: cols / 4, CellWidth: 8, CellHeight: 16, Pattern: "full", Format: format, Policy: policy}
					b.Run(fmt.Sprintf("%dx%d/%s/%s/%s", cols*8, cols*4, format, policy, phase), func(b *testing.B) { benchmarkWorkload(b, cfg, phase == "cold-pass") })
				}
			}
		}
	}
}

func benchmarkWorkload(b *testing.B, cfg benchkit.Config, cold bool) {
	w, err := benchkit.New(cfg, io.Discard, true)
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := w.Close(); err != nil {
			b.Error(err)
		}
	}()
	// Warm every source position before measuring steady-state replacements.
	for range 3 {
		if err := w.Step(); err != nil {
			b.Fatal(err)
		}
	}
	apc, grid := w.Driver.APCBytes, w.Driver.GridBytes
	b.ReportAllocs()
	for b.Loop() {
		if cold {
			err = w.ColdPass()
		} else {
			err = w.Step()
		}
		if err != nil {
			b.Fatal(err)
		}
	}
	frames := 1
	if cold {
		frames = 3
	}
	b.ReportMetric(float64(frames), "frames/op")
	b.ReportMetric(float64(w.Driver.APCBytes-apc)/float64(b.N*frames), "APC-bytes/frame")
	b.ReportMetric(float64(w.Driver.GridBytes-grid)/float64(b.N*frames), "grid-bytes/frame")
}
