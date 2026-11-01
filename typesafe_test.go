package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTypeSafeContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer test-secret" || r.Header.Get("TypeSafe-Danger-Zone-Images") != "true" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("contrat HTTP invalide")
		}
		var body struct {
			Model  string            `json:"model"`
			State  map[string]string `json:"state"`
			Assets map[string]struct {
				Type, Format string
				Data         string `json:"data_base64"`
			} `json:"assets"`
			Questions map[string]struct{ Type, Instructions string } `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		ref := body.State["image"]
		asset := body.Assets[ref]
		if len(body.Assets) != 1 || len(ref) != 48 || body.Model != "jev-1.14.0" || asset.Type != "image" || asset.Format != "jpg" || asset.Data != base64.StdEncoding.EncodeToString([]byte("pixels")) || len(body.Questions) != 1 || body.Questions["generated"].Type != "noul" || body.Questions["generated"].Instructions != question {
			t.Error("contrat JSON invalide")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"jev-1.14.0","answers":{"generated":{"type":"noul","noul":0.81}},"usage":{"input_tokens":100}}`))
	}))
	defer server.Close()
	client := &TypeSafe{"test-secret", "jev-1.14.0", server.URL + "/v1/systemone", server.Client()}
	e, err := client.Analyze(context.Background(), []byte("pixels"))
	if err != nil || e.Value != .81 || e.Model != "jev-1.14.0" {
		t.Fatalf("%+v %v", e, err)
	}
}
func TestTypeSafeRejectsInvalidResponses(t *testing.T) {
	for _, body := range []string{`{}`, `not json`, `{"model":"m","answers":{"generated":{"type":"noul"}}}`, `{"model":"m","answers":{"generated":{"type":"noul","noul":1.1}}}`, `{"model":"m","answers":{"generated":{"type":"choice","noul":0.5}}}`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer s.Close()
			c := &TypeSafe{"k", "m", s.URL, s.Client()}
			if _, err := c.Analyze(context.Background(), nil); err == nil {
				t.Fatal("réponse invalide acceptée")
			}
		})
	}
}
func TestTypeSafeHTTPError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429); w.Write([]byte("secret response")) }))
	defer s.Close()
	c := &TypeSafe{"k", "m", s.URL, s.Client()}
	_, err := c.Analyze(context.Background(), nil)
	if err == nil || strings.Contains(err.Error(), "secret") || reasonOf(err) != ReasonQuota {
		t.Fatalf("%v", err)
	}
}

// Chaque statut et chaque panne donnent une cause précise, jamais le corps de la réponse.
func TestTypeSafeErrorReasons(t *testing.T) {
	for status, want := range map[int]string{401: ReasonAuth, 403: ReasonAuth, 402: ReasonQuota, 429: ReasonQuota, 500: ReasonServer, 503: ReasonServer, 400: ReasonRejected, 413: ReasonRejected} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		_, err := (&TypeSafe{"k", "m", s.URL, s.Client()}).Analyze(context.Background(), nil)
		s.Close()
		if reasonOf(err) != want {
			t.Errorf("HTTP %d: %v, attendu %s", status, err, want)
		}
	}
	for body, want := range map[string]string{`pas du json`: ReasonInvalid, `{"model":"m","answers":{}}`: ReasonInvalid, `{"model":"m","answers":{"generated":{"type":"noul","noul":"0.5"}}}`: ReasonInvalid, `{"model":"m","answers":{"generated":{"type":"noul","noul":1.5}}}`: ReasonInvalid} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		_, err := (&TypeSafe{"k", "m", s.URL, s.Client()}).Analyze(context.Background(), nil)
		s.Close()
		if reasonOf(err) != want {
			t.Errorf("%s: %v", body, err)
		}
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := s.URL
	s.Close()
	if _, err := (&TypeSafe{"k", "m", url, http.DefaultClient}).Analyze(context.Background(), nil); reasonOf(err) != ReasonNetwork {
		t.Errorf("serveur fermé: %v", err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := (&TypeSafe{"k", "m", slow.URL, slow.Client()}).Analyze(ctx, nil); reasonOf(err) != ReasonTimeout {
		t.Errorf("délai: %v", err)
	}
}
func TestTypeSafeCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer s.Close()
	c := &TypeSafe{"k", "m", s.URL, s.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Analyze(ctx, nil); err == nil {
		t.Fatal("annulation ignorée")
	}
}

func TestProbeJevUsesOneRequest(t *testing.T) {
	var calls atomic.Int32
	a := analyzerFunc(func(ctx context.Context, b []byte) (Estimate, error) {
		calls.Add(1)
		if _, ok := ctx.Deadline(); !ok {
			t.Error("délai manquant")
		}
		if string(b) != "pixels" {
			t.Error("image modifiée")
		}
		return Estimate{.6, "local"}, nil
	})
	e, elapsed, err := probeJev(a, []byte("pixels"), time.Second)
	if err != nil || e.Model != "local" || elapsed < 0 || calls.Load() != 1 {
		t.Fatalf("sonde invalide: %+v %v", e, err)
	}
}
