package playback

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"
	picture "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"
)

func solidImage(bounds image.Rectangle, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(bounds)
	draw.Draw(img, bounds, &image.Uniform{C: c}, image.Point{}, draw.Src)
	return img
}

func colorPlayer(t testing.TB, size image.Point, colors ...color.RGBA) *animation.Player {
	t.Helper()
	bounds := image.Rect(0, 0, size.X, size.Y)
	clip := &animation.Clip{Bounds: bounds, LoopCount: -1}
	for _, c := range colors {
		clip.Frames = append(clip.Frames, animation.ClipFrame{
			Image: solidImage(bounds, c),
			Rect:  bounds,
			Delay: 10 * time.Millisecond,
		})
	}
	p, err := animation.NewPlayer(clip)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func driveLayers(t testing.TB, l *Layers, cmd tea.Cmd) {
	t.Helper()
	var msgs []tea.Msg
	driveLayersCollect(t, l, cmd, &msgs)
}

func driveLayersCollect(t testing.TB, l *Layers, cmd tea.Cmd, msgs *[]tea.Msg) {
	t.Helper()
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
			driveLayersCollect(t, l, v.Index(i).Interface().(tea.Cmd), msgs)
		}
		return
	}
	*msgs = append(*msgs, msg)
	driveLayersCollect(t, l, l.Update(msg), msgs)
}

func cellCount(line string) int {
	return strings.Count(line, string(kitty.Placeholder))
}

func idSGR(id int) string {
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", (id>>16)&0xff, (id>>8)&0xff, id&0xff)
}

// Under Kitty graphics a region animates in a cell-aligned overlay window
// without retransmitting the base picture, and its placeholders are spliced
// into the base grid at its cells.
func TestLayersPlayKittyRegionWithoutRetransmittingBase(t *testing.T) {
	red := color.RGBA{R: 255, A: 255}
	green := color.RGBA{G: 255, A: 255}
	bgColor := color.RGBA{R: 20, G: 30, B: 40, A: 255}

	const cols, rows, cw, ch = 10, 6, 8, 16
	base := solidImage(image.Rect(0, 0, cols*cw, rows*ch), bgColor)
	regionRect := image.Rect(16, 16, 48, 48) // cells (2,1)-(6,3)
	player := colorPlayer(t, image.Pt(32, 32), red, green)

	comp, err := animation.NewComposite(context.Background(), base, []animation.Region{
		{Key: "a.gif", Rect: regionRect, Player: player},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	nextID := 100
	session := picture.NewTerminalSession(picture.TerminalOptions{
		Kitty:           picture.KittyCapabilitySupported,
		CellPixelWidth:  cw,
		CellPixelHeight: ch,
	})
	defer session.Close()

	l := NewLayers(comp, Options{
		Wait: func(context.Context, time.Duration) bool { return true },
		Picture: picture.Options{
			Session:     session,
			NextID:      func() (int, error) { nextID++; return nextID, nil },
			RetireDelay: -1,
		},
	})
	defer l.Close()

	l.Pause(true)
	l.SetSize(cols, rows)
	l.SetTerminal(true, cw, ch)
	driveLayers(t, l, l.Init())

	layers := l.Layers()
	if len(layers) != 1 {
		t.Fatalf("got %d overlays, want 1", len(layers))
	}
	layer := layers[0]
	wantCells := image.Rect(regionRect.Min.X/cw, regionRect.Min.Y/ch, (regionRect.Max.X+cw-1)/cw, (regionRect.Max.Y+ch-1)/ch)
	if layer.Cells != wantCells {
		t.Fatalf("overlay cells = %v, want %v", layer.Cells, wantCells)
	}
	if layer.Window != regionRect {
		t.Fatalf("overlay window = %v, want %v", layer.Window, regionRect)
	}
	if layer.Status.Presented == nil || layer.Status.Source.Image().Bounds().Size() != image.Pt(wantCells.Dx()*cw, wantCells.Dy()*ch) {
		t.Fatalf("overlay not presented at window size: %+v", layer.Status)
	}

	lines := strings.Split(l.View(), "\n")
	if len(lines) != rows {
		t.Fatalf("got %d view lines, want %d", len(lines), rows)
	}
	baseSGR, overlaySGR := idSGR(101), idSGR(102)
	for y, line := range lines {
		if n := cellCount(line); n != cols {
			t.Fatalf("row %d has %d cells, want %d", y, n, cols)
		}
		inside := y >= wantCells.Min.Y && y < wantCells.Max.Y
		if strings.Contains(line, overlaySGR) != inside {
			t.Fatalf("row %d: overlay SGR present=%v, want %v", y, !inside, inside)
		}
		if !strings.Contains(line, baseSGR) {
			t.Fatalf("row %d lost base placeholders", y)
		}
	}

	// Verify initial snapshot has frame 1 (red) inside regionRect and bgColor outside.
	snap1 := l.Snapshot()
	if snap1 == nil || snap1.Bounds() != base.Bounds() {
		t.Fatalf("unexpected initial snapshot: %v", snap1)
	}
	if got := color.RGBAModel.Convert(snap1.At(20, 20)); got != red {
		t.Fatalf("snapshot region pixel = %v, want %v", got, red)
	}
	if got := color.RGBAModel.Convert(snap1.At(0, 0)); got != bgColor {
		t.Fatalf("snapshot base pixel = %v, want %v", got, bgColor)
	}

	// Unpause to advance the overlay to frame 2 (green). The base picture must not re-prepare.
	baseGenBefore := l.Status().Picture.Generation
	idsBefore := nextID
	driveLayers(t, l, l.Pause(false))

	if got := l.Status().Picture.Generation; got != baseGenBefore {
		t.Fatalf("base picture retransmitted during region animation: gen %d -> %d", baseGenBefore, got)
	}
	if nextID != idsBefore {
		t.Fatalf("stable-ID overlay allocated extra image IDs: %d -> %d", idsBefore, nextID)
	}
	if !l.Status().Finished {
		t.Fatalf("expected finite animation to finish: %+v", l.Status())
	}

	snap2 := l.Snapshot()
	if got := color.RGBAModel.Convert(snap2.At(20, 20)); got != green {
		t.Fatalf("snapshot after advance = %v, want %v", got, green)
	}
	// Earlier snapshot must remain immutable and still show frame 1.
	if got := color.RGBAModel.Convert(snap1.At(20, 20)); got != red {
		t.Fatalf("earlier snapshot mutated: %v, want %v", got, red)
	}
}

func TestSpliceCellsSingleAndMultiple(t *testing.T) {
	cell := func(x int) string {
		return string(kitty.Placeholder) + string(kitty.Diacritic(0)) + string(kitty.Diacritic(x))
	}
	row := func(id, n int) string {
		var b strings.Builder
		b.WriteString(idSGR(id))
		for x := 0; x < n; x++ {
			b.WriteString(cell(x))
		}
		b.WriteString("\x1b[39m")
		return b.String()
	}

	base, over := row(1, 6), row(2, 2)
	got := spliceCells(base, 2, 4, over)
	want := idSGR(1) + cell(0) + cell(1) + "\x1b[39m" + over + idSGR(1) + cell(4) + cell(5) + "\x1b[39m"
	if got != want {
		t.Fatalf("splice:\n%q\nwant\n%q", got, want)
	}
	if spliceCells(base, 5, 7, over) != base || spliceCells("text", 0, 1, over) != "text" {
		t.Fatal("an impossible splice changed the row")
	}

	// Left-edge splice followed by right-edge splice on the same row must preserve base SGR in between.
	overLeft, overRight := row(2, 2), row(3, 2)
	both := spliceCells(spliceCells(base, 0, 2, overLeft), 4, 6, overRight)
	wantBoth := overLeft + idSGR(1) + cell(2) + cell(3) + "\x1b[39m" + overRight
	if both != wantBoth {
		t.Fatalf("multi-splice:\n%q\nwant\n%q", both, wantBoth)
	}

	// Adjacent splices covering the whole row.
	overMid := row(4, 2)
	full := spliceCells(both, 2, 4, overMid)
	wantFull := overLeft + overMid + overRight
	if full != wantFull {
		t.Fatalf("adjacent full splice:\n%q\nwant\n%q", full, wantFull)
	}
}

// Regions sharing cells—including transitive overlaps and overlaps created by
// bounding-box union—merge into one overlay at the finer resolution.
func TestLayerPlanningMergesOverlappingAndTransitiveWindows(t *testing.T) {
	const cols, rows, cw, ch = 12, 8, 10, 20
	base := image.NewRGBA(image.Rect(0, 0, cols*cw, rows*ch))
	pSmall := colorPlayer(t, image.Pt(10, 20), color.RGBA{R: 255, A: 255}) // native 10x20
	pMed := colorPlayer(t, image.Pt(15, 30), color.RGBA{G: 255, A: 255})   // native 15x30
	pFine := colorPlayer(t, image.Pt(18, 36), color.RGBA{B: 255, A: 255})  // native 18x36

	// Group 1 (transitive + union expansion):
	// r1: cells (0,0)-(2,3), drawn 20x60 -> factor = min(1, 10/20, 20/60) = 1/3
	// r2: cells (2,2)-(4,4), drawn 20x40 -> factor = min(1, 18/20, 36/40) = 0.9 (does not overlap r1 directly)
	// r3: cells (1,0)-(3,1), drawn 20x20 -> factor = min(1, 15/20, 30/20) = 0.75 (overlaps r1; union (0,0)-(3,3) then overlaps r2!)
	// Separate Group 2 on the same rows:
	// r4: cells (8,1)-(10,3), drawn 20x40 -> factor = 0.5
	comp, err := animation.NewComposite(context.Background(), base, []animation.Region{
		{Key: "r1", Rect: image.Rect(0, 0, 20, 60), Player: pSmall},
		{Key: "r2", Rect: image.Rect(20, 40, 40, 80), Player: pFine},
		{Key: "r3", Rect: image.Rect(10, 0, 30, 20), Player: pMed},
		{Key: "r4", Rect: image.Rect(80, 20, 100, 60), Player: pSmall},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	groups := planLayers(comp, cols, rows, cw, ch)
	if len(groups) != 2 {
		t.Fatalf("got %d planned groups, want 2: %+v", len(groups), groups)
	}
	g0, g1 := groups[0], groups[1]
	if g0.cells != image.Rect(0, 0, 4, 4) {
		t.Fatalf("merged cells = %v, want (0,0)-(4,4)", g0.cells)
	}
	if g0.resolution != 0.9 {
		t.Fatalf("merged resolution = %v, want 0.9", g0.resolution)
	}
	if g1.cells != image.Rect(8, 1, 10, 3) || g1.resolution != 0.5 {
		t.Fatalf("second group = %+v, want cells (8,1)-(10,3) resolution 0.5", g1)
	}
}

// Enlarged animations prepare at their native resolution factor and snap to
// their native pixel size inside the overlay window.
func TestReducedResolutionOverlayRetainsPlacement(t *testing.T) {
	const cols, rows, cw, ch = 120, 29, 20, 40
	base := solidImage(image.Rect(0, 0, cols*cw, rows*ch), color.RGBA{R: 10, G: 10, B: 10, A: 255})
	// Native 40x20 GIF drawn at 1.2x (48x24) as in Gloss's 2400px measure test.
	player := colorPlayer(t, image.Pt(40, 20), color.RGBA{R: 255, A: 255}, color.RGBA{G: 255, A: 255})
	regionRect := image.Rect(60, 80, 60+48, 80+24) // cells (3,2)-(6,3)

	comp, err := animation.NewComposite(context.Background(), base, []animation.Region{
		{Key: "a.gif", Rect: regionRect, Player: player},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	l := NewLayers(comp, Options{Picture: picture.Options{RetireDelay: -1}})
	defer l.Close()
	l.Pause(true)
	l.SetSize(cols, rows)
	l.SetTerminal(true, cw, ch)
	driveLayers(t, l, l.Init())

	layers := l.Layers()
	if len(layers) != 1 {
		t.Fatalf("got %d overlays, want 1", len(layers))
	}
	o := layers[0]
	if o.Resolution >= 1 || o.Resolution < 0.8 {
		t.Fatalf("resolution factor = %v, want ~1/1.2", o.Resolution)
	}
	if o.Cells != image.Rect(3, 2, 6, 3) {
		t.Fatalf("overlay cells = %v, want (3,2)-(6,3)", o.Cells)
	}
	winComp, ok := o.Status.Source.(*animation.Composite)
	if !ok || winComp == nil {
		t.Fatalf("overlay source is not a Composite: %T", o.Status.Source)
	}
	regions := winComp.Regions()
	if len(regions) != 1 || regions[0].Rect.Size() != image.Pt(40, 20) {
		t.Fatalf("region not snapped to native 40x20 size: %+v", regions)
	}
	if o.Status.Presented == nil {
		t.Fatal("overlay not presented")
	}
	snap := l.Snapshot()
	if snap == nil || snap.Bounds() != base.Bounds() {
		t.Fatalf("snapshot bounds = %v, want %v", snap.Bounds(), base.Bounds())
	}
}

// Suspending playback activation during scrolling prepares only the base page;
// activating after settle constructs overlays at preserved positions without
// re-preparing the base.
func TestOverlaysWaitForActivationAndPreservePositionsAcrossReplacement(t *testing.T) {
	red := color.RGBA{R: 255, A: 255}
	green := color.RGBA{G: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}

	const cols, rows, cw, ch = 10, 8, 8, 16
	base1 := solidImage(image.Rect(0, 0, cols*cw, rows*ch), color.RGBA{R: 30, G: 30, B: 30, A: 255})
	player := colorPlayer(t, image.Pt(16, 16), red, green, blue)

	comp1, err := animation.NewComposite(context.Background(), base1, []animation.Region{
		{Key: "a.gif", Rect: image.Rect(16, 64, 32, 80), Player: player}, // row 4
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	l := NewLayers(comp1, Options{
		Wait:    func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil },
		Picture: picture.Options{RetireDelay: -1},
	})
	defer l.Close()

	l.Pause(true)
	l.SetSize(cols, rows)
	l.SetTerminal(true, cw, ch)
	driveLayers(t, l, l.Init())

	// Unpause until the overlay commits frame 2 (green), then pause before frame 3.
	var step func(tea.Cmd)
	step = func(cmd tea.Cmd) {
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
				step(v.Index(i).Interface().(tea.Cmd))
			}
			return
		}
		next := l.Update(msg)
		if !l.Status().Paused && color.RGBAModel.Convert(l.Snapshot().At(20, 70)) == green {
			l.Pause(true)
		}
		step(next)
	}
	step(l.Pause(false))

	if got := color.RGBAModel.Convert(l.Snapshot().At(20, 70)); got != green {
		t.Fatalf("overlay did not advance to frame 2 (green): got %v", got)
	}

	// Simulate scrolling with settle debounce: suspend activation and submit a new
	// viewport where the region moved up to row 1.
	base2 := solidImage(image.Rect(0, 0, cols*cw, rows*ch), color.RGBA{R: 50, G: 50, B: 50, A: 255})
	comp2, err := animation.NewComposite(context.Background(), base2, []animation.Region{
		{Key: "a.gif", Rect: image.Rect(16, 16, 32, 32), Player: player}, // row 1
	}, l.Positions())
	if err != nil {
		t.Fatal(err)
	}

	driveLayers(t, l, tea.Batch(l.SetActive(false), l.SetComposite(comp2)))
	if len(l.Layers()) != 0 {
		t.Fatalf("expected 0 overlays while inactive during scroll, got %d", len(l.Layers()))
	}
	// Base picture alone already shows the carried frame 2 (green) at the new position.
	if got := color.RGBAModel.Convert(l.Snapshot().At(20, 20)); got != green {
		t.Fatalf("carried base frame at scrolled position = %v, want %v", got, green)
	}

	// Settle timer fires: SetActive(true) builds the overlay without re-preparing the base.
	baseGenBeforeSettle := l.Status().Picture.Generation
	settledCmd := l.SetActive(true)
	if len(l.Layers()) != 1 || settledCmd == nil {
		t.Fatalf("overlays after SetActive(true) = %d, cmd = %v", len(l.Layers()), settledCmd != nil)
	}
	if l.Layers()[0].Cells != image.Rect(2, 1, 4, 2) {
		t.Fatalf("settled overlay cells = %v, want (2,1)-(4,2)", l.Layers()[0].Cells)
	}
	driveLayers(t, l, settledCmd)
	if got := l.Status().Picture.Generation; got != baseGenBeforeSettle {
		t.Fatalf("SetActive(true) re-prepared base picture: gen %d -> %d", baseGenBeforeSettle, got)
	}
	if got := color.RGBAModel.Convert(l.Snapshot().At(20, 20)); got != green {
		t.Fatalf("settled snapshot pixel = %v, want %v", got, green)
	}

	// Also verify SetComposite carries positions even when caller did not pass l.Positions() to NewComposite.
	comp3, err := animation.NewComposite(context.Background(), base2, []animation.Region{
		{Key: "a.gif", Rect: image.Rect(32, 32, 48, 48), Player: player}, // row 2, built with initial player
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	driveLayers(t, l, l.SetComposite(comp3))
	if got := color.RGBAModel.Convert(l.Snapshot().At(36, 36)); got != green {
		t.Fatalf("SetComposite did not preserve position: got %v, want %v", got, green)
	}
}

// Pending or stale frames cannot overwrite newer content, and Snapshot never
// silently exports an unpresented replacement or unpresented overlay frame.
func TestPendingReplacementAndStaleResultsDoNotOverwritePresentedState(t *testing.T) {
	red := color.RGBA{R: 255, A: 255}
	green := color.RGBA{G: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}

	const cols, rows, cw, ch = 6, 4, 8, 16
	base1 := solidImage(image.Rect(0, 0, cols*cw, rows*ch), color.RGBA{R: 10, A: 255})
	player1 := colorPlayer(t, image.Pt(16, 16), red, green)
	comp1, err := animation.NewComposite(context.Background(), base1, []animation.Region{
		{Key: "k1", Rect: image.Rect(0, 0, 16, 16), Player: player1},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	l := NewLayers(comp1, Options{
		Wait:    func(context.Context, time.Duration) bool { return true },
		Picture: picture.Options{RetireDelay: -1},
	})
	defer l.Close()
	l.Pause(true)
	l.SetSize(cols, rows)
	l.SetTerminal(true, cw, ch)

	// Before Init/commit, Snapshot is nil.
	if l.Snapshot() != nil {
		t.Fatal("snapshot non-nil before first commit")
	}
	driveLayers(t, l, l.Init())
	if got := color.RGBAModel.Convert(l.Snapshot().At(4, 4)); got != red {
		t.Fatalf("initial snapshot = %v, want red", got)
	}

	// Capture an in-flight advance on overlay 1 (to green) and an in-flight Restart,
	// then replace with comp2 (blue) before those stale commands return.
	oldOverlay := l.overlays[0].ctrl
	staleAdvanceCmd := oldOverlay.Pause(false)
	staleRestartCmd := l.Restart()

	base2 := solidImage(image.Rect(0, 0, cols*cw, rows*ch), color.RGBA{B: 40, A: 255})
	player2 := colorPlayer(t, image.Pt(16, 16), blue, red)
	comp2, err := animation.NewComposite(context.Background(), base2, []animation.Region{
		{Key: "k2", Rect: image.Rect(0, 0, 16, 16), Player: player2},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	l.Pause(true)
	replaceCmd := l.SetComposite(comp2)

	// While comp2's base replacement is still pending (replaceCmd not yet driven),
	// Snapshot must return comp1's last committed snapshot (red), NOT comp2 (blue).
	pendingSnap := l.Snapshot()
	if pendingSnap == nil {
		t.Fatal("snapshot nil while replacement is pending")
	}
	if got := color.RGBAModel.Convert(pendingSnap.At(4, 4)); got != red {
		t.Fatalf("pending replacement exported unpresented state: got %v, want red", got)
	}

	// Delivering the stale overlay advance and stale restart must be ignored.
	if staleAdvanceCmd != nil {
		driveLayers(t, l, l.Update(staleAdvanceCmd()))
	}
	if staleRestartCmd != nil {
		driveLayers(t, l, l.Update(staleRestartCmd()))
	}

	// Now drive comp2's replacement to completion.
	driveLayers(t, l, replaceCmd)
	if got := color.RGBAModel.Convert(l.Snapshot().At(4, 4)); got != blue {
		t.Fatalf("committed replacement snapshot = %v, want blue", got)
	}
}

// Clipped/offscreen regions and nonzero base image origins are planned and
// composited in the base image's coordinate system.
func TestClippedRegionsAndNonzeroOrigin(t *testing.T) {
	const cols, rows, cw, ch = 8, 4, 10, 20
	origin := image.Pt(50, 100)
	bounds := image.Rect(origin.X, origin.Y, origin.X+cols*cw, origin.Y+rows*ch)
	base := solidImage(bounds, color.RGBA{R: 20, G: 20, B: 20, A: 255})

	red := color.RGBA{R: 255, A: 255}
	green := color.RGBA{G: 255, A: 255}
	player := colorPlayer(t, image.Pt(20, 20), red, green)

	// Region 1 partially off top-left of bounds: (40, 90)-(70, 130) -> intersects (50, 100)-(70, 130), cells (0,0)-(2,2).
	// Region 2 completely offscreen: (200, 300)-(220, 320) -> ignored.
	comp, err := animation.NewComposite(context.Background(), base, []animation.Region{
		{Key: "clipped", Rect: image.Rect(40, 90, 70, 130), Player: player},
		{Key: "offscreen", Rect: image.Rect(200, 300, 220, 320), Player: player},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	l := NewLayers(comp, Options{
		Wait:    func(context.Context, time.Duration) bool { return true },
		Picture: picture.Options{RetireDelay: -1},
	})
	defer l.Close()
	l.SetSize(cols, rows)
	l.SetTerminal(true, cw, ch)
	driveLayers(t, l, l.Init())

	layers := l.Layers()
	if len(layers) != 1 {
		t.Fatalf("got %d overlays, want 1 (offscreen region should be skipped)", len(layers))
	}
	if layers[0].Cells != image.Rect(0, 0, 2, 2) {
		t.Fatalf("clipped cells = %v, want (0,0)-(2,2)", layers[0].Cells)
	}
	wantWindow := image.Rect(origin.X, origin.Y, origin.X+2*cw, origin.Y+2*ch)
	if layers[0].Window != wantWindow {
		t.Fatalf("clipped window = %v, want %v", layers[0].Window, wantWindow)
	}

	snap := l.Snapshot()
	if snap == nil || snap.Bounds() != bounds {
		t.Fatalf("snapshot bounds = %v, want %v", snap.Bounds(), bounds)
	}
	if got := color.RGBAModel.Convert(snap.At(origin.X+5, origin.Y+5)); got != green {
		t.Fatalf("snapshot clipped region pixel = %v, want green", got)
	}
}

// Glyph fallback animates the flattened composite without Kitty overlays, and
// switching between Glyph and Kitty preserves playback positions and snapshots.
func TestGlyphFallbackAndModeSwitching(t *testing.T) {
	red := color.RGBA{R: 255, A: 255}
	green := color.RGBA{G: 255, A: 255}
	base := solidImage(image.Rect(0, 0, 32, 32), color.RGBA{R: 10, G: 10, B: 10, A: 255})
	player := colorPlayer(t, image.Pt(16, 16), red, green)

	comp, err := animation.NewComposite(context.Background(), base, []animation.Region{
		{Key: "a.gif", Rect: image.Rect(0, 0, 16, 16), Player: player},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	l := NewLayers(comp, Options{
		Wait:    func(context.Context, time.Duration) bool { return true },
		Picture: picture.Options{RetireDelay: -1},
	})
	defer l.Close()
	l.SetSize(4, 2)
	driveLayers(t, l, l.Init())

	if l.Mode() != picture.PictureGlyph {
		t.Fatalf("mode = %v, want PictureGlyph", l.Mode())
	}
	if len(l.Layers()) != 0 {
		t.Fatalf("glyph mode built %d overlays, want 0", len(l.Layers()))
	}
	if !l.Status().Finished || l.View() == "" || strings.Contains(l.View(), string(kitty.Placeholder)) {
		t.Fatalf("unexpected glyph status/view: %+v", l.Status())
	}
	if got := color.RGBAModel.Convert(l.Snapshot().At(4, 4)); got != green {
		t.Fatalf("glyph snapshot pixel = %v, want green", got)
	}

	// Switch to Kitty mode: position (frame 2 green) is preserved and 1 overlay is built.
	driveLayers(t, l, l.SetTerminal(true, 8, 16))
	if len(l.Layers()) != 1 || !strings.Contains(l.View(), string(kitty.Placeholder)) {
		t.Fatalf("switch to Kitty failed: layers=%d", len(l.Layers()))
	}
	if got := color.RGBAModel.Convert(l.Snapshot().At(4, 4)); got != green {
		t.Fatalf("Kitty snapshot after mode switch = %v, want green", got)
	}

	// Test Viewport clipping in Kitty and Glyph modes.
	l.SetViewport(picture.Viewport{X: 0, Y: 0, Width: 2, Height: 1})
	clippedKitty := l.View()
	if strings.Contains(clippedKitty, "\n") || cellCount(clippedKitty) != 2 {
		t.Fatalf("unexpected Kitty viewport view: %q", clippedKitty)
	}
	driveLayers(t, l, l.SetTerminal(false, 8, 16))
	clippedGlyph := l.View()
	if strings.Contains(clippedGlyph, "\n") || clippedGlyph == "" {
		t.Fatalf("unexpected Glyph viewport view: %q", clippedGlyph)
	}
	l.ResetViewport()
	if len(strings.Split(l.View(), "\n")) != 2 {
		t.Fatalf("ResetViewport did not restore full rows")
	}
}

// Close and Keep handle the base and every overlay without closing the host-owned
// TerminalSession.
func TestCloseAndKeepHandleEveryLayerWithoutClosingSession(t *testing.T) {
	const cols, rows, cw, ch = 8, 4, 8, 16
	base := solidImage(image.Rect(0, 0, cols*cw, rows*ch), color.RGBA{R: 15, G: 25, B: 35, A: 255})
	p1 := colorPlayer(t, image.Pt(16, 16), color.RGBA{R: 255, A: 255}, color.RGBA{G: 255, A: 255})
	p2 := colorPlayer(t, image.Pt(16, 16), color.RGBA{B: 255, A: 255}, color.RGBA{R: 255, G: 255, A: 255})

	comp, err := animation.NewComposite(context.Background(), base, []animation.Region{
		{Key: "left", Rect: image.Rect(0, 0, 16, 16), Player: p1},
		{Key: "right", Rect: image.Rect(32, 16, 48, 32), Player: p2},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	session := picture.NewTerminalSession(picture.TerminalOptions{
		Kitty:           picture.KittyCapabilitySupported,
		CellPixelWidth:  cw,
		CellPixelHeight: ch,
	})
	defer session.Close()

	// 1. Test Close deletes base + both overlays and leaves session usable.
	nextID := 200
	l1 := NewLayers(comp, Options{
		Picture: picture.Options{
			Session:     session,
			NextID:      func() (int, error) { nextID++; return nextID, nil },
			RetireDelay: -1,
		},
	})
	l1.Pause(true)
	l1.SetSize(cols, rows)
	l1.SetTerminal(true, cw, ch)
	driveLayers(t, l1, l1.Init())

	if len(l1.Layers()) != 2 {
		t.Fatalf("got %d overlays, want 2", len(l1.Layers()))
	}
	var closeMsgs []tea.Msg
	driveLayersCollect(t, l1, l1.Close(), &closeMsgs)
	var rawDeletes string
	for _, m := range closeMsgs {
		if raw, ok := m.(tea.RawMsg); ok {
			if s, ok := raw.Msg.(string); ok {
				rawDeletes += s
			}
		}
	}
	for _, id := range []int{201, 202, 203} {
		wantDel := fmt.Sprintf("i=%d", id)
		if !strings.Contains(rawDeletes, wantDel) {
			t.Fatalf("Close did not delete Kitty ID %d in %q", id, rawDeletes)
		}
	}
	if l1.Snapshot() != nil || l1.View() != "" {
		t.Fatal("Close retained snapshot or view")
	}

	// Verify session is still open by reacting to a CellSizeEvent on a second Layers instance.
	l2 := NewLayers(comp, Options{
		Picture: picture.Options{
			Session:     session,
			NextID:      func() (int, error) { nextID++; return nextID, nil },
			RetireDelay: -1,
		},
	})
	l2.Pause(true)
	l2.SetSize(cols, rows)
	l2.SetMode(picture.PictureKitty)
	driveLayers(t, l2, l2.Init())

	driveLayers(t, l2, l2.Update(uv.CellSizeEvent{Width: 10, Height: 20}))
	if w, h := l2.CellPixelSize(); w != 10 || h != 20 {
		t.Fatalf("session did not forward cell size after l1.Close(): got %dx%d", w, h)
	}

	// 2. Test Keep preserves View and Snapshot, and subsequent Close is a no-op.
	keptView := l2.View()
	keptSnap := l2.Snapshot()
	if keptView == "" || keptSnap == nil {
		t.Fatal("empty view or snapshot before Keep")
	}
	var keepMsgs []tea.Msg
	driveLayersCollect(t, l2, l2.Keep(), &keepMsgs)
	if l2.View() != keptView || l2.Snapshot() != keptSnap {
		t.Fatal("Keep did not preserve View or Snapshot")
	}
	if l2.Close() != nil || l2.View() != keptView || l2.Snapshot() != keptSnap {
		t.Fatal("Close after Keep was not a no-op")
	}
}

// Until an overlay is presented, View retains the base picture's placeholders
// and Snapshot retains the base pixels; an uncommitted next frame on a presented
// overlay never leaks into Snapshot before its Picture commits.
func TestUnpresentedOverlayRetainsBasePixelsAndPlaceholders(t *testing.T) {
	red := color.RGBA{R: 255, A: 255}
	green := color.RGBA{G: 255, A: 255}
	const cols, rows, cw, ch = 4, 2, 8, 16
	base := solidImage(image.Rect(0, 0, cols*cw, rows*ch), color.RGBA{B: 50, A: 255})
	player := colorPlayer(t, image.Pt(16, 16), red, green)

	comp, err := animation.NewComposite(context.Background(), base, []animation.Region{
		{Key: "a.gif", Rect: image.Rect(0, 0, 16, 16), Player: player},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	nextID := 300
	l := NewLayers(comp, Options{
		Wait: func(context.Context, time.Duration) bool { return true },
		Picture: picture.Options{
			NextID:      func() (int, error) { nextID++; return nextID, nil },
			RetireDelay: -1,
		},
	})
	defer l.Close()
	l.Pause(true)
	l.SetSize(cols, rows)
	l.SetTerminal(true, cw, ch)

	// Start with active=false so only the base picture is prepared and presented.
	l.SetActive(false)
	driveLayers(t, l, l.Init())

	baseSGR := idSGR(301)
	if !strings.Contains(l.View(), baseSGR) {
		t.Fatalf("expected base SGR in view: %q", l.View())
	}

	// Activate overlay without driving its preparation command yet: overlay is unpresented.
	activateCmd := l.SetActive(true)
	overlaySGR := idSGR(302)
	if len(l.Layers()) != 1 || l.Layers()[0].Status.Presented != nil {
		t.Fatalf("overlay should be pending before command runs: %+v", l.Layers())
	}
	if strings.Contains(l.View(), overlaySGR) {
		t.Fatal("unpresented overlay placeholders were spliced into View")
	}
	if got := color.RGBAModel.Convert(l.Snapshot().At(4, 4)); got != red {
		t.Fatalf("snapshot while overlay is unpresented = %v, want base red", got)
	}

	// Drive initial overlay frame (frame 1 red) to presentation.
	driveLayers(t, l, activateCmd)
	if !strings.Contains(l.View(), overlaySGR) {
		t.Fatal("presented overlay placeholders missing from View")
	}

	// Now advance overlay's source to frame 2 (green) WITHOUT driving Picture preparation yet.
	ov := l.overlays[0].ctrl
	tickCmd := ov.Pause(false)
	if tickCmd == nil {
		t.Fatal("expected tick command")
	}
	prepCmd := l.Update(tickCmd()) // runs result -> submits frame 2 to Picture, not yet presented
	l.Pause(true)
	if ov.Status().Presented == ov.Status().Source {
		t.Fatal("frame 2 should not be presented before its picture preparation runs")
	}
	// Snapshot must still show presented frame 1 (red), not pending frame 2 (green).
	if got := color.RGBAModel.Convert(l.Snapshot().At(4, 4)); got != red {
		t.Fatalf("snapshot exported uncommitted overlay frame: got %v, want red", got)
	}

	// Driving prepCmd commits frame 2 (green).
	driveLayers(t, l, prepCmd)
	if got := color.RGBAModel.Convert(l.Snapshot().At(4, 4)); got != green {
		t.Fatalf("snapshot after frame 2 commit = %v, want green", got)
	}
}

// Region positions survive scrolling completely offscreen and returning later,
// replaced clips start at frame 1, and Restart resets all regions.
func TestOffscreenReturnReloadAndRestart(t *testing.T) {
	red := color.RGBA{R: 255, A: 255}
	green := color.RGBA{G: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}

	const cols, rows, cw, ch = 6, 4, 8, 16
	base := solidImage(image.Rect(0, 0, cols*cw, rows*ch), color.RGBA{R: 10, A: 255})
	player := colorPlayer(t, image.Pt(16, 16), red, green)

	comp1, err := animation.NewComposite(context.Background(), base, []animation.Region{
		{Key: "a.gif", Rect: image.Rect(0, 0, 16, 16), Player: player},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	l := NewLayers(comp1, Options{
		Wait:    func(context.Context, time.Duration) bool { return true },
		Picture: picture.Options{RetireDelay: -1},
	})
	defer l.Close()
	l.SetSize(cols, rows)
	l.SetTerminal(true, cw, ch)
	driveLayers(t, l, l.Init())

	// Overlay has finished at frame 2 (green).
	if got := color.RGBAModel.Convert(l.Snapshot().At(4, 4)); got != green {
		t.Fatalf("initial run did not reach green: %v", got)
	}
	l.Pause(true)

	// Scroll to a viewport with no animated regions at all (region "a.gif" is absent).
	compEmpty, err := animation.NewComposite(context.Background(), base, nil, l.Positions())
	if err != nil {
		t.Fatal(err)
	}
	driveLayers(t, l, l.SetComposite(compEmpty))
	if len(l.Layers()) != 0 {
		t.Fatalf("expected 0 overlays on empty viewport, got %d", len(l.Layers()))
	}

	// Scroll back to a viewport containing "a.gif": l.Positions() still remembers "a.gif" at frame 2 (green).
	compReturn, err := animation.NewComposite(context.Background(), base, []animation.Region{
		{Key: "a.gif", Rect: image.Rect(16, 16, 32, 32), Player: player},
	}, l.Positions())
	if err != nil {
		t.Fatal(err)
	}
	driveLayers(t, l, l.SetComposite(compReturn))
	if got := color.RGBAModel.Convert(l.Snapshot().At(20, 20)); got != green {
		t.Fatalf("returning region lost frame 2 (green): got %v", got)
	}

	// Restart resets all regions back to frame 1 (red) while preserving Pause(true).
	driveLayers(t, l, l.Restart())
	if !l.Status().Paused {
		t.Fatal("Restart did not preserve paused state")
	}
	if got := color.RGBAModel.Convert(l.Snapshot().At(20, 20)); got != red {
		t.Fatalf("snapshot after Restart = %v, want red", got)
	}

	// Advance to green again, then replace "a.gif" with a new clip identity (blue -> red): must start at blue.
	driveLayers(t, l, l.Pause(false))
	l.Pause(true)
	reloadedPlayer := colorPlayer(t, image.Pt(16, 16), blue, red)
	compReloaded, err := animation.NewComposite(context.Background(), base, []animation.Region{
		{Key: "a.gif", Rect: image.Rect(16, 16, 32, 32), Player: reloadedPlayer},
	}, l.Positions())
	if err != nil {
		t.Fatal(err)
	}
	driveLayers(t, l, l.SetComposite(compReloaded))
	if got := color.RGBAModel.Convert(l.Snapshot().At(20, 20)); got != blue {
		t.Fatalf("reloaded clip did not start at frame 1 (blue): got %v", got)
	}
}

func TestStillImageAndControlsOnLayers(t *testing.T) {
	l := NewLayers(nil, Options{Picture: picture.Options{RetireDelay: -1}})
	defer l.Close()
	l.SetSize(4, 2)
	l.SetTerminal(true, 8, 16)
	driveLayers(t, l, l.Init())

	cyan := color.RGBA{G: 200, B: 200, A: 255}
	still := solidImage(image.Rect(0, 0, 32, 32), cyan)
	driveLayers(t, l, l.SetImage(still))
	// Mutating caller's buffer after SetImage must not affect Snapshot.
	still.Set(0, 0, color.RGBA{R: 255, A: 255})
	if got := color.RGBAModel.Convert(l.Snapshot().At(0, 0)); got != cyan {
		t.Fatalf("SetImage did not copy pixels: got %v, want %v", got, cyan)
	}

	driveLayers(t, l, l.SetBackground(color.Black))
	driveLayers(t, l, l.SetFit(picture.FitFill))
	driveLayers(t, l, l.SetAnchor(picture.AnchorTop))
	if l.Fit() != picture.FitFill || l.Anchor() != picture.AnchorTop {
		t.Fatalf("fit=%v anchor=%v", l.Fit(), l.Anchor())
	}
	driveLayers(t, l, l.SetRate(2))
	if l.Status().Rate != 2 {
		t.Fatalf("rate = %v, want 2", l.Status().Rate)
	}

	driveLayers(t, l, l.SetVisible(false))
	if l.View() != "" || l.Snapshot() != nil {
		t.Fatal("hidden layers returned view or snapshot")
	}
	driveLayers(t, l, l.SetVisible(true))
	if l.View() == "" || l.Snapshot() == nil {
		t.Fatal("re-shown layers did not restore view and snapshot")
	}

	driveLayers(t, l, l.SetImage(nil))
	if l.Snapshot() != nil || l.View() != "" {
		t.Fatal("cleared layers retained view or snapshot")
	}
}

func FuzzLayersActions(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13})
	f.Add([]byte{7, 6, 1, 11, 0, 4, 5, 8, 9, 10, 2, 3, 12, 0})
	f.Fuzz(func(t *testing.T, ops []byte) {
		if len(ops) > 48 {
			ops = ops[:48]
		}
		base := solidImage(image.Rect(0, 0, 16, 16), color.RGBA{R: 20, G: 20, B: 20, A: 255})
		p := testPlayer(t)
		comp, err := animation.NewComposite(context.Background(), base, []animation.Region{
			{Key: "a", Rect: image.Rect(0, 0, 8, 8), Player: p},
			{Key: "b", Rect: image.Rect(8, 8, 16, 16), Player: p},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		l := NewLayers(comp, Options{
			Wait:    func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil },
			Picture: picture.Options{RetireDelay: -1},
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
			driveLayers(t, l, cmd)
		}
		enqueue(l.Init())
		for _, op := range ops {
			switch op % 14 {
			case 0:
				flushOne()
			case 1:
				enqueue(l.Pause(op&16 != 0))
			case 2:
				enqueue(l.SetActive(op&16 != 0))
			case 3:
				enqueue(l.SetRate(float64(int(op)%6) * 0.5))
			case 4:
				enqueue(l.Restart())
			case 5:
				enqueue(l.SetVisible(op&16 != 0))
			case 6:
				enqueue(l.SetSize(int(op%5), int((op>>2)%5)))
			case 7:
				enqueue(l.SetTerminal(op&16 != 0, int(op%8)+1, int((op>>2)%8)+1))
			case 8:
				l.SetViewport(picture.Viewport{X: int(op%4) - 1, Y: int((op>>2)%4) - 1, Width: int(op % 4), Height: int((op >> 3) % 4)})
			case 9:
				l.ResetViewport()
			case 10:
				enqueue(l.SetComposite(comp))
			case 11:
				enqueue(l.SetImage(base))
			case 12:
				enqueue(l.SetFit(picture.FitMode(op % 4)))
			case 13:
				enqueue(l.SetAnchor(picture.FitAnchor(op % 6)))
			}
			_ = l.View()
			_ = l.Snapshot()
			_ = l.Layers()
			_ = l.Positions()
			st := l.Status()
			if st.Rate < 0.25 || st.Rate > 4 {
				t.Fatalf("rate out of bounds: %v", st.Rate)
			}
		}
		for len(queue) > 0 {
			flushOne()
		}
		driveLayers(t, l, l.Close())
		if l.Status().Source != nil || l.Status().Busy || l.Snapshot() != nil {
			t.Fatalf("closed layers retained state: %+v", l.Status())
		}
	})
}
