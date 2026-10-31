package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSelection(t *testing.T) {
	for _, c := range []struct {
		a, b float64
		want int
	}{{.9, .1, 0}, {.2, .8, 1}, {.5, .5, -1}, {0, 0, -1}, {1, 1, -1}} {
		if got := selectJev(c.a, c.b); got != c.want {
			t.Fatalf("%v: %d", c, got)
		}
	}
}
func TestRandomizationPreservesTruth(t *testing.T) {
	p := Pair{Truth: 1}
	p.Images[0].Provenance.Origin = "real"
	p.Images[1].Provenance.Origin = "generated"
	for _, swap := range []bool{true, false} {
		q := shufflePair(p, swap)
		if q.Images[q.Truth].Provenance.Origin != "generated" || q.Images[1-q.Truth].Provenance.Origin != "real" {
			t.Fatal("vérité perdue")
		}
	}
}
func testGame() *Game {
	return &Game{Round: &Round{ID: "round", Player: -1, Jev: -1, Started: time.Now(), Pair: Pair{Truth: 1}}}
}
func TestLockAndReveal(t *testing.T) {
	g := testGame()
	if err := g.choose("old", 1); err == nil {
		t.Fatal("ancien round accepté")
	}
	if err := g.choose("round", 1); err != nil {
		t.Fatal(err)
	}
	if g.view().Reveal != nil || g.HumanScore != 0 {
		t.Fatal("révélation prématurée")
	}
	if err := g.choose("round", 0); err == nil {
		t.Fatal("second choix accepté")
	}
	g.Round.Done = true
	g.Round.Jev = 0
	g.settle()
	g.settle()
	if g.HumanScore != 1 || g.JevScore != 0 || g.view().Reveal == nil {
		t.Fatal("score ou révélation incorrect")
	}
}
func TestReadyDoesNotLeak(t *testing.T) {
	g := testGame()
	g.Round.Done = true
	g.Round.Jev = 1
	g.Round.Values = [2]float64{.1, .9}
	raw, _ := json.Marshal(g.view())
	for _, s := range []string{"truth", "values", "provenance", "playerMs", "generated"} {
		if strings.Contains(string(raw), s) {
			t.Fatalf("fuite: %s", raw)
		}
	}
	if err := g.choose("round", 0); err != nil {
		t.Fatal(err)
	}
	if g.JevScore != 1 || g.HumanScore != 0 {
		t.Fatal("scores incorrects")
	}
}
func TestAbstentionAndUnavailableScore(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		g := testGame()
		g.Round.Done = true
		g.Round.Unavailable = unavailable
		g.choose("round", 1)
		if g.JevScore != 0 || g.HumanScore != 1 {
			t.Fatal("score incorrect")
		}
	}
}

type analyzerFunc func(context.Context, []byte) (Estimate, error)

func (f analyzerFunc) Analyze(c context.Context, b []byte) (Estimate, error) { return f(c, b) }
func awaitDone(t *testing.T, g *Game) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		g.mu.Lock()
		done := g.Round.Done
		g.mu.Unlock()
		if done {
			return
		}
		select {
		case <-deadline:
			t.Fatal("analyse bloquée")
		case <-time.After(time.Millisecond):
		}
	}
}
func TestParallelAndPartialFailure(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	a := analyzerFunc(func(ctx context.Context, b []byte) (Estimate, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return Estimate{}, ctx.Err()
		}
		if string(b) == "bad" {
			return Estimate{}, errors.New("échec")
		}
		return Estimate{.9, "test"}, nil
	})
	p := Pair{Truth: 1}
	p.Images[0].Data = []byte("good")
	p.Images[1].Data = []byte("bad")
	g := &Game{Pairs: []Pair{p}}
	g.mu.Lock()
	g.start(a, time.Second)
	g.mu.Unlock()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("appels non parallèles")
		}
	}
	close(release)
	awaitDone(t, g)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.choose(g.Round.ID, g.Round.Pair.Truth)
	if !g.Round.Unavailable || g.Round.Jev != -1 || g.HumanScore != 1 || g.JevScore != 0 || g.view().Reveal.Values != nil {
		t.Fatal("échec partiel mal géré")
	}
}
func TestAnalysisTimeout(t *testing.T) {
	a := analyzerFunc(func(ctx context.Context, b []byte) (Estimate, error) { <-ctx.Done(); return Estimate{}, ctx.Err() })
	g := &Game{Pairs: []Pair{{}}}
	g.mu.Lock()
	g.start(a, 10*time.Millisecond)
	g.mu.Unlock()
	awaitDone(t, g)
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.Round.Unavailable {
		t.Fatal("timeout ignoré")
	}
	g.choose(g.Round.ID, 0)
	if !g.Round.Scored {
		t.Fatal("joueur bloqué")
	}
}
func TestLateResultCannotChangeNewRound(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	a := analyzerFunc(func(context.Context, []byte) (Estimate, error) {
		entered <- struct{}{}
		<-release
		return Estimate{.9, "test"}, nil
	})
	g := &Game{Pairs: []Pair{{}}}
	g.mu.Lock()
	g.start(a, time.Second)
	g.mu.Unlock()
	<-entered
	<-entered
	replacement := &Round{ID: "new", Player: -1}
	g.mu.Lock()
	g.Round = replacement
	g.mu.Unlock()
	close(release)
	time.Sleep(10 * time.Millisecond)
	g.mu.Lock()
	defer g.mu.Unlock()
	if replacement.Done {
		t.Fatal("réponse tardive appliquée")
	}
}

func TestUnavailableCountIsSettledOnce(t *testing.T) {
	g := testGame()
	g.Round.Done = true
	g.Round.Unavailable = true
	if err := g.choose("round", 1); err != nil {
		t.Fatal(err)
	}
	g.settle()
	if g.view().UnavailableRounds != 1 {
		t.Fatal("compte d'indisponibilité incorrect")
	}
}

func TestTeaserUsesTwoDifferentPairs(t *testing.T) {
	pairs := testPairs()
	for k := range pairs {
		pairs[k].Images[0].Data = []byte{byte(k), 0}
		pairs[k].Images[1].Data = []byte{byte(k), 1}
	}
	for n := 0; n < 50; n++ {
		got := teaserImages(pairs)
		if len(got) != 2 || got[0][0] == got[1][0] {
			t.Fatal("les deux images doivent venir de paires différentes")
		}
	}
	if teaserImages(pairs[:1]) != nil {
		t.Fatal("une seule paire ne doit rien montrer")
	}
}
