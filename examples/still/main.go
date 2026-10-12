// A standalone Picture with native Kitty detection and glyph fallback.
package main

import (
	"fmt"
	"image"
	"image/color"
	"os"

	tea "charm.land/bubbletea/v2"
	nt "nimbleterminal.dev/ntpicture"
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
	if key, ok := msg.(tea.KeyPressMsg); ok && (key.String() == "q" || key.String() == "ctrl+c") {
		return m, tea.Sequence(m.pic.Close(), tea.Quit)
	}
	cmd := m.pic.Update(msg)
	if nt.KittySupported() == nt.KittyCapabilitySupported && m.pic.Mode() != nt.PictureKitty {
		return m, tea.Batch(cmd, m.pic.SetMode(nt.PictureKitty))
	}
	return m, cmd
}
func (m model) View() tea.View {
	v := tea.NewView(m.pic.View() + "\nq: quit")
	v.AltScreen = true
	return v
}
func main() {
	p := nt.New(nt.Options{})
	defer p.Close() // Local resources are also released on abnormal program exit.
	if _, err := tea.NewProgram(model{p}).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
