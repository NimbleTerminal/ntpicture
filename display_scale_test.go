package ntpicture

import (
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// scaleSession returns a started session that sends no Kitty probe, so Init
// output holds only the geometry queries and no timeout tick.
func scaleSession(t *testing.T, opts TerminalOptions) (*TerminalSession, string) {
	t.Helper()
	opts.Kitty, opts.SharedMemory = KittyCapabilityUnsupported, KittyCapabilityUnsupported
	if opts.TmuxPassthrough == nil {
		off := false
		opts.TmuxPassthrough = &off
	}
	s := NewTerminalSession(opts)
	t.Cleanup(s.Close)
	return s, output(probeMessages(s.Init()))
}

func cellSizeReply(body string) uv.UnknownOscEvent {
	return uv.UnknownOscEvent("\x1b]1337;ReportCellSize=" + body + "\x07")
}

// skipWithoutQuery skips reply tests in the browser, where the session reads
// devicePixelRatio, sends no query, and so accepts no reply.
func skipWithoutQuery(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "js" {
		t.Skip("the browser reads devicePixelRatio and sends no query")
	}
}

func sessionMsgs(cmd tea.Cmd) (out []TerminalSessionMsg) {
	for _, msg := range probeMessages(cmd) {
		if v, ok := msg.(TerminalSessionMsg); ok {
			out = append(out, v)
		}
	}
	return out
}

func TestDisplayScaleIsUnknownUntilReported(t *testing.T) {
	t.Setenv("NT_DISPLAY_SCALE", "")
	s, _ := scaleSession(t, TerminalOptions{CellPixelWidth: 18, CellPixelHeight: 36})
	if got := s.Snapshot().DisplayScale; got != 0 {
		t.Fatalf("DisplayScale = %v before any report; it must not be inferred or defaulted", got)
	}
	s.Update(uv.CellSizeEvent{Width: 20, Height: 40})
	if got := s.Snapshot().DisplayScale; got != 0 {
		t.Fatalf("DisplayScale = %v after a cell-size report; it must not follow cell size", got)
	}
}

func TestDisplayScaleEnvironment(t *testing.T) {
	for value, want := range map[string]float64{
		"2": 2, "1.5": 1.5, "0.5": 0.5, "8": 8, " 2 ": 2,
		"": 0, "abc": 0, "0": 0, "0.49": 0, "8.01": 0, "-2": 0,
		"NaN": 0, "Inf": 0, "+Inf": 0, "0x1p1": 0, "2x": 0, "1e0": 0, "2;3": 0,
	} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("NT_DISPLAY_SCALE", value)
			s, _ := scaleSession(t, TerminalOptions{})
			if got := s.Snapshot().DisplayScale; got != want {
				t.Fatalf("NT_DISPLAY_SCALE=%q gave %v, want %v", value, got, want)
			}
		})
	}
}

func TestDisplayScaleOptionBeatsEnvironmentBeatsReply(t *testing.T) {
	t.Setenv("NT_DISPLAY_SCALE", "3")
	s, _ := scaleSession(t, TerminalOptions{DisplayScale: 1.25})
	if got := s.Snapshot().DisplayScale; got != 1.25 {
		t.Fatalf("option did not beat environment: %v", got)
	}
	if s.Update(cellSizeReply("17;8;2")) != nil || s.Snapshot().DisplayScale != 1.25 {
		t.Fatal("terminal reply replaced an explicit option")
	}

	s, _ = scaleSession(t, TerminalOptions{})
	if s.Update(cellSizeReply("17;8;2")) != nil || s.Snapshot().DisplayScale != 3 {
		t.Fatal("terminal reply replaced the environment value")
	}

	// An unusable option is "not set", so the environment still applies.
	s, _ = scaleSession(t, TerminalOptions{DisplayScale: 99})
	if got := s.Snapshot().DisplayScale; got != 3 {
		t.Fatalf("out-of-range option gave %v, want the environment's 3", got)
	}
}

func TestDisplayScaleQuery(t *testing.T) {
	skipWithoutQuery(t)
	t.Setenv("NT_DISPLAY_SCALE", "")
	if _, raw := scaleSession(t, TerminalOptions{}); strings.Count(raw, displayScaleQuery) != 1 {
		t.Fatalf("Init output %q does not hold exactly one display-scale query", raw)
	}
	on := true
	_, raw := scaleSession(t, TerminalOptions{TmuxPassthrough: &on})
	if !strings.Contains(raw, "\x1bPtmux;\x1b\x1b]1337;ReportCellSize\x07\x1b\\") {
		t.Fatalf("query was not wrapped for tmux: %q", raw)
	}
	if _, raw := scaleSession(t, TerminalOptions{DisplayScale: 2}); strings.Contains(raw, "1337") {
		t.Fatalf("query sent although the option set the scale: %q", raw)
	}
	t.Setenv("NT_DISPLAY_SCALE", "2")
	if _, raw := scaleSession(t, TerminalOptions{}); strings.Contains(raw, "1337") {
		t.Fatalf("query sent although the environment set the scale: %q", raw)
	}
}

func TestDisplayScaleQuerySuppressedAfterClose(t *testing.T) {
	t.Setenv("NT_DISPLAY_SCALE", "")
	off := false
	s := NewTerminalSession(TerminalOptions{Kitty: KittyCapabilityUnsupported, SharedMemory: KittyCapabilityUnsupported, TmuxPassthrough: &off})
	cmd := s.Init()
	s.Close()
	if msgs := probeMessages(cmd); len(msgs) != 0 {
		t.Fatalf("closed session emitted %v", msgs)
	}
}

func TestDisplayScaleReply(t *testing.T) {
	skipWithoutQuery(t)
	t.Setenv("NT_DISPLAY_SCALE", "")
	for body, want := range map[string]float64{
		"17.0;8.0;2.0": 2, "17;8;1": 1, "34.5;16.25;1.5": 1.5,
		// Two fields is iTerm2's older reply: points only, no scale.
		"17.0;8.0": 0,
		"":         0, "17;8;": 0, "17;8;abc": 0, "17;8;0": 0, "17;8;0.4": 0, "17;8;9": 0,
		"17;8;NaN": 0, "17;8;Inf": 0, "17;8;2;1": 0, "x;8;2": 0, "17;y;2": 0, "0;8;2": 0, "17;-8;2": 0,
	} {
		t.Run(body, func(t *testing.T) {
			s, _ := scaleSession(t, TerminalOptions{CellPixelWidth: 18, CellPixelHeight: 36})
			msgs := sessionMsgs(s.Update(cellSizeReply(body)))
			state := s.Snapshot()
			if state.DisplayScale != want {
				t.Fatalf("reply %q gave %v, want %v", body, state.DisplayScale, want)
			}
			if state.CellPixelWidth != 18 || state.CellPixelHeight != 36 {
				t.Fatalf("reply in points overwrote cell pixels: %dx%d", state.CellPixelWidth, state.CellPixelHeight)
			}
			if want == 0 && len(msgs) != 0 {
				t.Fatalf("ignored reply announced a change: %v", msgs)
			}
			if want != 0 && (len(msgs) != 1 || msgs[0].Session != s || msgs[0].State.DisplayScale != want) {
				t.Fatalf("change was not announced once with the new scale: %v", msgs)
			}
		})
	}
}

func TestDisplayScaleReplyTerminatorsAndStrangers(t *testing.T) {
	skipWithoutQuery(t)
	t.Setenv("NT_DISPLAY_SCALE", "")
	s, _ := scaleSession(t, TerminalOptions{})
	for _, raw := range []string{
		"\x1b]7501;state=idle\x07",
		"\x1b]1337;ReportCellSize\x07",
		"\x1b]1337;ReportCellSizes=17;8;2\x07",
		"\x1b]1338;ReportCellSize=17;8;2\x07",
		"ReportCellSize=17;8;2",
		"",
	} {
		if s.Update(uv.UnknownOscEvent(raw)) != nil || s.Snapshot().DisplayScale != 0 {
			t.Fatalf("unrelated sequence %q changed the scale", raw)
		}
	}
	s.Update(uv.UnknownOscEvent("\x1b]1337;ReportCellSize=17;8;2\x1b\\"))
	if got := s.Snapshot().DisplayScale; got != 2 {
		t.Fatalf("ST-terminated reply gave %v", got)
	}
}

func TestDisplayScaleReplyLifecycle(t *testing.T) {
	skipWithoutQuery(t)
	t.Setenv("NT_DISPLAY_SCALE", "")
	off := false
	opts := TerminalOptions{Kitty: KittyCapabilityUnsupported, SharedMemory: KittyCapabilityUnsupported, TmuxPassthrough: &off}

	s := NewTerminalSession(opts)
	defer s.Close()
	if s.Update(cellSizeReply("17;8;2")) != nil || s.Snapshot().DisplayScale != 0 {
		t.Fatal("reply before Init was accepted; nothing had been asked")
	}
	_ = s.Init()
	// Several pictures forward the same event to one shared session.
	if len(sessionMsgs(s.Update(cellSizeReply("17;8;2")))) != 1 {
		t.Fatal("first delivery did not announce the scale")
	}
	if s.Update(cellSizeReply("17;8;2")) != nil {
		t.Fatal("duplicate delivery announced the scale again")
	}
	// A late, different reply still applies: there is no timeout state.
	s.Update(terminalSessionTick{s})
	s.Update(cellSizeReply("17;8;1"))
	if got := s.Snapshot().DisplayScale; got != 1 {
		t.Fatalf("late reply gave %v", got)
	}

	closed := NewTerminalSession(opts)
	_ = closed.Init()
	closed.Close()
	if closed.Update(cellSizeReply("17;8;2")) != nil || closed.Snapshot().DisplayScale != 0 {
		t.Fatal("closed session accepted a reply")
	}
}

func TestHostGeometryPictureIgnoresDisplayScaleReply(t *testing.T) {
	skipWithoutQuery(t)
	t.Setenv("NT_DISPLAY_SCALE", "")
	s, _ := scaleSession(t, TerminalOptions{})
	p := New(Options{Session: s, HostGeometry: true})
	if p.Update(cellSizeReply("17;8;2")) != nil || s.Snapshot().DisplayScale != 0 {
		t.Fatal("host-geometry picture consumed a terminal reply")
	}
}

func TestDisplayScaleReachesSharedPictures(t *testing.T) {
	skipWithoutQuery(t)
	t.Setenv("NT_DISPLAY_SCALE", "")
	s, _ := scaleSession(t, TerminalOptions{})
	a, b := New(Options{Session: s}), New(Options{Session: s})
	_ = a.Update(cellSizeReply("17;8;2"))
	if b.Update(cellSizeReply("17;8;2")) != nil {
		t.Fatal("second picture re-announced the same reply")
	}
	if got := s.Snapshot().DisplayScale; got != 2 {
		t.Fatalf("picture did not route the reply to its session: %v", got)
	}
}

func TestLegacySessionIgnoresDisplayScale(t *testing.T) {
	ForceKittyCapability(KittyCapabilityUnsupported)
	t.Cleanup(func() { ForceKittyCapability(KittyCapabilitySupported) })
	t.Setenv("NT_DISPLAY_SCALE", "2")
	s := defaultSession()
	if raw := output(probeMessages(s.Init())); strings.Contains(raw, "1337") {
		t.Fatalf("legacy session sent the display-scale query: %q", raw)
	}
	s.Update(cellSizeReply("17;8;3"))
	if got := s.Snapshot().DisplayScale; got != 0 {
		t.Fatalf("legacy session reported DisplayScale %v", got)
	}
}
