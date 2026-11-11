// Command genicon rasterizes the SVG logo to a square PNG:
//
//	go run ./genicon logo.svg icon.png
package main

import (
	"image"
	"image/png"
	"log"
	"os"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"golang.org/x/image/draw"
)

func main() {
	icon, err := oksvg.ReadIcon(os.Args[1], oksvg.IgnoreErrorMode)
	if err != nil {
		log.Fatal(err)
	}
	// oksvg scales coordinates but not stroke widths, so render at the
	// drawing's own size and resample to 256, the largest image a Windows
	// icon holds (smaller ones are scaled from it).
	w, h := int(icon.ViewBox.W), int(icon.ViewBox.H)
	full := image.NewRGBA(image.Rect(0, 0, w, h))
	icon.Draw(rasterx.NewDasher(w, h, rasterx.NewScannerGV(w, h, full, full.Bounds())), 1)
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	draw.CatmullRom.Scale(img, img.Bounds(), full, full.Bounds(), draw.Src, nil)
	out, err := os.Create(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	if err = png.Encode(out, img); err != nil {
		log.Fatal(err)
	}
	if err = out.Close(); err != nil {
		log.Fatal(err)
	}
}
