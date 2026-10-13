// A standalone Picture with native Kitty detection and glyph fallback.
package main

import (
	"fmt"
	"image"
	"image/color"
	"os"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	nt "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/examples/internal/demohelp"
)

type model struct{ pic *nt.Picture }

func (m model) Init() tea.Cmd {
	img := image.NewRGBA(image.Rect(0, 0, 160, 80))
	for y := range 80 {
		for x := range 160 {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 255 / 159), uint8(y * 255 / 79), 140, 255})
		}
	}
	return tea.Batch(m.pic.Init(), m.pic.SetSize(40, 12), m.pic.SetImage(img))
}
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if press, ok := msg.(tea.KeyPressMsg); ok && key.Matches(press, m.HelpKeys().Quit) {
		return m, tea.Sequence(m.pic.Close(), tea.Quit)
	}
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		return m, tea.Batch(m.pic.Update(msg), m.pic.SetSize(max(0, size.Width), max(0, size.Height-1)))
	}
	cmd := m.pic.Update(msg)
	if cap, ok := msg.(nt.KittyCapabilityMsg); ok && cap.Capability == nt.KittyCapabilitySupported && m.pic.Mode() != nt.PictureKitty {
		return m, tea.Batch(cmd, m.pic.SetMode(nt.PictureKitty))
	}
	return m, cmd
}
func (m model) View() tea.View {
	v := tea.NewView(m.pic.View() + "\n" + m.HelpKeys().View())
	v.AltScreen = true
	return v
}
func (m model) HelpKeys() demohelp.Keys {
	return demohelp.New(false, nt.KittySupported() == nt.KittyCapabilitySupported)
}

func main() {
	p := nt.New(nt.Options{})
	defer p.Close() // Local resources are also released on abnormal program exit.
	if err := demohelp.Run(model{p}, "Still image"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
