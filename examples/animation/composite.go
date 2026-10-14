package main

import (
	"context"
	"fmt"
	"image"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	picture "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"
	"nimbleterminal.dev/ntpicture/examples/internal/demohelp"
	"nimbleterminal.dev/ntpicture/playback"
)

type compositeModel struct {
	controller *playback.Controller
	session    *picture.TerminalSession
	keeping    bool
	final      string
}

func initialCompositeModel() (*compositeModel, error) {
	regions := make([]animation.Region, 0, 2)
	for i, data := range [][]byte{gifData, webpData} {
		_, p, err := animation.DecodeContext(context.Background(), data, animation.Options{Animate: true})
		if err != nil {
			return nil, err
		}
		regions = append(regions, animation.Region{Key: fmt.Sprint(i), Player: p, Rect: image.Rect(i*240, 0, (i+1)*240, 180)})
	}
	source, err := animation.NewComposite(context.Background(), image.NewRGBA(image.Rect(0, 0, 480, 180)), regions, nil)
	if err != nil {
		return nil, err
	}
	session := picture.NewTerminalSession(picture.TerminalOptions{})
	return &compositeModel{session: session, controller: playback.NewSource(source, playback.Options{Picture: picture.Options{Session: session, Render: picture.Config{KittyMedium: picture.KittyMediumSharedMemory}}})}, nil
}
func (m *compositeModel) Init() tea.Cmd { return m.controller.Init() }
func (m *compositeModel) HelpKeys() demohelp.Keys {
	if m.keeping {
		return demohelp.Keys{}
	}
	keys := demohelp.Animation(m.controller.Status().Paused)
	keys.Previous.SetEnabled(false)
	keys.Next.SetEnabled(false)
	return keys
}
func (m *compositeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.keeping {
		return m, m.controller.Update(msg)
	}
	cmds := []tea.Cmd{m.controller.Update(msg)}
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		cmds = append(cmds, m.controller.SetSize(v.Width, max(0, v.Height-2)))
	case picture.TerminalSessionMsg:
		if v.Session == m.session && m.session.Snapshot().Kitty == picture.KittyCapabilitySupported {
			cmds = append(cmds, m.controller.SetMode(picture.PictureKitty))
		}
	case tea.KeyPressMsg:
		keys := m.HelpKeys()
		switch {
		case key.Matches(v, keys.Play):
			cmds = append(cmds, m.controller.Pause(!m.controller.Status().Paused))
		case key.Matches(v, keys.Restart):
			cmds = append(cmds, m.controller.Restart())
		case key.Matches(v, keys.Keep):
			m.final = m.controller.View() + "\n\n"
			m.keeping = true
			m.session.Close()
			return m, tea.Sequence(m.controller.Keep(), tea.Quit)
		case key.Matches(v, keys.Quit):
			m.session.Close()
			return m, tea.Sequence(m.controller.Close(), tea.Quit)
		}
	}
	return m, tea.Batch(cmds...)
}
func (m *compositeModel) View() tea.View {
	if m.keeping {
		return tea.NewView(m.final)
	}
	label := "GIF + WebP: one composed picture"
	if m.controller.Status().Paused {
		label += " paused"
	}
	if err := m.controller.Status().Err; err != nil {
		label = err.Error()
	}
	v := tea.NewView(label + "\n" + m.controller.View() + "\n" + m.HelpKeys().View())
	v.AltScreen = true
	return v
}
