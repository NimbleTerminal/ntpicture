package ntpicture

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"
	"nimbleterminal.dev/ntpicture/internal/framecache"
)

func frameTestSession(t *testing.T) *TerminalSession {
	t.Helper()
	s := NewTerminalSession(TerminalOptions{Kitty: KittyCapabilitySupported, SharedMemory: KittyCapabilityUnsupported,
		VirtualPlacement: KittyCapabilityUnsupported, FrameEdits: KittyCapabilitySupported, FrameAppend: KittyCapabilitySupported, FrameSelect: KittyCapabilitySupported})
	t.Cleanup(s.Close)
	return s
}

func featureReply(t *testing.T, id int, fields, payload string) uv.KittyGraphicsEvent {
	t.Helper()
	var opts kitty.Options
	if err := opts.UnmarshalText([]byte(fmt.Sprintf("i=%d,%s", id, fields))); err != nil {
		t.Fatal(err)
	}
	return uv.KittyGraphicsEvent{Options: opts, Payload: []byte(payload)}
}

func TestFrameFeaturesRequireOperationEchoes(t *testing.T) {
	for _, tmux := range []bool{false, true} {
		opts := sessionOptions(tmux)
		s := NewTerminalSession(opts)
		other := NewTerminalSession(opts)
		defer s.Close()
		defer other.Close()
		s.Init()
		other.Init()
		raw := output(probeMessages(s.Update(featureReply(t, s.probeID, "", "OK"))))
		for i, q := range frameFeatureQueries {
			if !strings.Contains(raw, q) {
				t.Fatalf("missing query %s: %q", q, raw)
			}
			id := s.probeID + 3 + i
			// Normal detection OK and partial/error feature replies must not enable it.
			for _, reply := range []uv.KittyGraphicsEvent{
				featureReply(t, id, "", "OK"),
				featureReply(t, id, q, "ENOTSUP"),
			} {
				s.Update(reply)
				if *s.state.frameCapabilities()[i] == KittyCapabilitySupported {
					t.Fatal("invalid reply granted support")
				}
			}
			reply := featureReply(t, id, q, "OK")
			other.Update(reply)
			if *other.state.frameCapabilities()[i] == KittyCapabilitySupported {
				t.Fatal("foreign session reply accepted")
			}
			s.Update(reply)
			if *s.state.frameCapabilities()[i] != KittyCapabilitySupported {
				t.Fatal("feature echo did not grant support")
			}
		}
		if strings.Contains(raw, "\x1bPtmux;") != tmux {
			t.Fatal("wrong feature transport")
		}
		s.Update(terminalFeatureTick{s})
		if !s.transport().frameEdits || !s.transport().frameCache {
			t.Fatal("timeout revoked positive feature replies")
		}
	}
}

func TestFrameFeaturePartialEchoTimeoutAndOverrides(t *testing.T) {
	s := NewTerminalSession(sessionOptions(false))
	defer s.Close()
	s.Init()
	s.Update(featureReply(t, s.probeID, "", "OK"))
	for i, q := range frameFeatureQueries {
		// Omit one required key (including c for selection).
		missing := q[:strings.LastIndex(q, ",")]
		s.Update(featureReply(t, s.probeID+3+i, missing, "OK"))
		if *s.state.frameCapabilities()[i] == KittyCapabilitySupported {
			t.Fatal("partial echo granted support")
		}
	}
	late := NewTerminalSession(sessionOptions(false))
	defer late.Close()
	late.Init()
	late.Update(featureReply(t, late.probeID, "", "OK"))
	late.Update(terminalFeatureTick{s})
	if late.Snapshot().FrameEdits != KittyCapabilityUnknown {
		t.Fatal("foreign timeout applied")
	}
	late.Update(terminalFeatureTick{late})
	for _, cap := range late.state.frameCapabilities() {
		if *cap != KittyCapabilityUnsupported {
			t.Fatal("timeout left capability unresolved")
		}
	}
	late.Update(featureReply(t, late.probeID+3, frameFeatureQueries[0], "OK"))
	if !late.transport().frameEdits || late.transport().frameCache {
		t.Fatal("late edit reply enabled unrelated cache operations")
	}
	forced := NewTerminalSession(TerminalOptions{Kitty: KittyCapabilitySupported, VirtualPlacement: KittyCapabilityUnsupported,
		FrameEdits: KittyCapabilityUnsupported, FrameAppend: KittyCapabilitySupported})
	defer forced.Close()
	forced.Init()
	forced.Update(featureReply(t, forced.probeID, "", "OK"))
	forced.Update(featureReply(t, forced.probeID+3, frameFeatureQueries[0], "OK"))
	forced.Update(terminalFeatureTick{forced})
	if forced.Snapshot().FrameEdits != KittyCapabilityUnsupported || forced.Snapshot().FrameAppend != KittyCapabilitySupported || forced.transport().frameCache {
		t.Fatal("probe overrode explicit capabilities")
	}
}

func TestStableIDFallbackBeforeFrameDetection(t *testing.T) {
	for _, cap := range []KittyCapability{KittyCapabilityUnknown, KittyCapabilityUnsupported, KittyCapabilitySupported} {
		t.Run(fmt.Sprint(cap), func(t *testing.T) {
			s := NewTerminalSession(TerminalOptions{Kitty: KittyCapabilitySupported, SharedMemory: KittyCapabilityUnsupported, FrameEdits: cap})
			defer s.Close()
			p := New(Options{Session: s, StableID: true, RetireDelay: -1, Render: Config{Fit: FitFill}})
			p.SetSize(8, 4)
			p.SetTerminal(true, 1, 1)
			defer func() { drivePicture(t, p, p.Close()) }()
			img := randomNRGBA(8, 4)
			drivePicture(t, p, p.SetImage(img))
			id, grid := p.front.cfg.KittyID, p.View()
			img.SetNRGBA(2, 2, color.NRGBA{R: 255, A: 255})
			raw := output(drivePicture(t, p, p.SetImage(img)))
			edits := cap == KittyCapabilitySupported
			if strings.Contains(raw, "a=f") != edits || strings.Contains(raw, "a=T") == edits {
				t.Fatalf("wrong frame path for %v: %q", cap, raw)
			}
			if p.front.cfg.KittyID != id || p.View() != grid || p.Status().Busy {
				t.Fatal("fallback lost stable placement or stalled")
			}
		})
	}
}

func TestFrameCacheRequiresAppendAndSelect(t *testing.T) {
	for _, caps := range [][2]KittyCapability{
		{KittyCapabilityUnknown, KittyCapabilityUnknown},
		{KittyCapabilitySupported, KittyCapabilityUnsupported},
		{KittyCapabilityUnsupported, KittyCapabilitySupported},
		{KittyCapabilitySupported, KittyCapabilitySupported},
	} {
		s := NewTerminalSession(TerminalOptions{Kitty: KittyCapabilitySupported, SharedMemory: KittyCapabilityUnsupported, FrameAppend: caps[0], FrameSelect: caps[1]})
		p := New(Options{Session: s, StableID: true, RetireDelay: -1, Render: Config{Fit: FitFill}})
		p.SetSize(8, 4)
		p.SetTerminal(true, 1, 1)
		src := &framecache.Source{Frames: 2, Bytes: 32 << 20}
		one := &framecache.Image{Image: randomNRGBA(8, 4), Source: src, Frame: 1}
		two := &framecache.Image{Image: randomNRGBA(8, 4), Source: src, Frame: 2}
		drivePicture(t, p, p.SetImageImmutable(one))
		raw := output(drivePicture(t, p, p.SetImageImmutable(two)))
		cached := caps[0] == KittyCapabilitySupported && caps[1] == KittyCapabilitySupported
		if strings.Contains(raw, "a=f") != cached || strings.Contains(raw, "a=a") != cached {
			t.Fatalf("cache used without both capabilities: %v %q", caps, raw)
		}
		raw = output(drivePicture(t, p, p.SetImageImmutable(one)))
		if strings.Contains(raw, "a=a") != cached || strings.Contains(raw, "a=T") == cached {
			t.Fatal("return to first frame used wrong path")
		}
		drivePicture(t, p, p.Close())
		s.Close()
	}
}

func TestLegacyFrameFeatureDetection(t *testing.T) {
	resetKittyCapability(t)
	ForceKittyCapability(KittyCapabilitySupported)
	raw := output(probeMessages(recordKittyResponse(featureReply(t, kittyProbeID, "", "OK"))))
	if !strings.Contains(raw, "e=f,r=1") {
		t.Fatal("forced basic support skipped feature discovery")
	}
	for i, q := range frameFeatureQueries {
		recordKittyResponse(featureReply(t, kittyFeatureProbeID+1+i, "", "OK"))
		state := defaultSession().Snapshot()
		if *state.frameCapabilities()[i] == KittyCapabilitySupported {
			t.Fatal("plain OK enabled legacy frames")
		}
		recordKittyResponse(featureReply(t, kittyFeatureProbeID+1+i, q, "OK"))
	}
	if !legacyTransport().frameEdits || !legacyTransport().frameCache {
		t.Fatal("legacy feature replies not recorded")
	}
	recordKittyTimeout()
	if !legacyTransport().frameEdits {
		t.Fatal("legacy timeout revoked detection")
	}
}
