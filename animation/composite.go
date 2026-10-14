package animation

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"maps"
	"math"
	"nimbleterminal.dev/ntpicture/internal/imageview"
	"slices"
	"time"

	xdraw "golang.org/x/image/draw"
)

// MaxCompositeRegions bounds construction and per-tick work. Hosts should apply
// their own smaller document limits and aggregate memory budgets.
const MaxCompositeRegions = 1024

// Region places a clip in pixel coordinates. Rect may extend outside the base.
// Key is opaque identity, unrelated to document destinations. Repeated keys
// share playback; they must name the same clip. Slice order is paint order.
type Region struct {
	Rect   image.Rectangle
	Player *Player
	Key    string
}
type compositePosition struct {
	player   *Player
	due      time.Duration
	finished bool
}

// Composite is an immutable pixel composition and playback position. Its base
// excludes animated content, so transparency/disposal reveals the base again.
// Images and regions are owned snapshots; positions share immutable players.
type Composite struct {
	base      image.Image
	regions   []Region
	positions map[string]compositePosition
	frame     image.Image
}

// RegionError identifies the region whose lazy frame or restart failed.
// Unwrap preserves FrameError and context cancellation for errors.Is/As.
type RegionError struct {
	Key string
	Err error
}

func (e *RegionError) Error() string { return fmt.Sprintf("animation region %q: %v", e.Key, e.Err) }
func (e *RegionError) Unwrap() error { return e.Err }

func compositeRectOK(r image.Rectangle) bool {
	for _, n := range []int{r.Min.X, r.Min.Y, r.Max.X, r.Max.Y} {
		if n < -MaxPixels || n > MaxPixels {
			return false
		}
	}
	return !r.Empty() && r.Dx() <= MaxPixels/r.Dy()
}

// NewComposite copies base pixels and region metadata. Each canvas and region
// is limited to MaxPixels and there are at most MaxCompositeRegions entries.
// Previous (a *Composite or saved Positions) supplies positions only for
// matching keys AND clip identities; replaced clips restart at the supplied
// Player, and removed keys are dropped.
// Construction/composition belongs off the UI loop for large canvases.
func NewComposite(ctx context.Context, base image.Image, regions []Region, previous PositionSource) (*Composite, error) {
	return newComposite(ctx, base, regions, previous, true)
}

// NewCompositeImmutable avoids copying base pixels. The caller must keep base
// and any backing storage immutable for the entire lifetime of this composite
// and all positions/windows derived from it. Region metadata is still copied,
// and outputs remain read-only views. All NewComposite bounds apply.
func NewCompositeImmutable(ctx context.Context, base image.Image, regions []Region, previous PositionSource) (*Composite, error) {
	return newComposite(ctx, base, regions, previous, false)
}

func newComposite(ctx context.Context, base image.Image, regions []Region, previous PositionSource, copyBase bool) (*Composite, error) {

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if base == nil || !compositeRectOK(base.Bounds()) || len(regions) > MaxCompositeRegions {
		return nil, fmt.Errorf("invalid composite canvas or region count")
	}
	s := &Composite{regions: append([]Region(nil), regions...), positions: map[string]compositePosition{}}
	for _, r := range s.regions {
		if r.Key == "" || r.Player == nil || !compositeRectOK(r.Rect) {
			return nil, fmt.Errorf("invalid composite region %q", r.Key)
		}
		if p, ok := s.positions[r.Key]; ok && p.player.data != r.Player.data {
			return nil, fmt.Errorf("conflicting clips for region key %q", r.Key)
		}
		if _, ok := s.positions[r.Key]; !ok {
			s.positions[r.Key] = compositePosition{player: r.Player, due: r.Player.Delay()}
		}
	}
	if copyBase {
		owned := image.NewRGBA(base.Bounds())
		draw.Draw(owned, owned.Bounds(), imageview.Unwrap(base), base.Bounds().Min, draw.Src)
		base = owned
	}
	s.base = imageview.New(base)
	if previous != nil {
		s.carry(previous)
	}
	return s.compose(ctx)
}
func (s *Composite) Image() image.Image { return s.frame }

// PositionSource supplies playback positions to carry: a *Composite or
// Positions. A nil *Composite supplies none.
type PositionSource interface {
	compositePositions() map[string]compositePosition
}

// Positions is an immutable snapshot of a composite's playback positions,
// detached from its base and composed image. Each entry keeps the clip
// identity, current frame, remaining delay and finished state; restoring
// applies the same key-and-clip matching as NewComposite's previous argument.
// Entries retain their immutable players, so each clip's current canvas stays
// alive; the base image and composed frame do not.
type Positions struct {
	positions map[string]compositePosition
}

// Positions exports this composite's playback positions without its pixels.
func (s *Composite) Positions() Positions {
	if s == nil {
		return Positions{}
	}
	return Positions{maps.Clone(s.positions)}
}

// Keys reports the saved region keys in sorted order.
func (p Positions) Keys() []string { return slices.Sorted(maps.Keys(p.positions)) }

func (s *Composite) compositePositions() map[string]compositePosition {
	if s == nil {
		return nil
	}
	return s.positions
}
func (p Positions) compositePositions() map[string]compositePosition { return p.positions }

// Regions returns a copy of placement metadata; players are immutable.
func (s *Composite) Regions() []Region { return append([]Region(nil), s.regions...) }
func (s *Composite) Delay() time.Duration {
	var delay time.Duration
	for _, p := range s.positions {
		if !p.finished && (delay == 0 || p.due < delay) {
			delay = p.due
		}
	}
	return delay
}
func (s *Composite) copy() *Composite {
	n := *s
	n.positions = make(map[string]compositePosition, len(s.positions))
	for k, p := range s.positions {
		n.positions[k] = p
	}
	return &n
}
func (s *Composite) carry(other PositionSource) {
	if other == nil {
		return
	}
	saved := other.compositePositions()
	for key, p := range s.positions {
		if old, ok := saved[key]; ok && old.player.data == p.player.data {
			s.positions[key] = old
		}
	}
}

// WithPositions carries matching positions from others (composites or saved
// Positions), later arguments winning, and recomposes a consistent image.
// It never retains keys absent from s.
func (s *Composite) WithPositions(ctx context.Context, others ...PositionSource) (*Composite, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n := s.copy()
	for _, other := range others {
		n.carry(other)
	}
	unchanged := true
	for key, p := range n.positions {
		if p != s.positions[key] {
			unchanged = false
			break
		}
	}
	if unchanged {
		return s, ctx.Err()
	}
	return n.compose(ctx)
}
func (s *Composite) Advance(ctx context.Context) (Source, bool, error) {
	if err := ctx.Err(); err != nil {
		return s, false, err
	}
	delta := s.Delay()
	if delta == 0 {
		return s, false, nil
	}
	n := s.copy()
	changed := false
	// Stable first-occurrence order also makes lazy failure reporting deterministic.
	seen := map[string]bool{}
	for _, r := range n.regions {
		if seen[r.Key] {
			continue
		}
		seen[r.Key] = true
		if err := ctx.Err(); err != nil {
			return s, false, err
		}
		p := n.positions[r.Key]
		if p.finished {
			continue
		}
		p.due -= delta
		if p.due <= 0 {
			next, more, err := p.player.NextContext(ctx)
			if err != nil {
				return s, false, &RegionError{r.Key, err}
			}
			if more {
				p.player = next
				p.due = next.Delay()
				changed = true
			} else {
				p.finished = true
			}
		}
		n.positions[r.Key] = p
	}
	if !changed {
		return n, n.Delay() > 0, nil
	}
	out, err := n.compose(ctx)
	if err != nil {
		return s, false, err
	}
	return out, true, nil
}
func (s *Composite) Reset(ctx context.Context) (Source, error) {
	if err := ctx.Err(); err != nil {
		return s, err
	}
	n := s.copy()
	seen := map[string]bool{}
	for _, r := range n.regions {
		if seen[r.Key] {
			continue
		}
		seen[r.Key] = true
		player, err := n.positions[r.Key].player.RestartContext(ctx)
		if err != nil {
			return s, &RegionError{r.Key, err}
		}
		n.positions[r.Key] = compositePosition{player: player, due: player.Delay()}
	}
	return n.compose(ctx)
}
func (s *Composite) compose(ctx context.Context) (*Composite, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(s.regions) == 0 {
		s.frame = s.base
		return s, nil
	}
	out := image.NewRGBA(s.base.Bounds())
	draw.Draw(out, out.Bounds(), imageview.Unwrap(s.base), s.base.Bounds().Min, draw.Src)
	for _, r := range s.regions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !r.Rect.Overlaps(out.Bounds()) {
			continue
		}
		frame := s.positions[r.Key].player.Image()
		scaleComposite(out, r.Rect, frame, frame.Bounds(), draw.Over)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.frame = imageview.New(out)
	return s, nil
}
func scaleComposite(dst draw.Image, rect image.Rectangle, src image.Image, bounds image.Rectangle, op draw.Op) {
	src = imageview.Unwrap(src)
	switch {
	case rect.Size() == bounds.Size():
		draw.Draw(dst, rect, src, bounds.Min, op)
	case 2*rect.Dx() >= bounds.Dx() && rect.Dx() <= 2*bounds.Dx() && 2*rect.Dy() >= bounds.Dy() && rect.Dy() <= 2*bounds.Dy():
		xdraw.ApproxBiLinear.Scale(dst, rect, src, bounds, op, nil)
	default:
		xdraw.CatmullRom.Scale(dst, rect, src, bounds, op, nil)
	}
}

// Window constructs a new pixel source: crop base to r, optionally rescale it,
// and remap intersecting regions while retaining their playback positions.
// Zero size preserves the intersected rectangle's pixel size. Empty or oversized
// windows return errors. Near-native regions snap within two pixels to avoid
// repeated resampling. Scaling/codec calls already running cannot be interrupted.
//
// Unlike Picture.SetViewport (cell-only presentation clipping), Window allocates
// pixels and changes the image to compose/encode on future frames. The host
// chooses windows and decides when the savings justify constructing a new source.
func (s *Composite) Window(ctx context.Context, r image.Rectangle, size image.Point) (*Composite, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r = r.Intersect(s.base.Bounds())
	if r.Empty() {
		return nil, fmt.Errorf("empty composite window")
	}
	if size == (image.Point{}) {
		size = r.Size()
	}
	bounds := image.Rectangle{Max: size}
	if !compositeRectOK(bounds) {
		return nil, fmt.Errorf("invalid composite window size")
	}
	base := image.NewRGBA(bounds)
	scaleComposite(base, bounds, s.base, r, draw.Src)
	fx, fy := float64(size.X)/float64(r.Dx()), float64(size.Y)/float64(r.Dy())
	n := &Composite{base: imageview.New(base), positions: map[string]compositePosition{}}
	for _, region := range s.regions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !region.Rect.Overlaps(r) {
			continue
		}
		rr := region.Rect.Sub(r.Min)
		// Check float coordinates before integer conversion or allocation.
		coords := []float64{float64(rr.Min.X) * fx, float64(rr.Min.Y) * fy, float64(rr.Max.X) * fx, float64(rr.Max.Y) * fy}
		for _, v := range coords {
			if v < -MaxPixels || v > MaxPixels {
				return nil, fmt.Errorf("composite window region exceeds bounds")
			}
		}
		rect := image.Rect(int(math.Round(coords[0])), int(math.Round(coords[1])), int(math.Round(coords[2])), int(math.Round(coords[3])))
		own := region.Player.Image().Bounds().Size()
		if absComposite(rect.Dx()-own.X) <= 2 && absComposite(rect.Dy()-own.Y) <= 2 {
			rect.Max = rect.Min.Add(own)
		}
		if rect.Empty() {
			continue
		}
		if !compositeRectOK(rect) {
			return nil, fmt.Errorf("composite window region exceeds pixel limit")
		}
		region.Rect = rect
		n.regions = append(n.regions, region)
		n.positions[region.Key] = s.positions[region.Key]
	}
	return n.compose(ctx)
}
func absComposite(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
