// Adapted from NTCharts examples/picture/simple (MIT, Neomantra Corp).
package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	_ "image/png" // decode the embedded PNG asset
	"os"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	picture "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/examples/internal/demohelp"
)

//go:embed Fuji-01.png
var fujiPNG []byte

type model struct {
	pic           *picture.Picture
	initCmd       tea.Cmd
	width, height int
}

func initialModel() (model, error) {
	pic := picture.New(picture.Options{Render: picture.Config{KittyMedium: picture.KittyMediumSharedMemory}})

	img, _, err := image.Decode(bytes.NewReader(fujiPNG))
	if err != nil {
		return model{}, fmt.Errorf("decode embedded image: %w", err)
	}
	return model{pic: pic, initCmd: pic.SetImage(img)}, nil
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.pic.Init(), m.initCmd)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.HelpKeys().Quit):
			return m, tea.Sequence(m.pic.Close(), tea.Quit)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Reserve one bottom row for the footer.
		if c := m.pic.SetSize(m.width, max(0, m.height-1)); c != nil {
			cmds = append(cmds, c)
		}
	}

	if c := m.pic.Update(msg); c != nil {
		cmds = append(cmds, c)
	}

	if cap, ok := msg.(picture.KittyCapabilityMsg); ok && cap.Capability == picture.KittyCapabilitySupported && m.pic.Mode() != picture.PictureKitty {
		cmds = append(cmds, m.pic.SetMode(picture.PictureKitty))
	}
	return m, tea.Batch(cmds...)
}

func (m model) View() tea.View {
	footer := lipgloss.NewStyle().
		Width(m.width).
		Foreground(lipgloss.Color("242")).
		Render(m.HelpKeys().View())

	imageBox := lipgloss.Place(m.width, max(0, m.height-1),
		lipgloss.Center, lipgloss.Center, m.pic.View())

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, imageBox, footer))
	v.AltScreen = true
	return v
}

func (m model) HelpKeys() demohelp.Keys {
	return demohelp.New(false, picture.KittySupported() == picture.KittyCapabilitySupported)
}

func main() {
	m, err := initialModel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer m.pic.Close()
	if err := demohelp.Run(m, "Embedded image"); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
