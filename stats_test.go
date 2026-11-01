package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func finishedGame(human, jev, unavailable int) *Game {
	return &Game{Pairs: make([]Pair, 5), HumanScore: human, JevScore: jev, UnavailableRounds: unavailable, HumanMS: 5000, JevMS: 2000}
}

func TestStatsRecord(t *testing.T) {
	s := &Stats{}
	s.record(finishedGame(4, 3, 0))
	s.record(finishedGame(2, 5, 0))
	s.record(finishedGame(3, 3, 0))
	s.record(finishedGame(5, 0, 1)) // Jev indisponible : ignorée
	got := s.snapshot()
	want := StatsData{Games: 3, Rounds: 15, HumanWins: 1, JevWins: 1, Ties: 1, HumanCorrect: 9, JevCorrect: 11, HumanMS: 15000, JevMS: 6000}
	if got != want {
		t.Fatalf("stats: %+v, attendu %+v", got, want)
	}
	var none *Stats
	none.record(finishedGame(1, 1, 0)) // sans stats configurées, rien ne casse
	if none.snapshot() != (StatsData{}) {
		t.Fatal("stats nil non vides")
	}
}

func TestStatsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	s, err := loadStats(path)
	if err != nil {
		t.Fatal(err)
	}
	s.record(finishedGame(4, 3, 0))
	again, err := loadStats(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.snapshot() != s.snapshot() {
		t.Fatalf("stats perdues: %+v", again.snapshot())
	}
}

func TestStatsRejectCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	os.WriteFile(path, []byte("{"), 0o600)
	if _, err := loadStats(path); err == nil {
		t.Fatal("fichier corrompu accepté : il serait écrasé")
	}
}

// Le dernier round peut se terminer côté Jev après le clic du joueur : la partie compte quand même, une seule fois.
func TestStatsRecordedWhenJevAnswersLast(t *testing.T) {
	release := make(chan struct{})
	a := analyzerFunc(func(context.Context, []byte) (Estimate, error) {
		<-release
		return Estimate{.9, "test"}, nil
	})
	stats := &Stats{}
	g := &Game{Pairs: make([]Pair, 5), Index: 4, Stats: stats}
	g.mu.Lock()
	g.start(a, time.Second)
	if err := g.choose(g.Round.ID, 0); err != nil {
		t.Fatal(err)
	}
	g.mu.Unlock()
	if stats.snapshot().Games != 0 {
		t.Fatal("partie comptée avant la réponse de Jev")
	}
	close(release)
	for i := 0; i < 1000 && stats.snapshot().Games == 0; i++ {
		time.Sleep(time.Millisecond)
	}
	g.mu.Lock()
	g.settle()
	g.mu.Unlock()
	if got := stats.snapshot(); got.Games != 1 || got.Rounds != 5 {
		t.Fatalf("stats: %+v", got)
	}
}
