package ntpicture

import (
	"runtime"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"
)

func sessionOptions(tmux bool) TerminalOptions {
	return TerminalOptions{Kitty: KittyCapabilitySupported, SharedMemory: KittyCapabilityUnsupported,
		CellPixelWidth: 1, CellPixelHeight: 2, TmuxPassthrough: &tmux}
}

func TestTerminalSessionIndependentTransport(t *testing.T) {
	resetKittyCapability(t)
	ForceKittyCapability(KittyCapabilityUnsupported)
	old := tmuxPassthroughEnabled.Load()
	t.Cleanup(func() { SetTmuxPassthrough(old) })
	for _, format := range []KittyFormat{KittyFormatPNG, KittyFormatRGBA, KittyFormatZlib} {
		for _, tmux := range []bool{false, true} {
			s := NewTerminalSession(sessionOptions(tmux))
			p := New(Options{Session: s, Render: Config{KittyFormat: format}, RetireDelay: -1})
			drivePicture(t, p, p.SetSize(2, 2))
			drivePicture(t, p, p.SetMode(PictureKitty))
			// Conflicting global changes must not affect an explicit session's output.
			SetTmuxPassthrough(!tmux)
			out := output(drivePicture(t, p, p.SetImage(randomNRGBA(2, 4))))
			if p.Mode() != PictureKitty || (strings.Contains(out, "\x1bPtmux;") != tmux) {
				t.Fatalf("session transport leaked global state: format=%v tmux=%v", format, tmux)
			}
			out = output(drivePicture(t, p, p.Close()))
			if strings.Contains(out, "\x1bPtmux;") != tmux {
				t.Fatal("cleanup used wrong transport")
			}
			s.Close()
		}
	}
	if KittySupported() != KittyCapabilityUnsupported {
		t.Fatal("session modified global capability")
	}
}

func TestTerminalSessionProbeRouting(t *testing.T) {
	t.Setenv("NTCHARTS_KITTY", "")
	t.Setenv("TERM", "xterm-kitty")
	opts := sessionOptions(false)
	opts.Kitty = KittyCapabilityUnknown
	a, b := NewTerminalSession(opts), NewTerminalSession(opts)
	defer a.Close()
	defer b.Close()
	if a.Init() == nil || a.Init() != nil {
		t.Fatal("Init must run once")
	}
	_ = b.Init()
	reply := uv.KittyGraphicsEvent{Options: kitty.Options{ID: a.probeID}}
	if b.Update(reply) != nil || b.Snapshot().Kitty != KittyCapabilityUnknown {
		t.Fatal("foreign probe accepted")
	}
	a.Update(terminalSessionTick{a})
	if a.Snapshot().Kitty != KittyCapabilityUnsupported {
		t.Fatal("timeout did not resolve")
	}
	cmd := a.Update(reply)
	if cmd == nil || a.Snapshot().Kitty != KittyCapabilitySupported {
		t.Fatal("late reply not accepted")
	}
	msg := cmd().(TerminalSessionMsg)
	if msg.Session != a || msg.State.Kitty != KittyCapabilitySupported || !IsPictureMsg(msg) {
		t.Fatal("missing scoped notification")
	}
	if a.Update(reply) != nil {
		t.Fatal("duplicate reply notified twice")
	}
	if b.Update(terminalSessionTick{a}) != nil {
		t.Fatal("foreign timeout accepted")
	}
	forced := NewTerminalSession(TerminalOptions{Kitty: KittyCapabilityUnsupported})
	defer forced.Close()
	_ = forced.Init()
	forced.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: forced.probeID}})
	if forced.Snapshot().Kitty != KittyCapabilityUnsupported {
		t.Fatal("reply overrode explicit unsupported")
	}
}

func TestTerminalSessionSharedPicturesGeometry(t *testing.T) {
	s := NewTerminalSession(sessionOptions(false))
	defer s.Close()
	a, b := New(Options{Session: s, RetireDelay: -1}), New(Options{Session: s, RetireDelay: -1})
	defer func() { drivePicture(t, a, a.Close()); drivePicture(t, b, b.Close()) }()
	if a.Init() == nil || b.Init() != nil {
		t.Fatal("shared pictures probed twice")
	}
	for _, p := range []*Picture{a, b} {
		drivePicture(t, p, p.SetSize(2, 2))
		drivePicture(t, p, p.SetImage(randomNRGBA(2, 4)))
	}
	// Every picture consumes geometry, but only the first forwarding publishes it.
	event := uv.CellSizeEvent{Width: 3, Height: 6}
	msgs := drivePicture(t, a, a.Update(event))
	count := 0
	for _, m := range msgs {
		if _, ok := m.(TerminalSessionMsg); ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("got %d session notifications", count)
	}
	msgs = drivePicture(t, b, b.Update(event))
	for _, m := range msgs {
		if _, ok := m.(TerminalSessionMsg); ok {
			t.Fatal("duplicate notification")
		}
	}
	for _, p := range []*Picture{a, b} {
		w, h := p.CellPixelSize()
		if w != 3 || h != 6 {
			t.Fatal("geometry not shared")
		}
		before := p.Status().Generation
		drivePicture(t, p, p.Update(uv.CellSizeEvent{}))
		if p.Status().Generation != before {
			t.Fatal("invalid geometry replaced frame")
		}
	}
}

func TestTerminalSessionCloseSuppressesQueuedProbe(t *testing.T) {
	t.Setenv("NTCHARTS_KITTY", "")
	t.Setenv("TERM", "xterm-kitty")
	opts := sessionOptions(false)
	opts.Kitty = KittyCapabilityUnknown
	s := NewTerminalSession(opts)
	cmds := collectBatchCmds(s.Init())
	s.Close()
	s.Close()
	// The final command is the timeout; it is harmless and need not be waited on.
	for _, cmd := range cmds[:len(cmds)-1] {
		if cmd() != nil {
			t.Fatal("closed session emitted output")
		}
	}
	if s.Init() != nil || s.Update(terminalSessionTick{s}) != nil {
		t.Fatal("closed session revived")
	}
}

func TestTerminalSessionSharedCapabilityIsScoped(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("browser shared-memory support is the live registry contract")
	}
	opts := sessionOptions(false)
	opts.SharedMemory = KittyCapabilityUnknown
	a, b := NewTerminalSession(opts), NewTerminalSession(opts)
	defer a.Close()
	defer b.Close()
	_ = a.Init()
	_ = b.Init()
	// Exercise response routing even on hosts where object creation is denied.
	a.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: a.probeID + 1}, Payload: []byte("OK")})
	if a.Snapshot().SharedMemory != KittyCapabilitySupported {
		t.Fatal("shared response ignored")
	}
	before := b.Snapshot()
	b.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: a.probeID + 1}, Payload: []byte("OK")})
	if b.Snapshot() != before {
		t.Fatal("shared response crossed sessions")
	}
}

func TestTerminalSessionOverridesEnvironment(t *testing.T) {
	t.Setenv("NTCHARTS_KITTY", "unsupported")
	t.Setenv("NTCHARTS_TMUX_PASSTHROUGH", "true")
	s := NewTerminalSession(sessionOptions(false))
	defer s.Close()
	state := s.Snapshot()
	if state.Kitty != KittyCapabilitySupported || state.TmuxPassthrough {
		t.Fatal("environment overrode explicit options")
	}
	inherited := NewTerminalSession(TerminalOptions{})
	defer inherited.Close()
	inherited.Init()
	if inherited.Snapshot().Kitty != KittyCapabilityUnsupported || !inherited.Snapshot().TmuxPassthrough {
		t.Fatal("environment defaults not inherited")
	}
}

func TestDefaultTerminalSessionTracksLegacyGeometry(t *testing.T) {
	old := defaultTerminalSession.state
	t.Cleanup(func() { defaultTerminalSession.state = old })
	m := NewModel()
	m.Update(uv.CellSizeEvent{Width: 9, Height: 18})
	if state := DefaultTerminalSession().Snapshot(); state.CellPixelWidth != 9 || state.CellPixelHeight != 18 {
		t.Fatal("legacy geometry not recorded")
	}
	p := New(Options{})
	p.Update(uv.CellSizeEvent{Width: 10, Height: 20})
	if state := DefaultTerminalSession().Snapshot(); state.CellPixelWidth != 10 || state.CellPixelHeight != 20 {
		t.Fatal("default picture geometry not recorded")
	}
}
