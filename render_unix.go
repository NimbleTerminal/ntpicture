//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package ntpicture

import (
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

func openControllingTerminal() (int, error) {
	return unix.Open("/dev/tty", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
}

func readTerminalGeometry() (g terminalGeometry) {
	fd, err := openControllingTerminal()
	if err != nil {
		return g
	}
	defer func() { _ = unix.Close(fd) }()
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 || ws.Row == 0 {
		return g
	}
	g.cols, g.rows = int(ws.Col), int(ws.Row)
	// Many terminals leave the pixel fields zero.
	if w, h := int(ws.Xpixel)/g.cols, int(ws.Ypixel)/g.rows; w > 0 && h > 0 {
		g.cellW, g.cellH = w, h
	}
	return g
}

// probeTerminal asks the controlling terminal directly, so it works when
// stdin or stdout is redirected. It uses select because /dev/tty supports
// neither kqueue nor poll on macOS.
func probeTerminal(tmux bool, timeout time.Duration) ProbeResult {
	probe := NewProbe(ProbeOptions{TmuxPassthrough: &tmux})
	fd, err := openControllingTerminal()
	if err != nil {
		return ProbeResult{Kitty: KittyCapabilityUnsupported}
	}
	defer func() { _ = unix.Close(fd) }()
	state, err := term.MakeRaw(uintptr(fd))
	if err != nil {
		return ProbeResult{Kitty: KittyCapabilityUnsupported}
	}
	defer func() { _ = term.Restore(uintptr(fd), state) }()
	if _, err := unix.Write(fd, []byte(probe.Query())); err != nil {
		return ProbeResult{Kitty: KittyCapabilityUnsupported}
	}
	deadline := time.Now().Add(timeout)
	chunk := make([]byte, 256)
	for !probe.Done() {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		var fds unix.FdSet
		fds.Set(fd)
		tv := unix.NsecToTimeval(remaining.Nanoseconds())
		n, err := unix.Select(fd+1, &fds, nil, nil, &tv)
		if err == unix.EINTR {
			continue
		}
		if err != nil || n == 0 {
			break
		}
		n, err = unix.Read(fd, chunk)
		if err == unix.EINTR {
			continue
		}
		if err != nil || n <= 0 {
			break
		}
		// Keys typed during the probe are dropped: nothing here reads them.
		probe.Feed(chunk[:n])
	}
	probe.Timeout()
	return probe.Result()
}
