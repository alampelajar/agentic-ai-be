package httpguard

import (
	"strings"
	"sync"
	"time"
)

type failures struct {
	count       int
	first       time.Time
	lockedUntil time.Time
}

// LoginGuard slows down password guessing. Failures are counted per address and
// email together (a few mistakes by the real user from their own device lock only
// that pair), and per email across all addresses at a much higher threshold (a
// botnet spreading guesses over many addresses). A locked pair is refused without
// even checking the password, so a correct guess made during the lock does not
// reveal itself.
//
// The per-email lock can be triggered on purpose by someone who knows an address,
// which is why its threshold is high and its lock short. State is in memory, so it
// is per process and forgotten on restart.
type LoginGuard struct {
	MaxPerPair    int
	MaxPerAccount int
	Window        time.Duration // failures older than this are forgotten
	Lock          time.Duration
	now           func() time.Time

	mu      sync.Mutex
	entries map[string]*failures
}

func NewLoginGuard() *LoginGuard {
	return &LoginGuard{MaxPerPair: 5, MaxPerAccount: 50, Window: 15 * time.Minute, Lock: 15 * time.Minute, now: time.Now, entries: map[string]*failures{}}
}

func pairKey(email, ip string) string { return "pair|" + strings.ToLower(email) + "|" + ClientKey(ip) }
func accountKey(email string) string  { return "acct|" + strings.ToLower(email) }

// Check says whether this address may try this email now, and for how long it
// must wait if not.
func (g *LoginGuard) Check(email, ip string) (locked bool, wait time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for _, key := range []string{pairKey(email, ip), accountKey(email)} {
		if entry, ok := g.entries[key]; ok && now.Before(entry.lockedUntil) {
			if remaining := entry.lockedUntil.Sub(now); remaining > wait {
				wait = remaining
			}
			locked = true
		}
	}
	return locked, wait
}

// Fail records a wrong password.
func (g *LoginGuard) Fail(email, ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if len(g.entries) >= maxTrackedKeys {
		g.sweep(now)
	}
	g.record(pairKey(email, ip), g.MaxPerPair, now)
	g.record(accountKey(email), g.MaxPerAccount, now)
}

func (g *LoginGuard) record(key string, limit int, now time.Time) {
	entry, ok := g.entries[key]
	if !ok {
		if len(g.entries) >= maxTrackedKeys {
			return // full of live entries: do not grow, the per-address rate limit still applies
		}
		entry = &failures{first: now}
		g.entries[key] = entry
	}
	if now.Sub(entry.first) > g.Window && !now.Before(entry.lockedUntil) {
		*entry = failures{first: now}
	}
	entry.count++
	if entry.count >= limit {
		entry.lockedUntil = now.Add(g.Lock)
		entry.count = 0
		entry.first = now
	}
}

// Success forgets the failures of this address and email pair.
func (g *LoginGuard) Success(email, ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.entries, pairKey(email, ip))
}

func (g *LoginGuard) sweep(now time.Time) {
	for key, entry := range g.entries {
		if now.Sub(entry.first) > g.Window && !now.Before(entry.lockedUntil) {
			delete(g.entries, key)
		}
	}
}
