package animation

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"image"
	"image/color"
	"testing"
	"time"

	"golang.org/x/image/webp"
)

// Still WebP files made with cwebp from one-row PNGs: lossless VP8L frames
// with exact colors, and lossy VP8 frames with and without an ALPH chunk.
var webpStills = map[string]string{
	"red3":       "524946461c000000574542505650384c0f0000002f020000000710fd8ffe0722a2ff0100",
	"tg2":        "524946461e000000574542505650384c110000002f010000100f30ff11f31f8c0a44f43f0000",
	"yellow1":    "524946461c000000574542505650384c100000002f0000000007d0fffe47ff0311d1ff00",
	"blue1":      "524946461c000000574542505650384c0f0000002f000000000710d1fffe0722a2ff0100",
	"blue1lossy": "52494646420000005745425056503820360000001002009d012a0100010000000025a00274ba01f801fa0003c800feffdaa505fffc4f4e6f1153cafff89e9fff0e3ea30a0ffc2fe00000",
	"half2":      "524946466200000057454250565038580a00000010000000010000000000414c5048030000000080000056503820380000005002009d012a0200010000000025a00274ba01f801fa01fc00079000feff6f56fffcb0fc7f5279ffcb10ffff13d39bc454f2bffe26400000",
}

func webpStill(t *testing.T, name string) []byte {
	t.Helper()
	b, err := hex.DecodeString(webpStills[name])
	if err != nil || len(b) == 0 {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

// webpBitstream is a still file's frame data: its chunks after the RIFF
// header, without any VP8X chunk, which is how an ANMF chunk carries them.
func webpBitstream(t *testing.T, name string) []byte {
	t.Helper()
	b := webpStill(t, name)[12:]
	var out []byte
	for len(b) >= 8 {
		n := int(binary.LittleEndian.Uint32(b[4:8]))
		size := 8 + n + n%2
		if string(b[:4]) != "VP8X" {
			out = append(out, b[:size]...)
		}
		b = b[size:]
	}
	return out
}

type webpFrame struct {
	bitstream   string
	x, y        int
	duration    int
	blend       bool // alpha-blend over the canvas instead of replacing it
	toBackgound bool // dispose to background afterwards
}

type webpAnim struct {
	width, height int
	loop          int
	opaque        bool    // leave the VP8X alpha flag clear
	background    [4]byte // B, G, R, A as the ANIM chunk stores it
	frames        []webpFrame
	extra         []byte // chunks appended after the frames
}

func chunk(id string, payload []byte) []byte {
	out := append([]byte(id), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(payload)))
	out = append(out, payload...)
	if len(payload)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

func put24(b []byte, v int) { b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16) }

func webpTestData(t *testing.T, a webpAnim) []byte {
	t.Helper()
	vp8x := make([]byte, 10)
	vp8x[0] = 1 << 1 // animation
	if !a.opaque {
		vp8x[0] |= 1 << 4 // alpha
	}
	put24(vp8x[4:], a.width-1)
	put24(vp8x[7:], a.height-1)
	body := chunk("VP8X", vp8x)
	anim := append([]byte{}, a.background[:]...)
	anim = append(anim, byte(a.loop), byte(a.loop>>8))
	body = append(body, chunk("ANIM", anim)...)
	for _, f := range a.frames {
		cfg, err := webp.DecodeConfig(bytes.NewReader(webpStill(t, f.bitstream)))
		if err != nil {
			t.Fatal(err)
		}
		hdr := make([]byte, 16)
		put24(hdr[0:], f.x/2)
		put24(hdr[3:], f.y/2)
		put24(hdr[6:], cfg.Width-1)
		put24(hdr[9:], cfg.Height-1)
		put24(hdr[12:], f.duration)
		if !f.blend {
			hdr[15] |= 1 << 1
		}
		if f.toBackgound {
			hdr[15] |= 1
		}
		body = append(body, chunk("ANMF", append(hdr, webpBitstream(t, f.bitstream)...))...)
	}
	body = append(body, a.extra...)
	out := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(4+len(body)))
	return append(append(out, "WEBP"...), body...)
}

func assertRow(t *testing.T, img image.Image, want ...color.Color) {
	t.Helper()
	for x, c := range want {
		if got, want := color.NRGBAModel.Convert(img.At(x, 0)), color.NRGBAModel.Convert(c); got != want {
			t.Fatalf("pixel %d: %v, want %v", x, got, want)
		}
	}
}

var (
	red    = color.NRGBA{255, 0, 0, 255}
	green  = color.NRGBA{0, 255, 0, 255}
	blue   = color.NRGBA{0, 0, 255, 255}
	yellow = color.NRGBA{255, 255, 0, 255}
	clear  = color.NRGBA{}
)

// The four-frame clip mirrors the GIF fixture: a full first frame, an
// alpha-blended partial frame, a frame disposed to the background, and a
// final frame that reveals the cleared pixel.
func webpFourFrames(loop int) webpAnim {
	return webpAnim{width: 3, height: 1, loop: loop, frames: []webpFrame{
		{bitstream: "red3"},
		{bitstream: "tg2", duration: 10, blend: true},
		{bitstream: "blue1", x: 2, duration: 20, toBackgound: true},
		{bitstream: "yellow1", duration: 150},
	}}
}

func TestWebPCompositionAndDisposal(t *testing.T) {
	img, p, err := DecodeWebP(webpTestData(t, webpFourFrames(1)), true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Frames() != 4 || p.Frame() != 1 {
		t.Fatalf("frames %d at %d", p.Frames(), p.Frame())
	}
	assertRow(t, img, red, red, red)
	next, ok := p.Next()
	if !ok {
		t.Fatal("stopped early")
	}
	assertRow(t, next.Image(), red, green, red)
	next, _ = next.Next()
	assertRow(t, next.Image(), red, green, blue)
	next, _ = next.Next()
	assertRow(t, next.Image(), yellow, green, clear)
	assertRow(t, img, red, red, red)
	if last, ok := next.Next(); ok || last != next {
		t.Fatal("once-only animation did not stop on last frame")
	}
	if got := p.Delay(); got != 100*time.Millisecond {
		t.Fatalf("zero duration became %v, want 100ms", got)
	}
}

func TestWebPLoopsAndDelays(t *testing.T) {
	for _, plays := range []int{0, 1, 2, 3} {
		_, p, err := DecodeWebP(webpTestData(t, webpFourFrames(plays)), true)
		if err != nil {
			t.Fatal(err)
		}
		for i, want := range []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 20 * time.Millisecond, 150 * time.Millisecond} {
			if p.Delay() != want {
				t.Fatalf("frame %d delay %v, want %v", i, p.Delay(), want)
			}
			if i < 3 {
				p, _ = p.Next()
			}
		}
		p = p.Restart()
		count := 1
		for count < 20 {
			next, more := p.Next()
			if !more {
				break
			}
			p = next
			count++
		}
		want := 4 * plays
		if plays == 0 {
			want = 20
		}
		if count != want {
			t.Fatalf("%d plays: %d frames, want %d", plays, count, want)
		}
		assertRow(t, p.Restart().Image(), red, red, red)
	}
}

func TestWebPLossyFramesWithAlpha(t *testing.T) {
	// Frame offsets are stored halved, so the alpha frame sits at an even x.
	data := webpTestData(t, webpAnim{width: 4, height: 1, frames: []webpFrame{
		{bitstream: "red3"},
		{bitstream: "blue1lossy", duration: 50},
		{bitstream: "half2", x: 2, duration: 50, blend: true},
	}})
	_, p, err := DecodeWebP(data, true)
	if err != nil {
		t.Fatal(err)
	}
	near := func(got color.Color, want color.NRGBA) bool {
		c := color.NRGBAModel.Convert(got).(color.NRGBA)
		diff := func(a, b uint8) bool { d := int(a) - int(b); return -24 < d && d < 24 }
		return diff(c.R, want.R) && diff(c.G, want.G) && diff(c.B, want.B) && diff(c.A, want.A)
	}
	p, _ = p.Next()
	if got := p.Image().At(0, 0); !near(got, blue) {
		t.Fatalf("lossy VP8 pixel %v, want about %v", got, blue)
	}
	p, _ = p.Next()
	// Half-transparent red over red stays red; the transparent pixel changes nothing.
	if got := p.Image().At(2, 0); !near(got, red) {
		t.Fatalf("ALPH-blended pixel %v, want about %v", got, red)
	}
	if got := p.Image().At(0, 0); !near(got, blue) {
		t.Fatalf("pixel outside the frame changed: %v", got)
	}
	if got := color.NRGBAModel.Convert(p.Image().At(3, 0)); got != clear {
		t.Fatalf("transparent frame pixel changed the canvas: %v", got)
	}
}

func TestWebPFirstFrameOnlyAndStill(t *testing.T) {
	img, p, err := DecodeWebP(webpTestData(t, webpFourFrames(0)), false)
	if err != nil || p != nil {
		t.Fatalf("first frame only: player %v, err %v", p, err)
	}
	assertRow(t, img, red, red, red)
	if b := img.Bounds(); b != image.Rect(0, 0, 3, 1) {
		t.Fatalf("canvas %v", b)
	}
	img, p, err = DecodeWebP(webpStill(t, "tg2"), true)
	if err != nil || p != nil {
		t.Fatalf("still: player %v, err %v", p, err)
	}
	assertRow(t, img, clear, green)
	stats, err := PreflightWebP(webpStill(t, "tg2"))
	if err != nil || stats.Frames != 1 || stats.Bytes != 8 || stats.Bounds != image.Rect(0, 0, 2, 1) {
		t.Fatalf("still preflight %+v, %v", stats, err)
	}
	// Account for the whole retained file, including container metadata.
	data := webpTestData(t, webpFourFrames(0))
	stats, err = PreflightWebP(data)
	if err != nil || stats.Frames != 4 || stats.Bytes != 3*1*4+len(data) || stats.Bounds != image.Rect(0, 0, 3, 1) {
		t.Fatalf("animation preflight %+v, %v (want %d bytes)", stats, err, 3*1*4+len(data))
	}
}

// Frames after the first stay compressed until painted, so a long,
// full-canvas animation is not bounded by decoded frame storage, and a frame
// that fails to decode is skipped rather than failing the file.
func TestWebPFramesDecodeOnDemand(t *testing.T) {
	a := webpFourFrames(0)
	// A frame declared 3×1 whose bitstream is 1×1: it fails to decode when painted.
	hdr := make([]byte, 16)
	put24(hdr[6:], 2)
	a.extra = chunk("ANMF", append(hdr, webpBitstream(t, "blue1")...))
	data := webpTestData(t, a)
	img, p, err := DecodeWebP(data, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Frames() != 5 {
		t.Fatalf("%d frames", p.Frames())
	}
	for i, f := range p.data.Frames {
		if (f.Image != nil) != (i == 0) || (f.LoadContext != nil) == (i == 0) {
			t.Fatalf("frame %d: image %v, loader %v", i, f.Image != nil, f.LoadContext != nil)
		}
	}
	assertRow(t, img, red, red, red)
	for range 3 {
		p, _ = p.Next()
	}
	assertRow(t, p.Image(), yellow, green, clear)
	p, _ = p.Next()
	assertRow(t, p.Image(), yellow, green, clear)
	// Many full-canvas frames of a large canvas open; only the first decodes.
	big := webpAnim{width: 4000, height: 4000, frames: []webpFrame{{bitstream: "red3"}}}
	for range 200 {
		h := make([]byte, 16)
		put24(h[6:], 4000-1)
		put24(h[9:], 4000-1)
		big.extra = append(big.extra, chunk("ANMF", append(h, webpBitstream(t, "red3")...))...)
	}
	stats, err := PreflightWebP(webpTestData(t, big))
	if err != nil || stats.Frames != 201 || stats.Bytes < 4000*4000*4 {
		t.Fatalf("large preflight %+v, %v", stats, err)
	}
	if _, p, err := DecodeWebP(webpTestData(t, big), true); err != nil || p.Frames() != 201 {
		t.Fatalf("large animation: %v", err)
	}
}

func TestWebPOpaqueBackground(t *testing.T) {
	a := webpAnim{width: 3, height: 1, opaque: true, background: [4]byte{0x12, 0x1b, 0x2e, 0xff}, frames: []webpFrame{
		{bitstream: "red3"},
		{bitstream: "blue1", x: 2, duration: 20, toBackgound: true},
		{bitstream: "yellow1", duration: 20},
	}}
	_, p, err := DecodeWebP(webpTestData(t, a), true)
	if err != nil {
		t.Fatal(err)
	}
	p, _ = p.Next()
	p, _ = p.Next()
	assertRow(t, p.Image(), yellow, red, color.NRGBA{0x2e, 0x1b, 0x12, 0xff})
	a.opaque = false
	_, p, err = DecodeWebP(webpTestData(t, a), true)
	if err != nil {
		t.Fatal(err)
	}
	p, _ = p.Next()
	p, _ = p.Next()
	assertRow(t, p.Image(), yellow, red, clear)
}

func TestIsAnimatedWebP(t *testing.T) {
	anim := webpTestData(t, webpFourFrames(0))
	if !IsAnimatedWebP(anim) {
		t.Fatal("animation not recognized")
	}
	for name, data := range map[string][]byte{"still": webpStill(t, "red3"), "extended still": webpStill(t, "half2"), "short": anim[:29], "gif": []byte("GIF89a..............................")} {
		if IsAnimatedWebP(data) {
			t.Fatalf("%s recognized as an animation", name)
		}
	}
}

func TestWebPPreflightRejects(t *testing.T) {
	good := webpTestData(t, webpFourFrames(0))
	// A frame whose header claims a huge size but carries red3's bitstream.
	bigFrame := func(w, h int) []byte {
		hdr := make([]byte, 16)
		put24(hdr[6:], w-1)
		put24(hdr[9:], h-1)
		return chunk("ANMF", append(hdr, webpBitstream(t, "red3")...))
	}
	var manyFrames []byte
	for range MaxGIFFrames + 1 {
		manyFrames = append(manyFrames, bigFrame(1, 1)...)
	}
	cases := map[string][]byte{
		"truncated":       good[:len(good)-5],
		"frame overrun":   append(good[:len(good):len(good)], chunk("ANMF", make([]byte, 20))...),
		"out of canvas":   webpTestData(t, webpAnim{width: 2, height: 1, frames: []webpFrame{{bitstream: "red3"}}}),
		"no frames":       webpTestData(t, webpAnim{width: 2, height: 1}),
		"no bitstream":    webpTestData(t, webpAnim{width: 3, height: 1, extra: chunk("ANMF", make([]byte, 16))}),
		"too many frames": webpTestData(t, webpAnim{width: 3, height: 1, frames: []webpFrame{{bitstream: "red3"}}, extra: manyFrames}),
	}
	// A canvas over the pixel limit.
	huge := webpTestData(t, webpAnim{width: 3, height: 1, frames: []webpFrame{{bitstream: "red3"}}})
	put24(huge[24:], 1<<24-1)
	put24(huge[27:], 1<<24-1)
	cases["canvas too large"] = huge
	for name, data := range cases {
		if _, err := PreflightWebP(data); err == nil {
			t.Errorf("%s: preflight accepted", name)
		}
		if _, _, err := DecodeWebP(data, true); err == nil {
			t.Errorf("%s: decode accepted", name)
		}
	}
	// The first frame is decoded on opening, so one whose bitstream is 1×1
	// under a 3×1 header fails the file; the headers alone pass preflight.
	h := make([]byte, 16)
	put24(h[6:], 2)
	bad := webpTestData(t, webpAnim{width: 3, height: 1, extra: chunk("ANMF", append(h, webpBitstream(t, "blue1")...))})
	if _, err := PreflightWebP(bad); err != nil {
		t.Error("preflight decoded pixels:", err)
	}
	for _, animate := range []bool{false, true} {
		if _, _, err := DecodeWebP(bad, animate); err == nil {
			t.Errorf("animate %v: mismatched first frame decoded", animate)
		}
	}
}
