package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	picture "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"
	"nimbleterminal.dev/ntpicture/examples/internal/demohelp"
	"nimbleterminal.dev/ntpicture/playback"
)

type compositeModel struct {
	layers     *playback.Layers
	session    *picture.TerminalSession
	gifPlayer  *animation.Player
	webpPlayer *animation.Player
	cols, rows int
	keeping    bool
	final      string
}

func initialCompositeModel() (*compositeModel, error) {
	_, gifPlayer, err := animation.DecodeContext(context.Background(), gifData, animation.Options{Animate: true})
	if err != nil {
		return nil, err
	}
	_, webpPlayer, err := animation.DecodeContext(context.Background(), webpData, animation.Options{Animate: true})
	if err != nil {
		return nil, err
	}
	m := &compositeModel{
		session:    picture.NewTerminalSession(picture.TerminalOptions{}),
		gifPlayer:  gifPlayer,
		webpPlayer: webpPlayer,
	}
	source, err := m.buildScene(60, 16, 8, 16, nil)
	if err != nil {
		m.session.Close()
		return nil, err
	}
	m.layers = playback.NewLayers(source, playback.Options{
		Picture: picture.Options{
			Session: m.session,
			Render: picture.Config{
				Fit:         picture.FitFill,
				KittyMedium: picture.KittyMediumSharedMemory,
			},
		},
	})
	return m, nil
}

func (m *compositeModel) buildScene(cols, rows, cw, ch int, previous animation.PositionSource) (*animation.Composite, error) {
	cw, ch = max(1, cw), max(1, ch)
	w, h := max(16, cols*cw), max(16, rows*ch)
	base := paintStillBackground(w, h, cw, ch)

	padX := max(cw, w/10)
	padY := max(ch, h/6)
	availW := max(8, (w-3*padX)/2)
	availH := max(8, h-2*padY)

	leftRect := image.Rect(padX, padY, padX+availW, padY+availH)
	rightRect := image.Rect(2*padX+availW, padY, 2*padX+2*availW, padY+availH)
	drawPanelFrame(base, leftRect, color.RGBA{R: 72, G: 138, B: 199, A: 255})
	drawPanelFrame(base, rightRect, color.RGBA{R: 214, G: 132, B: 74, A: 255})

	regions := []animation.Region{
		{Key: "gif", Player: m.gifPlayer, Rect: leftRect},
		{Key: "webp", Player: m.webpPlayer, Rect: rightRect},
	}
	return animation.NewCompositeImmutable(context.Background(), base, regions, previous)
}

func paintStillBackground(w, h, cw, ch int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	stepX, stepY := max(4, cw*2), max(4, ch)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			baseR := uint8(18 + (y*16)/max(1, h))
			baseG := uint8(24 + (x*14)/max(1, w))
			baseB := uint8(38 + (y*22)/max(1, h))
			if ((x/stepX)+(y/stepY))%2 == 0 {
				baseR += 5
				baseG += 6
				baseB += 8
			}
			if x < 2 || x >= w-2 || y < 2 || y >= h-2 {
				baseR, baseG, baseB = 88, 104, 132
			}
			img.SetRGBA(x, y, color.RGBA{R: baseR, G: baseG, B: baseB, A: 255})
		}
	}
	return img
}

func drawPanelFrame(img *image.RGBA, r image.Rectangle, accent color.RGBA) {
	frame := r.Inset(-2).Intersect(img.Bounds())
	for y := frame.Min.Y; y < frame.Max.Y; y++ {
		for x := frame.Min.X; x < frame.Max.X; x++ {
			if !image.Pt(x, y).In(r) {
				img.SetRGBA(x, y, accent)
			}
		}
	}
}

func (m *compositeModel) Init() tea.Cmd { return m.layers.Init() }
func (m *compositeModel) HelpKeys() demohelp.Keys {
	if m.keeping {
		return demohelp.Keys{}
	}
	keys := demohelp.Animation(m.layers.Status().Paused)
	keys.Previous.SetEnabled(false)
	keys.Next.SetEnabled(false)
	return keys
}
func (m *compositeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.keeping {
		return m, m.layers.Update(msg)
	}
	prevCW, prevCH := m.layers.CellPixelSize()
	cmds := []tea.Cmd{m.layers.Update(msg)}
	relayout := false
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		cols, rows := max(0, v.Width), max(0, v.Height-2)
		if cols != m.cols || rows != m.rows {
			m.cols, m.rows = cols, rows
			cmds = append(cmds, m.layers.SetSize(cols, rows))
			relayout = true
		}
	case picture.TerminalSessionMsg:
		if v.Session == m.session && m.session.Snapshot().Kitty == picture.KittyCapabilitySupported && m.layers.Mode() != picture.PictureKitty {
			cmds = append(cmds, m.layers.SetMode(picture.PictureKitty))
			relayout = true
		}
	case tea.KeyPressMsg:
		keys := m.HelpKeys()
		switch {
		case key.Matches(v, keys.Play):
			cmds = append(cmds, m.layers.Pause(!m.layers.Status().Paused))
		case key.Matches(v, keys.Restart):
			cmds = append(cmds, m.layers.Restart())
		case key.Matches(v, keys.Keep):
			content := m.View().Content
			m.final = content[:strings.LastIndex(content, "\n")+1]
			m.keeping = true
			m.session.Close()
			return m, tea.Sequence(m.layers.Keep(), tea.Quit)
		case key.Matches(v, keys.Quit):
			m.session.Close()
			return m, tea.Sequence(m.layers.Close(), tea.Quit)
		}
	}
	if nw, nh := m.layers.CellPixelSize(); nw != prevCW || nh != prevCH {
		relayout = true
	}
	if relayout && m.cols > 0 && m.rows > 0 {
		cw, ch := m.layers.CellPixelSize()
		if comp, err := m.buildScene(m.cols, m.rows, cw, ch, m.layers.Positions()); err == nil {
			cmds = append(cmds, m.layers.SetComposite(comp))
		}
	}
	return m, tea.Batch(cmds...)
}
func (m *compositeModel) View() tea.View {
	if m.keeping {
		return tea.NewView(m.final)
	}
	label := "GIF + WebP: still background with animated layers"
	if n := len(m.layers.Layers()); n > 0 {
		label = fmt.Sprintf("GIF + WebP: still background with %d Kitty overlays", n)
	}
	if m.layers.Status().Paused {
		label += " paused"
	}
	if err := m.layers.Status().Err; err != nil {
		label = err.Error()
	}
	v := tea.NewView(label + "\n" + m.layers.View() + "\n" + m.HelpKeys().View())
	v.AltScreen = true
	return v
}
