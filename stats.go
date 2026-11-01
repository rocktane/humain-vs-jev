package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// StatsData holds aggregate counters over complete games: all rounds played and
// answered by Jev. No per-player data is kept.
type StatsData struct {
	Games        int   `json:"games"`
	Rounds       int   `json:"rounds"`
	HumanWins    int   `json:"humanWins"`
	JevWins      int   `json:"jevWins"`
	Ties         int   `json:"ties"`
	HumanCorrect int   `json:"humanCorrect"`
	JevCorrect   int   `json:"jevCorrect"`
	HumanMS      int64 `json:"humanMs"` // player time, from round display to click
	JevMS        int64 `json:"jevMs"`   // both analyses, run in parallel
}

// Stats is safe for concurrent use. Lock order: Game.mu, then Stats.mu, never the reverse.
type Stats struct {
	mu   sync.Mutex
	path string // empty: memory only
	data StatsData
}

// loadStats reads the counters from path. A missing file starts from zero; an
// unreadable one is an error, so a restart never silently overwrites history.
func loadStats(path string) (*Stats, error) {
	s := &Stats{path: path}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, fmt.Errorf("%s illisible: %w", path, err)
	}
	return s, nil
}

func (s *Stats) snapshot() StatsData {
	if s == nil {
		return StatsData{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data
}

// record adds one finished game. Called once per game, with g.mu held.
// A game where Jev failed on any round is not a fair duel and is ignored.
func (s *Stats) record(g *Game) {
	if s == nil || g.UnavailableRounds > 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := &s.data
	d.Games++
	d.Rounds += len(g.Pairs)
	d.HumanCorrect += g.HumanScore
	d.JevCorrect += g.JevScore
	d.HumanMS += g.HumanMS
	d.JevMS += g.JevMS
	switch {
	case g.HumanScore > g.JevScore:
		d.HumanWins++
	case g.HumanScore < g.JevScore:
		d.JevWins++
	default:
		d.Ties++
	}
	if err := s.save(); err != nil {
		log.Printf("stats: %v", err)
	}
}

// save writes atomically: a crash mid-write leaves the previous file intact.
func (s *Stats) save() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".stats-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
