package main

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

// Health coupe Jev quelques minutes quand TypeSafe refuse plusieurs appels de suite pour une
// raison qui ne passera pas toute seule (clé refusée, requête rejetée). Sans lui, chaque partie
// lancée échouerait à tous ses rounds. Un appel réussi remet le compteur à zéro.
type Health struct {
	mu        sync.Mutex
	Threshold int
	Pause     time.Duration
	failures  int
	until     time.Time
	now       func() time.Time
}

func newHealth(threshold int, pause time.Duration) *Health {
	return &Health{Threshold: threshold, Pause: pause, now: time.Now}
}

// record tient compte du résultat d'un appel : reason vide pour un succès.
func (h *Health) record(reason string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch reason {
	case "":
		h.failures = 0
	case ReasonAuth, ReasonRejected:
		h.failures++
		if h.failures >= h.Threshold {
			h.until = h.now().Add(h.Pause)
			h.failures = 0
			log.Printf("Jev en pause %s : %d refus de TypeSafe d'affilée (%s)", h.Pause, h.Threshold, reason)
		}
	}
}

// down dit si Jev est en pause.
func (h *Health) down() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now().Before(h.until)
}

// retryable : erreurs passagères qui méritent un second essai.
func retryable(reason string) bool {
	return reason == ReasonServer || reason == ReasonNetwork || reason == ReasonQuota
}

// analyzeOnce appelle Jev et réessaie une fois après une erreur passagère, si le délai du
// round et le budget du jour le permettent. Le second essai respecte Retry-After quand il est court.
func analyzeOnce(ctx context.Context, a Analyzer, data []byte, budget *Budget, health *Health) (Estimate, error) {
	e, err := a.Analyze(ctx, data)
	health.record(reasonIfErr(err))
	if err == nil || !retryable(reasonOf(err)) {
		return e, err
	}
	wait := 400 * time.Millisecond
	var je *JevError
	if errors.As(err, &je) && je.RetryAfter > 0 {
		wait = je.RetryAfter
	}
	// Un second essai n'a de sens que s'il lui reste au moins une seconde après l'attente.
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait+time.Second {
		return e, err
	}
	if !budget.reserve(1) {
		return e, err
	}
	select {
	case <-time.After(wait):
	case <-ctx.Done():
		return e, err
	}
	log.Printf("nouvel essai après %v", err)
	e, err = a.Analyze(ctx, data)
	health.record(reasonIfErr(err))
	return e, err
}

func reasonIfErr(err error) string {
	if err == nil {
		return ""
	}
	return reasonOf(err)
}
