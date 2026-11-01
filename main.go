package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var frontend embed.FS

type App struct {
	mu       sync.Mutex
	Games    map[string]*Game
	Corpus   []Pair
	Teaser   [][]byte
	Analyzer Analyzer
	Timeout  time.Duration
	Stats    *Stats

	Hosts      map[string]bool // Host acceptés ; nil : pas de contrôle (tests)
	TrustProxy bool            // X-Forwarded-For et -Proto fiables quand la requête vient de la boucle locale
	Limiter    *RateLimiter    // parties par adresse IP
	Budget     *Budget         // appels TypeSafe par jour
	Calls      chan struct{}   // appels TypeSafe simultanés
	Health     *Health         // pause de Jev après des refus répétés
}

// callsPerGame : deux images analysées par round, cinq rounds.
const callsPerGame = 10

func envInt(key string, fallback int) int {
	s := os.Getenv(key)
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		log.Fatalf("%s doit être un entier positif ou nul", key)
	}
	return n
}

func env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
func main() {
	check := flag.Bool("check-corpus", false, "Valider le manifeste et les images, sans lancer le serveur")
	checkJev := flag.Bool("check-jev", false, "Tester une image du corpus avec TypeSafe (un appel réel potentiellement facturé)")
	flag.Parse()
	if err := loadEnvFile(".env"); err != nil {
		log.Fatal(err)
	}
	manifest := env("CORPUS_MANIFEST", "corpus/manifest.json")
	corpus, err := loadCorpus(manifest)
	if errors.Is(err, fs.ErrNotExist) {
		// Un clone neuf n'a pas de corpus : le serveur démarre, Jev reste indisponible.
		log.Printf("Aucun manifeste %s : partez de corpus/manifest.example.json pour importer vos images.", manifest)
		corpus, err = nil, nil
	}
	if err != nil {
		log.Fatal("Corpus: ", err)
	}
	if *check {
		log.Printf("Corpus valide : %d paires (5 minimum pour jouer avec Jev)", len(corpus))
		return
	}
	timeout, err := time.ParseDuration(env("JEV_TIMEOUT", "20s"))
	if err != nil || timeout <= 0 || timeout > time.Minute {
		log.Fatal("JEV_TIMEOUT doit être > 0 et <= 1m")
	}
	stats, err := loadStats(env("STATS_FILE", "stats.json"))
	if err != nil {
		log.Fatal("Statistiques: ", err)
	}
	addr := env("ADDR", "127.0.0.1:8080")
	app := &App{Games: map[string]*Game{}, Corpus: corpus, Teaser: teaserImages(corpus), Timeout: timeout, Stats: stats,
		Hosts:      allowedHosts(addr, os.Getenv("PUBLIC_HOST")),
		TrustProxy: os.Getenv("TRUST_PROXY") == "1",
		Limiter:    newRateLimiter(envInt("GAMES_PER_HOUR", 20), time.Hour),
		Budget:     newBudget(envInt("DAILY_CALLS", 1000)),
		Health:     newHealth(3, 10*time.Minute),
	}
	if n := envInt("MAX_CONCURRENT_CALLS", 8); n > 0 {
		app.Calls = make(chan struct{}, n)
	}
	if key := os.Getenv("TYPESAFE_API_KEY"); key != "" {
		app.Analyzer = &TypeSafe{key, env("TYPESAFE_MODEL", "jev-1.14.0"), typesafeEndpoint, &http.Client{Timeout: timeout}}
	}
	if *checkJev {
		if app.Analyzer == nil {
			log.Fatal("Test Jev impossible : TYPESAFE_API_KEY absente")
		}
		if len(corpus) < 5 {
			log.Fatal("Test Jev impossible : importer cinq paires vérifiées d'abord")
		}
		estimate, elapsed, err := probeJev(app.Analyzer, corpus[0].Images[0].Data, timeout)
		if err != nil {
			log.Fatal("Test Jev échoué : ", err)
		}
		log.Printf("Test Jev réussi : modèle %s, estimation %.4f, temps réel %s. Ce test n'est pas un benchmark.", estimate.Model, estimate.Value, elapsed.Round(time.Millisecond))
		return
	}
	log.Printf("Humain vs Jev sur http://%s (%d paires importées)", addr, len(corpus))
	server := &http.Server{Addr: addr, Handler: app.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(server.ListenAndServe())
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	writeJSON(w, map[string]string{"error": msg})
}
func (a *App) session(r *http.Request) *Game {
	c, err := r.Cookie("game")
	if err != nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	g := a.Games[c.Value]
	if g == nil || time.Now().After(g.Expires) {
		return nil
	}
	return g
}

// drop retire une partie et annule son analyse en cours. Appelé avec a.mu.
func (a *App) drop(id string, g *Game, why string) {
	g.mu.Lock()
	g.abandon(why)
	g.stop()
	g.mu.Unlock()
	delete(a.Games, id)
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		fail(w, 400, "Requête invalide.")
		return false
	}
	return true
}
func (a *App) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		exhausted, paused := a.Budget.exhausted(callsPerGame), a.Health.down()
		writeJSON(w, map[string]any{"live": a.Analyzer != nil && len(a.Corpus) >= 5 && !exhausted && !paused, "hasKey": a.Analyzer != nil, "pairs": len(a.Corpus), "budgetExhausted": exhausted, "jevPaused": paused, "stats": a.Stats.snapshot()})
	})
	mux.HandleFunc("POST /api/games", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !decode(w, r, &in) {
			return
		}
		pairs := a.Corpus
		if a.Analyzer == nil || len(pairs) < 5 {
			fail(w, 409, "Jev indisponible : clé et cinq paires vérifiées nécessaires.")
			return
		}
		if a.Health.down() {
			fail(w, 503, "Jev fait une pause. Réessaie dans quelques minutes.")
			return
		}
		if ok, wait := a.Limiter.allow(clientIP(r, a.TrustProxy)); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			fail(w, 429, fmt.Sprintf("Trop de parties lancées. Réessaie dans %d min.", int(wait.Minutes())+1))
			return
		}
		// Only one active game per browser; stale games expire and memory is bounded.
		a.mu.Lock()
		defer a.mu.Unlock()
		for id, g := range a.Games {
			if time.Now().After(g.Expires) {
				a.drop(id, g, "expirée")
			}
		}
		if c, err := r.Cookie("game"); err == nil {
			if g := a.Games[c.Value]; g != nil {
				a.drop(c.Value, g, "nouvelle partie lancée")
			}
		}
		if len(a.Games) >= 200 {
			fail(w, 503, "Plateau complet, réessaie plus tard.")
			return
		}
		// Une partie ne démarre que s'il reste de quoi la jouer entière ; chaque round réserve ensuite ses appels.
		if a.Budget.exhausted(callsPerGame) {
			fail(w, 503, "Jev a atteint son quota d’analyses pour aujourd’hui. Reviens demain.")
			return
		}
		pool := append([]Pair(nil), pairs...)
		for i := len(pool) - 1; i > 0; i-- {
			j := randomN(i + 1)
			pool[i], pool[j] = pool[j], pool[i]
		}
		g := &Game{ID: token(), Tag: token()[:8], Pairs: pool[:5], Expires: time.Now().Add(2 * time.Hour), Stats: a.Stats, Calls: a.Calls, Budget: a.Budget, Health: a.Health}
		g.mu.Lock()
		defer g.mu.Unlock()
		g.start(a.Analyzer, a.Timeout)
		a.Games[g.ID] = g
		log.Printf("partie %s démarrée (%d en cours)", g.Tag, len(a.Games))
		http.SetCookie(w, &http.Cookie{Name: "game", Value: g.ID, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secureRequest(r, a.TrustProxy), MaxAge: 7200})
		writeJSON(w, g.view())
	})
	mux.HandleFunc("GET /api/round", func(w http.ResponseWriter, r *http.Request) {
		g := a.session(r)
		if g == nil {
			fail(w, 401, "Partie expirée. Relance une partie.")
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		writeJSON(w, g.view())
	})
	mux.HandleFunc("POST /api/choose", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RoundID string `json:"roundId"`
			Choice  *int   `json:"choice"`
		}
		if !decode(w, r, &in) {
			return
		}
		g := a.session(r)
		if g == nil {
			fail(w, 401, "Partie expirée.")
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		if in.Choice == nil {
			fail(w, 400, "Choix requis.")
			return
		}
		if err := g.choose(in.RoundID, *in.Choice); err != nil {
			fail(w, 409, err.Error())
			return
		}
		writeJSON(w, g.view())
	})
	mux.HandleFunc("POST /api/next", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RoundID string `json:"roundId"`
		}
		if !decode(w, r, &in) {
			return
		}
		g := a.session(r)
		if g == nil {
			fail(w, 401, "Partie expirée.")
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.Round.ID != in.RoundID || !g.Round.Scored || g.Index >= 4 {
			fail(w, 409, "Impossible de passer au round suivant.")
			return
		}
		g.Index++
		g.start(a.Analyzer, a.Timeout)
		writeJSON(w, g.view())
	})
	mux.HandleFunc("GET /images/{id}", func(w http.ResponseWriter, r *http.Request) {
		g := a.session(r)
		if g == nil {
			http.NotFound(w, r)
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		for i, id := range g.Round.ImageIDs {
			if r.PathValue("id") == id {
				w.Header().Set("Content-Type", "image/jpeg")
				_, _ = w.Write(g.Round.Pair.Images[i].Data)
				return
			}
		}
		http.NotFound(w, r)
	})
	// Home page visuals: two images from different pairs, never labelled.
	mux.HandleFunc("GET /teaser/{n}", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil || n < 0 || n >= len(a.Teaser) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(a.Teaser[n])
	})
	files, _ := fs.Sub(frontend, "web")
	mux.Handle("GET /", http.FileServer(http.FS(files)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.Hosts != nil && !a.Hosts[strings.ToLower(r.Host)] {
			fail(w, 421, "Hôte inconnu.")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self'; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if r.Method == "POST" {
			origin := r.Header.Get("Origin")
			if origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) || (u.Scheme != "http" && u.Scheme != "https") {
					fail(w, 403, "Origine refusée.")
					return
				}
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				fail(w, 403, "Origine refusée.")
				return
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				fail(w, 415, "JSON requis.")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

// Explicit CLI opt-in: exactly one request, no retry and no provenance sent.
func probeJev(analyzer Analyzer, pixels []byte, timeout time.Duration) (Estimate, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	started := time.Now()
	estimate, err := analyzer.Analyze(ctx, pixels)
	if err != nil {
		return Estimate{}, time.Since(started), fmt.Errorf("analyse indisponible: %w", err)
	}
	return estimate, time.Since(started), nil
}
