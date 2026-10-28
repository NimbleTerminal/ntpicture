package ntpicture

import (
	"fmt"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

const frameFeatureCount = 3

// Keep independent probes for editing a root frame, appending a cached frame,
// and selecting one. Basic graphics support implies none of these operations.
var frameFeatureQueries = [frameFeatureCount]string{
	"e=f,r=1,X=1,x=1,y=1",
	"e=f,c=1,X=1,z=-1",
	"e=a,c=1",
}

func buildKittyFrameQueryAPC(id, feature int) string {
	return fmt.Sprintf("\x1b_Ga=q,%s,i=%d\x1b\\", frameFeatureQueries[feature], id)
}

func (s *TerminalState) frameCapabilities() [frameFeatureCount]*KittyCapability {
	return [frameFeatureCount]*KittyCapability{&s.FrameEdits, &s.FrameAppend, &s.FrameSelect}
}

func (s *TerminalSession) needsFeatureQueries() bool {
	if !s.virtualForced {
		return true
	}
	for _, forced := range s.frameForced {
		if !forced {
			return true
		}
	}
	return false
}

func (s *TerminalSession) featureQueries() string {
	var out strings.Builder
	transport := s.transport()
	if !s.virtualForced {
		out.WriteString(transport.wrap(buildKittyFeatureQueryAPC(s.probeID + 2)))
	}
	for i, forced := range s.frameForced {
		if !forced {
			out.WriteString(transport.wrap(buildKittyFrameQueryAPC(s.probeID+3+i, i)))
		}
	}
	return out.String()
}

// The pinned ANSI parser does not expose e. Require the non-default keys for
// the exact operation, echoed on its unique probe ID. The Kitty feature-query
// specification forbids these echoes when the action is unsupported; legacy
// normal-query OK responses contain no such keys and cannot enable frames.
func resolveFrameCapability(cap *KittyCapability, ev uv.KittyGraphicsEvent, feature int) {
	o := ev.Options
	supported := false
	switch feature {
	case 0:
		supported = o.Rows == 1 && o.OffsetX == 1 && o.X == 1 && o.Y == 1
	case 1:
		supported = o.Columns == 1 && o.OffsetX == 1 && o.Z == -1
	case 2:
		supported = o.Columns == 1
	}
	if string(ev.Payload) == "OK" && supported {
		*cap = KittyCapabilitySupported
	} else if *cap == KittyCapabilityUnknown {
		*cap = KittyCapabilityUnsupported
	}
}

func timeoutLegacyFrameCapabilities() {
	for i := range kittyFrameCaps {
		kittyFrameCaps[i].CompareAndSwap(int32(KittyCapabilityUnknown), int32(KittyCapabilityUnsupported))
	}
}
