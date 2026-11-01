package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Fournisseur local qui renvoie les statuts donnés dans l'ordre, puis une réponse valide.
func flakyProvider(t *testing.T, statuses ...int) (*TypeSafe, *atomic.Int32) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		n := int(calls.Add(1))
		if n <= len(statuses) {
			w.WriteHeader(statuses[n-1])
			io.WriteString(w, `{"error":{"code":"some_code","message":"never logged"}}`)
			return
		}
		io.WriteString(w, `{"model":"m","answers":{"generated":{"type":"noul","noul":0.7}}}`)
	}))
	t.Cleanup(s.Close)
	return &TypeSafe{"k", "m", s.URL, s.Client()}, &calls
}

func TestRetryOnceAfterTransientError(t *testing.T) {
	c, calls := flakyProvider(t, 503)
	b := newBudget(10)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e, err := analyzeOnce(ctx, c, nil, b, nil)
	if err != nil || e.Value != 0.7 || calls.Load() != 2 {
		t.Fatalf("%v %v appels=%d", e, err, calls.Load())
	}
	if !b.reserve(9) || b.reserve(1) {
		t.Fatal("le second essai doit compter dans le budget")
	}
}

func TestRetryOnlyOnce(t *testing.T) {
	c, calls := flakyProvider(t, 503, 503, 503)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := analyzeOnce(ctx, c, nil, nil, nil); reasonOf(err) != ReasonServer || calls.Load() != 2 {
		t.Fatalf("%v appels=%d", err, calls.Load())
	}
}

func TestNoRetryWhenUseless(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Clé refusée : un second essai échouerait pareil.
	c, calls := flakyProvider(t, 401)
	if _, err := analyzeOnce(ctx, c, nil, nil, nil); reasonOf(err) != ReasonAuth || calls.Load() != 1 {
		t.Fatalf("401: %v appels=%d", err, calls.Load())
	}
	// Budget du jour épuisé.
	b := newBudget(1)
	b.reserve(1)
	c, calls = flakyProvider(t, 503)
	if _, err := analyzeOnce(ctx, c, nil, b, nil); err == nil || calls.Load() != 1 {
		t.Fatalf("budget épuisé: %v appels=%d", err, calls.Load())
	}
	// Délai du round trop court.
	short, cancelShort := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancelShort()
	c, calls = flakyProvider(t, 503)
	if _, err := analyzeOnce(short, c, nil, nil, nil); err == nil || calls.Load() != 1 {
		t.Fatalf("délai court: %v appels=%d", err, calls.Load())
	}
}

func TestHealthPausesAfterRepeatedRefusals(t *testing.T) {
	now := time.Unix(0, 0)
	h := newHealth(3, 10*time.Minute)
	h.now = func() time.Time { return now }
	h.record(ReasonAuth)
	h.record(ReasonAuth)
	h.record("")
	h.record(ReasonAuth)
	h.record(ReasonServer)
	if h.down() {
		t.Fatal("un succès ou une panne passagère remet le compteur à zéro ou ne compte pas")
	}
	h.record(ReasonAuth)
	h.record(ReasonRejected)
	if !h.down() {
		t.Fatal("trois refus d'affilée doivent couper Jev")
	}
	now = now.Add(11 * time.Minute)
	if h.down() {
		t.Fatal("la pause doit finir")
	}
}

func TestPausedJevBlocksNewGames(t *testing.T) {
	h := newHealth(1, time.Minute)
	h.record(ReasonAuth)
	a := &App{Games: map[string]*Game{}, Corpus: testPairs(), Analyzer: analyzerFunc(func(context.Context, []byte) (Estimate, error) { return Estimate{0.5, "m"}, nil }), Timeout: time.Second, Health: h}
	r := httptest.NewRequest("POST", "/api/games", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("code %d", w.Code)
	}
	w = httptest.NewRecorder()
	a.handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/config", nil))
	if !strings.Contains(w.Body.String(), `"jevPaused":true`) || !strings.Contains(w.Body.String(), `"live":false`) {
		t.Fatal(w.Body.String())
	}
}

func TestErrorCodeNeverFreeText(t *testing.T) {
	for body, want := range map[string]string{
		`{"error":{"code":"invalid_api_key","message":"clé sk-123 refusée"}}`: "invalid_api_key",
		`{"error":{"type":"rate_limit_error"}}`:                               "rate_limit_error",
		`{"code":"quota.exceeded"}`:                                           "quota.exceeded",
		`{"error":"Your key sk-abc is invalid"}`:                              "",
		`{"error":"unauthorized"}`:                                            "unauthorized",
		`<html>`:                                                              "",
	} {
		if got := errorCode(strings.NewReader(body)); got != want {
			t.Errorf("%s: %q", body, got)
		}
	}
	for h, want := range map[string]time.Duration{"2": 2 * time.Second, "60": 0, "": 0, "-1": 0, "Wed, 21 Oct 2015 07:28:00 GMT": 0} {
		if got := retryAfter(h); got != want {
			t.Errorf("Retry-After %q: %v", h, got)
		}
	}
}
