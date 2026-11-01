package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"sync"
	"time"
)

func token() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func randomN(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic(err)
	}
	return int(v.Int64())
}
func shufflePair(p Pair, swap bool) Pair {
	if swap {
		p.Images[0], p.Images[1] = p.Images[1], p.Images[0]
		p.Truth = 1 - p.Truth
	}
	return p
}
func points(choice, truth int) int {
	if choice == truth {
		return 1
	}
	return 0
}

type Round struct {
	ID          string
	Pair        Pair
	ImageIDs    [2]string
	Started     time.Time
	Player      int
	PlayerMS    int64
	Done        bool
	Unavailable bool
	Jev         int
	Values      [2]float64
	Models      [2]string
	CallMS      [2]int64
	TotalMS     int64
	Scored      bool
}
type Game struct {
	mu                   sync.Mutex
	ID                   string
	Pairs                []Pair
	Index                int
	Round                *Round
	HumanScore, JevScore int
	UnavailableRounds    int
	HumanMS, JevMS       int64 // cumulated answer times, for stats
	Expires              time.Time
	Stats                *Stats
}

func (g *Game) settle() {
	r := g.Round
	if r.Player < 0 || !r.Done || r.Scored {
		return
	}
	g.HumanScore += points(r.Player, r.Pair.Truth)
	g.HumanMS += r.PlayerMS
	g.JevMS += r.TotalMS
	if !r.Unavailable {
		g.JevScore += points(r.Jev, r.Pair.Truth)
	} else {
		g.UnavailableRounds++
	}
	r.Scored = true
	if g.Index == len(g.Pairs)-1 {
		g.Stats.record(g)
	}
}
func (g *Game) choose(id string, choice int) error {
	r := g.Round
	if r.ID != id || choice < 0 || choice > 1 || r.Player >= 0 {
		return fmt.Errorf("choix déjà verrouillé ou round invalide")
	}
	r.Player = choice
	r.PlayerMS = time.Since(r.Started).Milliseconds()
	g.settle()
	return nil
}
func (g *Game) start(analyzer Analyzer, timeout time.Duration) {
	r := &Round{ID: token(), Pair: shufflePair(g.Pairs[g.Index], randomN(2) == 1), ImageIDs: [2]string{token(), token()}, Started: time.Now(), Player: -1, Jev: -1}
	g.Round = r
	go func() {
		started := time.Now()
		var values [2]float64
		var models [2]string
		var times [2]int64
		failed := false
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		type result struct {
			Index    int
			Estimate Estimate
			MS       int64
			Err      error
		}
		ch := make(chan result, 2)
		for i := 0; i < 2; i++ {
			go func(i int) {
				t := time.Now()
				e, err := analyzer.Analyze(ctx, r.Pair.Images[i].Data)
				ch <- result{i, e, time.Since(t).Milliseconds(), err}
			}(i)
		}
		for i := 0; i < 2; i++ {
			out := <-ch
			values[out.Index] = out.Estimate.Value
			models[out.Index] = out.Estimate.Model
			times[out.Index] = out.MS
			if out.Err != nil {
				failed = true
				log.Printf("analyse %s appel %d: %v", r.ID, out.Index+1, out.Err)
			}
		}
		elapsed := time.Since(started).Milliseconds()
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.Round != r {
			return
		}
		r.Values = values
		r.Models = models
		r.CallMS = times
		r.TotalMS = elapsed
		r.Unavailable = failed
		r.Done = true
		if !failed {
			r.Jev = selectJev(values[0], values[1])
		}
		g.settle()
	}()
}

type Reveal struct {
	Truth       int           `json:"truth"`
	Player      int           `json:"player"`
	Jev         int           `json:"jev"`
	Unavailable bool          `json:"unavailable"`
	HumanPoints int           `json:"humanPoints"`
	JevPoints   int           `json:"jevPoints"`
	PlayerMS    int64         `json:"playerMs"`
	TotalMS     int64         `json:"totalMs"`
	CallMS      [2]int64      `json:"callMs"`
	Values      *[2]float64   `json:"values,omitempty"`
	Models      [2]string     `json:"models"`
	Provenance  [2]Provenance `json:"provenance"`
}
type View struct {
	RoundID           string    `json:"roundId"`
	Number            int       `json:"number"`
	Images            [2]string `json:"images"`
	Chosen            bool      `json:"chosen"`
	Ready             bool      `json:"ready"`
	HumanScore        int       `json:"humanScore"`
	JevScore          int       `json:"jevScore"`
	UnavailableRounds int       `json:"unavailableRounds"`
	Reveal            *Reveal   `json:"reveal,omitempty"`
}

// Construct a safe public DTO. Never marshal Game, Round, Pair, or Asset.
func (g *Game) view() View {
	r := g.Round
	v := View{RoundID: r.ID, Number: g.Index + 1, Images: [2]string{"/images/" + r.ImageIDs[0], "/images/" + r.ImageIDs[1]}, Chosen: r.Player >= 0, Ready: r.Done, HumanScore: g.HumanScore, JevScore: g.JevScore, UnavailableRounds: g.UnavailableRounds}
	if r.Scored {
		rev := &Reveal{Truth: r.Pair.Truth, Player: r.Player, Jev: r.Jev, Unavailable: r.Unavailable, HumanPoints: points(r.Player, r.Pair.Truth), PlayerMS: r.PlayerMS, TotalMS: r.TotalMS, CallMS: r.CallMS, Models: r.Models}
		if !r.Unavailable {
			rev.JevPoints = points(r.Jev, r.Pair.Truth)
			rev.Values = &r.Values
		}
		for i, a := range r.Pair.Images {
			meta := a.Provenance
			meta.Path = ""
			rev.Provenance[i] = meta
		}
		v.Reveal = rev
	}
	return v
}

// Pick one random image from each of two random pairs, so the home page never shows a full pair.
func teaserImages(corpus []Pair) [][]byte {
	if len(corpus) < 2 {
		return nil
	}
	i := randomN(len(corpus))
	j := (i + 1 + randomN(len(corpus)-1)) % len(corpus)
	return [][]byte{corpus[i].Images[randomN(2)].Data, corpus[j].Images[randomN(2)].Data}
}
