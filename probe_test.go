package ntpicture

import (
	"fmt"
	"strings"
	"testing"
)

func newTestProbe(t *testing.T, opts ProbeOptions) *Probe {
	t.Helper()
	t.Setenv("NT_KITTY", "")
	t.Setenv("NT_TMUX", "0")
	return NewProbe(opts)
}

var probeOK = fmt.Sprintf("\x1b_Gi=%d;OK\x1b\\", kittyProbeID)

func TestProbeQuery(t *testing.T) {
	q := newTestProbe(t, ProbeOptions{}).Query()
	if !strings.HasPrefix(q, "\x1b_Ga=q") || !strings.HasSuffix(q, "\x1b[16t\x1b[c") {
		t.Errorf("unexpected query %q", q)
	}
	if q := newTestProbe(t, ProbeOptions{Kitty: KittyCapabilitySupported}).Query(); q != "\x1b[16t\x1b[c" {
		t.Errorf("known capability still asked: %q", q)
	}
	on := true
	// tmux answers an unwrapped device-attributes query itself, ahead of the
	// outer terminal's Kitty reply, so both must use passthrough.
	wrappedDA := "\x1bPtmux;\x1b\x1b[c\x1b\\"
	if q := newTestProbe(t, ProbeOptions{TmuxPassthrough: &on}).Query(); !strings.HasPrefix(q, "\x1bPtmux;\x1b\x1b_Ga=q") || !strings.HasSuffix(q, "\x1b[16t"+wrappedDA) || strings.Count(q, "[c") != 1 {
		t.Errorf("tmux query not routed through passthrough: %q", q)
	}
	if q := newTestProbe(t, ProbeOptions{TmuxPassthrough: &on, Kitty: KittyCapabilitySupported}).Query(); q != "\x1b[16t\x1b[c" {
		t.Errorf("tmux query with a known capability: %q", q)
	}
	t.Setenv("NT_KITTY", "unsupported")
	p := NewProbe(ProbeOptions{})
	if strings.Contains(p.Query(), "\x1b_G") || p.Result().Kitty != KittyCapabilityUnsupported {
		t.Errorf("environment override ignored: %q %+v", p.Query(), p.Result())
	}
}

func TestProbeFeed(t *testing.T) {
	for name, tc := range map[string]struct {
		opts  ProbeOptions
		input string
		rest  string
		want  ProbeResult
		done  bool
	}{
		"kitty terminal":     {ProbeOptions{}, probeOK + "\x1b[6;20;10t\x1b[?62;4c", "", ProbeResult{KittyCapabilitySupported, 10, 20}, true},
		"plain terminal":     {ProbeOptions{}, "\x1b[?1;2c", "", ProbeResult{Kitty: KittyCapabilityUnsupported}, true},
		"no answer yet":      {ProbeOptions{}, probeOK, "", ProbeResult{Kitty: KittyCapabilitySupported}, false},
		"probe error":        {ProbeOptions{}, fmt.Sprintf("\x1b_Gi=%d;ENOTSUPPORTED\x1b\\\x1b[?6c", kittyProbeID), "", ProbeResult{Kitty: KittyCapabilityUnsupported}, true},
		"keys kept":          {ProbeOptions{}, "a\x1b[A" + probeOK + "b\x1b[?6cc\x1b[B", "a\x1b[Abc\x1b[B", ProbeResult{Kitty: KittyCapabilitySupported}, true},
		"foreign replies":    {ProbeOptions{}, "\x1b_Gi=7;OK\x1b\\\x1b[4;600;800t\x1b[1;5R\x1bOP", "\x1b_Gi=7;OK\x1b\\\x1b[4;600;800t\x1b[1;5R\x1bOP", ProbeResult{}, false},
		"bad cell size":      {ProbeOptions{}, "\x1b[6;0;0t\x1b[?6c", "", ProbeResult{Kitty: KittyCapabilityUnsupported}, true},
		"tmux reply order":   {ProbeOptions{}, "\x1b[6;32;16t" + probeOK + "\x1b[?62;4c", "", ProbeResult{KittyCapabilitySupported, 16, 32}, true},
		"alt+_ then plain":   {ProbeOptions{}, "\x1b_\x1b[?1;2c", "\x1b_", ProbeResult{Kitty: KittyCapabilityUnsupported}, true},
		"alt+_ then kitty":   {ProbeOptions{}, "\x1b_" + probeOK + "\x1b[?6c", "\x1b_", ProbeResult{Kitty: KittyCapabilitySupported}, true},
		"alt+[ then reply":   {ProbeOptions{}, "\x1b[\x1b[?1;2c", "\x1b[", ProbeResult{Kitty: KittyCapabilityUnsupported}, true},
		"alt+[ then kitty":   {ProbeOptions{}, "\x1b[" + probeOK + "\x1b[6;20;10t\x1b[?6c", "\x1b[", ProbeResult{KittyCapabilitySupported, 10, 20}, true},
		"broken apc":         {ProbeOptions{}, "\x1b_Gi=1" + probeOK + "\x1b[?6c", "\x1b_Gi=1", ProbeResult{Kitty: KittyCapabilitySupported}, true},
		"intermediate bytes": {ProbeOptions{}, "\x1b[?2026;1$y\x1b[?6c", "\x1b[?2026;1$y", ProbeResult{Kitty: KittyCapabilityUnsupported}, true},
		"override wins":      {ProbeOptions{Kitty: KittyCapabilityUnsupported}, probeOK + "\x1b[?6c", "", ProbeResult{Kitty: KittyCapabilityUnsupported}, true},
	} {
		// Whole, then one byte per read: the result must not depend on framing.
		for _, step := range []int{len(tc.input), 1} {
			p := newTestProbe(t, tc.opts)
			var rest []byte
			for i := 0; i < len(tc.input); i += step {
				out, _ := p.Feed([]byte(tc.input[i:min(i+step, len(tc.input))]))
				rest = append(rest, out...)
			}
			if string(rest) != tc.rest || p.Result() != tc.want || p.Done() != tc.done {
				t.Errorf("%s (step %d): rest=%q result=%+v done=%v, want %q %+v %v", name, step, rest, p.Result(), p.Done(), tc.rest, tc.want, tc.done)
			}
		}
	}
}

func TestProbeTimeoutReleasesHeldInput(t *testing.T) {
	p := newTestProbe(t, ProbeOptions{})
	if rest, done := p.Feed([]byte("x\x1b")); string(rest) != "x" || done {
		t.Fatalf("rest=%q done=%v", rest, done)
	}
	if p.Result().Kitty != KittyCapabilityUnknown {
		t.Fatalf("pending probe reported %+v", p.Result())
	}
	if rest := p.Timeout(); string(rest) != "\x1b" {
		t.Fatalf("Timeout returned %q", rest)
	}
	if !p.Done() || p.Result().Kitty != KittyCapabilityUnsupported {
		t.Fatalf("after timeout: done=%v %+v", p.Done(), p.Result())
	}
	if rest, done := p.Feed([]byte(probeOK)); string(rest) != probeOK || !done {
		t.Fatalf("finished probe consumed input: %q", rest)
	}
	if rest := p.Timeout(); len(rest) != 0 {
		t.Fatalf("second Timeout returned %q", rest)
	}
}

func TestProbeDoesNotHoldRunawaySequences(t *testing.T) {
	p := newTestProbe(t, ProbeOptions{})
	junk := "\x1b_" + strings.Repeat("x", 2*probeMaxAPC)
	rest, _ := p.Feed([]byte(junk))
	if string(rest)+string(p.Timeout()) != junk {
		t.Fatal("unterminated APC was not passed through intact")
	}
}
