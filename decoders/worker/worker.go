// Package worker decodes untrusted still images in a separate, OS-confined
// process. It is a thin layer over nimbleterminal.dev/ntmediabox (hostile-peer
// wire protocol) and nimbleterminal.dev/ntconfine (Landlock plus seccomp on
// Linux, sandbox-exec on macOS, no enforcement on Windows).
//
// The host treats the worker as compromised: every length and dimension the
// worker sends is checked against host-chosen caps before memory is
// allocated, the returned format name is matched against the formats the
// worker can produce, and a timeout or protocol violation kills the worker.
//
// One binary usually plays three roles. The host calls Start or Decode. On
// Linux, the stage role handles ntmediabox.StageArg in main (or the host
// ships an ntconfine-stage binary beside itself and sets Config.StagePath).
// The worker role calls Serve on ntmediabox.WorkerConn(). See the README's
// "Sandboxed decoding" section for the full contract and its limits.
//
// This package is separate from nimbleterminal.dev/ntpicture/decoders so that
// the blank import used to register image formats does not pull ntmediabox
// and ntconfine into programs that never spawn a worker.
package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"time"

	"nimbleterminal.dev/ntmediabox"

	_ "nimbleterminal.dev/ntpicture/decoders" // register PNG, JPEG, GIF, WebP, BMP, TIFF in the worker
)

// DefaultMaxPixels is the default pixel budget (16 megapixels, 64 MiB of RGBA)
// enforced by the host on every response and by the worker before decoding.
const DefaultMaxPixels int64 = 16 << 20

// DefaultTimeout is the default per-request timeout.
const DefaultTimeout = 10 * time.Second

// formats lists the format names the worker can return. The host rejects
// anything else, since the name is worker-controlled bytes.
var formats = map[string]bool{
	"png": true, "jpeg": true, "gif": true, "webp": true, "bmp": true, "tiff": true,
}

// Config configures sandboxed out-of-process image decoding.
type Config struct {
	// WorkerPath is the executable that calls Serve. Required unless Client
	// is set. It must be a regular file owned by the current user or root,
	// not group- or world-writable, and not setuid; see ntconfine.VerifyBinary.
	WorkerPath string
	// WorkerArgs are passed to WorkerPath.
	WorkerArgs []string
	// StagePath is the Linux stage executable, usually the host binary itself
	// when main handles ntmediabox.StageArg. On Linux it is required unless an
	// ntconfine-stage binary sits beside the host executable. Ignored elsewhere.
	StagePath string
	// Policy is the OS confinement policy. When ExecPaths is empty it becomes
	// [WorkerPath]; when ReadPaths is empty it becomes
	// ntmediabox.DefaultLibPaths(). Pass explicit ReadPaths to narrow that.
	Policy ntmediabox.Policy
	// AllowUnsandboxed lets the worker run with no OS confinement at all when
	// enforcement is unavailable (always on Windows; on Linux without
	// Landlock ABI 3; on macOS when sandbox-exec is missing or nested).
	// The downgrade is silent per call. Check Session.Support after Start, or
	// ntmediabox.Probe at startup, before relying on confinement.
	AllowUnsandboxed bool
	// MaxWidth and MaxHeight reject images wider or taller than these values.
	// Zero means ntmediabox.MaxRenderSide (8192).
	MaxWidth  int
	MaxHeight int
	// MaxPixels caps Width*Height. Zero means DefaultMaxPixels; values above
	// ntmediabox.MaxRenderPixels are clamped to it.
	MaxPixels int64
	// Timeout bounds each decode request (default DefaultTimeout). A timeout
	// closes the session and kills the worker; Start again afterwards.
	Timeout time.Duration
	// Client is an optional pre-connected RenderClient. When set, Start does
	// not spawn or confine anything, Session.Support reports that, and Close
	// does not close Client. The caller owns the worker's lifecycle.
	Client *ntmediabox.RenderClient
}

// Session is a connection to one running worker. Requests on a Session are
// serialized; use several sessions for concurrent decodes.
type Session struct {
	cfg     Config
	client  *ntmediabox.RenderClient
	helper  *ntmediabox.Helper
	support ntmediabox.Support
}

// Start launches and confines a worker (or wraps cfg.Client) and returns a
// Session for repeated decodes. Call Close when done.
func Start(cfg Config) (*Session, error) {
	s := &Session{cfg: cfg}
	if cfg.Client != nil {
		s.client = cfg.Client
		s.support = ntmediabox.Support{
			Available: false,
			Mechanism: ntmediabox.MechanismNone,
			Detail:    "caller-supplied RenderClient; ntpicture did not start or confine the worker",
		}
		return s, nil
	}
	if cfg.WorkerPath == "" {
		return nil, errors.New("worker: WorkerPath or Client is required")
	}
	policy := cfg.Policy
	if len(policy.ExecPaths) == 0 {
		policy.ExecPaths = []string{cfg.WorkerPath}
	}
	if len(policy.ReadPaths) == 0 {
		policy.ReadPaths = ntmediabox.DefaultLibPaths()
	}
	if cfg.AllowUnsandboxed {
		policy.AllowUnsandboxed = true
	}
	client, helper, err := ntmediabox.StartRenderHelper(ntmediabox.HelperConfig{
		WorkerPath: cfg.WorkerPath,
		WorkerArgs: cfg.WorkerArgs,
		StagePath:  cfg.StagePath,
		Policy:     policy,
	})
	if err != nil {
		return nil, fmt.Errorf("worker: start: %w", err)
	}
	s.client, s.helper, s.support = client, helper, helper.Support()
	return s, nil
}

// Support reports the OS confinement actually applied to this session's
// worker. Available is false when the worker runs unsandboxed because
// Config.AllowUnsandboxed was set, or when the caller supplied Config.Client.
func (s *Session) Support() ntmediabox.Support {
	if s == nil {
		return ntmediabox.Support{Mechanism: ntmediabox.MechanismNone, Detail: "no session"}
	}
	return s.support
}

// Enforced reports whether an OS sandbox confines this session's worker.
func (s *Session) Enforced() bool { return s.Support().Available }

// Close closes the connection and, when Start spawned the worker, kills and
// reaps it. It does not close a caller-supplied Config.Client.
func (s *Session) Close() error {
	if s == nil || s.client == nil || s.cfg.Client != nil {
		return nil
	}
	// Closing the client runs the kill hook that StartRenderHelper installed,
	// which already closes, kills, and reaps the helper. The explicit Close
	// below is a no-op guard; its error only reports the already-closed socket.
	err := s.client.Close()
	if s.helper != nil {
		_ = s.helper.Close()
	}
	return err
}

// Decode sends data to the worker and returns a bounds-checked *image.RGBA and
// the format name (one of png, jpeg, gif, webp, bmp, tiff). Decode errors
// inside the worker come back as errors and leave the session usable; a
// timeout or protocol violation closes the session.
func (s *Session) Decode(ctx context.Context, data []byte) (*image.RGBA, string, error) {
	if s == nil || s.client == nil {
		return nil, "", errors.New("worker: session not started")
	}
	if len(data) == 0 {
		return nil, "", errors.New("worker: empty image data")
	}
	maxPixels := s.cfg.MaxPixels
	if maxPixels <= 0 {
		maxPixels = DefaultMaxPixels
	}
	if maxPixels > ntmediabox.MaxRenderPixels {
		maxPixels = ntmediabox.MaxRenderPixels
	}
	timeout := s.cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := s.client.Do(reqCtx, ntmediabox.RenderRequest{
		Kind:      "image",
		Width:     s.cfg.MaxWidth,
		Height:    s.cfg.MaxHeight,
		MaxPixels: int(maxPixels),
		Data:      data,
	})
	if err != nil {
		return nil, "", err
	}
	if resp.Image == nil {
		return nil, "", errors.New("worker: worker returned no image")
	}
	// ntmediabox already enforced the caps before allocating; re-check the
	// budget this package promised so a change there cannot widen it silently.
	b := resp.Image.Bounds()
	if int64(b.Dx())*int64(b.Dy()) > maxPixels {
		return nil, "", fmt.Errorf("worker: image dimensions %dx%d exceed %d pixel budget", b.Dx(), b.Dy(), maxPixels)
	}
	// Meta is raw worker-controlled bytes. Match it against the closed set of
	// format names and never echo it.
	format := string(resp.Meta)
	if !formats[format] {
		return nil, "", fmt.Errorf("worker: unexpected format name from worker (%d bytes)", len(resp.Meta))
	}
	return resp.Image, format, nil
}

// Decode is the one-shot form: Start, Session.Decode, Close. Each call spawns
// one worker. Use Start for repeated decodes or to inspect Session.Support.
func Decode(ctx context.Context, data []byte, cfg Config) (*image.RGBA, string, error) {
	if len(data) == 0 {
		return nil, "", errors.New("worker: empty image data")
	}
	s, err := Start(cfg)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = s.Close() }()
	return s.Decode(ctx, data)
}

// Serve runs the worker side over rwc (typically ntmediabox.WorkerConn()). It
// checks maxPixels and the requested width and height with image.DecodeConfig
// before allocating a frame, then decodes with image.Decode. Decode failures
// are reported to the host per request; Serve returns when the host closes
// the connection.
func Serve(rwc io.ReadWriteCloser, maxPixels int64) error {
	if maxPixels <= 0 {
		maxPixels = DefaultMaxPixels
	}
	return ntmediabox.ServeRender(rwc, ntmediabox.RenderFunc(func(req ntmediabox.RenderRequest) (ntmediabox.RenderResponse, error) {
		if req.Kind != "image" {
			return ntmediabox.RenderResponse{}, fmt.Errorf("worker: unsupported request kind %q", req.Kind)
		}
		if len(req.Data) == 0 {
			return ntmediabox.RenderResponse{}, errors.New("worker: empty image data")
		}
		limit := maxPixels
		if req.MaxPixels > 0 && int64(req.MaxPixels) < limit {
			limit = int64(req.MaxPixels)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(req.Data))
		if err != nil {
			return ntmediabox.RenderResponse{}, err
		}
		if cfg.Width <= 0 || cfg.Height <= 0 {
			return ntmediabox.RenderResponse{}, fmt.Errorf("worker: invalid image dimensions %dx%d", cfg.Width, cfg.Height)
		}
		if int64(cfg.Width)*int64(cfg.Height) > limit {
			return ntmediabox.RenderResponse{}, fmt.Errorf("worker: image dimensions %dx%d exceed %d pixel budget", cfg.Width, cfg.Height, limit)
		}
		if req.Width > 0 && cfg.Width > req.Width {
			return ntmediabox.RenderResponse{}, fmt.Errorf("worker: image width %d exceeds max %d", cfg.Width, req.Width)
		}
		if req.Height > 0 && cfg.Height > req.Height {
			return ntmediabox.RenderResponse{}, fmt.Errorf("worker: image height %d exceeds max %d", cfg.Height, req.Height)
		}
		img, decodedFormat, err := image.Decode(bytes.NewReader(req.Data))
		if err != nil {
			return ntmediabox.RenderResponse{}, err
		}
		if decodedFormat != "" {
			format = decodedFormat
		}
		rgba := ntmediabox.ToRGBA(img)
		b := rgba.Bounds()
		return ntmediabox.RenderResponse{
			Width:  float64(b.Dx()),
			Height: float64(b.Dy()),
			Image:  rgba,
			Meta:   []byte(format),
		}, nil
	}))
}
