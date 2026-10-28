package ntpicture

import (
	tea "charm.land/bubbletea/v2"
	"reflect"
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
	t.Setenv("NTPICTURE_KITTY", "")
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
	reply := uv.KittyGraphicsEvent{Options: kitty.Options{ID: a.probeID}, Payload: []byte("OK")}
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
	var msg TerminalSessionMsg
	for _, m := range probeMessages(cmd) {
		if v, ok := m.(TerminalSessionMsg); ok {
			msg = v
		}
	}
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
	forced.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: forced.probeID}, Payload: []byte("OK")})
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
	t.Setenv("NTPICTURE_KITTY", "")
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

	// Timeout resolves Unknown -> Unsupported so direct fallback proceeds immediately,
	// but keeps b.probe linked so a slow terminal can still read it and reply OK.
	bHasProbe := b.probe != nil
	b.Update(terminalSessionTick{b})
	if b.Snapshot().SharedMemory != KittyCapabilityUnsupported {
		t.Fatal("timeout did not resolve unknown shared memory to unsupported")
	}
	if bHasProbe && (b.probe == nil || b.probe.Done()) {
		t.Fatal("timeout unlinked shared-memory probe before late terminal reply")
	}
	b.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: b.probeID + 1}, Payload: []byte("OK")})
	if b.Snapshot().SharedMemory != KittyCapabilitySupported || b.probe != nil {
		t.Fatalf("late OK after timeout did not upgrade SharedMemory or unlink probe: state=%+v probe=%v", b.Snapshot(), b.probe)
	}
}

func TestTerminalSessionOverridesEnvironment(t *testing.T) {
	t.Setenv("NTPICTURE_KITTY", "unsupported")
	t.Setenv("NTPICTURE_TMUX_PASSTHROUGH", "true")
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
	resetKittyCapability(t)
	resetKittySharedCap(t)
	old := defaultTerminalSession.Snapshot()
	t.Cleanup(func() {
		defaultTerminalSession.setCellSize(old.CellPixelWidth, old.CellPixelHeight)
	})
	defaultTerminalSession.setCellSize(0, 0)

	m := NewModel()
	m.Update(uv.CellSizeEvent{Width: 9, Height: 18})
	if state := DefaultTerminalSession().Snapshot(); state.CellPixelWidth != 9 || state.CellPixelHeight != 18 {
		t.Fatal("legacy geometry not recorded")
	}

	// Standalone New(Options{}) owns an isolated session and must not mutate DefaultTerminalSession().
	isolated := New(Options{})
	isolated.Update(uv.CellSizeEvent{Width: 14, Height: 28})
	if state := DefaultTerminalSession().Snapshot(); state.CellPixelWidth != 9 || state.CellPixelHeight != 18 {
		t.Fatalf("owned picture session leaked geometry into DefaultTerminalSession: %+v", state)
	}
	if state := isolated.TerminalState(); state.CellPixelWidth != 14 || state.CellPixelHeight != 28 {
		t.Fatalf("owned picture session did not record geometry: %+v", state)
	}
	_ = isolated.Close()
	if !isolated.Session().closed.Load() {
		t.Fatal("Picture.Close did not close owned TerminalSession")
	}

	p := New(Options{Session: DefaultTerminalSession()})
	defer func() { _ = p.Close() }()
	p.Update(uv.CellSizeEvent{Width: 10, Height: 20})
	if state := DefaultTerminalSession().Snapshot(); state.CellPixelWidth != 10 || state.CellPixelHeight != 20 {
		t.Fatal("default session picture geometry not recorded")
	}

	cmd := DefaultTerminalSession().Update(uv.CellSizeEvent{Width: 11, Height: 22})
	if cmd == nil {
		t.Fatal("default session did not emit command on cell size change")
	}
	if msg, ok := cmd().(TerminalSessionMsg); !ok || msg.Session != DefaultTerminalSession() || msg.State.CellPixelWidth != 11 || msg.State.CellPixelHeight != 22 {
		t.Fatalf("unexpected default session cell size msg: %#v", msg)
	}

	withDefault := New(Options{Session: DefaultTerminalSession()})
	defer func() { _ = withDefault.Close() }()
	msgs := drivePicture(t, withDefault, withDefault.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: kittyProbeID}, Payload: []byte("OK")}))
	found := false
	for _, m := range msgs {
		if msg, ok := m.(TerminalSessionMsg); ok && msg.Session == DefaultTerminalSession() && msg.State.Kitty == KittyCapabilitySupported {
			found = true
		}
	}
	if !found {
		t.Fatal("picture with DefaultTerminalSession did not emit TerminalSessionMsg on probe reply")
	}
}

// Flatten probe commands, including their timeout messages, without routing them.
func probeMessages(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for i := 0; i < v.Len(); i++ {
		out = append(out, probeMessages(v.Index(i).Interface().(tea.Cmd))...)
	}
	return out
}

func TestTerminalSessionFeatureDetection(t *testing.T) {
	t.Setenv("NTPICTURE_KITTY", "")
	t.Setenv("TERM", "xterm-kitty")
	for _, tmux := range []bool{false, true} {
		opts := sessionOptions(tmux)
		opts.Kitty = KittyCapabilityUnknown
		s := NewTerminalSession(opts)
		defer s.Close()
		initial := output(probeMessages(s.Init()))
		if strings.Contains(initial, "e=T") || !strings.Contains(initial, "a=q,t=d,f=24") {
			t.Fatalf("not a plain detect query: %q", initial)
		}
		s.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: s.probeID}, Payload: []byte("EINVAL")})
		if s.Snapshot().Kitty == KittyCapabilitySupported || s.featureSent {
			t.Fatal("error granted support")
		}
		raw := output(probeMessages(s.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: s.probeID}, Payload: []byte("OK")})))
		if !strings.Contains(raw, "a=q,e=T,U=1") || strings.Contains(raw, "AAAA") || strings.Contains(raw, "\x1bPtmux;") != tmux {
			t.Fatalf("wrong feature query: %q", raw)
		}
		if s.Snapshot().VirtualPlacement != KittyCapabilityUnknown || !s.transport().disableVirtualCrop {
			t.Fatal("plain OK granted virtual placements")
		}
		s.Update(terminalFeatureTick{s})
		if s.Snapshot().VirtualPlacement != KittyCapabilityUnsupported {
			t.Fatal("feature timeout did not resolve")
		}
		// A terminal ignoring feature keys may return OK without U=1.
		s.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: s.probeID + 2}, Payload: []byte("OK")})
		if s.Snapshot().VirtualPlacement != KittyCapabilityUnsupported {
			t.Fatal("missing U granted support")
		}
		s.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: s.probeID + 2, VirtualPlacement: true}, Payload: []byte("OK")})
		if s.Snapshot().VirtualPlacement != KittyCapabilitySupported || s.transport().disableVirtualCrop {
			t.Fatal("U=1 not recorded")
		}
	}
}
