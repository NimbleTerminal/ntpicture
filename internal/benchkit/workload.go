package benchkit

import (
	"fmt"
	"image"
	"io"
	"time"

	nt "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"
	"nimbleterminal.dev/ntpicture/playback"
)

// Config explicitly fixes transport and geometry; no terminal detection occurs.
// Shared memory is excluded because a discard sink cannot consume its objects.
type Config struct {
	Cols, Rows, CellWidth, CellHeight int
	Pattern                           string
	Format                            nt.KittyFormat
	Glyph, Tmux                       bool
	Policy                            string // fresh, stable, cache, uncached
}

func (c Config) Validate() error {
	if c.Cols < 1 || c.Rows < 1 || c.Cols > 256 || c.Rows > 256 || c.CellWidth < 1 || c.CellHeight < 1 || c.CellWidth > 64 || c.CellHeight > 64 {
		return fmt.Errorf("invalid cell geometry")
	}
	if c.Cols*c.CellWidth > 4096 || c.Rows*c.CellHeight > 4096 {
		return fmt.Errorf("raster exceeds fixture limit")
	}
	switch c.Policy {
	case "fresh", "stable", "cache", "uncached":
	default:
		return fmt.Errorf("unknown policy %q", c.Policy)
	}
	switch c.Format {
	case nt.KittyFormatPNG, nt.KittyFormatRGBA, nt.KittyFormatZlib:
	default:
		return fmt.Errorf("unknown Kitty format")
	}
	return nil
}

// Workload owns immutable fixtures and one presentation component. Cache and
// uncached policies use Controller.Seek so decoder/compositor work is included.
type Workload struct {
	Config   Config
	Driver   Driver
	pic      *nt.Picture
	ctrl     *playback.Controller
	session  *nt.TerminalSession
	frames   []image.Image
	player   *animation.Player
	position int
}

func New(cfg Config, output io.Writer, draw bool) (*Workload, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	frames, err := Fixtures(cfg.Cols*cfg.CellWidth, cfg.Rows*cfg.CellHeight, cfg.Pattern)
	if err != nil {
		return nil, err
	}
	cap := nt.KittyCapabilitySupported
	if cfg.Glyph {
		cap = nt.KittyCapabilityUnsupported
	}
	session := nt.NewTerminalSession(nt.TerminalOptions{FrameEdits: cap, FrameAppend: cap, FrameSelect: cap, Kitty: cap, SharedMemory: nt.KittyCapabilityUnsupported, CellPixelWidth: cfg.CellWidth, CellPixelHeight: cfg.CellHeight, TmuxPassthrough: &cfg.Tmux})
	w := &Workload{Config: cfg, Driver: Driver{Output: output, Draw: draw}, session: session, frames: frames}
	opts := nt.Options{Session: session, HostGeometry: true, StableID: cfg.Policy == "stable", RetireDelay: -1, Render: nt.Config{Fit: nt.FitFill, KittyFormat: cfg.Format, CellPixelWidth: cfg.CellWidth, CellPixelHeight: cfg.CellHeight}}
	if cfg.Policy == "cache" || cfg.Policy == "uncached" {
		clip := &animation.Clip{Bounds: frames[0].Bounds(), LoopCount: 0}
		for _, img := range frames {
			clip.Frames = append(clip.Frames, animation.ClipFrame{Image: img, Delay: time.Second})
		}
		w.player, err = animation.NewPlayer(clip)
		if err != nil {
			session.Close()
			return nil, err
		}
		budget := -1
		if cfg.Policy == "cache" {
			budget = 32 << 20
		}
		w.ctrl = playback.New(w.player, playback.Options{Picture: opts, FrameCacheBytes: budget})
		w.ctrl.Pause(true)
		w.ctrl.SetSize(cfg.Cols, cfg.Rows)
		w.ctrl.SetTerminal(!cfg.Glyph, cfg.CellWidth, cfg.CellHeight)
	} else {
		w.pic = nt.New(opts)
		w.pic.SetSize(cfg.Cols, cfg.Rows)
		w.pic.SetTerminal(!cfg.Glyph, cfg.CellWidth, cfg.CellHeight)
	}
	return w, nil
}

// Step presents successive positions 1,2,3,1,... with no artificial delay.
func (w *Workload) Step() error {
	var err error
	if w.ctrl != nil {
		err = w.Driver.Run(w.ctrl, w.ctrl.Seek(w.position+1))
		if err == nil {
			err = w.ctrl.Status().Err
		}
	} else {
		err = w.Driver.Run(w.pic, w.pic.SetImageImmutable(w.frames[w.position]))
	}
	if err == nil {
		w.position = (w.position + 1) % len(w.frames)
	}
	return err
}

// ColdPass resets the source cache and presents all three frames. Fixture and
// clip construction are excluded; cache invalidation and initial upload count.
func (w *Workload) ColdPass() error {
	if w.ctrl == nil {
		return fmt.Errorf("cold pass requires playback policy")
	}
	if err := w.Driver.Run(w.ctrl, w.ctrl.SetSource(w.player)); err != nil {
		return err
	}
	w.position = 1
	for range 2 {
		if err := w.Step(); err != nil {
			return err
		}
	}
	return nil
}

func (w *Workload) Close() error {
	defer w.session.Close()
	if w.ctrl != nil {
		return w.Driver.Run(w.ctrl, w.ctrl.Close())
	}
	return w.Driver.Run(w.pic, w.pic.Close())
}

// Geometry reports the actual preparation target, including glyph downsampling.
func (w *Workload) Geometry() nt.RasterGeometry {
	if w.ctrl != nil {
		return w.ctrl.RasterGeometry()
	}
	return w.pic.RasterGeometry()
}

func (w *Workload) CacheEligible() bool {
	return w.Config.Policy == "cache" && !w.Config.Glyph &&
		int64(w.Config.Cols)*int64(w.Config.Rows)*int64(w.Config.CellWidth)*int64(w.Config.CellHeight)*4*3 <= 32<<20
}
