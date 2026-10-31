package main

import (
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestCorpusValidation(t *testing.T) {
	dir := t.TempDir()
	specs := make([]PairSpec, 5)
	for i := range specs {
		specs[i].ID = string(rune('a' + i))
		for j := 0; j < 2; j++ {
			name := specs[i].ID + string(rune('a'+j)) + ".jpg"
			img := image.NewRGBA(image.Rect(0, 0, 280, 280))
			img.Set(0, 0, color.RGBA{R: uint8(20 + i*30 + j*15), A: 255}) // Distinct broad patches survive JPEG normalization.
			for y := 0; y < 100; y++ {
				for x := 0; x < 100; x++ {
					img.Set(x, y, color.RGBA{R: uint8(20 + i*30 + j*15), G: uint8(j * 100), A: 255})
				}
			}
			f, _ := os.Create(filepath.Join(dir, name))
			jpeg.Encode(f, img, nil)
			f.Close()
			origin := "real"
			if j == 1 {
				origin = "generated"
			}
			specs[i].Images = append(specs[i].Images, Provenance{Path: name, Origin: origin, Source: "source", Credit: "author", License: "CC0", Evidence: "proof"})
		}
	}
	manifest := filepath.Join(dir, "manifest.json")
	save := func() { data, _ := json.Marshal(specs); os.WriteFile(manifest, data, 0600) }
	save()
	pairs, err := loadCorpus(manifest)
	if err != nil || len(pairs) != 5 {
		t.Fatalf("%v", err)
	}
	specs[0].Images[1].Origin = "real"
	save()
	if _, err := loadCorpus(manifest); err == nil {
		t.Fatal("deux photos acceptées")
	}
	specs[0].Images[1].Origin = "generated"
	specs[0].Images[0].Evidence = ""
	save()
	if _, err := loadCorpus(manifest); err == nil {
		t.Fatal("provenance absente acceptée")
	}
}

// main() démarre sans corpus quand le manifeste n'existe pas : l'erreur doit rester reconnaissable.
func TestMissingManifestIsNotExist(t *testing.T) {
	_, err := loadCorpus(filepath.Join(t.TempDir(), "absent.json"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("erreur inattendue: %v", err)
	}
}
