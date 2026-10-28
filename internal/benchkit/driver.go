package benchkit

import (
	"fmt"
	"io"
	"reflect"
	"time"

	tea "charm.land/bubbletea/v2"
	nt "nimbleterminal.dev/ntpicture"
)

type Component interface {
	Update(tea.Msg) tea.Cmd
	View() string
}

// Driver runs commands sequentially, including Batch and Bubble Tea's private
// sequence message. Timing includes command execution and synchronous writes,
// but excludes a real Bubble Tea scheduler/renderer and terminal display ACKs.
type Driver struct {
	Output              io.Writer
	Draw                bool
	APCBytes, GridBytes int64
	WriteTime           time.Duration
	Presented           int
	view                string
}

func (d *Driver) write(s string, grid bool) error {
	start := time.Now()
	n, err := io.WriteString(d.Output, s)
	d.WriteTime += time.Since(start)
	if grid {
		d.GridBytes += int64(n)
	} else {
		d.APCBytes += int64(n)
	}
	if err == nil && n != len(s) {
		err = io.ErrShortWrite
	}
	return err
}

func (d *Driver) Run(c Component, cmd tea.Cmd) error {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	// Both BatchMsg and the private sequence message are slices of tea.Cmd.
	value := reflect.ValueOf(msg)
	if value.Kind() == reflect.Slice {
		for i := 0; i < value.Len(); i++ {
			child, ok := value.Index(i).Interface().(tea.Cmd)
			if !ok {
				return fmt.Errorf("unsupported command message %T", msg)
			}
			if err := d.Run(c, child); err != nil {
				return err
			}
		}
		return nil
	}
	switch m := msg.(type) {
	case tea.RawMsg:
		text, ok := m.Msg.(string)
		if !ok {
			return fmt.Errorf("unsupported raw message %T", m.Msg)
		}
		if err := d.write(text, false); err != nil {
			return err
		}
	case nt.ErrorMsg:
		return m.Err
	case nt.PresentedMsg:
		d.Presented++
	}
	next := c.Update(msg)
	if d.Draw {
		view := c.View()
		if view != d.view {
			if err := d.write("\x1b[H"+view, true); err != nil {
				return err
			}
			d.view = view
		}
	}
	return d.Run(c, next)
}
