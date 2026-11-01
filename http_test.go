package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestFullLiveHTTPWithLocalProvider(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint("providerFailure=", fail), func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Count only analyses sent by the game, not stray connections to the test port.
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-only" {
					http.NotFound(w, r)
					return
				}
				calls.Add(1)
				io.Copy(io.Discard, r.Body)
				if fail {
					w.WriteHeader(503)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"model":"local-test","answers":{"generated":{"type":"noul","noul":0.5}}}`)
			}))
			defer provider.Close()
			client := &TypeSafe{Key: "test-only", Model: "local-test", Endpoint: provider.URL, HTTP: provider.Client()}
			result, stats := exerciseFullGameHTTP(t, client)
			if calls.Load() != 10 {
				t.Fatalf("nombre d'appels: %d", calls.Load())
			}
			if result.JevScore != 0 {
				t.Fatal("égalité ou panne doit donner zéro point Jev")
			}
			if fail && result.UnavailableRounds != 5 {
				t.Fatal("rounds indisponibles non comptés")
			}
			if !fail && result.UnavailableRounds != 0 {
				t.Fatal("faux échec")
			}
			// Une partie complète compte ; une partie où Jev a échoué est ignorée.
			if want := map[bool]int{false: 1, true: 0}[fail]; stats.Games != want || stats.Rounds != 5*want {
				t.Fatalf("stats: %+v", stats)
			}
		})
	}
}

func exerciseFullGameHTTP(t *testing.T, analyzer Analyzer) (View, StatsData) {
	t.Helper()
	// Procedural fixtures exercise routing only; never advertised as verified photos.
	app := &App{Games: map[string]*Game{}, Corpus: testPairs(), Analyzer: analyzer, Timeout: time.Second, Stats: &Stats{}}
	s := httptest.NewServer(app.handler())
	defer s.Close()
	jar, _ := cookiejar.New(nil)
	c := s.Client()
	c.Jar = jar
	request := func(path string, body any) (int, View) {
		t.Helper()
		var r *http.Response
		var err error
		if body == nil {
			r, err = c.Get(s.URL + path)
		} else {
			b, _ := json.Marshal(body)
			r, err = c.Post(s.URL+path, "application/json", bytes.NewReader(b))
		}
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var v View
		json.NewDecoder(r.Body).Decode(&v)
		return r.StatusCode, v
	}
	status, v := request("/api/games", map[string]string{})
	if status != 200 || v.Reveal != nil {
		t.Fatalf("début invalide: %d", status)
	}
	seen := map[string]bool{}
	for round := 1; round <= 5; round++ {
		if v.Number != round || seen[v.RoundID] {
			t.Fatal("progression invalide")
		}
		seen[v.RoundID] = true
		for _, path := range v.Images {
			res, err := c.Get(s.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" || len(data) == 0 {
				t.Fatal("image invalide")
			}
		}
		// Poll until both analyses are complete; truth stays hidden.
		for i := 0; i < 1000; i++ {
			_, v = request("/api/round", nil)
			if v.Ready {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if v.Reveal != nil {
			t.Fatal("révélation sans choix")
		}
		old := v.RoundID
		status, v = request("/api/choose", map[string]any{"roundId": old, "choice": 0})
		if status != 200 || v.Reveal == nil {
			t.Fatal("révélation absente")
		}
		if status, _ = request("/api/choose", map[string]any{"roundId": old, "choice": 1}); status != 409 {
			t.Fatal("double soumission acceptée")
		}
		if round < 5 {
			status, v = request("/api/next", map[string]string{"roundId": old})
			if status != 200 {
				t.Fatal("round suivant refusé")
			}
			if status, _ = request("/api/choose", map[string]any{"roundId": old, "choice": 1}); status != 409 {
				t.Fatal("ancien round accepté")
			}
		} else if status, _ = request("/api/next", map[string]string{"roundId": old}); status != 409 {
			t.Fatal("sixième round accepté")
		}
	}
	if v.HumanScore < 0 || v.HumanScore > 5 || v.JevScore < 0 || v.JevScore > 5 {
		t.Fatal("scores invalides")
	}
	var config struct{ Stats StatsData }
	r, err := c.Get(s.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		t.Fatal(err)
	}
	return v, config.Stats
}
func TestCrossOriginRejected(t *testing.T) {
	a := &App{}
	r := httptest.NewRequest("POST", "http://localhost/api/games", bytes.NewBufferString(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("origine externe acceptée")
	}
}
func TestGameNeedsAnalyzerAndCorpus(t *testing.T) {
	for _, app := range []*App{{Games: map[string]*Game{}, Corpus: testPairs()}, {Games: map[string]*Game{}, Analyzer: &TypeSafe{}}} {
		r := httptest.NewRequest("POST", "http://localhost/api/games", bytes.NewBufferString(`{}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		app.handler().ServeHTTP(w, r)
		if w.Code != 409 {
			t.Fatalf("partie créée sans Jev ou sans corpus: %d", w.Code)
		}
	}
}
