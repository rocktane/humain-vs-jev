package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
)

type Provenance struct {
	Path     string `json:"path"`
	Origin   string `json:"origin"`
	Source   string `json:"source"`
	Credit   string `json:"credit"`
	License  string `json:"license"`
	Evidence string `json:"evidence"`
}
type PairSpec struct {
	ID     string       `json:"id"`
	Images []Provenance `json:"images"`
}
type Asset struct {
	Data       []byte
	Provenance Provenance
}
type Pair struct {
	ID     string
	Images [2]Asset
	Truth  int
}

func loadCorpus(path string) ([]Pair, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var specs []PairSpec
	if err := json.Unmarshal(raw, &specs); err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		return nil, nil
	}
	if len(specs) < 5 {
		return nil, fmt.Errorf("au moins 5 paires nécessaires")
	}
	pairs := make([]Pair, 0, len(specs))
	ids := map[string]bool{}
	hashes := map[[32]byte]bool{}
	for _, s := range specs {
		if s.ID == "" || ids[s.ID] || len(s.Images) != 2 {
			return nil, fmt.Errorf("paire invalide ou identifiant dupliqué: %q", s.ID)
		}
		ids[s.ID] = true
		p := Pair{ID: s.ID, Truth: -1}
		real := 0
		var dimensions image.Point
		for i, meta := range s.Images {
			if meta.Source == "" || meta.Credit == "" || meta.License == "" || meta.Evidence == "" {
				return nil, fmt.Errorf("%s: provenance incomplète", s.ID)
			}
			switch meta.Origin {
			case "generated":
				if p.Truth != -1 {
					return nil, fmt.Errorf("%s: deux générations", s.ID)
				}
				p.Truth = i
			case "real":
				real++
			default:
				return nil, fmt.Errorf("%s: origine invalide", s.ID)
			}
			if meta.Path == "" || filepath.IsAbs(meta.Path) || !filepath.IsLocal(meta.Path) {
				return nil, fmt.Errorf("%s: chemin local relatif attendu", s.ID)
			}
			data, err := os.ReadFile(filepath.Join(filepath.Dir(path), meta.Path))
			if err != nil {
				return nil, err
			}
			if len(data) > 16<<20 {
				return nil, fmt.Errorf("%s: fichier trop volumineux", s.ID)
			}
			config, format, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil || (format != "jpeg" && format != "png") {
				return nil, fmt.Errorf("%s: JPEG ou PNG requis", s.ID)
			}
			if config.Width < 280 || config.Height < 280 || config.Width > 1568 || config.Height > 1568 {
				return nil, fmt.Errorf("%s: dimensions hors limites 280–1568", s.ID)
			}
			size := image.Pt(config.Width, config.Height)
			if i == 1 && dimensions != size {
				return nil, fmt.Errorf("%s: dimensions différentes", s.ID)
			}
			dimensions = size
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
			// Re-encode both identically: remove EXIF, text chunks, and format clues.
			var b bytes.Buffer
			if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
				return nil, err
			}
			h := sha256.Sum256(b.Bytes())
			if hashes[h] {
				return nil, fmt.Errorf("%s: image dupliquée", s.ID)
			}
			hashes[h] = true
			p.Images[i] = Asset{b.Bytes(), meta}
		}
		if p.Truth < 0 || real != 1 {
			return nil, fmt.Errorf("%s: exactement une photo et une génération requises", s.ID)
		}
		pairs = append(pairs, p)
	}
	return pairs, nil
}
