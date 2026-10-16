// A continuously rendered framebuffer: a procedural scene is drawn off the
// event loop at the picture's raster size and streamed with stable Kitty IDs.
package main

import (
	"context"
	"fmt"
	"image"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	picture "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/examples/internal/demohelp"
)

const (
	headerRows = 2
	helpRows   = 1
	// The prepared picture is larger than the visible viewport by this
	// fraction on every side, so panning reveals clipped content.
	overscan  = 4 // one quarter of the visible size per side
	panStepX  = 2
	panStepY  = 1
	phaseStep = 0.025
)

var resolutions = []float64{0.25, 0.5, 0.75, 1}

// frameMsg carries one rendered framebuffer back to the event loop together
// with the request and geometry it was rendered for.
type frameMsg struct {
	request  uint64
	geometry picture.RasterGeometry
	img      *image.RGBA
	took     time.Duration
	err      error
}

type model struct {
	session *picture.TerminalSession
	pic     *picture.Picture
	kitty   bool // Kitty selected after discovery

	width, height int
	cols, rows    int // full prepared picture
	view          picture.Viewport
	panX, panY    int // viewport offset from the centered position

	// Producer state. request identifies the current render; invalidation
	// increments it so results from obsolete renders are dropped. At most one
	// render runs at a time, and a cancelled render still counts as in flight
	// until its result returns, bounding concurrency to one goroutine.
	request   uint64
	inflight  bool
	cancel    context.CancelFunc
	awaiting  bool   // a submitted frame has not been presented yet
	submitted uint64 // picture generation of the awaited frame
	paused    bool
	frames    uint64 // accepted frames; drives the scene phase
	lastStart time.Time
	interval  time.Duration
	geometry  picture.RasterGeometry
	took      time.Duration
	err       error

	resolution int // index into resolutions
	keeping    bool
	finalView  string
}

func newModel(opts picture.TerminalOptions) *model {
	session := picture.NewTerminalSession(opts)
	m := &model{
		session:    session,
		interval:   40 * time.Millisecond,
		resolution: 1,
	}
	m.pic = picture.New(picture.Options{
		Session:  session,
		StableID: true,
		Render: picture.Config{
			Fit:                   picture.FitFill,
			KittyMedium:           picture.KittyMediumSharedMemory,
			KittyFormat:           picture.KittyFormatZlib,
			KittyResolutionFactor: resolutions[m.resolution],
		},
	})
	m.geometry = m.pic.RasterGeometry()
	return m
}

func (m *model) Init() tea.Cmd { return m.pic.Init() }

func (m *model) HelpKeys() demohelp.Keys {
	if m.keeping {
		return demohelp.Keys{}
	}
	kitty := m.session.Snapshot().Kitty == picture.KittyCapabilitySupported
	return demohelp.Framebuffer(m.paused, kitty, m.pic.Mode() == picture.PictureKitty)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.keeping {
		return m, m.pic.Update(msg)
	}
	var cmds []tea.Cmd
	switch v := msg.(type) {
	case tea.KeyPressMsg:
		if cmd, done := m.handleKey(v); done {
			return m, cmd
		} else if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		cmds = append(cmds, m.layout())
	case frameMsg:
		cmds = append(cmds, m.handleFrame(v))
	case picture.PresentedMsg:
		if v.Owner == m.pic && m.awaiting && v.Generation >= m.submitted {
			m.awaiting = false
			cmds = append(cmds, m.start())
		}
	case picture.ErrorMsg:
		if v.Owner == m.pic && v.Generation >= m.submitted {
			// Stop producing until a geometry, resolution, or playback change.
			m.awaiting = false
			m.err = v.Err
		}
	}
	cmds = append(cmds, m.pic.Update(msg))
	if event, ok := msg.(picture.TerminalSessionMsg); ok && event.Session == m.session && !m.kitty &&
		m.session.Snapshot().Kitty == picture.KittyCapabilitySupported {
		m.kitty = true
		cmds = append(cmds, m.pic.SetMode(picture.PictureKitty))
	}
	// Any change to the raster target makes an in-flight render obsolete.
	if g := m.pic.RasterGeometry(); g != m.geometry {
		m.geometry = g
		cmds = append(cmds, m.invalidate())
	}
	return m, tea.Batch(cmds...)
}

func (m *model) handleKey(press tea.KeyPressMsg) (tea.Cmd, bool) {
	keys := m.HelpKeys()
	switch {
	case key.Matches(press, keys.Keep):
		// Preserve the displayed layout, replacing help with a prompt row.
		content := m.View().Content
		m.finalView = content[:strings.LastIndex(content, "\n")+1]
		m.keeping = true
		m.stop()
		m.session.Close()
		return tea.Sequence(m.pic.Keep(), tea.Quit), true
	case key.Matches(press, keys.Quit):
		m.stop()
		m.session.Close()
		return tea.Sequence(m.pic.Close(), tea.Quit), true
	case key.Matches(press, keys.Play):
		m.paused = !m.paused
		return m.invalidate(), false
	case key.Matches(press, keys.Sharper), key.Matches(press, keys.Softer):
		next := m.resolution + 1
		if key.Matches(press, keys.Softer) {
			next = m.resolution - 1
		}
		if next < 0 || next >= len(resolutions) {
			return nil, false
		}
		m.resolution = next
		// Run the command even though a new frame follows: it may clear an
		// invalid-factor error or schedule work that the frame supersedes.
		return m.pic.SetResolution(resolutions[next]), false
	case key.Matches(press, keys.Toggle):
		mode := picture.PictureKitty
		if m.pic.Mode() == picture.PictureKitty {
			mode = picture.PictureGlyph
		}
		return m.pic.SetMode(mode), false
	case key.Matches(press, keys.Up):
		m.panY -= panStepY
	case key.Matches(press, keys.Down):
		m.panY += panStepY
	case key.Matches(press, keys.Left):
		m.panX -= panStepX
	case key.Matches(press, keys.Right):
		m.panX += panStepX
	case key.Matches(press, keys.Center):
		m.panX, m.panY = 0, 0
	default:
		return nil, false
	}
	// Panning only re-addresses prepared cells: no preparation, encoding,
	// transmission, or producer invalidation.
	m.applyViewport()
	return nil, false
}

// layout sizes the prepared picture from the terminal size and positions the
// viewport. A zero-sized area prepares nothing.
func (m *model) layout() tea.Cmd {
	vw, vh := m.width, m.height-headerRows-helpRows
	if vw <= 0 || vh <= 0 {
		m.cols, m.rows = 0, 0
		m.view = picture.Viewport{}
		m.pic.SetViewport(m.view)
		return m.pic.SetSize(0, 0)
	}
	m.cols, m.rows = vw+2*(vw/overscan), vh+2*(vh/overscan)
	m.view.Width, m.view.Height = vw, vh
	m.applyViewport()
	return m.pic.SetSize(m.cols, m.rows)
}

func (m *model) applyViewport() {
	if m.view.Width <= 0 || m.view.Height <= 0 {
		return
	}
	m.panX = max(-(m.cols-m.view.Width)/2, min((m.cols-m.view.Width+1)/2, m.panX))
	m.panY = max(-(m.rows-m.view.Height)/2, min((m.rows-m.view.Height+1)/2, m.panY))
	m.view.X = (m.cols-m.view.Width)/2 + m.panX
	m.view.Y = (m.rows-m.view.Height)/2 + m.panY
	m.pic.SetViewport(m.view)
}

// invalidate rejects the current render and any awaited presentation, then
// starts a fresh render when one can run.
func (m *model) invalidate() tea.Cmd {
	m.request++
	if m.cancel != nil {
		m.cancel()
	}
	m.awaiting = false
	m.err = nil
	return m.start()
}

func (m *model) stop() {
	m.request++
	if m.cancel != nil {
		m.cancel()
	}
	m.awaiting = false
}

// start launches one render for the current geometry unless the producer is
// paused, stopped by an error, already rendering, or waiting for presentation.
func (m *model) start() tea.Cmd {
	if m.keeping || m.paused || m.inflight || m.awaiting || m.err != nil {
		return nil
	}
	g := m.pic.RasterGeometry()
	if g.Pixels.X <= 0 || g.Pixels.Y <= 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.inflight = true
	request := m.request
	phase := float64(m.frames) * phaseStep
	due := m.lastStart.Add(m.interval)
	return func() tea.Msg {
		if wait := time.Until(due); wait > 0 || m.frames > 0 {
			if !demohelp.Wait(ctx, max(0, wait)) {
				return frameMsg{request: request, geometry: g, err: ctx.Err()}
			}
		}
		started := time.Now()
		img, err := renderScene(ctx, g.Pixels.X, g.Pixels.Y, g.DisplayAspect, phase)
		return frameMsg{request, g, img, time.Since(started), err}
	}
}

func (m *model) handleFrame(f frameMsg) tea.Cmd {
	if f.request != m.request {
		// Obsolete: its request was invalidated while it ran. The slot is free.
		m.inflight = false
		return m.start()
	}
	m.inflight = false
	if f.err != nil || f.geometry != m.pic.RasterGeometry() {
		return m.start()
	}
	m.frames++
	m.took = f.took
	m.lastStart = time.Now()
	m.awaiting = true
	cmd := m.pic.SetImageImmutable(f.img)
	m.submitted = m.pic.Status().Generation
	return cmd
}

func (m *model) View() tea.View {
	if m.keeping {
		return tea.NewView(m.finalView)
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).Render("Framebuffer")
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	header := title + "  " + m.statusLine()
	detail := dim.Render(m.detailLine())
	body := m.pic.View()
	if m.view.Height > 0 {
		// Keep the layout stable before the first frame is presented.
		lines := strings.Count(body, "\n") + 1
		if body == "" {
			lines = 0
		}
		if lines < m.view.Height {
			body += strings.Repeat("\n", m.view.Height-lines)
		}
	}
	v := tea.NewView(header + "\n" + detail + "\n" + body + "\n" + dim.Render(m.HelpKeys().View()))
	v.AltScreen = true
	return v
}

func (m *model) statusLine() string {
	g := m.pic.RasterGeometry()
	backend := "glyph"
	if m.pic.Mode() == picture.PictureKitty {
		s := m.pic.Status()
		medium := "direct"
		if s.Medium == picture.KittyMediumSharedMemory {
			medium = "shared memory"
		}
		backend = fmt.Sprintf("kitty %s/%s", s.Format, medium)
		if s.Presented == 0 {
			backend = "kitty (first frame pending)"
		}
	} else if m.session.Snapshot().Kitty == picture.KittyCapabilityUnknown {
		backend = "glyph (probing kitty)"
	}
	resolution := "cols×2rows (glyph)"
	if m.pic.Mode() == picture.PictureKitty {
		resolution = fmt.Sprintf("%.2f", resolutions[m.resolution])
	}
	state := "playing"
	if m.paused {
		state = "paused"
	}
	if m.err != nil {
		state = "error: " + m.err.Error()
	}
	return fmt.Sprintf("%s · target %d×%d px · resolution %s · %s", backend, g.Pixels.X, g.Pixels.Y, resolution, state)
}

func (m *model) detailLine() string {
	return fmt.Sprintf("viewport %d,%d of %d×%d cells · aspect %.2f · frame %d · render %s",
		m.view.X, m.view.Y, m.cols, m.rows, m.geometry.DisplayAspect, m.frames, m.took.Round(100*time.Microsecond))
}

func main() {
	m := newModel(picture.TerminalOptions{})
	// Local resources are also released on abnormal program exit.
	defer func() { _ = m.pic.Close() }()
	defer m.session.Close()
	if err := demohelp.Run(m, "Framebuffer"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
