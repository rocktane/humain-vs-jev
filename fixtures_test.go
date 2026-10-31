package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
)

// Code-made drawings used only by tests, never a photographic corpus.
func testPairs() []Pair {
	pairs := make([]Pair, 5)
	for k := range pairs {
		p := Pair{ID: fmt.Sprintf("fixture-%d", k), Truth: k % 2}
		for j := 0; j < 2; j++ {
			img := image.NewRGBA(image.Rect(0, 0, 800, 600))
			for y := 0; y < 600; y++ {
				for x := 0; x < 800; x++ {
					sky := color.RGBA{uint8(180 - k*12), uint8(211 - k*8), uint8(237 - k*5), 255}
					horizon := 330 + int(65*math.Sin(float64(x)/180+float64(k)))
					if y > horizon {
						sky = color.RGBA{uint8(45 + k*17), uint8(111 + k*9), uint8(120 + k*8), 255}
					}
					ridge := 440 + int(45*math.Sin(float64(x)/110+float64(j)*0.12))
					if y > ridge {
						sky = color.RGBA{uint8(23 + k*8), uint8(65 + k*8), uint8(89 + k*8), 255}
					}
					dx, dy := x-(570-k*60+j*14), y-(130+k*12)
					if dx*dx+dy*dy < 50*50 {
						sky = color.RGBA{255, 191, 91, 255}
					}
					img.SetRGBA(x, y, sky)
				}
			}
			var b bytes.Buffer
			_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 90})
			p.Images[j] = Asset{b.Bytes(), Provenance{Origin: "fixture", Source: "Illustration procédurale locale", Credit: "Humain vs Jev", License: "CC0-1.0", Evidence: "fixtures_test.go : testPairs (dessin par code, aucune photo)"}}
		}
		pairs[k] = p
	}
	return pairs
}
