package main

import (
	"encoding/json"
	"image"
	"image/color"
	"os"

	nt "nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/legacy"
)

func main() {
	legacy.ForceKittyCapability(nt.KittyCapabilitySupported)
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{B: 255, A: 255})
	var frames []legacy.KittyFrameMsg
	for i, f := range []nt.KittyFormat{nt.KittyFormatPNG, nt.KittyFormatRGBA, nt.KittyFormatZlib} {
		m := legacy.NewModelWithConfig(nt.Config{KittyID: 100 + i, Fit: nt.FitFill, KittyFormat: f, CellPixelWidth: 1, CellPixelHeight: 1})
		m.SetSize(2, 1)
		m.Toggle()
		frames = append(frames, m.SetImage(img)().(legacy.KittyFrameMsg))
	}
	json.NewEncoder(os.Stdout).Encode(frames)
}
