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
	grid                                bool
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
func (k Keys) ShortHelp() []key.Binding {
	if k.grid {
		return []key.Binding{k.Up, k.Down, k.Left, k.Right, k.Toggle}
	}
	return []key.Binding{k.Quit}
}
func (k Keys) FullHelp() [][]key.Binding {
	if k.grid {
		return [][]key.Binding{k.ShortHelp(), {k.Quit}}
	}
	return [][]key.Binding{{k.Quit}}
}
func (k Keys) View() string {
	h := help.New()
	// Keep quit visible in the terminal even when the browser puts it under More.
	bindings := k.ShortHelp()
	if k.grid {
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
