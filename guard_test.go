package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestUnknownHostRejected(t *testing.T) {
	a := &App{Hosts: allowedHosts("127.0.0.1:8080", "jeu.example.com")}
	for host, want := range map[string]int{"127.0.0.1:8080": 200, "localhost:8080": 200, "JEU.example.com": 200, "evil.example:8080": 421, "127.0.0.1:9999": 421} {
		r := httptest.NewRequest("GET", "/api/config", nil)
		r.Host = host
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d, attendu %d", host, w.Code, want)
		}
	}
}

func TestClientIPTrustsOnlyLocalProxy(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if got := clientIP(r, true); got != "203.0.113.9" {
		t.Fatalf("proxy local: %s", got)
	}
	if got := clientIP(r, false); got != "127.0.0.1" {
		t.Fatalf("proxy non fiable: %s", got)
	}
	r.Header.Del("X-Forwarded-For")
	r.Header.Add("X-Forwarded-For", "203.0.113.9")
	r.Header.Add("X-Forwarded-For", "6.6.6.6")
	if got := clientIP(r, true); got != "6.6.6.6" {
		t.Fatalf("en-tête sur plusieurs lignes: %s", got)
	}
	r.RemoteAddr = "198.51.100.4:5555"
	if got := clientIP(r, true); got != "198.51.100.4" {
		t.Fatalf("X-Forwarded-For accepté hors boucle locale: %s", got)
	}
	r.RemoteAddr = "[2001:db8:1:2:3:4:5:6]:443"
	if got := clientIP(r, false); got != "2001:db8:1:2::/64" {
		t.Fatalf("IPv6 non regroupée: %s", got)
	}
}

func TestSecureCookieBehindProxy(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Forwarded-Proto", "https")
	if !secureRequest(r, true) || secureRequest(r, false) {
		t.Fatal("X-Forwarded-Proto mal interprété")
	}
}

func TestRateLimiterWindow(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(2, time.Hour)
	l.now = func() time.Time { return now }
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("1")
	}
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("2")
	}
	if ok, wait := l.allow("a"); ok || wait != time.Hour {
		t.Fatalf("3e partie acceptée ou attente fausse: %v", wait)
	}
	if ok, _ := l.allow("b"); !ok {
		t.Fatal("autre adresse bloquée")
	}
	now = now.Add(time.Hour + time.Second)
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("fenêtre non glissante")
	}
}

func TestBudgetReserveRefundAndDay(t *testing.T) {
	now := time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC)
	b := newBudget(20)
	b.now = func() time.Time { return now }
	if !b.reserve(10) || !b.reserve(10) || b.reserve(10) || !b.exhausted(10) {
		t.Fatal("plafond non respecté")
	}
	now = now.Add(2 * time.Hour)
	if b.exhausted(10) || !b.reserve(20) {
		t.Fatal("budget non remis à zéro le lendemain")
	}
}

// Une adresse qui relance des parties en boucle est bloquée, cookie renvoyé ou non.
func TestGamesRateLimitedPerIP(t *testing.T) {
	var calls atomic.Int32
	a := &App{Games: map[string]*Game{}, Corpus: testPairs(), Analyzer: analyzerFunc(func(ctx context.Context, _ []byte) (Estimate, error) {
		calls.Add(1)
		return Estimate{0.5, "m"}, nil
	}), Timeout: time.Second, Limiter: newRateLimiter(3, time.Hour)}
	h := a.handler()
	codes := []int{}
	cookie := ""
	for i := 0; i < 5; i++ {
		r := httptest.NewRequest("POST", "/api/games", strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.Header.Set("Cookie", cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		codes = append(codes, w.Code)
		if c := w.Result().Cookies(); len(c) > 0 {
			cookie = c[0].Name + "=" + c[0].Value
		}
	}
	if codes[2] != 200 || codes[3] != 429 || codes[4] != 429 {
		t.Fatalf("codes: %v", codes)
	}
	if len(a.Games) != 1 {
		t.Fatalf("parties remplacées non supprimées: %d", len(a.Games))
	}
}

// Une partie remplacée annule son analyse, et seuls les rounds lancés consomment le budget.
func TestReplacedGameCancelledAndRefunded(t *testing.T) {
	cancelled := make(chan struct{}, 1)
	a := &App{Games: map[string]*Game{}, Corpus: testPairs(), Analyzer: analyzerFunc(func(ctx context.Context, _ []byte) (Estimate, error) {
		<-ctx.Done()
		select {
		case cancelled <- struct{}{}:
		default:
		}
		return Estimate{}, ctx.Err()
	}), Timeout: 5 * time.Second, Budget: newBudget(20)}
	h := a.handler()
	post := func(cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/games", strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.Header.Set("Cookie", cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := post("")
	c := first.Result().Cookies()[0]
	// Réserver la partie entière bloquerait le budget de 20 dès la 3e partie ; ici chaque partie ne prend que 2 appels.
	for i := 0; i < 3; i++ {
		w := post(c.Name + "=" + c.Value)
		if w.Code != 200 {
			t.Fatalf("partie %d: %d %s", i+2, w.Code, w.Body)
		}
		c = w.Result().Cookies()[0]
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("analyse de la partie remplacée non annulée")
	}
}

func TestBudgetExhaustedBlocksNewGames(t *testing.T) {
	a := &App{Games: map[string]*Game{}, Corpus: testPairs(), Analyzer: analyzerFunc(func(context.Context, []byte) (Estimate, error) { return Estimate{0.5, "m"}, nil }), Timeout: time.Second, Budget: newBudget(5)}
	r := httptest.NewRequest("POST", "/api/games", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("code %d", w.Code)
	}
	w = httptest.NewRecorder()
	a.handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/config", nil))
	if !strings.Contains(w.Body.String(), `"live":false`) || !strings.Contains(w.Body.String(), `"budgetExhausted":true`) {
		t.Fatal(w.Body.String())
	}
}

// Le sémaphore borne les appels simultanés, toutes parties confondues.
func TestConcurrentCallsBounded(t *testing.T) {
	var inflight, peak atomic.Int32
	slow := analyzerFunc(func(ctx context.Context, _ []byte) (Estimate, error) {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		inflight.Add(-1)
		return Estimate{0.5, "m"}, nil
	})
	calls := make(chan struct{}, 3)
	games := make([]*Game, 4)
	for i := range games {
		games[i] = &Game{Pairs: testPairs()[:1], Calls: calls}
		games[i].mu.Lock()
		games[i].start(slow, time.Second)
		games[i].mu.Unlock()
	}
	deadline := time.Now().Add(2 * time.Second)
	for _, g := range games {
		for {
			g.mu.Lock()
			done := g.Round.Done
			g.mu.Unlock()
			if done || time.Now().After(deadline) {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if peak.Load() > 3 || peak.Load() == 0 {
		t.Fatalf("pic d'appels simultanés: %d", peak.Load())
	}
}

// Quota atteint en cours de partie : le round suivant se joue sans Jev, sans appel.
func TestRoundWithoutBudgetSkipsJev(t *testing.T) {
	var calls atomic.Int32
	a := analyzerFunc(func(context.Context, []byte) (Estimate, error) { calls.Add(1); return Estimate{0.5, "m"}, nil })
	b := newBudget(2)
	g := &Game{Pairs: testPairs()[:2], Budget: b}
	g.mu.Lock()
	g.start(a, time.Second)
	g.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	g.mu.Lock()
	g.Index++
	g.start(a, time.Second)
	r := g.Round
	g.mu.Unlock()
	if !r.Done || !r.Unavailable || calls.Load() != 2 {
		t.Fatalf("round sans budget: done=%v unavailable=%v appels=%d", r.Done, r.Unavailable, calls.Load())
	}
}
