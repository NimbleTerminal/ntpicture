package ntpicture

import (
	"errors"
	"image"
	"image/color"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// Drive the actual Bubble Tea command tree, including its private sequence
// message, without opening a terminal. Commands here have no retirement waits.
func drivePicture(t *testing.T, p *Picture, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	var out []tea.Msg
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		msg := cmd()
		if msg == nil {
			return
		}
		v := reflect.ValueOf(msg)
		if v.Kind() == reflect.Slice {
			for i := 0; i < v.Len(); i++ {
				run(v.Index(i).Interface().(tea.Cmd))
			}
			return
		}
		out = append(out, msg)
		run(p.Update(msg))
	}
	run(cmd)
	return out
}
func testPicture(kitty bool) *Picture {
	p := New(Options{Render: Config{Fit: FitFill, CellPixelWidth: 1, CellPixelHeight: 1}, RetireDelay: -1})
	p.SetSize(2, 2)
	p.SetTerminal(kitty, 1, 1)
	return p
}
func testPixels(c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}
func output(messages []tea.Msg) string {
	var out strings.Builder
	for _, msg := range messages {
		if v, ok := msg.(tea.RawMsg); ok {
			if s, ok := v.Msg.(string); ok {
				out.WriteString(s)
			}
		}
	}
	return out.String()
}

func TestPictureRetainsFrontAndPreservesConfig(t *testing.T) {
	p := testPicture(true)
	p.opts.Render.Anchor = AnchorBottom
	p.opts.Render.KittyZ = -3
	p.opts.Render.KittyFormat = KittyFormatZlib
	defer func() { drivePicture(t, p, p.Close()) }()
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{R: 255, A: 255})))
	front := p.front
	oldView := p.View()
	pending := p.SetImage(testPixels(color.RGBA{B: 255, A: 255}))
	if p.View() != oldView || p.front != front || !p.Status().Busy {
		t.Fatal("replacement did not retain front")
	}
	msgs := drivePicture(t, p, pending)
	if p.front == front || p.Status().Busy || !strings.Contains(output(msgs), "o=z") || !strings.Contains(output(msgs), "z=-3") {
		t.Fatal("replacement not committed with requested encoding")
	}
	if p.front.cfg.Fit != FitFill || p.front.cfg.Anchor != AnchorBottom {
		t.Fatal("render configuration lost")
	}
	if !strings.Contains(output(msgs), kittyDeleteImage(front.cfg.KittyID)) {
		t.Fatal("old placement not retired")
	}
}

func TestPictureSnapshotsAndIndependentOwners(t *testing.T) {
	p, q := testPicture(true), testPicture(true)
	defer func() { drivePicture(t, p, p.Close()) }()
	defer func() { drivePicture(t, q, q.Close()) }()
	img := testPixels(color.RGBA{R: 255, A: 255})
	cmd := p.SetImage(img)
	img.SetRGBA(0, 0, color.RGBA{B: 255, A: 255})
	ready := cmd()
	if q.Update(ready) != nil || q.View() != "" {
		t.Fatal("foreign message accepted")
	}
	drivePicture(t, p, p.Update(ready))
	if p.front.image.At(0, 0) != (color.RGBA{R: 255, A: 255}) {
		t.Fatal("mutable input was retained")
	}
	drivePicture(t, q, q.SetImage(img))
	if p.front.cfg.KittyID == q.front.cfg.KittyID {
		t.Fatal("pictures allocated the same ID")
	}
}

func TestPictureCoalescesAndCancelsQueuedWork(t *testing.T) {
	p := testPicture(true)
	defer func() { drivePicture(t, p, p.Close()) }()
	first := p.SetImage(testPixels(color.RGBA{R: 255, A: 255}))
	job := p.job
	if cmd := p.SetImage(testPixels(color.RGBA{G: 255, A: 255})); cmd != nil {
		t.Fatal("second worker started")
	}
	p.SetImage(testPixels(color.RGBA{B: 255, A: 255}))
	if p.job != job || job.ctx.Err() == nil {
		t.Fatal("obsolete work not cancelled/coalesced")
	}
	msgs := drivePicture(t, p, first)
	if p.Status().Busy || p.front.image.At(0, 0) != (color.RGBA{B: 255, A: 255}) {
		t.Fatal("latest frame lost")
	}
	count := 0
	for _, m := range msgs {
		if _, ok := m.(PresentedMsg); ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("presented %d frames; want only latest", count)
	}
}

func TestPictureCloseAfterTransmissionAcceptance(t *testing.T) {
	p := testPicture(true)
	frame := p.SetImage(testPixels(color.RGBA{R: 255, A: 255}))()
	transmit := p.Update(frame)
	id := p.job.placement.cfg.KittyID
	// Simulate output emitted, then removal before completion. A host need not
	// route further messages to this component for the delete to be emitted.
	seq := reflect.ValueOf(transmit())
	raw := seq.Index(0).Interface().(tea.Cmd)()
	if _, ok := raw.(tea.RawMsg); !ok {
		t.Fatal("no image transmission")
	}
	drivePicture(t, p, p.Close())
	completion := seq.Index(1).Interface().(tea.Cmd)
	msgs := drivePicture(t, p, completion)
	if !strings.Contains(output(msgs), kittyDeleteImage(id)) {
		t.Fatal("late transmission not deleted")
	}
	if p.View() != "" || p.Status().Busy || p.Close() != nil {
		t.Fatal("close not final/idempotent")
	}
}

func TestPictureHideResizeAndClearPolicy(t *testing.T) {
	p := testPicture(true)
	defer func() { drivePicture(t, p, p.Close()) }()
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{R: 255, A: 255})))
	old := p.front
	resize := p.SetSize(1, 1)
	if strings.Contains(p.View(), "\n") {
		t.Fatal("old front not clipped to resized area")
	}
	drivePicture(t, p, resize)
	if p.front == old || p.front.cols != 1 {
		t.Fatal("resize not committed")
	}
	drivePicture(t, p, p.SetVisible(false))
	if p.View() != "" {
		t.Fatal("hidden placement still visible")
	}
	drivePicture(t, p, p.SetVisible(true))
	if p.View() == "" {
		t.Fatal("show lost source")
	}
	p.opts.ClearOnReplace = true
	next := p.SetImage(testPixels(color.RGBA{B: 255, A: 255}))
	if p.View() != "" {
		t.Fatal("clear policy retained old content")
	}
	drivePicture(t, p, next)
}

func TestPictureBoundsAndAllocatorFailure(t *testing.T) {
	p := testPicture(true)
	defer func() { drivePicture(t, p, p.Close()) }()
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{A: 255})))
	front := p.View()
	drivePicture(t, p, p.SetSize(int(^uint(0)>>1), 2))
	if p.Status().Err == nil || p.View() != front {
		t.Fatal("invalid geometry not bounded")
	}
	p.opts.NextID = func() (int, error) { return 0, ErrImageIDExhausted }
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{R: 255, A: 255})))
	if !errors.Is(p.Status().Err, ErrImageIDExhausted) || p.Status().Busy || p.View() != front {
		t.Fatal("allocator failure lost front or stayed busy")
	}
}

// A gated image demonstrates that cancellation cannot open another worker while
// a third-party Image/encoder call is still running. It also exercises Close
// against real concurrent preparation under the race detector.
type gatedImage struct {
	image.Image
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedImage) At(x, y int) color.Color {
	g.once.Do(func() { close(g.entered); <-g.release })
	return g.Image.At(x, y)
}
func TestPictureCloseDuringPreparation(t *testing.T) {
	p := testPicture(true)
	g := &gatedImage{Image: testPixels(color.RGBA{A: 255}), entered: make(chan struct{}), release: make(chan struct{})}
	cmd := p.SetImageImmutable(g)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-g.entered
	cleanup := p.Close()
	close(g.release)
	drivePicture(t, p, cleanup)
	msgs := drivePicture(t, p, p.Update(<-done))
	if strings.Contains(output(msgs), "a=T") || p.View() != "" {
		t.Fatal("closed preparation published content")
	}
}

func TestPictureGlyphRetainsFront(t *testing.T) {
	p := testPicture(false)
	defer func() { drivePicture(t, p, p.Close()) }()
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{R: 255, A: 255})))
	previous := p.View()
	cmd := p.SetImage(testPixels(color.RGBA{B: 255, A: 255}))
	if p.View() != previous {
		t.Fatal("glyph replacement blanked the front")
	}
	drivePicture(t, p, cmd)
	if p.View() == previous || p.Status().Busy {
		t.Fatal("glyph replacement not committed")
	}
}

func TestPictureRejectsInvalidCellGeometryWithoutCorruptingFront(t *testing.T) {
	p := testPicture(true)
	defer func() { drivePicture(t, p, p.Close()) }()
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{A: 255})))
	previous := p.View()
	drivePicture(t, p, p.SetTerminal(true, int(^uint(0)>>1), 2))
	cw, ch := p.CellPixelSize()
	if p.Status().Err == nil || cw != 1 || ch != 1 || p.View() != previous {
		t.Fatal("invalid cell geometry corrupted the displayed state")
	}
}

func TestPictureLateReplacementCannotRetireLatest(t *testing.T) {
	p := testPicture(true)
	defer func() { drivePicture(t, p, p.Close()) }()
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{R: 255, A: 255})))
	encoded := p.SetImage(testPixels(color.RGBA{G: 255, A: 255}))()
	seq := reflect.ValueOf(p.Update(encoded)())
	_ = seq.Index(0).Interface().(tea.Cmd)() // old replacement has left the producer
	p.SetImage(testPixels(color.RGBA{B: 255, A: 255}))
	messages := drivePicture(t, p, seq.Index(1).Interface().(tea.Cmd))
	if p.Status().Busy || p.front.image.At(0, 0) != (color.RGBA{B: 255, A: 255}) {
		t.Fatal("late completion lost newest image")
	}
	if strings.Contains(output(messages), kittyDeleteImage(p.front.cfg.KittyID)) {
		t.Fatal("stale cleanup deleted the newest placement")
	}
}

func TestPictureRetirementKeepsNewerPlacements(t *testing.T) {
	p := testPicture(true)
	old := &picturePlacement{retireAt: time.Now().Add(-time.Second)}
	newer := &picturePlacement{retireAt: time.Now().Add(time.Hour)}
	old.active.Store(true)
	newer.active.Store(true)
	p.retired = []*picturePlacement{old, newer}
	cmd := p.Update(pictureRetire{p})
	if old.active.Load() || !newer.active.Load() || len(p.retired) != 1 {
		t.Fatal("retirement ignored per-placement deadlines")
	}
	drivePicture(t, p, p.Close())
	drivePicture(t, p, cmd) // The outstanding hour-long wait must cancel immediately.
	if newer.active.Load() {
		t.Fatal("close retained a retired placement")
	}
}

func TestPictureKeepFreezesPresentedView(t *testing.T) {
	for _, kitty := range []bool{false, true} {
		p := testPicture(kitty)
		p.opts.Render.KittyMedium = KittyMediumSharedMemory
		drivePicture(t, p, p.SetImage(testPixels(color.RGBA{R: 255, A: 255})))
		front := p.front
		pending := p.SetSize(1, 1)
		job := p.job
		view := p.View()
		messages := drivePicture(t, p, p.Keep())
		drivePicture(t, p, pending)
		if job.ctx.Err() == nil || p.Status().Busy || p.View() != view || view == "" {
			t.Fatal("keep did not cancel replacement and freeze resized front")
		}
		if p.front != nil || p.img != nil || p.job != nil || len(p.retired) != 0 {
			t.Fatal("keep retained producer state")
		}
		if kitty {
			raw := output(messages)
			if strings.Contains(raw, kittyDeleteImage(front.cfg.KittyID)) || strings.Contains(raw, "a=T") != (p.medium == KittyMediumSharedMemory) || strings.Contains(raw, "t=s") {
				t.Fatalf("keep must only retransmit shared fronts without deleting front: %q", raw)
			}
		}
		if p.Close() != nil || p.Keep() != nil || p.SetImage(nil) != nil || p.SetSize(3, 3) != nil || p.SetVisible(false) != nil || p.SetFit(FitContain) != nil || p.SetAnchor(AnchorTop) != nil || p.View() != view {
			t.Fatal("keep not final and idempotent")
		}
	}
}

func TestPictureKeepRejectsAcceptedReplacement(t *testing.T) {
	p := testPicture(true)
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{A: 255})))
	front, view := p.front, p.View()
	prepared := p.SetImage(testPixels(color.RGBA{B: 255, A: 255}))()
	job := p.job
	seq := reflect.ValueOf(p.Update(prepared)())
	// Output was accepted, but its completion has not reached the event loop.
	seq.Index(0).Interface().(tea.Cmd)()
	messages := drivePicture(t, p, p.Keep())
	messages = append(messages, drivePicture(t, p, seq.Index(1).Interface().(tea.Cmd))...)
	raw := output(messages)
	if !strings.Contains(raw, kittyDeleteImage(job.placement.cfg.KittyID)) || strings.Contains(raw, kittyDeleteImage(front.cfg.KittyID)) || p.View() != view {
		t.Fatal("late replacement was kept or retired the retained front")
	}
}

func TestPictureKeepWithoutFront(t *testing.T) {
	p := testPicture(true)
	pending := p.SetImage(testPixels(color.RGBA{A: 255}))
	drivePicture(t, p, p.Keep())
	messages := drivePicture(t, p, pending)
	if p.View() != "" || p.Status().Busy || strings.Contains(output(messages), "a=T") {
		t.Fatal("keep displayed an unpresented image")
	}
}

func TestPictureIgnoresInvalidCellReports(t *testing.T) {
	p := testPicture(true)
	pending := p.SetImage(testPixels(color.RGBA{R: 255, A: 255}))
	job, generation := p.job, p.generation
	for _, size := range []uv.CellSizeEvent{{Width: 0, Height: 0}, {Width: 8, Height: 0}, {Width: -1, Height: 16}} {
		if p.Update(size) != nil || p.job != job || p.generation != generation || p.img == nil || p.Status().Err != nil || job.ctx.Err() != nil {
			t.Fatal("invalid terminal report discarded source")
		}
	}
	drivePicture(t, p, pending)
	if p.View() == "" {
		t.Fatal("initial image stalled")
	}
	drivePicture(t, p, p.Close())
}

func TestKeepDirectFrontNeedsNoTransmission(t *testing.T) {
	for _, stable := range []bool{false, true} {
		p := testPicture(true)
		p.opts.StableID = stable
		drivePicture(t, p, p.SetImage(testPixels(color.RGBA{A: 255})))
		view := p.View()
		if got := output(drivePicture(t, p, p.Keep())); got != "" {
			t.Fatalf("direct keep emitted %q", got)
		}
		if p.View() != view {
			t.Fatal("lost direct view")
		}
	}
}

func TestPictureGlyphResizeShrinkPreservesANSIReset(t *testing.T) {
	p := testPicture(false)
	defer func() { drivePicture(t, p, p.Close()) }()
	drivePicture(t, p, p.SetSize(3, 2))
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{R: 255, G: 64, B: 32, A: 255})))
	// Shrink horizontally while the 3x2 front is still retained before replacement completes.
	_ = p.SetSize(2, 2)
	for i, row := range strings.Split(p.View(), "\n") {
		if !strings.HasSuffix(row, "\x1b[0m") {
			t.Fatalf("row %d missing trailing ANSI reset after resize shrink: %q", i, row)
		}
	}
}

func TestPictureInvalidGeometryPreservesPendingAndHiddenSource(t *testing.T) {
	p := testPicture(true)
	defer func() { drivePicture(t, p, p.Close()) }()
	drivePicture(t, p, p.SetVisible(false))
	drivePicture(t, p, p.SetImage(testPixels(color.RGBA{R: 255, A: 255})))
	// Invalid SetSize / SetTerminal while hidden (front == nil) must not wipe p.img.
	if cmd := p.SetSize(int(^uint(0)>>1), 2); cmd == nil || p.Status().Err == nil || p.img == nil {
		t.Fatal("invalid SetSize wiped hidden source or missed error")
	}
	if cmd := p.SetTerminal(true, 0, 10); cmd == nil || p.Status().Err == nil || p.img == nil {
		t.Fatal("invalid SetTerminal wiped hidden source or missed error")
	}
	drivePicture(t, p, p.SetVisible(true))
	if p.View() == "" {
		t.Fatal("hidden source lost after invalid geometry calls")
	}
}

func FuzzPictureActions(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, true, false, false)
	f.Add([]byte{4, 2, 1, 6, 3, 5, 0, 7, 8, 11, 2, 0}, false, true, true)
	f.Fuzz(func(t *testing.T, ops []byte, stableID, clearOnReplace, hostGeometry bool) {
		if len(ops) > 64 {
			ops = ops[:64]
		}
		sess := NewTerminalSession(TerminalOptions{Kitty: KittyCapabilitySupported, CellPixelWidth: 2, CellPixelHeight: 4})
		defer sess.Close()
		p := New(Options{
			Session:        sess,
			StableID:       stableID,
			ClearOnReplace: clearOnReplace,
			HostGeometry:   hostGeometry,
			RetireDelay:    -1,
		})
		var queue []tea.Cmd
		enqueue := func(cmd tea.Cmd) {
			if cmd != nil {
				queue = append(queue, cmd)
			}
		}
		flushOne := func() {
			if len(queue) == 0 {
				return
			}
			cmd := queue[0]
			queue = queue[1:]
			drivePicture(t, p, cmd)
		}
		imgA := testPixels(color.RGBA{R: 200, G: 10, B: 20, A: 255})
		imgB := testPixels(color.RGBA{R: 10, G: 200, B: 30, A: 255})
		for _, op := range ops {
			switch op % 14 {
			case 0:
				flushOne()
			case 1:
				enqueue(p.SetImage(imgA))
			case 2:
				enqueue(p.SetImageImmutable(imgB))
			case 3:
				enqueue(p.SetImage(nil))
			case 4:
				enqueue(p.SetSize(int(op%5), int((op>>2)%5)))
			case 5:
				enqueue(p.SetTerminal(op&16 != 0, int(op%8)-1, int((op>>2)%8)+1))
			case 6:
				mode := PictureGlyph
				if op&16 != 0 {
					mode = PictureKitty
				}
				enqueue(p.SetMode(mode))
			case 7:
				enqueue(p.SetVisible(op&16 != 0))
			case 8:
				enqueue(p.SetResolution(float64(op%5) * 0.25))
			case 9:
				p.SetViewport(Viewport{X: int(op%5) - 1, Y: int((op>>2)%5) - 1, Width: int(op % 4), Height: int((op >> 3) % 4)})
			case 10:
				p.ResetViewport()
			case 11:
				enqueue(p.Submit(Frame{
					Image:      imgA,
					Cols:       int(op%5) - 1,
					Rows:       int((op>>2)%5) - 1,
					Mode:       PictureMode(op % 3),
					CellWidth:  int(op % 4),
					CellHeight: int((op>>2)%4) + 1,
					Exact:      op&32 != 0,
					Immutable:  op&64 != 0,
				}))
			case 12:
				enqueue(p.SetFit(FitMode(op % 4)))
			case 13:
				enqueue(p.SetAnchor(FitAnchor(op % 6)))
			}
			_ = p.View()
			_ = p.Status()
			_ = p.RasterGeometry()
			_ = p.Fit()
			_ = p.Anchor()
		}
		for len(queue) > 0 {
			flushOne()
		}
		drivePicture(t, p, p.Close())
		if p.Status().Busy {
			t.Fatalf("closed picture remained busy: %+v", p.Status())
		}
	})
}

func TestKeepAndFailureAfterDrainedStableIDBorrowerRetransmitsFront(t *testing.T) {
	imgA := testPixels(color.RGBA{R: 250, G: 10, B: 20, A: 255})
	imgB := testPixels(color.RGBA{R: 10, G: 250, B: 30, A: 255})

	// Helper that commits imgA on a StableID Kitty picture, then prepares imgB
	// and runs step 0 of its sequence (emitting B's APC under the shared ID),
	// returning step 1 so the caller can supersede B before step 1 closes outputDone.
	setupOverwrittenBorrower := func(t *testing.T) (*Picture, string, tea.Cmd) {
		t.Helper()
		p := New(Options{
			StableID:    true,
			RetireDelay: -1,
			Render:      Config{Fit: FitFill, CellPixelWidth: 1, CellPixelHeight: 1, KittyFormat: KittyFormatRGBA},
		})
		p.SetSize(2, 2)
		p.SetTerminal(true, 1, 1)
		frontAPC := output(drivePicture(t, p, p.SetImage(imgA)))
		if frontAPC == "" {
			t.Fatal("expected non-empty front APC for image A")
		}

		prepCmd := p.SetImage(imgB)
		prepMsg := prepCmd()
		seqCmd := p.Update(prepMsg)
		seqSteps := reflect.ValueOf(seqCmd())
		if seqSteps.Kind() != reflect.Slice || seqSteps.Len() != 2 {
			t.Fatalf("expected 2-step sequence from prepared Kitty frame, got %T", seqCmd())
		}
		// Run step 0 (emits B's APC under shared Kitty ID).
		rawMsg := seqSteps.Index(0).Interface().(tea.Cmd)()
		rawStr, _ := rawMsg.(tea.RawMsg).Msg.(string)
		if rawStr == "" || rawStr == frontAPC {
			t.Fatalf("expected borrower B to emit distinct APC, got %q", rawStr)
		}
		if !p.front.lease.overwritten.Load() {
			t.Fatal("expected front lease to record overwritten=true after borrower emitted APC")
		}
		step1 := seqSteps.Index(1).Interface().(tea.Cmd)
		return p, frontAPC, step1
	}

	t.Run("KeepRetransmitsOverwrittenFront", func(t *testing.T) {
		p, frontAPC, step1 := setupOverwrittenBorrower(t)
		// Supersede borrower B after step0 emitted its APC, then drain step1 through Update
		// so B is disposed and p.job.outputDone is nil before Keep() is called.
		_ = p.SetImage(imgB)
		seqMsg := step1() // closes B's outputDone once and returns sequence(delete, pictureReady)
		v := reflect.ValueOf(seqMsg)
		for i := 0; i < v.Len(); i++ {
			if msg := v.Index(i).Interface().(tea.Cmd)(); msg != nil {
				if _, ok := msg.(pictureReady); ok {
					_ = p.Update(msg)
				}
			}
		}
		if p.job == nil || p.job.outputDone != nil {
			t.Fatalf("expected new un-drained job with outputDone == nil after draining borrower B, got job=%+v", p.job)
		}
		keepOut := output(drivePicture(t, p, p.Keep()))
		if !strings.Contains(keepOut, frontAPC) {
			t.Fatalf("Keep() did not retransmit front APC after drained borrower overwrote shared ID; got %q", keepOut)
		}
	})

	t.Run("FailSubmissionRepreparesOverwrittenFront", func(t *testing.T) {
		p, frontAPC, step1 := setupOverwrittenBorrower(t)
		defer func() { drivePicture(t, p, p.Close()) }()
		// A rejected submission triggers failSubmission (cancelling B) while front.lease.overwritten is true.
		errCmd := p.SetImage(image.NewRGBA(image.Rectangle{}))
		msgs := drivePicture(t, p, tea.Batch(errCmd, step1))
		if !strings.Contains(output(msgs), frontAPC) {
			t.Fatalf("rejected submission after overwritten borrower did not re-prepare and retransmit front APC; got %q", output(msgs))
		}
		if p.front.lease.overwritten.Load() {
			t.Fatal("expected re-committed front to clear overwritten flag")
		}
	})
}

func TestFailedSubmissionDoesNotAbandonPendingGeometryChange(t *testing.T) {
	p := testPicture(false)
	defer func() { drivePicture(t, p, p.Close()) }()
	imgA := testPixels(color.RGBA{R: 200, G: 100, B: 50, A: 255})
	drivePicture(t, p, p.SetImage(imgA))
	if p.front.cols != 2 || p.front.rows != 2 {
		t.Fatalf("initial front size = %dx%d, want 2x2", p.front.cols, p.front.rows)
	}

	// Start a resize to 4x3, then submit an invalid image before the resize job finishes.
	sizeCmd := p.SetSize(4, 3)
	errCmd := p.SetImage(image.NewRGBA(image.Rectangle{}))
	drivePicture(t, p, tea.Batch(sizeCmd, errCmd))

	if p.Status().Busy {
		t.Fatalf("expected Busy=false after recovery, got %+v", p.Status())
	}
	if p.front == nil || p.front.cols != 4 || p.front.rows != 3 {
		t.Fatalf("front size after rejected SetImage during pending SetSize = %+v, want 4x3", p.front)
	}
}

func TestRejectedSetImageWhileHiddenPreservesRetainedSource(t *testing.T) {
	p := testPicture(false)
	defer func() { drivePicture(t, p, p.Close()) }()
	imgA := testPixels(color.RGBA{R: 40, G: 180, B: 90, A: 255})
	drivePicture(t, p, p.SetImage(imgA))
	wantView := p.View()
	if wantView == "" {
		t.Fatal("expected non-empty initial view")
	}

	drivePicture(t, p, p.SetVisible(false))
	// Rejecting an invalid image while hidden must not discard the retained source.
	drivePicture(t, p, p.SetImage(image.NewRGBA(image.Rectangle{})))
	drivePicture(t, p, p.SetVisible(true))

	if got := p.View(); got != wantView {
		t.Fatalf("View() after SetVisible(true) = %q, want retained %q", got, wantView)
	}
}

func TestNormalizeConfigRejectsOver24BitKittyID(t *testing.T) {
	maxValid := (1 << 24) - 1
	if got := normalizeConfig(Config{KittyID: maxValid}).KittyID; got != maxValid {
		t.Fatalf("normalizeConfig(max 24-bit ID) = %d, want %d", got, maxValid)
	}
	if got := normalizeConfig(Config{KittyID: 1 << 24}).KittyID; got != DefaultKittyID {
		t.Fatalf("normalizeConfig(1<<24) = %d, want DefaultKittyID (%d)", got, DefaultKittyID)
	}
}

type uncomparableValueImage struct {
	pix    []color.RGBA
	bounds image.Rectangle
}

func (u uncomparableValueImage) ColorModel() color.Model { return color.RGBAModel }
func (u uncomparableValueImage) Bounds() image.Rectangle { return u.bounds }
func (u uncomparableValueImage) At(x, y int) color.Color {
	if !(image.Point{X: x, Y: y}.In(u.bounds)) || len(u.pix) == 0 {
		return color.RGBA{}
	}
	return u.pix[0]
}

func TestFailPreparedJobWithUncomparableValueImagesDoesNotPanic(t *testing.T) {
	p := testPicture(false)
	defer func() { drivePicture(t, p, p.Close()) }()

	imgA := uncomparableValueImage{
		pix:    []color.RGBA{{R: 200, G: 20, B: 20, A: 255}},
		bounds: image.Rect(0, 0, 16, 32),
	}
	imgB := uncomparableValueImage{
		pix:    []color.RGBA{{R: 20, G: 20, B: 200, A: 255}},
		bounds: image.Rect(0, 0, 16, 32),
	}

	drivePicture(t, p, p.SetImageImmutable(imgA))
	if p.front == nil || p.front.cols != 2 || p.front.rows != 2 {
		t.Fatalf("initial front = %+v, want 2x2", p.front)
	}

	// Queue a resize to 4x3 and submit imgB as a value-type image with a slice field.
	_ = p.SetSize(4, 3)
	_ = p.SetImageImmutable(imgB)
	if p.job == nil {
		t.Fatal("expected in-flight job for imgB")
	}

	// Simulate an asynchronous preparation failure for imgB; failPreparedJob must
	// not panic when comparing value-type images and must re-prepare imgA at 4x3.
	recCmd := p.Update(picturePrepared{owner: p, job: p.job, frame: nil})
	drivePicture(t, p, recCmd)

	if p.front == nil || p.front.cols != 4 || p.front.rows != 3 {
		t.Fatalf("front after failPreparedJob recovery = %+v, want 4x3", p.front)
	}
}

type uncomparableColor struct {
	rgba []uint16
}

func (u uncomparableColor) RGBA() (r, g, b, a uint32) {
	return uint32(u.rgba[0]), uint32(u.rgba[1]), uint32(u.rgba[2]), uint32(u.rgba[3])
}

func TestNormalizeConfigConvertsUncomparableBackgroundColor(t *testing.T) {
	bg := uncomparableColor{rgba: []uint16{0xffff, 0, 0, 0xffff}}
	cfg := normalizeConfig(Config{Background: bg})
	if cfg != cfg {
		t.Fatal("normalized Config should be comparable")
	}
	p := New(Options{Render: Config{Background: bg, Fit: FitFill, CellPixelWidth: 1, CellPixelHeight: 1}, RetireDelay: -1})
	defer func() { drivePicture(t, p, p.Close()) }()
	drivePicture(t, p, p.Submit(Frame{
		Image:      testPixels(color.RGBA{R: 200, A: 255}),
		Cols:       2,
		Rows:       2,
		Mode:       PictureGlyph,
		CellWidth:  1,
		CellHeight: 1,
	}))
	if p.frontNeedsPrepare() {
		t.Fatal("frontNeedsPrepare should be false and not panic after Submit with uncomparable Background")
	}
}

func TestFailPreparedJobDoesNotLoopOnOverwrittenFrontResizeFailure(t *testing.T) {
	p := New(Options{
		StableID:    true,
		RetireDelay: -1,
		Render:      Config{Fit: FitFill, CellPixelWidth: 1, CellPixelHeight: 1, KittyFormat: KittyFormatRGBA},
	})
	defer func() { drivePicture(t, p, p.Close()) }()
	p.SetSize(2, 2)
	p.SetTerminal(true, 1, 1)
	imgA := testPixels(color.RGBA{R: 250, A: 255})
	drivePicture(t, p, p.SetImage(imgA))

	// Mark front's lease overwritten as if an earlier borrower emitted step0.
	p.front.lease.overwritten.Store(true)

	// Resize to 4x3 (which schedules p.front's image under a new lease) and
	// simulate a preparation failure for p.front itself (failedImgID == p.front.imgID).
	_ = p.SetSize(4, 3)
	if p.job == nil || p.job.placement.imgID != p.front.imgID {
		t.Fatalf("expected resize job for front image, got %+v", p.job)
	}
	cmd := p.Update(picturePrepared{owner: p, job: p.job, frame: nil})
	if p.job != nil || p.dirty {
		t.Fatalf("failPreparedJob re-scheduled a failing front job in a loop: job=%v dirty=%v", p.job, p.dirty)
	}
	msgs := drivePicture(t, p, cmd)
	errCount := 0
	for _, m := range msgs {
		if _, ok := m.(ErrorMsg); ok {
			errCount++
		}
	}
	if errCount != 1 {
		t.Fatalf("expected single ErrorMsg without retry loop, got %d error messages", errCount)
	}
}

func TestCancelledStableIDBorrowerBeforeStep0ClearsOutputDone(t *testing.T) {
	p := New(Options{
		StableID:    true,
		RetireDelay: -1,
		Render:      Config{Fit: FitFill, CellPixelWidth: 1, CellPixelHeight: 1, KittyFormat: KittyFormatRGBA},
	})
	p.SetSize(2, 2)
	p.SetTerminal(true, 1, 1)
	imgA := testPixels(color.RGBA{R: 250, A: 255})
	imgB := testPixels(color.RGBA{G: 250, A: 255})
	drivePicture(t, p, p.SetImage(imgA))

	// Prepare borrower B and deliver picturePrepared to Update, setting lease.outputDone,
	// then cancel B via a rejected SetImage BEFORE step0 of B's sequence ever runs.
	prepMsg := p.SetImage(imgB)()
	seqCmd := p.Update(prepMsg)
	if p.front.lease.loadOutputDone() == nil {
		t.Fatal("expected non-nil outputDone before borrower cancellation")
	}

	errCmd := p.SetImage(image.NewRGBA(image.Rectangle{}))
	if p.front.lease.loadOutputDone() != nil {
		t.Fatal("expected cancelled borrower before step0 to clear lease.outputDone")
	}
	if p.dirty {
		t.Fatal("expected untouched front not to mark p.dirty on failSubmission")
	}
	msgs := drivePicture(t, p, tea.Batch(seqCmd, errCmd))
	if got := output(msgs); got != "" {
		t.Fatalf("expected no APC output when borrower was cancelled before step0, got %q", got)
	}
	if p.dirty || p.job != nil {
		t.Fatalf("expected idle picture after draining cancelled borrower, got dirty=%v job=%v", p.dirty, p.job)
	}

	// Keep on the untouched direct front must take the fast path and emit no APC.
	if got := output(drivePicture(t, p, p.Keep())); got != "" {
		t.Fatalf("Keep() retransmitted untouched direct front after pre-step0 borrower cancellation: %q", got)
	}
}
