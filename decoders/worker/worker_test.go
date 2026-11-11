package worker

import (
	"context"
	_ "embed"
	"errors"
	"image"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntmediabox"
)

var (
	//go:embed testdata/tiny.webp
	embedWebP []byte
	//go:embed testdata/tiny.bmp
	embedBMP []byte
	//go:embed testdata/tiny.tiff
	embedTIFF []byte
)

func init() {
	if len(os.Args) == 3 && os.Args[1] == ntmediabox.StageArg {
		_ = ntmediabox.RunStage(os.Args[1:])
		os.Exit(9)
	}
	if len(os.Args) >= 2 && os.Args[1] == "--ntpicture-test-worker" {
		conn := os.NewFile(3, "media")
		if conn == nil {
			os.Exit(2)
		}
		if err := Serve(conn, DefaultMaxPixels); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
}

// pipeSession serves Serve over an in-memory pipe and returns a Session wrapping
// the host end.
func pipeSession(t *testing.T, maxPixels int64, cfg Config) *Session {
	t.Helper()
	c1, c2 := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Serve(c2, maxPixels)
	}()
	cfg.Client = ntmediabox.NewRenderClient(c1)
	s, err := Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = cfg.Client.Close()
		_ = c1.Close()
		_ = c2.Close()
		<-done
	})
	return s
}

func TestSessionDecodesEachFormat(t *testing.T) {
	s := pipeSession(t, DefaultMaxPixels, Config{Timeout: 5 * time.Second})
	if s.Enforced() {
		t.Fatal("Enforced() = true for a caller-supplied client")
	}
	if s.Support().Mechanism != ntmediabox.MechanismNone {
		t.Fatalf("Support().Mechanism = %q, want none", s.Support().Mechanism)
	}
	for _, tc := range []struct {
		format string
		data   []byte
	}{
		{"webp", embedWebP},
		{"bmp", embedBMP},
		{"tiff", embedTIFF},
	} {
		img, format, err := s.Decode(context.Background(), tc.data)
		if err != nil {
			t.Fatalf("Decode(%s): %v", tc.format, err)
		}
		if format != tc.format {
			t.Fatalf("format = %q, want %q", format, tc.format)
		}
		if img.Bounds().Dx() != 4 || img.Bounds().Dy() != 4 {
			t.Fatalf("bounds = %v, want 4x4", img.Bounds())
		}
	}
}

func TestSessionSurvivesBadImage(t *testing.T) {
	s := pipeSession(t, DefaultMaxPixels, Config{Timeout: 5 * time.Second})
	if _, _, err := s.Decode(context.Background(), []byte("not an image")); err == nil {
		t.Fatal("expected decode error for garbage input")
	}
	if _, _, err := s.Decode(context.Background(), embedWebP); err != nil {
		t.Fatalf("session unusable after a bad image: %v", err)
	}
}

func TestPixelBudgetRejectsBeforeDecode(t *testing.T) {
	// Worker budget of 8 pixels rejects the 4x4 fixture.
	s := pipeSession(t, 8, Config{})
	if _, _, err := s.Decode(context.Background(), embedWebP); err == nil {
		t.Fatal("expected pixel budget error for 4x4 image with worker maxPixels=8")
	}
	// Host-requested budget also travels to the worker.
	s = pipeSession(t, DefaultMaxPixels, Config{MaxPixels: 8})
	if _, _, err := s.Decode(context.Background(), embedWebP); err == nil {
		t.Fatal("expected pixel budget error for 4x4 image with Config.MaxPixels=8")
	}
	s = pipeSession(t, DefaultMaxPixels, Config{MaxWidth: 2})
	if _, _, err := s.Decode(context.Background(), embedWebP); err == nil {
		t.Fatal("expected width error for 4x4 image with MaxWidth=2")
	}
}

// TestHostRejectsUnknownFormatName simulates a compromised worker that returns
// a valid image with terminal escapes in the format field.
func TestHostRejectsUnknownFormatName(t *testing.T) {
	c1, c2 := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ntmediabox.ServeRender(c2, ntmediabox.RenderFunc(func(ntmediabox.RenderRequest) (ntmediabox.RenderResponse, error) {
			return ntmediabox.RenderResponse{
				Image: image.NewRGBA(image.Rect(0, 0, 1, 1)),
				Meta:  []byte("\x1b]52;c;c2VjcmV0\x07png"),
			}, nil
		}))
	}()
	client := ntmediabox.NewRenderClient(c1)
	t.Cleanup(func() {
		_ = client.Close()
		_ = c1.Close()
		_ = c2.Close()
		<-done
	})
	s, err := Start(Config{Client: client})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	img, format, err := s.Decode(context.Background(), embedWebP)
	if err == nil {
		t.Fatalf("expected error, got format %q", format)
	}
	if img != nil || format != "" {
		t.Fatalf("rejected decode leaked image=%v format=%q", img != nil, format)
	}
	if strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("error echoes worker bytes: %q", err.Error())
	}
}

func TestZeroSessionIsSafe(t *testing.T) {
	var s *Session
	if _, _, err := s.Decode(context.Background(), embedWebP); err == nil {
		t.Fatal("expected error from nil session")
	}
	if s.Enforced() || s.Close() != nil {
		t.Fatal("nil session should report unenforced and close cleanly")
	}
	z := &Session{}
	if _, _, err := z.Decode(context.Background(), embedWebP); err == nil {
		t.Fatal("expected error from zero session")
	}
	if err := z.Close(); err != nil {
		t.Fatalf("zero session Close: %v", err)
	}
}

func TestStartRequiresWorkerPathOrClient(t *testing.T) {
	if _, err := Start(Config{}); err == nil {
		t.Fatal("expected error without WorkerPath or Client")
	}
	if _, _, err := Decode(context.Background(), nil, Config{}); err == nil {
		t.Fatal("expected error for empty data")
	}
}

func TestSandboxedSubprocess(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "windows" {
		t.Skip("subprocess worker test only runs on unix")
	}
	if runtime.GOOS == "linux" && raceEnabled {
		t.Skip("Linux Landlock+RLIMIT_AS subprocess test requires a non-race worker binary (covered by task test-no-cgo)")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cfg := Config{
		WorkerPath:       exe,
		WorkerArgs:       []string{"--ntpicture-test-worker"},
		StagePath:        exe,
		AllowUnsandboxed: true,
		Timeout:          5 * time.Second,
	}
	s, err := Start(cfg)
	if err != nil {
		t.Fatalf("Start subprocess: %v", err)
	}
	defer func() { _ = s.Close() }()

	sup := s.Support()
	if sup.Available != ntmediabox.Available() {
		t.Fatalf("Support().Available = %v, Probe says %v", sup.Available, ntmediabox.Available())
	}
	t.Logf("sandbox mechanism: %s (enforced=%v) %s", sup.Mechanism, s.Enforced(), sup.Detail)
	// CI sets NT_REQUIRE_SANDBOX=1 so a runner without Landlock or
	// sandbox-exec fails loudly instead of silently passing unsandboxed.
	if os.Getenv("NT_REQUIRE_SANDBOX") == "1" && !s.Enforced() {
		t.Fatalf("NT_REQUIRE_SANDBOX=1 but worker is not confined: %s", sup.Detail)
	}

	for i := 0; i < 2; i++ { // the session is reusable
		img, format, err := s.Decode(context.Background(), embedWebP)
		if err != nil {
			t.Fatalf("Decode subprocess: %v", err)
		}
		if format != "webp" {
			t.Fatalf("format = %q, want webp", format)
		}
		if img.Bounds().Dx() != 4 || img.Bounds().Dy() != 4 {
			t.Fatalf("bounds = %v, want 4x4", img.Bounds())
		}
	}
	if _, _, err := s.Decode(context.Background(), []byte("garbage")); err == nil {
		t.Fatal("expected decode error")
	}
	if _, _, err := s.Decode(context.Background(), embedBMP); err != nil {
		t.Fatalf("session unusable after bad image: %v", err)
	}

	// One-shot form.
	img, format, err := Decode(context.Background(), embedTIFF, cfg)
	if err != nil {
		t.Fatalf("Decode one-shot: %v", err)
	}
	if format != "tiff" || img.Bounds().Dx() != 4 {
		t.Fatalf("one-shot = %q %v", format, img.Bounds())
	}
}

func TestClosedSessionReportsError(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "windows" {
		t.Skip("subprocess worker test only runs on unix")
	}
	if runtime.GOOS == "linux" && raceEnabled {
		t.Skip("Linux Landlock+RLIMIT_AS subprocess test requires a non-race worker binary (covered by task test-no-cgo)")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	s, err := Start(Config{
		WorkerPath:       exe,
		WorkerArgs:       []string{"--ntpicture-test-worker"},
		StagePath:        exe,
		AllowUnsandboxed: true,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, _, err := s.Decode(context.Background(), embedWebP); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Decode after Close = %v, want closed-pipe error", err)
	}
}
