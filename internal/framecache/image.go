// Package framecache carries private playback cache identities to Picture.
package framecache

import "image"

// Source identifies one immutable bounded animation and its terminal budget.
type Source struct{ Frames, Bytes int }

// Image associates immutable pixels with a one-based animation position.
type Image struct {
	image.Image
	Source *Source
	Frame  int
}
