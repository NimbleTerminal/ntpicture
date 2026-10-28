package ntpicture

import (
	"encoding/base64"
	"fmt"
	"io"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

// encodeKittyGraphicsData frames encoded image bytes or a shared-memory name
// as base64 APC sequences, chunked when o.Chunk is set. It does not compress
// data. Options and APC serialization come from the upstream ANSI package.
func encodeKittyGraphicsData(w io.Writer, data []byte, o *kitty.Options) error {
	if o == nil {
		o = &kitty.Options{}
	}
	payload := base64.StdEncoding.AppendEncode(nil, data)
	if !o.Chunk {
		_, err := io.WriteString(w, ansi.KittyGraphics(payload, kittyPlacementOptions(o)...))
		return err
	}
	for first := true; ; first = false {
		// Match Charm's framing at exact boundaries: a full chunk is followed
		// by an empty m=0 terminator, including for a single full chunk.
		last := len(payload) < kitty.MaxChunkSize
		n := min(len(payload), kitty.MaxChunkSize)
		var opts []string
		if first {
			opts = kittyPlacementOptions(o)
		} else {
			if o.Quiet > 0 {
				opts = append(opts, fmt.Sprintf("q=%d", o.Quiet))
			}
			if o.Action == kitty.Frame {
				opts = append(opts, "a=f")
			}
		}
		if !first || !last {
			more := "m=1"
			if last {
				more = "m=0"
			}
			opts = append(opts, more)
		}
		apc := ansi.KittyGraphics(payload[:n], opts...)
		if o.ChunkFormatter != nil {
			apc = o.ChunkFormatter(apc)
		}
		if _, err := io.WriteString(w, apc); err != nil {
			return err
		}
		if last {
			return nil
		}
		payload = payload[n:]
	}
}

// The pinned ANSI serializer emits z only when positive. Kitty supports signed
// placement depth, so preserve negative values until that dependency fixes it.
func kittyPlacementOptions(o *kitty.Options) []string {
	opts := o.Options()
	// A zero coordinate must reset a previous nonzero source rectangle.
	// The upstream serializer otherwise omits x=0 and y=0.
	if o.VirtualPlacement && o.Width > 0 && o.Height > 0 {
		if o.X == 0 {
			opts = append(opts, "x=0")
		}
		if o.Y == 0 {
			opts = append(opts, "y=0")
		}
	}
	if o.Z < 0 {
		opts = append(opts, fmt.Sprintf("z=%d", o.Z))
	}
	return opts
}
