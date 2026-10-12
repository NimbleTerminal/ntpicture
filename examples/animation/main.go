// GIF and lazy WebP playback using the original motion fixtures from Gloss.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	picture "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"
	"nimbleterminal.dev/ntpicture/examples/internal/demohelp"
	"nimbleterminal.dev/ntpicture/playback"
)

//go:embed motion.gif
var gifData []byte

//go:embed motion.webp
var webpData []byte

type model struct {
	tracks [2]*playback.Controller
	width  int
	kitty  bool
}

func initialModel() (model, error) {
	m := model{}
	for i, data := range [][]byte{gifData, webpData} {
		_, p, err := animation.DecodeContext(context.Background(), data, animation.Options{Animate: true})
		if err != nil {
			return m, err
		}
		m.tracks[i] = playback.New(p, playback.Options{Picture: picture.Options{Render: picture.Config{KittyMedium: picture.KittyMediumSharedMemory}}})
	}
	return m, nil
}
func (m model) Init() tea.Cmd { return tea.Batch(m.tracks[0].Init(), m.tracks[1].Init()) }
func (m model) HelpKeys() demohelp.Keys {
	s := m.tracks[0].Status()
	return demohelp.Animation(s.Paused || s.Finished)
}
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if press, ok := msg.(tea.KeyPressMsg); ok {
		keys := m.HelpKeys()
		if key.Matches(press, keys.Quit) {
			return m, tea.Sequence(tea.Batch(m.tracks[0].Close(), m.tracks[1].Close()), tea.Quit)
		}
		paused := !m.tracks[0].Status().Paused
		for _, c := range m.tracks {
			s := c.Status()
			switch {
			case key.Matches(press, keys.Play):
				cmds = append(cmds, c.Pause(paused && !s.Finished))
			case key.Matches(press, keys.Previous):
				cmds = append(cmds, c.Seek(s.Player.Frame()-1))
			case key.Matches(press, keys.Next):
				cmds = append(cmds, c.Seek(s.Player.Frame()+1))
			case key.Matches(press, keys.Restart):
				cmds = append(cmds, c.Seek(1))
			}
		}
	}
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		for _, c := range m.tracks {
			cmds = append(cmds, c.SetSize(max(0, (size.Width-3)/2), max(0, size.Height-4)))
		}
	}
	for _, c := range m.tracks {
		cmds = append(cmds, c.Update(msg))
	}
	if !m.kitty && picture.KittySupported() == picture.KittyCapabilitySupported {
		m.kitty = true
		for _, c := range m.tracks {
			cmds = append(cmds, c.SetMode(picture.PictureKitty))
		}
	}
	return m, tea.Batch(cmds...)
}
func (m model) View() tea.View {
	var panes []string
	for i, c := range m.tracks {
		label := []string{"GIF", "WebP"}[i]
		s := c.Status()
		status := label + " closed"
		if s.Player != nil {
			status = fmt.Sprintf("%s %d/%d", label, s.Player.Frame(), s.Player.Frames())
		}
		if s.Paused {
			status += " paused"
		}
		if s.Err != nil {
			status += " error: " + s.Err.Error()
		}
		panes = append(panes, lipgloss.NewStyle().Width(max(1, (m.width-3)/2)).Render(status+"\n"+c.View()))
	}
	v := tea.NewView(lipgloss.JoinHorizontal(lipgloss.Top, panes[0], "   ", panes[1]) + "\n" + m.HelpKeys().View())
	v.AltScreen = true
	return v
}
func main() {
	m, err := initialModel()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer m.tracks[0].Close()
	defer m.tracks[1].Close()
	if err := demohelp.Run(m, "Animation"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
