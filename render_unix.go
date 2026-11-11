//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package ntpicture

import (
	"time"

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
//
// While it waits, the terminal has echo, line buffering, and signal keys off;
// output processing stays on, so concurrent output is unaffected. A signal key
// typed meanwhile (Ctrl+C, Ctrl+\, Ctrl+Z) ends the wait, and its signal is
// sent once the terminal is restored. Leaving signal keys on instead would let
// Ctrl+C kill the process with the terminal still not echoing.
func probeTerminal(tmux bool, timeout time.Duration) ProbeResult {
	unsupported := ProbeResult{Kitty: KittyCapabilityUnsupported}
	fd, err := openControllingTerminal()
	if err != nil {
		return unsupported
	}
	defer func() { _ = unix.Close(fd) }()
	// select cannot watch a descriptor this high, and FdSet.Set would panic.
	if fd >= unix.FD_SETSIZE {
		return unsupported
	}
	// A background job that changes terminal settings is stopped by SIGTTOU,
	// or, if it ignores that, changes them under the foreground program.
	if pgrp, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP); err != nil || pgrp != unix.Getpgrp() {
		return unsupported
	}
	saved, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return unsupported
	}
	quiet := *saved
	quiet.Lflag &^= unix.ICANON | unix.ECHO | unix.ISIG
	quiet.Cc[unix.VMIN], quiet.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &quiet); err != nil {
		return unsupported
	}
	probe := NewProbe(ProbeOptions{TmuxPassthrough: &tmux})
	signal := probeReplies(fd, probe, saved, timeout)
	_ = unix.IoctlSetTermios(fd, ioctlSetTermios, saved)
	if signal != 0 {
		// To the foreground process group, as the terminal would have.
		_ = unix.Kill(0, signal)
	}
	return probe.Result()
}

// probeReplies writes the query and feeds replies to probe until it is done or
// timeout passes. It returns the signal for a signal key typed meanwhile.
func probeReplies(fd int, probe *Probe, saved *unix.Termios, timeout time.Duration) unix.Signal {
	defer probe.Timeout()
	if _, err := unix.Write(fd, []byte(probe.Query())); err != nil {
		return 0
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
		// Other keys typed during the probe are dropped: nothing here reads them.
		keys, _ := probe.Feed(chunk[:n])
		if signal := signalKey(keys, saved); signal != 0 {
			return signal
		}
	}
	return 0
}

// signalKey returns the signal the terminal would have raised for the first
// signal key in keys, going by the settings in force before the probe.
func signalKey(keys []byte, saved *unix.Termios) unix.Signal {
	if saved.Lflag&unix.ISIG == 0 {
		return 0
	}
	for _, key := range keys {
		if key == 0 || key == 0xff { // disabled control characters
			continue
		}
		switch key {
		case saved.Cc[unix.VINTR]:
			return unix.SIGINT
		case saved.Cc[unix.VQUIT]:
			return unix.SIGQUIT
		case saved.Cc[unix.VSUSP]:
			return unix.SIGTSTP
		}
	}
	return 0
}
