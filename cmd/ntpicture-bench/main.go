// ntpicture-bench runs deterministic output workloads against a terminal or a
// discard sink. It never interprets write completion as visible frame completion.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	nt "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/internal/benchkit"
)

type settings struct {
	output, sink, terminal, version, support, mode, format, pattern, policy string
	cols, rows, cellWidth, cellHeight, frames, warmup                       int
	interval                                                                time.Duration
	tmux                                                                    bool
}

type sample struct {
	CompletionNS int64 `json:"completion_ns"`
	WriteNS      int64 `json:"write_ns"`
	APCBytes     int64 `json:"apc_bytes"`
	GridBytes    int64 `json:"grid_bytes"`
}

type report struct {
	Raster        nt.RasterGeometry `json:"raster_geometry"`
	CacheEligible bool              `json:"cache_eligible"`
	WindowColumns int               `json:"terminal_columns"`
	WindowRows    int               `json:"terminal_rows"`

	Schema                int               `json:"schema"`
	FixtureVersion        int               `json:"fixture_version"`
	Started               time.Time         `json:"started_utc"`
	Status                string            `json:"status"`
	Measurement           string            `json:"measurement"`
	Sink                  string            `json:"sink"`
	Terminal              string            `json:"terminal"`
	TerminalVersion       string            `json:"terminal_version"`
	CapabilityDeclaration string            `json:"kitty_support_declared"`
	CellGeometrySource    string            `json:"cell_geometry_source"`
	OS                    string            `json:"os"`
	Arch                  string            `json:"arch"`
	GoVersion             string            `json:"go_version"`
	Revision              string            `json:"revision"`
	Dirty                 bool              `json:"dirty"`
	Environment           map[string]string `json:"environment"`
	Config                benchkit.Config   `json:"config"`
	RequestedFrames       int               `json:"requested_frames"`
	IntervalNS            int64             `json:"interval_ns"`
	WarmupFrames          int               `json:"warmup_frames"`
	Warmup                sample            `json:"warmup"`
	WallNS                int64             `json:"wall_ns"`
	P50NS                 int64             `json:"p50_ns"`
	P95NS                 int64             `json:"p95_ns"`
	P99NS                 int64             `json:"p99_ns"`
	Samples               []sample          `json:"samples"`
	VisualVerification    string            `json:"visual_verification"`
}

func (s settings) config() (benchkit.Config, error) {
	c := benchkit.Config{Cols: s.cols, Rows: s.rows, CellWidth: s.cellWidth, CellHeight: s.cellHeight, Pattern: s.pattern, Policy: s.policy, Tmux: s.tmux}
	switch s.mode {
	case "glyph":
		c.Glyph = true
	case "kitty":
	default:
		return c, fmt.Errorf("mode must be glyph or kitty")
	}
	switch s.format {
	case "png":
		c.Format = nt.KittyFormatPNG
	case "rgba":
		c.Format = nt.KittyFormatRGBA
	case "zlib":
		c.Format = nt.KittyFormatZlib
	default:
		return c, fmt.Errorf("format must be png, rgba or zlib")
	}
	if s.sink != "discard" && s.sink != "terminal" {
		return c, fmt.Errorf("sink must be discard or terminal")
	}
	if s.frames < 1 || s.frames > 100000 || s.warmup < 0 || s.warmup > 10000 || s.interval < 0 || s.interval > time.Minute {
		return c, fmt.Errorf("invalid frame count, warmup or interval")
	}
	if s.output == "" {
		return c, fmt.Errorf("provide -out for the JSON report")
	}
	switch s.support {
	case "none", "static", "frames":
	default:
		return c, fmt.Errorf("kitty-support must be none, static or frames")
	}
	if s.sink == "terminal" {
		if strings.TrimSpace(s.terminal) == "" || strings.TrimSpace(s.version) == "" {
			return c, fmt.Errorf("terminal runs require -terminal and -terminal-version")
		}
		if !c.Glyph && (s.support == "none" || (s.policy != "fresh" && s.support != "frames")) {
			return c, fmt.Errorf("requested Kitty policy exceeds declared kitty-support; use glyph or fresh as appropriate")
		}
	}
	if c.Glyph && (s.policy == "cache" || s.policy == "uncached") {
		return c, fmt.Errorf("playback cache comparison requires Kitty mode; use fresh for glyph runs")
	}
	return c, c.Validate()
}

func measure(w *benchkit.Workload, fn func() error) (sample, error) {
	beforeAPC, beforeGrid, beforeWrite := w.Driver.APCBytes, w.Driver.GridBytes, w.Driver.WriteTime
	start := time.Now()
	err := fn()
	return sample{time.Since(start).Nanoseconds(), (w.Driver.WriteTime - beforeWrite).Nanoseconds(), w.Driver.APCBytes - beforeAPC, w.Driver.GridBytes - beforeGrid}, err
}

func percentile(values []int64, p int) int64 {
	if len(values) == 0 {
		return 0
	}
	// Nearest-rank percentile; values are sorted by the caller.
	return values[(len(values)*p+99)/100-1]
}

func run(ctx context.Context, s settings) (err error) {
	cfg, err := s.config()
	if err != nil {
		return err
	}
	output := io.Discard
	windowColumns, windowRows := 0, 0
	if s.sink == "terminal" {
		if !term.IsTerminal(os.Stdout.Fd()) {
			return fmt.Errorf("terminal sink requires a terminal stdout; use -sink discard for automated runs")
		}
		width, height, sizeErr := term.GetSize(os.Stdout.Fd())
		if sizeErr != nil {
			return sizeErr
		}
		if width <= s.cols || height <= s.rows {
			return fmt.Errorf("terminal must be larger than %dx%d cells to avoid wrapping/scrolling", s.cols, s.rows)
		}

		windowColumns, windowRows = width, height
		output = os.Stdout
	}
	w, err := benchkit.New(cfg, output, true)
	if err != nil {
		return err
	}
	screenActive := false
	defer func() {
		err = errors.Join(err, w.Close())
		if screenActive {
			_, restoreErr := io.WriteString(output, "\x1b[0m\x1b[?25h\x1b[?1049l")
			err = errors.Join(err, restoreErr)
		}
	}()
	if err = os.MkdirAll(filepath.Dir(s.output), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(s.output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	r := report{Schema: 1, FixtureVersion: benchkit.FixtureVersion, Started: time.Now().UTC(), Status: "complete", Measurement: "synchronous command execution and output writes; no display acknowledgement", Sink: s.sink, Terminal: s.terminal, TerminalVersion: s.version, CapabilityDeclaration: s.support, CellGeometrySource: "explicit command-line values; not probed", OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version(), Config: cfg, RequestedFrames: s.frames, IntervalNS: s.interval.Nanoseconds(), WarmupFrames: s.warmup, VisualVerification: "not recorded; annotate externally after observation", Environment: map[string]string{}}
	r.Raster = w.Geometry()
	r.CacheEligible = w.CacheEligible()
	r.WindowColumns, r.WindowRows = windowColumns, windowRows
	for _, key := range []string{"TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "COLORTERM"} {
		r.Environment[key] = os.Getenv(key)
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, v := range info.Settings {
			switch v.Key {
			case "vcs.revision":
				r.Revision = v.Value
			case "vcs.modified":
				r.Dirty = v.Value == "true"
			}
		}
	}
	if s.sink == "terminal" {
		screenActive = true
		if _, err = io.WriteString(output, "\x1b[?1049h\x1b[2J\x1b[H\x1b[?25l"); err != nil {
			return err
		}

	}
	r.Warmup, err = measure(w, func() error {
		for range s.warmup {
			if e := w.Step(); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	start := time.Now()
	for range s.frames {
		if ctx.Err() != nil {
			r.Status = "interrupted"
			break
		}
		began := time.Now()
		value, e := measure(w, w.Step)
		if e != nil {
			return e
		}
		r.Samples = append(r.Samples, value)
		if remaining := s.interval - time.Since(began); remaining > 0 {
			timer := time.NewTimer(remaining)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
	r.WallNS = time.Since(start).Nanoseconds()
	durations := make([]int64, len(r.Samples))
	for i, v := range r.Samples {
		durations[i] = v.CompletionNS
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	r.P50NS, r.P95NS, r.P99NS = percentile(durations, 50), percentile(durations, 95), percentile(durations, 99)
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func main() {
	var s settings
	flag.StringVar(&s.output, "out", "", "JSON report path (must not exist)")
	flag.StringVar(&s.sink, "sink", "discard", "discard or terminal")
	flag.StringVar(&s.terminal, "terminal", "", "terminal name for manual runs")
	flag.StringVar(&s.version, "terminal-version", "", "terminal version for manual runs")
	flag.StringVar(&s.support, "kitty-support", "none", "declared capability: none, static, frames (not auto-detected)")
	flag.StringVar(&s.mode, "mode", "glyph", "glyph or kitty")
	flag.StringVar(&s.format, "format", "zlib", "png, rgba or zlib")
	flag.StringVar(&s.pattern, "pattern", "localized", "unchanged, localized, scattered or full")
	flag.StringVar(&s.policy, "policy", "fresh", "fresh, stable, cache or uncached")
	flag.IntVar(&s.cols, "cols", 40, "placement columns")
	flag.IntVar(&s.rows, "rows", 10, "placement rows")
	flag.IntVar(&s.cellWidth, "cell-width", 8, "declared cell pixel width")
	flag.IntVar(&s.cellHeight, "cell-height", 16, "declared cell pixel height")
	flag.IntVar(&s.frames, "frames", 300, "measured frames")
	flag.IntVar(&s.warmup, "warmup", 3, "unmeasured warmup frames; 3 populates the cache")
	flag.DurationVar(&s.interval, "interval", time.Second/60, "minimum interval between frame starts; 0 saturates output")
	flag.BoolVar(&s.tmux, "tmux", os.Getenv("TMUX") != "", "wrap APCs for tmux passthrough")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, s); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "Report:", s.output)
}
