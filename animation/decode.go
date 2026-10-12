package animation

import (
	"bytes"
	"fmt"
	"image"
)

func isGIF(data []byte) bool {
	return bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))
}

// Animated reports whether data is a format that may play: any GIF, or a
// WebP whose header declares an animation. Decode decides from the frames.
func Animated(data []byte) bool { return isGIF(data) || IsAnimatedWebP(data) }

// Preflight sizes a GIF or WebP from its headers, without decoding pixels.
func Preflight(data []byte) (Stats, error) {
	if isGIF(data) {
		return PreflightGIF(data)
	}
	return PreflightWebP(data)
}

// Decode opens a GIF or WebP as DecodeGIF and DecodeWebP do.
func Decode(data []byte, animate bool) (image.Image, *Player, error) {
	if isGIF(data) {
		return DecodeGIF(data, animate)
	}
	if bytes.HasPrefix(data, []byte("RIFF")) {
		return DecodeWebP(data, animate)
	}
	return nil, nil, fmt.Errorf("not a GIF or WebP")
}
