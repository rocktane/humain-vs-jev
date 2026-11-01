package main

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Protections d'un serveur public dont chaque partie coûte des appels TypeSafe facturés.

// allowedHosts liste les valeurs de Host acceptées : l'adresse d'écoute, ses alias locaux
// et le nom public éventuel. Refuser les autres bloque le DNS rebinding.
func allowedHosts(addr, public string) map[string]bool {
	hosts := map[string]bool{}
	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		for _, h := range []string{host, "localhost", "127.0.0.1", "[::1]"} {
			if h != "" && h != "0.0.0.0" && h != "::" {
				hosts[strings.ToLower(h)+":"+port] = true
			}
		}
	}
	for _, h := range strings.Split(public, ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			hosts[h] = true
		}
	}
	return hosts
}

// clientIP renvoie l'adresse du visiteur. Derrière un reverse proxy local de confiance,
// elle vient de la dernière entrée de X-Forwarded-For, ajoutée par le proxy lui-même.
// Les adresses IPv6 sont regroupées par /64, la taille habituelle d'un abonnement.
func clientIP(r *http.Request, trustProxy bool) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if trustProxy && ip != nil && ip.IsLoopback() {
		if parts := strings.Split(r.Header.Get("X-Forwarded-For"), ","); parts[len(parts)-1] != "" {
			if fwd := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); fwd != nil {
				ip = fwd
			}
		}
	}
	if ip == nil {
		return host
	}
	if ip.To4() == nil {
		return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
	return ip.String()
}

// secureRequest dit si le visiteur est en HTTPS, directement ou via le proxy de confiance.
func secureRequest(r *http.Request, trustProxy bool) bool {
	if r.TLS != nil {
		return true
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return trustProxy && ip != nil && ip.IsLoopback() && r.Header.Get("X-Forwarded-Proto") == "https"
}

// RateLimiter autorise au plus Max événements par clé sur une fenêtre glissante.
type RateLimiter struct {
	mu     sync.Mutex
	Max    int
	Window time.Duration
	seen   map[string][]time.Time
	now    func() time.Time
}

func newRateLimiter(max int, window time.Duration) *RateLimiter {
	return &RateLimiter{Max: max, Window: window, seen: map[string][]time.Time{}, now: time.Now}
}

// allow enregistre un événement s'il reste de la place, sinon renvoie l'attente avant le prochain.
func (l *RateLimiter) allow(key string) (bool, time.Duration) {
	if l == nil || l.Max <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cut := now.Add(-l.Window)
	// Ménage : la carte ne garde que les clés actives.
	for k, ts := range l.seen {
		if len(ts) == 0 || !ts[len(ts)-1].After(cut) {
			delete(l.seen, k)
		}
	}
	kept := l.seen[key][:0]
	for _, t := range l.seen[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.Max {
		l.seen[key] = kept
		return false, kept[0].Sub(cut)
	}
	l.seen[key] = append(kept, now)
	return true, 0
}

// Budget plafonne les appels TypeSafe par jour (UTC). Il vit en mémoire : un redémarrage le remet à zéro.
type Budget struct {
	mu   sync.Mutex
	Max  int
	day  string
	used int
	now  func() time.Time
}

func newBudget(max int) *Budget { return &Budget{Max: max, now: time.Now} }

func (b *Budget) roll() {
	if d := b.now().UTC().Format("2006-01-02"); d != b.day {
		b.day, b.used = d, 0
	}
}

// reserve réserve n appels, ou aucun s'il n'en reste pas assez.
func (b *Budget) reserve(n int) bool {
	if b == nil || b.Max <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.roll()
	if b.used+n > b.Max {
		return false
	}
	b.used += n
	return true
}

// refund rend les appels réservés mais jamais lancés (partie abandonnée).
func (b *Budget) refund(n int) {
	if b == nil || b.Max <= 0 || n <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.roll()
	b.used = max(0, b.used-n)
}

// exhausted dit s'il ne reste pas de quoi jouer une partie complète.
func (b *Budget) exhausted(perGame int) bool {
	if b == nil || b.Max <= 0 {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.roll()
	return b.used+perGame > b.Max
}
