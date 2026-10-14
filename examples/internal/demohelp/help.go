// Package demohelp shares the examples' terminal help with booba action buttons.
package demohelp

import (
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	booba "github.com/NimbleMarkets/go-booba"
	"github.com/NimbleMarkets/go-booba/actions"
	"github.com/NimbleMarkets/go-booba/actions/bubblehelp"
)

type Keys struct {
	Quit, Up, Down, Left, Right, Toggle key.Binding
	Play, Previous, Next, Restart, Keep key.Binding
	Center, Sharper, Softer             key.Binding
	animation                           bool
	grid                                bool
	framebuffer                         bool
}

func New(grid, kitty bool) Keys {
	binding := func(label string, keys ...string) key.Binding {
		return key.NewBinding(key.WithKeys(keys...), key.WithHelp(keys[0], label))
	}
	k := Keys{
		Quit: binding("Quit", "q", "ctrl+c"),
		Up:   binding("Up", "up", "w", "k"), Down: binding("Down", "down", "s", "j"),
		Left: binding("Left", "left", "a", "h"), Right: binding("Right", "right", "d", "l"),
		Toggle: binding("Glyph / Kitty", "g"), grid: grid,
	}
	k.Toggle.SetEnabled(kitty)
	return k
}
func Animation(paused bool) Keys {
	k := New(false, false)
	k.animation = true
	label := "Pause"
	if paused {
		label = "Play"
	}
	k.Play = key.NewBinding(key.WithKeys("space"), key.WithHelp("space", label))
	k.Previous = key.NewBinding(key.WithKeys(","), key.WithHelp(",", "Previous frame"))
	k.Next = key.NewBinding(key.WithKeys("."), key.WithHelp(".", "Next frame"))
	k.Restart = key.NewBinding(key.WithKeys("home"), key.WithHelp("home", "First frame"))
	k.Keep = key.NewBinding(key.WithKeys("Q"), key.WithHelp("Q", "Keep screen & quit"))
	return k
}

// Framebuffer describes the continuously rendered demo. Movement keys pan the
// viewport. Resolution keys apply only while Kitty is selected, because glyph
// output stays at two pixel rows per cell; the rendering toggle is advertised
// only after Kitty support is confirmed.
func Framebuffer(paused, kitty, kittyMode bool) Keys {
	k := New(true, kitty)
	k.grid = false
	k.framebuffer = true
	label := "Pause"
	if paused {
		label = "Play"
	}
	k.Play = key.NewBinding(key.WithKeys("space"), key.WithHelp("space", label))
	k.Center = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "Center view"))
	k.Sharper = key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "Higher resolution"))
	k.Softer = key.NewBinding(key.WithKeys("["), key.WithHelp("[", "Lower resolution"))
	k.Sharper.SetEnabled(kittyMode)
	k.Softer.SetEnabled(kittyMode)
	k.Keep = key.NewBinding(key.WithKeys("Q"), key.WithHelp("Q", "Keep screen & quit"))
	return k
}
func (k Keys) ShortHelp() []key.Binding {
	if k.animation {
		return []key.Binding{k.Play, k.Previous, k.Next, k.Restart, k.Keep}
	}
	if k.framebuffer {
		return []key.Binding{k.Play, k.Up, k.Down, k.Left, k.Right, k.Center, k.Softer, k.Sharper, k.Toggle, k.Keep}
	}
	if k.grid {
		return []key.Binding{k.Up, k.Down, k.Left, k.Right, k.Toggle}
	}
	return []key.Binding{k.Quit}
}
func (k Keys) FullHelp() [][]key.Binding {
	if k.grid || k.animation || k.framebuffer {
		return [][]key.Binding{k.ShortHelp(), {k.Quit}}
	}
	return [][]key.Binding{{k.Quit}}
}
func (k Keys) View() string {
	h := help.New()
	if k.animation || k.framebuffer {
		// Compact terminal labels; browser buttons retain the full descriptions.
		for binding, label := range map[*key.Binding]string{
			&k.Previous: "Prev", &k.Next: "Next", &k.Restart: "First", &k.Keep: "Keep screen",
			&k.Center: "Center", &k.Softer: "Res-", &k.Sharper: "Res+",
		} {
			binding.SetHelp(binding.Help().Key, label)
		}
	}
	// Keep quit visible in the terminal even when the browser puts it under More.
	bindings := k.ShortHelp()
	if k.framebuffer {
		// One pan entry keeps the terminal help to a single row.
		pan := key.NewBinding(key.WithKeys("up", "down", "left", "right"), key.WithHelp("arrows", "Pan"))
		bindings = []key.Binding{k.Play, pan, k.Center, k.Softer, k.Sharper, k.Toggle, k.Keep}
	}
	if k.grid || k.animation || k.framebuffer {
		bindings = append(bindings, k.Quit)
	}
	return h.ShortHelpView(bindings)
}

type Model interface {
	tea.Model
	HelpKeys() Keys
}

func Run(m Model, title string) error {
	wrapped, err := bubblehelp.NewModel(m, func(inner tea.Model) bubblehelp.Description {
		return bubblehelp.Description{
			Context: strings.ReplaceAll(strings.ToLower(title), " ", "-"), Keys: inner.(Model).HelpKeys(),
			Status: actions.ProgramStatus{App: "ntpicture", Summary: title},
		}
	}, bubblehelp.ModelOptions{Adapter: bubblehelp.Options{Customize: func(_ key.Binding, a *actions.Action) error {
		first := 0
		a.Preferred = &first
		return nil
	}}, OnError: func(err error) { fmt.Fprintln(os.Stderr, err) }})
	if err != nil {
		return err
	}
	return booba.Run(wrapped)
}
