package ntpicture

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// KittyCapability reports whether the host terminal supports the Kitty
// graphics protocol. Explicit TerminalSession instances track support
// independently for each connection; the compatibility helpers in
// nimbleterminal.dev/ntpicture/legacy share process-wide state.
type KittyCapability int8

const (
	// KittyCapabilityUnknown is the default state, before the probe has
	// completed. Entering Kitty mode is blocked in this state — both
	// Unknown and Unsupported keep Kitty escapes off the wire, so a
	// non-Kitty terminal never sees garbage. Kitty terminals typically
	// resolve to Supported within a few milliseconds.
	KittyCapabilityUnknown KittyCapability = iota

	// KittyCapabilitySupported means the terminal answered the Kitty
	// query (or was configured as supported) — Kitty graphics escapes will
	// render correctly.
	KittyCapabilitySupported

	// KittyCapabilityUnsupported means the probe timed out without a
	// response or the environment was not recognized as Kitty-capable.
	KittyCapabilityUnsupported
)

// kittyProbeID is the image ID used for the Kitty support probe.
// Chosen to be far above any sensible consumer-assigned ID so it can
// never collide with real images.
const kittyProbeID = 42069101

// kittyProbeTimeout is how long probing waits for a response before concluding
// the terminal does not support Kitty graphics.
const kittyProbeTimeout = 250 * time.Millisecond

var (
	kittyCap                   atomic.Int32 // holds KittyCapability values
	kittyEnvSignalled          atomic.Bool  // recorded result of the env preflight
	kittyProbeMu               sync.Mutex
	kittyProbed                atomic.Bool
	kittySharedProbed          atomic.Bool
	kittySharedAutoUnsupported atomic.Bool
)

// KittyCapabilityMsg reports probe resolution on the default terminal session.
type KittyCapabilityMsg struct{ Capability KittyCapability }

func kittyCapabilityCommand(c KittyCapability) tea.Cmd {
	return func() tea.Msg {
		return KittyCapabilityMsg{Capability: c}
	}
}

func kittyEnvSignalledFn() bool {
	ensureLegacyEnv()
	return kittyEnvSignalled.Load()
}

func kittySupported() KittyCapability {
	ensureLegacyEnv()
	return KittyCapability(kittyCap.Load())
}

// Errors returned by Picture.SetMode and legacy.KittyUnavailable explaining
// why entering Kitty mode is blocked. Compare with errors.Is.
var (
	// ErrKittyProbePending means the Kitty probe has not resolved yet. It
	// resolves within a few milliseconds of Init in a Kitty-capable
	// terminal, so a later SetMode will succeed.
	ErrKittyProbePending = errors.New("kitty graphics probe has not resolved yet; try again shortly")

	// ErrKittyNotDetected means the environment did not look Kitty-aware,
	// so no probe was sent (or the capability was set to unsupported by
	// NTPICTURE_KITTY or TerminalOptions.Kitty).
	ErrKittyNotDetected = errors.New("terminal not recognized as Kitty-capable; set NTPICTURE_KITTY=supported or configure TerminalOptions.Kitty to override")

	// ErrKittyProbeTimeout means a probe was sent but the terminal did not
	// answer in time, which usually points to a multiplexer or transport
	// that drops the query.
	ErrKittyProbeTimeout = errors.New("terminal did not answer the Kitty graphics probe; set NTPICTURE_KITTY=supported or configure TerminalOptions.Kitty to override")
)

func kittyUnavailable() error {
	return defaultSession().unavailable()
}

func forceKittyCapability(c KittyCapability) {
	ensureLegacyEnv()
	defaultTerminalSession.setKittyForced(c != KittyCapabilityUnknown)
	kittyCap.Store(int32(c))
	switch c {
	case KittyCapabilityUnsupported:
		if kittySharedCap.CompareAndSwap(int32(KittyCapabilityUnknown), int32(KittyCapabilityUnsupported)) {
			kittySharedAutoUnsupported.Store(true)
		}
		_ = kittySharedProbe.Swap(nil).Unlink()
	case KittyCapabilitySupported:
		if kittySharedAutoUnsupported.CompareAndSwap(true, false) {
			kittySharedCap.CompareAndSwap(int32(KittyCapabilityUnsupported), int32(KittyCapabilityUnknown))
		}
	}
}

func resetKittyProbeState() {
	kittyProbeMu.Lock()
	kittyProbed.Store(false)
	kittySharedProbed.Store(false)
	kittySharedAutoUnsupported.Store(false)
	_ = kittySharedProbe.Swap(nil).Unlink()
	kittyProbeMu.Unlock()
}

type kittyProbeTickMsg struct{}

func queryKittySupport() tea.Cmd {
	ensureLegacyEnv()
	var cmds []tea.Cmd
	kittyProbeMu.Lock()
	var kittyQuery, sharedQuery string
	switch kittySupported() {
	case KittyCapabilitySupported:
		if !kittySharedProbed.Load() {
			if !kittySharedProbeable() {
				if kittySharedCap.CompareAndSwap(int32(KittyCapabilityUnknown), int32(KittyCapabilityUnsupported)) {
					kittySharedAutoUnsupported.Store(true)
				}
			} else {
				sharedQuery = kittySharedQueryAPC()
				if sharedQuery != "" {
					kittySharedProbed.Store(true)
				} else if kittySharedCap.CompareAndSwap(int32(KittyCapabilityUnknown), int32(KittyCapabilityUnsupported)) {
					kittySharedAutoUnsupported.Store(true)
				}
			}
		}
	case KittyCapabilityUnsupported:
		if kittySharedCap.CompareAndSwap(int32(KittyCapabilityUnknown), int32(KittyCapabilityUnsupported)) {
			kittySharedAutoUnsupported.Store(true)
		}
	default:
		if !kittyProbed.Load() {
			if !kittyEnvSignal() {
				kittyEnvSignalled.Store(false)
				kittyCap.CompareAndSwap(int32(KittyCapabilityUnknown), int32(KittyCapabilityUnsupported))
				if kittySharedCap.CompareAndSwap(int32(KittyCapabilityUnknown), int32(KittyCapabilityUnsupported)) {
					kittySharedAutoUnsupported.Store(true)
				}
			} else {
				kittyEnvSignalled.Store(true)
				kittyProbed.Store(true)
				kittyQuery = tmuxWrap(buildKittyQueryAPC(kittyProbeID))
				if !kittySharedProbed.Load() {
					if !kittySharedProbeable() {
						if kittySharedCap.CompareAndSwap(int32(KittyCapabilityUnknown), int32(KittyCapabilityUnsupported)) {
							kittySharedAutoUnsupported.Store(true)
						}
					} else {
						sharedQuery = kittySharedQueryAPC()
						if sharedQuery != "" {
							kittySharedProbed.Store(true)
						} else if kittySharedCap.CompareAndSwap(int32(KittyCapabilityUnknown), int32(KittyCapabilityUnsupported)) {
							kittySharedAutoUnsupported.Store(true)
						}
					}
				}
			}
		}
	}
	kittyProbeMu.Unlock()
	if sharedQuery != "" || kittyQuery != "" {
		raw := sharedQuery + kittyQuery
		cmds = append(cmds,
			func() tea.Msg { return tea.RawMsg{Msg: raw} },
			tea.Tick(kittyProbeTimeout, func(time.Time) tea.Msg {
				return kittyProbeTickMsg{}
			}),
		)
	}
	if c := kittySupported(); c != KittyCapabilityUnknown {
		cmds = append(cmds, kittyCapabilityCommand(c))
	}
	return tea.Batch(cmds...)
}

func kittyEnvSignal() bool {
	ensureLegacyEnv()
	return kittyEnvSignalFor(tmuxPassthroughEnabled.Load())
}

func kittyEnvSignalFor(tmux bool) bool {
	if tmux {
		return true
	}
	if os.Getenv("KITTY_WINDOW_ID") != "" || os.Getenv("KITTY_INSTALLATION_DIR") != "" {
		return true
	}
	if os.Getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return true
	}
	if os.Getenv("WEZTERM_EXECUTABLE") != "" || os.Getenv("WEZTERM_PANE") != "" {
		return true
	}
	switch os.Getenv("TERM") {
	case "xterm-kitty", "xterm-ghostty":
		return true
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "ghostty", "WezTerm", "kitty", "iTerm.app":
		return true
	}
	return false
}

// buildKittyQueryAPC encodes a Kitty graphics query (a=q): a tiny 1×1
// transmit whose only purpose is to elicit a response. Kitty terminals
// reply with `\e_Gi=<id>;OK\e\\` (and echo supported keys such as `U=1`
// when `e=T` feature queries are supported); non-Kitty terminals don't reply.
// The payload "AAAA" is base64 of three zero bytes (one RGB pixel).
func buildKittyQueryAPC(id int) string {
	return fmt.Sprintf("\x1b_Ga=q,t=d,e=T,U=1,f=24,s=1,v=1,i=%d;AAAA\x1b\\", id)
}

// recordKittyResponse handles a uv.KittyGraphicsEvent. Any response
// carrying our probe's image ID proves the terminal speaks the protocol
// (even an error response — only a Kitty-aware terminal would have
// produced it).
//
// A real response is authoritative and overrides the timeout's
// pessimistic Unsupported conclusion: bubbletea's startup can deliver
// the kittyProbeTickMsg before draining the input event queue, so the
// timeout sometimes fires first even though the terminal responded
// immediately. Two CompareAndSwaps cover both starting states
// (Unknown after probe-but-no-tick, Unsupported after tick-but-late-
// response) without overriding a Forced(Supported|Unsupported) state.
func recordKittyResponse(ev uv.KittyGraphicsEvent) tea.Cmd {
	if ev.Options.ID == kittySharedProbeID {
		recordKittySharedResponse(string(ev.Payload) == "OK")
		return nil
	}
	if ev.Options.ID != kittyProbeID || defaultTerminalSession.isKittyForced() {
		return nil
	}
	if kittyCap.CompareAndSwap(int32(KittyCapabilityUnknown), int32(KittyCapabilitySupported)) ||
		kittyCap.CompareAndSwap(int32(KittyCapabilityUnsupported), int32(KittyCapabilitySupported)) {
		return kittyCapabilityCommand(KittyCapabilitySupported)
	}
	return nil
}

// recordKittyTimeout marks Kitty unsupported if the probe window has
// elapsed without a response. Idempotent and a no-op if the capability
// was already resolved (by an earlier response or by ForceKittyCapability).
func recordKittyTimeout() tea.Cmd {
	changed := kittyCap.CompareAndSwap(int32(KittyCapabilityUnknown), int32(KittyCapabilityUnsupported))
	kittySharedCap.CompareAndSwap(int32(KittyCapabilityUnknown), int32(KittyCapabilityUnsupported))
	if changed {
		return kittyCapabilityCommand(KittyCapabilityUnsupported)
	}
	return nil
}
