package benchkit

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	nt "nimbleterminal.dev/ntpicture"
)

func TestWorkloadPaths(t *testing.T) {
	for _, policy := range []string{"fresh", "stable", "cache", "uncached"} {
		t.Run(policy, func(t *testing.T) {
			var out bytes.Buffer
			w, err := New(Config{Cols: 20, Rows: 10, CellWidth: 1, CellHeight: 1, Pattern: "full", Format: nt.KittyFormatRGBA, Policy: policy}, &out, true)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := w.Close(); err != nil {
					t.Error(err)
				}
			}()
			for range 3 {
				if err := w.Step(); err != nil {
					t.Fatal(err)
				}
			}
			out.Reset()
			if err := w.Step(); err != nil {
				t.Fatal(err)
			}
			raw := out.String()
			switch policy {
			case "cache":
				if !strings.Contains(raw, "a=a") || strings.Contains(raw, "a=f") || strings.Contains(raw, "a=T") || len(raw) > 40 {
					t.Fatalf("cache miss: %q", raw)
				}
			case "fresh":
				if !strings.Contains(raw, "a=T") {
					t.Fatal("fresh frame not transmitted")
				}
			default:
				if !strings.Contains(raw, "a=f") {
					t.Fatal("stable edit not transmitted")
				}
			}
			if w.Driver.Presented != 4 {
				t.Fatalf("presented=%d", w.Driver.Presented)
			}
			if policy == "cache" {
				out.Reset()
				if err := w.ColdPass(); err != nil {
					t.Fatal(err)
				}
				if strings.Count(out.String(), "a=T") != 1 || strings.Count(out.String(), "a=f") != 2 {
					t.Fatal("cold pass did not repopulate cache")
				}
			}
		})
	}
}

func TestUnchangedAndGlyph(t *testing.T) {
	for _, glyph := range []bool{false, true} {
		w, err := New(Config{Cols: 20, Rows: 10, CellWidth: 1, CellHeight: 1, Pattern: "unchanged", Policy: "stable", Glyph: glyph}, io.Discard, true)
		if err != nil {
			t.Fatal(err)
		}
		if err = w.Step(); err != nil {
			t.Fatal(err)
		}
		apc, grid := w.Driver.APCBytes, w.Driver.GridBytes
		if err = w.Step(); err != nil {
			t.Fatal(err)
		}
		if w.Driver.APCBytes != apc || w.Driver.GridBytes != grid {
			t.Fatal("unchanged frame wrote output")
		}
		if glyph && apc != 0 {
			t.Fatal("glyph path emitted Kitty output")
		}
		if err = w.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestOutputFailure(t *testing.T) {
	w, err := New(Config{Cols: 20, Rows: 10, CellWidth: 1, CellHeight: 1, Pattern: "full", Policy: "stable"}, brokenWriter{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Step(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("error=%v", err)
	}
	w.Driver.Output = io.Discard
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
}
