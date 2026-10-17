package ntpicture

import (
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"nimbleterminal.dev/ntpicture/internal/kittyshm"
)

// TerminalOptions configures an independent terminal connection. Unknown Kitty
// uses environment hints and a query; Supported/Unsupported bypass that query.
// SharedMemory is independently probed unless explicitly overridden. Creation
// failures still fall back to direct transport even when support is forced.
// Zero cell dimensions default to 8x16. Nil TmuxPassthrough reads the environment.
// Background, fit, encoding, and selection of glyph/Kitty remain picture policy.
type TerminalOptions struct {
	Kitty, SharedMemory             KittyCapability
	CellPixelWidth, CellPixelHeight int
	TmuxPassthrough                 *bool
}

// TerminalState is a value snapshot, never mutable shared configuration.
// SharedMemory describes terminal support, not guaranteed local allocation.
type TerminalState struct {
	Kitty, SharedMemory             KittyCapability
	CellPixelWidth, CellPixelHeight int
	TmuxPassthrough                 bool
	EnvSignalled                    bool
}

// TerminalSessionMsg announces a discovery or geometry change. Match Session
// before handling it. State is the snapshot at emission; Snapshot returns the
// current state if a newer event has already arrived. Duplicate Init calls on
// an explicit session do not emit duplicate probes or startup notifications.
type TerminalSessionMsg struct {
	Session *TerminalSession
	State   TerminalState
}

type terminalSessionTick struct{ session *TerminalSession }

// TerminalSession owns discovery for one terminal connection. Share its pointer
// among pictures in one Bubble Tea program. Methods belong to that program's
// event loop; do not copy it. Forward messages to its pictures and execute all
// returned commands (or call Update directly when there are no pictures).
// Raw cell-size events belong to the connection: do not broadcast one terminal's
// input to sessions for other terminals. Probe replies and timeouts are scoped.
// Close releases probe resources; pictures retain their own placement cleanup.
type TerminalSession struct {
	mu                        sync.Mutex // guards defaultTerminalSession fields across goroutines; explicit sessions belong to one event loop
	state                     TerminalState
	started                   bool
	closed                    atomic.Bool
	kittyForced, sharedForced bool
	probeID                   int
	probe                     *kittyshm.Object
	legacy                    bool
}

// The default session preserves the legacy package helpers and Model state.
// Explicit sessions never consult or modify that state.
var defaultTerminalSession = &TerminalSession{legacy: true}
var terminalProbeIDs atomic.Uint32

func defaultSession() *TerminalSession {
	ensureLegacyEnv()
	return defaultTerminalSession
}

func (s *TerminalSession) setCellSize(w, h int) {
	if s.legacy {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	s.state.CellPixelWidth, s.state.CellPixelHeight = w, h
}

func (s *TerminalSession) setKittyForced(forced bool) {
	if s.legacy {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	s.kittyForced = forced
}

func (s *TerminalSession) isKittyForced() bool {
	if s.legacy {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	return s.kittyForced
}

func (s *TerminalSession) setKitty(c KittyCapability) {
	if s == nil || s.closed.Load() {
		return
	}
	if s.legacy {
		forceKittyCapability(c)
		return
	}
	s.state.Kitty = c
	s.kittyForced = c != KittyCapabilityUnknown
	if c == KittyCapabilityUnsupported && !s.sharedForced {
		s.state.SharedMemory = KittyCapabilityUnsupported
		_ = s.probe.Unlink()
		s.probe = nil
	}
}

func (s *TerminalSession) setTerminalKitty(kitty bool) {
	if s == nil || s.closed.Load() || s.legacy {
		return
	}
	if kitty {
		s.state.Kitty = KittyCapabilitySupported
	} else if s.state.Kitty == KittyCapabilitySupported && !s.kittyForced {
		s.state.Kitty = KittyCapabilityUnsupported
	}
}

func envTerminalConfig() (tmux bool, kitty KittyCapability, kittyForced bool) {
	tmux = os.Getenv("TMUX") != ""
	switch os.Getenv("NTPICTURE_TMUX_PASSTHROUGH") {
	case "1", "true", "on", "yes":
		tmux = true
	case "0", "false", "off", "no":
		tmux = false
	}
	switch os.Getenv("NTPICTURE_KITTY") {
	case "supported", "true", "1", "on", "yes":
		kitty, kittyForced = KittyCapabilitySupported, true
	case "unsupported", "false", "0", "off", "no":
		kitty, kittyForced = KittyCapabilityUnsupported, true
	}
	return tmux, kitty, kittyForced
}

// NewTerminalSession snapshots environment defaults; subsequent package-level
// overrides affect only the compatibility session. Call Init to start discovery.
func NewTerminalSession(opts TerminalOptions) *TerminalSession {
	s := &TerminalSession{state: TerminalState{
		Kitty: opts.Kitty, SharedMemory: opts.SharedMemory,
		CellPixelWidth: opts.CellPixelWidth, CellPixelHeight: opts.CellPixelHeight,
	}}
	if s.state.CellPixelWidth <= 0 {
		s.state.CellPixelWidth = defaultCellPixelW
	}
	if s.state.CellPixelHeight <= 0 {
		s.state.CellPixelHeight = defaultCellPixelH
	}
	tmux, envKitty, _ := envTerminalConfig()
	s.state.TmuxPassthrough = tmux
	if opts.TmuxPassthrough != nil {
		s.state.TmuxPassthrough = *opts.TmuxPassthrough
	}
	if s.state.Kitty == KittyCapabilityUnknown {
		s.state.Kitty = envKitty
	}
	s.kittyForced = s.state.Kitty != KittyCapabilityUnknown
	s.sharedForced = s.state.SharedMemory != KittyCapabilityUnknown
	// Outside the 24-bit image allocator and distinct from legacy probe IDs.
	s.probeID = kittySharedProbeID + 1 + int(terminalProbeIDs.Add(2))
	return s
}

func (s *TerminalSession) Snapshot() TerminalState {
	var state TerminalState
	if s.legacy {
		ensureLegacyEnv()
		s.mu.Lock()
		state = s.state
		s.mu.Unlock()
		state.Kitty = kittySupported()
		state.SharedMemory = KittyCapability(kittySharedCap.Load())
		state.TmuxPassthrough = tmuxPassthroughEnabled.Load()
		state.EnvSignalled = kittyEnvSignalledFn()
	} else {
		state = s.state
	}
	if runtime.GOOS == "js" && !s.sharedForced {
		state.SharedMemory = KittyCapabilityUnsupported
		if kittyshm.Supported() {
			state.SharedMemory = KittyCapabilitySupported
		}
	}
	if state.CellPixelWidth <= 0 {
		state.CellPixelWidth = defaultCellPixelW
	}
	if state.CellPixelHeight <= 0 {
		state.CellPixelHeight = defaultCellPixelH
	}
	return state
}

func (s *TerminalSession) notification() tea.Cmd {
	msg := TerminalSessionMsg{s, s.Snapshot()}
	return func() tea.Msg {
		if s.closed.Load() {
			return nil
		}
		return msg
	}
}

// Init queries cell geometry and graphics/transport support once. It never reads
// stdin; the host delivers terminal replies through Update. Run its command.
func (s *TerminalSession) Init() tea.Cmd {
	if s.legacy {
		return tea.Batch(requestCellSize(), queryKittySupport())
	}
	if s.started || s.closed.Load() {
		return nil
	}
	s.started = true
	s.state.EnvSignalled = kittyEnvSignalFor(s.state.TmuxPassthrough)
	if s.state.Kitty == KittyCapabilityUnknown && !s.state.EnvSignalled {
		s.state.Kitty = KittyCapabilityUnsupported
	}
	if s.state.Kitty == KittyCapabilityUnsupported && !s.sharedForced {
		s.state.SharedMemory = KittyCapabilityUnsupported
	}
	transport := s.transport()
	var query string
	if s.state.Kitty == KittyCapabilityUnknown {
		query = transport.wrap(buildKittyQueryAPC(s.probeID))
	}
	if s.state.Kitty != KittyCapabilityUnsupported && !s.sharedForced && runtime.GOOS != "js" {
		s.state.SharedMemory = KittyCapabilityUnsupported
		if sharedAPC, obj := buildKittySharedProbeAPC(s.probeID+1, transport); obj != nil {
			s.probe = obj
			s.state.SharedMemory = KittyCapabilityUnknown
			query = sharedAPC + query
		}
	}
	cmds := []tea.Cmd{s.notification(), func() tea.Msg {
		if s.closed.Load() {
			return nil
		}
		return requestCellSize()()
	}}
	if query != "" {
		cmds = append(cmds, func() tea.Msg {
			if s.closed.Load() {
				return nil
			}
			return tea.RawMsg{Msg: query}
		}, tea.Tick(kittyProbeTimeout, func(time.Time) tea.Msg { return terminalSessionTick{s} }))
	}
	return tea.Batch(cmds...)
}

// Update is idempotent when several pictures forward the same terminal event.
// Invalid cell reports are ignored, and late positive probe replies can recover
// from timeouts. Explicit capability overrides are never replaced by replies.
func (s *TerminalSession) Update(msg tea.Msg) tea.Cmd {
	if s.closed.Load() {
		return nil
	}
	before := s.Snapshot()
	var legacy tea.Cmd
	switch v := msg.(type) {
	case uv.CellSizeEvent:
		if v.Width <= 0 || v.Height <= 0 {
			return nil
		}
		s.setCellSize(v.Width, v.Height)
	case uv.KittyGraphicsEvent:
		if s.legacy {
			legacy = recordKittyResponse(v)
			break
		}
		if !s.started {
			return nil
		}
		if (v.Options.ID == s.probeID || v.Options.ID == kittyProbeID) && !s.isKittyForced() {
			s.state.Kitty = KittyCapabilitySupported
		}
		if v.Options.ID == s.probeID+1 && !s.sharedForced {
			if string(v.Payload) == "OK" {
				s.state.SharedMemory = KittyCapabilitySupported
			} else if s.state.SharedMemory == KittyCapabilityUnknown {
				s.state.SharedMemory = KittyCapabilityUnsupported
			}
			_ = s.probe.Unlink()
			s.probe = nil
		}
	case terminalSessionTick:
		if v.session != s {
			return nil
		}
		if s.state.Kitty == KittyCapabilityUnknown {
			s.state.Kitty = KittyCapabilityUnsupported
		}
		if s.state.SharedMemory == KittyCapabilityUnknown {
			s.state.SharedMemory = KittyCapabilityUnsupported
		}
	case kittyProbeTickMsg:
		if s.legacy {
			legacy = recordKittyTimeout()
		}
	}
	if before != s.Snapshot() {
		return tea.Batch(legacy, s.notification())
	}
	return legacy
}

// SetCellSize updates the session's cell pixel dimensions and emits
// TerminalSessionMsg if the snapshot changed. Non-positive dimensions are ignored.
func (s *TerminalSession) SetCellSize(width, height int) tea.Cmd {
	if s == nil || s.closed.Load() || width <= 0 || height <= 0 {
		return nil
	}
	before := s.Snapshot()
	s.setCellSize(width, height)
	if before != s.Snapshot() {
		return s.notification()
	}
	return nil
}

// SetKittyCapability updates the session's Kitty graphics capability and emits
// TerminalSessionMsg if the snapshot changed. Passing KittyCapabilityUnknown
// clears an explicit override.
func (s *TerminalSession) SetKittyCapability(c KittyCapability) tea.Cmd {
	if s == nil || s.closed.Load() {
		return nil
	}
	if c != KittyCapabilityUnknown && c != KittyCapabilityUnsupported && c != KittyCapabilitySupported {
		return nil
	}
	before := s.Snapshot()
	s.setKitty(c)
	if before != s.Snapshot() {
		return s.notification()
	}
	return nil
}

// Close cancels queued probe output and unlinks a pending probe immediately.
// Close pictures separately and run their returned cleanup commands first.
func (s *TerminalSession) Close() {
	if s.legacy || s.closed.Swap(true) {
		return
	}
	_ = s.probe.Unlink()
	s.probe = nil
}

func (s *TerminalSession) unavailable() error {
	state := s.Snapshot()
	switch state.Kitty {
	case KittyCapabilitySupported:
		return nil
	case KittyCapabilityUnknown:
		return ErrKittyProbePending
	default:
		if state.EnvSignalled && !s.isKittyForced() {
			return ErrKittyProbeTimeout
		}
		return ErrKittyNotDetected
	}
}

type kittyTransport struct{ tmux, shared bool }

func legacyTransport() kittyTransport {
	ensureLegacyEnv()
	return kittyTransport{tmuxPassthroughEnabled.Load(), kittySharedUsable()}
}
func (s *TerminalSession) transport() kittyTransport {
	if s.legacy {
		return legacyTransport()
	}
	state := s.Snapshot()
	return kittyTransport{state.TmuxPassthrough, state.SharedMemory == KittyCapabilitySupported}
}
func (t kittyTransport) wrap(seq string) string {
	if t.tmux {
		return ansi.TmuxPassthrough(seq)
	}
	return seq
}
