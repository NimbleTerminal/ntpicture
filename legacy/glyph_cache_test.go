package legacy_test

import (
	"image"
	"image/color"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/legacy"
)

type countingImage struct {
	image.Image
	reads atomic.Int64
}

func (i *countingImage) At(x, y int) color.Color {
	i.reads.Add(1)
	return i.Image.At(x, y)
}
func cacheSource() *countingImage {
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 40), A: 255})
		}
	}
	return &countingImage{Image: img}
}

func TestGlyphCachePublicPresentation(t *testing.T) {
	for _, firstString := range []bool{false, true} {
		t.Run(map[bool]string{false: "View", true: "String"}[firstString], func(t *testing.T) {
			var m legacy.Model // Also exercise lazy zero-value initialization.
			src := cacheSource()
			m.SetSize(4, 2)
			m.SetImage(src)
			var first string
			if firstString {
				first = m.String()
			} else {
				first = m.View().Content
			}
			reads := src.reads.Load()
			if reads == 0 || first == "" {
				t.Fatal("first presentation did not render")
			}
			for range 3 {
				if m.View().Content != first || m.String() != first || src.reads.Load() != reads {
					t.Fatal("unchanged presentation re-read source pixels")
				}
			}
		})
	}
}

// Match hosts whose value-receiver View contains a pointer to the picture.
type valueViewHost struct{ picture *legacy.Model }

func (h valueViewHost) View() tea.View { return h.picture.View() }
func TestGlyphCacheSurvivesValueReceiverView(t *testing.T) {
	m := legacy.NewModel()
	src := cacheSource()
	m.SetSize(4, 2)
	m.SetImage(src)
	host := valueViewHost{picture: &m}
	first := host.View().Content
	reads := src.reads.Load()
	if reads == 0 {
		t.Fatal("first view did not read source")
	}
	if host.View().Content != first || src.reads.Load() != reads {
		t.Fatal("value-receiver host lost glyph cache")
	}
}

func TestGlyphCacheInvalidation(t *testing.T) {
	m := legacy.NewModel()
	src := cacheSource()
	m.SetSize(4, 2)
	m.SetImage(src)
	for _, change := range []struct {
		name  string
		apply func()
	}{
		{"initial", func() {}},
		{"image", func() { m.SetImage(src) }},
		{"size", func() { m.SetSize(5, 3) }},
		{"fit", func() { m.SetFit(ntpicture.FitCover) }},
		{"anchor", func() { m.SetAnchor(ntpicture.AnchorTop) }},
		{"cell size event", func() { m.Update(uv.CellSizeEvent{Width: 9, Height: 19}) }},
	} {
		t.Run(change.name, func(t *testing.T) {
			before := src.reads.Load()
			change.apply()
			first := m.View().Content
			reads := src.reads.Load()
			if reads <= before {
				t.Fatal("change did not invalidate presentation")
			}
			if m.String() != first || src.reads.Load() != reads {
				t.Fatal("new presentation was not cached")
			}
		})
	}
	reads := src.reads.Load()
	m.Update(uv.CellSizeEvent{Width: 0, Height: 0})
	m.Update(struct{}{})
	m.View()
	if src.reads.Load() != reads {
		t.Fatal("irrelevant messages invalidated cache")
	}
	m.SetImage(nil)
	if m.String() != "" {
		t.Fatal("cleared image still visible")
	}
}

func TestGlyphCacheModelCopies(t *testing.T) {
	original := legacy.NewModel()
	src := cacheSource()
	original.SetSize(4, 2)
	original.SetImage(src)
	first := original.String()
	copied := original
	copied.SetFit(ntpicture.FitCover)
	copied.SetSize(3, 3)
	if copied.String() == first {
		t.Fatal("copy did not change")
	}
	reads := src.reads.Load()
	if original.String() != first || src.reads.Load() != reads {
		t.Fatal("copy mutated original cache")
	}
	// Concurrent rendering of independent copies must not share mutable scratch.
	a, b := original, original
	a.SetFit(ntpicture.FitCover)
	b.SetFit(ntpicture.FitFill)
	done := make(chan struct{})
	go func() { a.View(); close(done) }()
	b.View()
	<-done
}

func TestGlyphCachePreservesPendingKittyRender(t *testing.T) {
	previous := legacy.KittySupported()
	defer legacy.ForceKittyCapability(previous)
	legacy.ForceKittyCapability(ntpicture.KittyCapabilitySupported)
	m := legacy.NewModelWithConfig(ntpicture.Config{KittyMedium: ntpicture.KittyMediumDirect})
	src := cacheSource()
	m.SetSize(4, 2)
	m.SetImage(src)
	pending := m.Toggle()
	if pending == nil || src.reads.Load() != 0 {
		t.Fatal("Kitty preparation was not deferred")
	}
	fallback := m.View().Content
	reads := src.reads.Load()
	if m.String() != fallback || src.reads.Load() != reads {
		t.Fatal("pending Kitty fallback was not cached")
	}
	frame := pending()
	if _, ok := frame.(legacy.KittyFrameMsg); !ok {
		t.Fatalf("unexpected frame %T", frame)
	}
	if m.Update(frame) == nil {
		t.Fatal("presentation lost pending frame state")
	}
	// A later image must still reject the old asynchronous result.
	m.SetImage(cacheSource())
	if m.Update(frame) != nil {
		t.Fatal("accepted stale frame after replacement")
	}
}
