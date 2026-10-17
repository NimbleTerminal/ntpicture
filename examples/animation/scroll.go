package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	picture "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"
	"nimbleterminal.dev/ntpicture/examples/internal/demohelp"
	"nimbleterminal.dev/ntpicture/playback"
)

// scrollablePicture is the shared presentation surface implemented by both
// *picture.Picture and *playback.Controller for mixed still/animated views.
type scrollablePicture interface {
	Init() tea.Cmd
	Update(tea.Msg) tea.Cmd
	View() string
	SetSize(cols, rows int) tea.Cmd
	Size() (cols, rows int)
	SetTerminal(kitty bool, cellWidth, cellHeight int) tea.Cmd
	SetCellSize(cellWidth, cellHeight int) tea.Cmd
	CellPixelSize() (width, height int)
	SetKittyCapability(picture.KittyCapability) tea.Cmd
	KittySupported() picture.KittyCapability
	TerminalState() picture.TerminalState
	Session() *picture.TerminalSession
	SetMode(picture.PictureMode) tea.Cmd
	ToggleMode() tea.Cmd
	Mode() picture.PictureMode
	AutoMode() bool
	SetViewport(picture.Viewport)
	ResetViewport()
	Viewport() (picture.Viewport, bool)
	SetVisible(visible bool) tea.Cmd
	Visible() bool
	Stop() tea.Cmd
	Keep() tea.Cmd
	KeepOnMainScreen() tea.Cmd
	Close() tea.Cmd
	Closed() bool
}

type scrollItem struct {
	title    string
	caption  string
	pic      scrollablePicture
	anim     *playback.Controller
	baseRows int
	startRow int
	cols     int
	rows     int
}

type scrollLine struct {
	text string
	item int // -1 for text rows; otherwise index into scrollModel.items
	row  int
}

type scrollModel struct {
	session       *picture.TerminalSession
	items         []scrollItem
	lines         []scrollLine
	initCmd       tea.Cmd
	width, height int
	offset        int
	cw, ch        int
	paused        bool
	keeping       bool
	final         string
}

func initialScrollModel() (*scrollModel, error) {
	return initialScrollModelWithOptions(picture.TerminalOptions{})
}

func initialScrollModelWithOptions(termOpts picture.TerminalOptions) (*scrollModel, error) {
	_, gifPlayer, err := animation.DecodeContext(context.Background(), gifData, animation.Options{Animate: true})
	if err != nil {
		return nil, err
	}
	_, webpPlayer, err := animation.DecodeContext(context.Background(), webpData, animation.Options{Animate: true})
	if err != nil {
		return nil, err
	}
	session := picture.NewTerminalSession(termOpts)
	st := session.Snapshot()
	picOpts := func(fit picture.FitMode) picture.Options {
		return picture.Options{
			Session:  session,
			AutoMode: true,
			Render: picture.Config{
				Fit:         fit,
				KittyMedium: picture.KittyMediumSharedMemory,
				KittyFormat: picture.KittyFormatZlib,
			},
		}
	}

	gifCtrl := playback.New(gifPlayer, playback.Options{Picture: picOpts(picture.FitContain)})
	webpCtrl := playback.New(webpPlayer, playback.Options{Picture: picOpts(picture.FitContain)})

	still1 := picture.New(picOpts(picture.FitFill))
	still2 := picture.New(picOpts(picture.FitFill))
	stillCmd1 := still1.SetImageImmutable(paintScrollBanner(240, 80, color.RGBA{R: 52, G: 118, B: 186, A: 255}))
	stillCmd2 := still2.SetImageImmutable(paintScrollBanner(240, 80, color.RGBA{R: 186, G: 108, B: 56, A: 255}))

	m := &scrollModel{
		session: session,
		width:   60,
		height:  16,
		cw:      st.CellPixelWidth,
		ch:      st.CellPixelHeight,
		items: []scrollItem{
			{
				title:    "1. Orbit GIF (animated)",
				caption:  "GIF frames advance through playback.Controller.",
				pic:      gifCtrl,
				anim:     gifCtrl,
				baseRows: 6,
			},
			{
				title:    "2. Horizon Card (still)",
				caption:  "Still image managed through the same scrollablePicture interface.",
				pic:      still1,
				baseRows: 5,
			},
			{
				title:    "3. Orbit WebP (animated)",
				caption:  "Offscreen animations call SetVisible(false) until scrolled into view.",
				pic:      webpCtrl,
				anim:     webpCtrl,
				baseRows: 6,
			},
			{
				title:    "4. Reference Grid (still)",
				caption:  "Press Q to keep visible placements on the main screen and quit.",
				pic:      still2,
				baseRows: 5,
			},
		},
	}
	m.initCmd = tea.Batch(stillCmd1, stillCmd2, m.layout())
	return m, nil
}

func paintScrollBanner(w, h int, accent color.RGBA) *image.RGBA {
	img := paintStillBackground(w, h, 8, 16)
	drawPanelFrame(img, image.Rect(4, 4, w-4, h-4), accent)
	return img
}

func (m *scrollModel) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.items)+1)
	for i := range m.items {
		cmds = append(cmds, m.items[i].pic.Init())
	}
	cmds = append(cmds, m.initCmd)
	return tea.Batch(cmds...)
}

func (m *scrollModel) HelpKeys() demohelp.Keys {
	if m.keeping {
		return demohelp.Keys{}
	}
	kitty := m.session.Snapshot().Kitty == picture.KittyCapabilitySupported
	return demohelp.Scroll(m.paused, kitty)
}

func (m *scrollModel) bodyHeight() int {
	return max(1, m.height-2)
}

func (m *scrollModel) itemRows(baseRows int) int {
	cw, ch := max(1, m.cw), max(1, m.ch)
	scaled := int(math.Round(float64(baseRows*2) * float64(cw) / float64(ch)))
	return max(2, min(14, scaled))
}

func (m *scrollModel) layout() tea.Cmd {
	cols := max(1, m.width)
	m.lines = nil
	var cmds []tea.Cmd
	for i := range m.items {
		item := &m.items[i]
		m.lines = append(m.lines, scrollLine{text: item.title, item: -1})
		item.startRow = len(m.lines)
		item.cols = cols
		item.rows = m.itemRows(item.baseRows)
		for r := 0; r < item.rows; r++ {
			m.lines = append(m.lines, scrollLine{item: i, row: r})
		}
		m.lines = append(m.lines, scrollLine{text: item.caption, item: -1})
		if i+1 < len(m.items) {
			m.lines = append(m.lines, scrollLine{text: "", item: -1})
		}
		cmds = append(cmds, item.pic.SetCellSize(m.cw, m.ch), item.pic.SetSize(item.cols, item.rows))
	}
	m.clampOffset()
	cmds = append(cmds, m.syncViewport())
	return tea.Batch(cmds...)
}

func (m *scrollModel) clampOffset() {
	maxOffset := max(0, len(m.lines)-m.bodyHeight())
	m.offset = max(0, min(maxOffset, m.offset))
}

func (m *scrollModel) scroll(delta int) tea.Cmd {
	prev := m.offset
	m.offset += delta
	m.clampOffset()
	if m.offset == prev {
		return nil
	}
	return m.syncViewport()
}

// syncViewport applies partial cell clipping (SetViewport/ResetViewport) to
// items intersecting the visible scroll window and hides offscreen items via
// SetVisible(false) so offscreen animations pause and release placements while
// retaining their current playback position.
func (m *scrollModel) syncViewport() tea.Cmd {
	bodyH := m.bodyHeight()
	winStart := m.offset
	winEnd := m.offset + bodyH
	var cmds []tea.Cmd
	for i := range m.items {
		item := &m.items[i]
		visStart := max(item.startRow, winStart)
		visEnd := min(item.startRow+item.rows, winEnd)
		if visStart >= visEnd {
			item.pic.ResetViewport()
			cmds = append(cmds, item.pic.SetVisible(false))
			continue
		}
		visY := visStart - item.startRow
		visH := visEnd - visStart
		if visY == 0 && visH == item.rows {
			item.pic.ResetViewport()
		} else {
			item.pic.SetViewport(picture.Viewport{
				X:      0,
				Y:      visY,
				Width:  item.cols,
				Height: visH,
			})
		}
		cmds = append(cmds, item.pic.SetVisible(true))
	}
	return tea.Batch(cmds...)
}

func (m *scrollModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.keeping {
		var cmds []tea.Cmd
		for i := range m.items {
			cmds = append(cmds, m.items[i].pic.Update(msg))
		}
		return m, tea.Batch(cmds...)
	}

	var cmds []tea.Cmd
	prevState := m.session.Snapshot()

	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		w, h := max(1, v.Width), max(3, v.Height)
		if w != m.width || h != m.height {
			m.width, m.height = w, h
			cmds = append(cmds, m.layout())
		}
	case tea.KeyPressMsg:
		keys := m.HelpKeys()
		switch {
		case key.Matches(v, keys.Keep):
			content := m.View().Content
			m.final = content[:strings.LastIndex(content, "\n")+1]
			m.keeping = true
			m.session.Close()
			var keepCmds []tea.Cmd
			for i := range m.items {
				if m.items[i].pic.Visible() {
					keepCmds = append(keepCmds, m.items[i].pic.KeepOnMainScreen())
				} else {
					keepCmds = append(keepCmds, m.items[i].pic.Close())
				}
			}
			return m, tea.Sequence(tea.Batch(keepCmds...), tea.Quit)
		case key.Matches(v, keys.Quit):
			m.session.Close()
			var closeCmds []tea.Cmd
			for i := range m.items {
				closeCmds = append(closeCmds, m.items[i].pic.Close())
			}
			return m, tea.Sequence(tea.Batch(closeCmds...), tea.Quit)
		case key.Matches(v, keys.Play):
			m.paused = !m.paused
			for i := range m.items {
				if m.items[i].anim != nil {
					cmds = append(cmds, m.items[i].anim.Pause(m.paused))
				}
			}
		case key.Matches(v, keys.Toggle):
			for i := range m.items {
				cmds = append(cmds, m.items[i].pic.ToggleMode())
			}
		case key.Matches(v, keys.Up):
			cmds = append(cmds, m.scroll(-1))
		case key.Matches(v, keys.Down):
			cmds = append(cmds, m.scroll(1))
		case key.Matches(v, keys.Restart):
			cmds = append(cmds, m.scroll(-m.offset))
		case v.String() == "pgup" || v.String() == "b":
			cmds = append(cmds, m.scroll(-max(1, m.bodyHeight()/2)))
		case v.String() == "pgdown":
			cmds = append(cmds, m.scroll(max(1, m.bodyHeight()/2)))
		case v.String() == "end" || v.String() == "G":
			cmds = append(cmds, m.scroll(len(m.lines)))
		}
	}

	for i := range m.items {
		cmds = append(cmds, m.items[i].pic.Update(msg))
	}

	if nextState := m.session.Snapshot(); nextState.CellPixelWidth != prevState.CellPixelWidth || nextState.CellPixelHeight != prevState.CellPixelHeight {
		m.cw, m.ch = nextState.CellPixelWidth, nextState.CellPixelHeight
		cmds = append(cmds, m.layout())
	}

	return m, tea.Batch(cmds...)
}

func (m *scrollModel) statusLine() string {
	mode := "glyph"
	if len(m.items) > 0 && m.items[0].pic.Mode() == picture.PictureKitty {
		mode = "kitty"
	}
	visCount, clipCount := 0, 0
	for i := range m.items {
		if m.items[i].pic.Visible() {
			visCount++
			if _, clipped := m.items[i].pic.Viewport(); clipped {
				clipCount++
			}
		}
	}
	state := fmt.Sprintf("scroll %d/%d · %s · cell %dx%dpx · %d/%d visible (%d clipped)",
		m.offset, max(0, len(m.lines)-m.bodyHeight()), mode, m.cw, m.ch, visCount, len(m.items), clipCount)
	if m.paused {
		state += " · paused"
	}
	return ansi.Truncate(state, max(1, m.width), "…")
}

func (m *scrollModel) View() tea.View {
	if m.keeping {
		return tea.NewView(m.final)
	}
	bodyH := m.bodyHeight()
	winStart := m.offset
	winEnd := min(len(m.lines), m.offset+bodyH)

	views := make([][]string, len(m.items))
	visStarts := make([]int, len(m.items))
	for i := range m.items {
		item := &m.items[i]
		visStarts[i] = max(item.startRow, winStart)
		if item.pic.Visible() {
			if v := item.pic.View(); v != "" {
				views[i] = strings.Split(v, "\n")
			}
		}
	}

	out := make([]string, 0, bodyH+2)
	out = append(out, m.statusLine())
	for docRow := winStart; docRow < winEnd; docRow++ {
		line := m.lines[docRow]
		if line.item < 0 {
			out = append(out, ansi.Truncate(line.text, max(1, m.width), "…"))
			continue
		}
		rel := docRow - visStarts[line.item]
		rows := views[line.item]
		if rel >= 0 && rel < len(rows) {
			out = append(out, rows[rel])
		} else {
			out = append(out, "")
		}
	}
	for len(out)-1 < bodyH {
		out = append(out, "")
	}
	out = append(out, m.HelpKeys().View())
	v := tea.NewView(strings.Join(out, "\n"))
	v.AltScreen = true
	return v
}
