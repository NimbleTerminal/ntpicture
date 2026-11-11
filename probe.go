package ntpicture

import (
	"bytes"
	"strconv"
	"strings"
)

// ProbeOptions configures a Probe. The zero value reads the environment.
type ProbeOptions struct {
	// Kitty states Kitty graphics support, so the probe does not ask. When
	// it is KittyCapabilityUnknown the NT_KITTY environment variable
	// is used, and the terminal is asked if that is unset too.
	Kitty KittyCapability

	// TmuxPassthrough wraps the Kitty query for tmux. Nil reads TMUX and
	// NT_TMUX.
	TmuxPassthrough *bool
}

// ProbeResult is what a Probe has learned so far.
type ProbeResult struct {
	// Kitty is KittyCapabilityUnknown while the terminal may still answer.
	// Once the probe is done it is Supported or Unsupported.
	Kitty KittyCapability

	// CellPixelWidth and CellPixelHeight are the terminal's cell size in
	// pixels, or zero when the terminal did not report it.
	CellPixelWidth, CellPixelHeight int
}

// Probe detects Kitty graphics support and the cell size from any event loop.
// It does no I/O: the host writes Query to the terminal, passes terminal input
// to Feed, and reads Result. Render and Print use it for their own detection;
// Bubble Tea programs use TerminalSession instead.
//
//	p := ntpicture.NewProbe(ntpicture.ProbeOptions{})
//	io.WriteString(tty, p.Query()) // the terminal must be in raw mode
//	for !p.Done() {
//		// read input, or after a timeout: input = p.Timeout()
//		input, _ = p.Feed(input)
//		// handle input as usual; probe replies have been removed
//	}
//	res := p.Result()
//
// Pass the result to RenderOptions.Kitty, CellPixelWidth, and CellPixelHeight.
// A Probe is single use and not safe for concurrent use.
type Probe struct {
	tmux     bool
	known    KittyCapability
	answered bool
	done     bool
	cellW    int
	cellH    int
	held     []byte
}

// Limits on input held back while a reply may still be incomplete.
const (
	probeMaxAPC = 256
	probeMaxCSI = 64
)

// NewProbe snapshots the environment overrides.
func NewProbe(opts ProbeOptions) *Probe {
	tmux, envKitty, _ := envTerminalConfig()
	if opts.TmuxPassthrough != nil {
		tmux = *opts.TmuxPassthrough
	}
	known := opts.Kitty
	if known != KittyCapabilitySupported && known != KittyCapabilityUnsupported {
		known = envKitty
	}
	return &Probe{tmux: tmux, known: known}
}

// Query returns the bytes to write to the terminal, once. It asks for Kitty
// graphics support unless that is already known, then the cell size, then
// device attributes: every terminal answers the last, which ends the probe
// without waiting for a timeout. The terminal must not echo or line-buffer its
// replies, so put it in raw mode first.
func (p *Probe) Query() string {
	var kitty string
	if p.known == KittyCapabilityUnknown {
		kitty = kittyTransport{tmux: p.tmux}.wrap(buildKittyQueryAPC(kittyProbeID))
	}
	return kitty + "\x1b[16t\x1b[c"
}

// Feed takes terminal input as it arrives and returns it with the probe's
// replies removed, for the host to handle as ordinary input. The result may
// alias input. Replies split across reads are held until complete, so rest can
// lag by a partial escape sequence. done reports that the terminal has
// answered; after that Feed returns its input unchanged.
func (p *Probe) Feed(input []byte) (rest []byte, done bool) {
	if p.done {
		return input, true
	}
	buf := input
	if len(p.held) > 0 {
		buf = append(p.held, input...)
		p.held = nil
	}
	i := bytes.IndexByte(buf, 0x1b)
	if i < 0 {
		return buf, false
	}
	rest = append(make([]byte, 0, len(buf)), buf[:i]...)
	for i < len(buf) {
		if buf[i] != 0x1b {
			next := bytes.IndexByte(buf[i:], 0x1b)
			if next < 0 {
				next = len(buf) - i
			}
			rest = append(rest, buf[i:i+next]...)
			i += next
			continue
		}
		n, ours := p.reply(buf[i:])
		switch {
		case n == 0: // incomplete: wait for more
			p.held = append([]byte(nil), buf[i:]...)
			return rest, false
		case !ours:
			rest = append(rest, buf[i:i+n]...)
		}
		i += n
		if p.done {
			return append(rest, buf[i:]...), true
		}
	}
	return rest, false
}

// reply examines the escape sequence at the start of b. It returns its length,
// or 0 when more input is needed to tell, and whether it was a probe reply.
func (p *Probe) reply(b []byte) (n int, ours bool) {
	if len(b) < 2 {
		return 0, false
	}
	switch b[1] {
	case '_': // APC: Kitty graphics replies are ESC _ G <keys> ; <message> ESC \
		end := bytes.Index(b, []byte("\x1b\\"))
		if end < 0 {
			if len(b) > probeMaxAPC {
				return 1, false
			}
			return 0, false
		}
		n = end + 2
		keys, message, _ := strings.Cut(string(b[2:end]), ";")
		if !strings.HasPrefix(keys, "G") {
			return n, false
		}
		for key := range strings.SplitSeq(keys[1:], ",") {
			if key == "i="+strconv.Itoa(kittyProbeID) {
				p.answered = p.answered || message == "OK"
				return n, true
			}
		}
		return n, false
	case '[': // CSI: parameter bytes, then one final byte
		end := 2
		for end < len(b) && (b[end] >= '0' && b[end] <= '9' || b[end] == ';' || b[end] == '?') {
			end++
		}
		if end == len(b) {
			if len(b) > probeMaxCSI {
				return 1, false
			}
			return 0, false
		}
		n = end + 1
		params := string(b[2:end])
		switch {
		case b[end] == 'c' && strings.HasPrefix(params, "?"):
			p.done = true
			return n, true
		case b[end] == 't' && strings.HasPrefix(params, "6;"):
			fields := strings.Split(params, ";")
			if len(fields) != 3 {
				return n, false
			}
			h, _ := strconv.Atoi(fields[1])
			w, _ := strconv.Atoi(fields[2])
			if w > 0 && h > 0 && w <= 1024 && h <= 1024 {
				p.cellW, p.cellH = w, h
			}
			return n, true
		}
		return n, false
	}
	return 1, false
}

// Timeout ends a probe the terminal has not finished answering. It returns any
// input Feed was still holding, for the host to handle as ordinary input.
// Anything not yet answered is reported as unsupported or unknown. Waiting one
// second before calling it is plenty.
func (p *Probe) Timeout() (rest []byte) {
	rest, p.held = p.held, nil
	p.done = true
	return rest
}

// Done reports whether the terminal has answered or Timeout was called.
func (p *Probe) Done() bool { return p.done }

// Result returns what is known so far. It is final once Done reports true.
func (p *Probe) Result() ProbeResult {
	res := ProbeResult{Kitty: p.known, CellPixelWidth: p.cellW, CellPixelHeight: p.cellH}
	if res.Kitty == KittyCapabilityUnknown {
		switch {
		case p.answered:
			res.Kitty = KittyCapabilitySupported
		case p.done:
			res.Kitty = KittyCapabilityUnsupported
		}
	}
	return res
}
