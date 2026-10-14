// Package decoders is a batteries-included, import-for-side-effect helper that
// registers a broad set of image decoders with the standard image package.
//
// Picture renders caller-supplied image.Image values. This optional package
// registers PNG, JPEG, GIF, WebP, BMP, and TIFF with image.Decode:
//
//	import _ "nimbleterminal.dev/ntpicture/decoders"
//
// Import individual codecs instead when only specific formats are needed.
// Core already imports image/png for Kitty encoding, which also registers PNG
// decoding. URL fetching and caching remain in NTCharts's picture/pictureurl;
// this module does not provide that adapter. For bounded GIF/WebP animation
// decoding and playback positions, use this module's animation package.
//
// AVIF and HEIC are intentionally excluded: they require CGO or heavy external
// dependencies, which is at odds with this library's pure-Go (and WASM) pipeline.
package decoders

import (
	_ "image/gif"  // GIF decoder registration
	_ "image/jpeg" // JPEG decoder registration
	_ "image/png"  // PNG decoder registration

	_ "golang.org/x/image/bmp"  // BMP decoder registration
	_ "golang.org/x/image/tiff" // TIFF decoder registration
	_ "golang.org/x/image/webp" // WebP decoder registration
)
