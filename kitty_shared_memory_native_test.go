//go:build linux || (darwin && cgo)

package ntpicture

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntpicture/internal/kittyshm"
)

func TestSharedMemoryTmuxWrapsOnce(t *testing.T) {
	original := tmuxPassthroughEnabled.Load()
	SetTmuxPassthrough(true)
	defer SetTmuxPassthrough(original)
	apc, obj, err := buildKittySharedMemoryAPC(randomNRGBA(2, 2), 45, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Unlink()
	if strings.Count(apc, "tmux;") != 1 || !strings.HasPrefix(apc, "\x1bPtmux;\x1b\x1b_G") {
		t.Fatalf("shared-memory APC must have exactly one tmux wrapper: %q", apc)
	}
}

func TestNativeSharedMemoryFrameLifecycle(t *testing.T) {
	original := tmuxPassthroughEnabled.Load()
	SetTmuxPassthrough(false)
	defer SetTmuxPassthrough(original)
	m := NewModelWithConfig(Config{KittyMedium: KittyMediumSharedMemory, Fit: FitFill, CellPixelWidth: 1, CellPixelHeight: 1})
	m.mode = PictureKitty
	m.cols, m.rows = 2, 2
	m.img = randomNRGBA(2, 2)
	defer m.SetImage(nil)
	first := m.renderCmd()().(KittyFrameMsg)
	if first.Medium != KittyMediumSharedMemory || first.Format != KittyFormatRGBA {
		t.Fatalf("native shared memory unavailable: %+v", first)
	}
	name := string(decodeKittyRaw(t, first.APC, "t=s", "S=16", "s=2,v=2", "U=1", "q=2"))
	if name != first.sharedMemory.Name {
		t.Fatal("wrong shared-memory reference")
	}
	if m.Update(first) == nil {
		t.Fatal("frame not submitted")
	}
	next := m.SetImage(randomNRGBA(2, 2))().(KittyFrameMsg)
	if first.sharedMemory.Done() {
		t.Fatal("submitted frame unlinked before terminal could read")
	}
	// A frame that never reaches Update may be discarded immediately.
	last := m.SetImage(randomNRGBA(2, 2))().(KittyFrameMsg)
	if !next.sharedMemory.Done() {
		t.Fatal("dropped frame not unlinked")
	}
	m.SetImage(nil)
	if !first.sharedMemory.Done() || !last.sharedMemory.Done() {
		t.Fatal("model clear leaked buffers")
	}
}

func BenchmarkKittySharedMemory(b *testing.B) {
	src := image.NewRGBA(image.Rect(0, 0, 1280, 960))
	draw.Draw(src, src.Bounds(), image.NewUniform(color.RGBA{R: 20, G: 40, B: 60, A: 255}), image.Point{}, draw.Src)
	b.ReportAllocs()
	for b.Loop() {
		_, obj, err := buildKittySharedMemoryAPC(src, 45, 160, 60)
		if err != nil {
			b.Fatal(err)
		}
		if err := obj.Unlink(); err != nil {
			b.Fatal(err)
		}
	}
}

func TestNativeSharedMemoryWaitsForProbe(t *testing.T) {
	original := tmuxPassthroughEnabled.Load()
	SetTmuxPassthrough(false)
	defer SetTmuxPassthrough(original)
	resetKittySharedCap(t)
	m := NewModelWithConfig(Config{KittyMedium: KittyMediumSharedMemory, Fit: FitFill, CellPixelWidth: 1, CellPixelHeight: 1})
	m.mode = PictureKitty
	m.cols, m.rows = 2, 2
	m.img = randomNRGBA(2, 2)
	defer m.SetImage(nil)
	for _, c := range []KittyCapability{KittyCapabilityUnknown, KittyCapabilityUnsupported} {
		kittySharedCap.Store(int32(c))
		f := m.SetImage(randomNRGBA(2, 2))().(KittyFrameMsg)
		if f.Medium != KittyMediumDirect || f.sharedMemory != nil || strings.Contains(f.APC, "t=s") {
			t.Fatalf("capability %v: sent a shared-memory frame: %+v", c, f)
		}
	}
	recordKittyResponse(sharedProbeReply("OK"))
	if f := m.SetImage(randomNRGBA(2, 2))().(KittyFrameMsg); f.Medium != KittyMediumSharedMemory {
		t.Fatalf("probed terminal got a direct frame: %+v", f)
	}
}

func TestNativeSharedMemoryQuery(t *testing.T) {
	original := tmuxPassthroughEnabled.Load()
	SetTmuxPassthrough(false)
	defer SetTmuxPassthrough(original)
	resetKittySharedCap(t)
	apc := kittySharedQueryAPC()
	probe := kittySharedProbe.Load()
	if probe == nil {
		t.Fatal("no probe object")
	}
	defer probe.Unlink()
	name := string(decodeKittyRaw(t, apc, "a=q", "t=s", "f=32", "s=1,v=1", "S=4", fmt.Sprintf("i=%d", kittySharedProbeID)))
	if name != probe.Name {
		t.Fatalf("query names %q, want %q", name, probe.Name)
	}
	if strings.Contains(apc, "q=") {
		t.Fatalf("query suppresses its reply: %q", apc)
	}
	recordKittyTimeout()
	if !probe.Done() || kittySharedProbe.Load() != nil {
		t.Fatal("unanswered probe object not released")
	}
}

// queryKittySupportRaw reruns the once-only startup probe and returns what
// it writes to the terminal.
func queryKittySupportRaw(t *testing.T) string {
	t.Helper()
	original := tmuxPassthroughEnabled.Load()
	SetTmuxPassthrough(false)
	t.Cleanup(func() { SetTmuxPassthrough(original) })
	kittyQueryOnce = sync.Once{}
	cmds := collectBatchCmds(QueryKittySupport())
	if len(cmds) == 0 {
		t.Fatal("no probe sent")
	}
	t.Cleanup(func() { _ = kittySharedProbe.Swap(nil).Unlink() })
	raw, _ := cmds[0]().(tea.RawMsg)
	seq, _ := raw.Msg.(string)
	return seq
}

func TestQueryKittySupportProbesSharedMemoryFirst(t *testing.T) {
	resetKittyCapability(t)
	resetKittySharedCap(t)
	t.Setenv("KITTY_WINDOW_ID", "1")
	seq := queryKittySupportRaw(t)
	shared, direct := strings.Index(seq, "a=q,t=s"), strings.Index(seq, "a=q,t=d")
	if shared < 0 || direct < shared {
		t.Fatalf("want the t=s query before the t=d query: %q", seq)
	}
}

func TestQueryKittySupportProbesSharedMemoryWhenForced(t *testing.T) {
	resetKittySharedCap(t)
	seq := queryKittySupportRaw(t)
	if !strings.Contains(seq, "a=q,t=s") || strings.Contains(seq, "a=q,t=d") {
		t.Fatalf("want only the t=s query: %q", seq)
	}
}

// Inspect the command shape even when the host cannot create a shared-memory
// object. The existing payload tests separately require working shared memory.
func TestQueryKittySupportForcedFlatBatch(t *testing.T) {
	resetKittyCapability(t)
	ForceKittyCapability(KittyCapabilitySupported)
	kittyQueryOnce = sync.Once{}
	t.Cleanup(func() { kittyQueryOnce = sync.Once{}; _ = kittySharedProbe.Swap(nil).Unlink() })
	cmds := collectBatchCmds(QueryKittySupport())
	if len(cmds) != 3 {
		t.Fatalf("want raw query, timeout, capability commands in one batch; got %d", len(cmds))
	}
	if msg := cmds[0](); msg == nil {
		t.Fatal("missing raw command")
	} else if _, ok := msg.(tea.RawMsg); !ok {
		t.Fatalf("first command returned %T, want tea.RawMsg", msg)
	}
	if msg := cmds[2](); msg.(KittyCapabilityMsg).Capability != KittyCapabilitySupported {
		t.Fatal("missing known capability")
	}
}

func TestPictureNativeSharedMemoryRetiresSubmittedFrames(t *testing.T) {
	original := tmuxPassthroughEnabled.Load()
	SetTmuxPassthrough(false)
	defer SetTmuxPassthrough(original)
	resetKittySharedCap(t)
	kittySharedCap.Store(int32(KittyCapabilitySupported))

	p := testPicture(true)
	p.opts.Render.KittyMedium = KittyMediumSharedMemory
	drivePicture(t, p, p.SetImage(randomNRGBA(2, 2)))
	firstObj := p.front.kittyShared.object
	if p.Status().Medium != KittyMediumSharedMemory || firstObj == nil || firstObj.Done() {
		t.Fatalf("expected active shared-memory front: %+v", p.Status())
	}

	// Replacing a committed frame must retire (poll consumption), not immediately unlink.
	drivePicture(t, p, p.SetImage(randomNRGBA(2, 2)))
	secondObj := p.front.kittyShared.object
	if firstObj.Done() {
		t.Fatal("submitted Picture frame was unlinked immediately on replacement before terminal could read")
	}
	if secondObj == nil || secondObj.Done() {
		t.Fatal("replacement shared-memory frame not active")
	}

	// Close must unlink both retired and current shared-memory objects.
	drivePicture(t, p, p.Close())
	if !firstObj.Done() || !secondObj.Done() {
		t.Fatal("Close leaked shared-memory buffers")
	}
}

func TestSharedStateReleaseRejectsLateReplace(t *testing.T) {
	var s kittySharedState
	s.begin(1)
	s.release()
	obj, err := kittyshm.Create(4, func([]byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Unlink()
	if s.replace(1, obj) {
		t.Fatal("released state accepted a late object")
	}
	if !obj.Done() {
		t.Fatal("late object not unlinked")
	}
}
