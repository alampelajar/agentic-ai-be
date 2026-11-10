package services

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"agentic-ai-backend/models"
)

// ComboTarget is one model a combo may call, with what the call needs to be logged.
type ComboTarget struct {
	ModelDBID uint
	Name      string
	IsSystem  bool
	Weight    int
}

// ComboAttempt is what happened to one model in a request.
type ComboAttempt struct {
	ModelDBID  uint               `json:"model_id"`
	Name       string             `json:"name"`
	Status     string             `json:"status"` // ok, failed or skipped
	ErrorClass ProviderErrorClass `json:"error_class,omitempty"`
	Reason     string             `json:"reason,omitempty"`
	LatencyMS  int64              `json:"latency_ms"`
}

// ComboOutcome is the result of running a combo.
type ComboOutcome struct {
	Answer   string
	Used     ComboTarget
	Attempts []ComboAttempt
}

// ---------------------------------------------------------------------------
// Health: a model that keeps failing is rested instead of being tried first every time.
// ---------------------------------------------------------------------------

const (
	comboFailuresBeforeRest = 3
	comboRestBase           = 60 * time.Second
	comboRestMax            = 10 * time.Minute
)

type modelHealth struct {
	failures int
	restTill time.Time
}

// ComboHealth remembers recent failures per model, in memory. A second backend
// instance would keep its own; that is acceptable until the backend is scaled out.
type ComboHealth struct {
	mu     sync.Mutex
	models map[uint]*modelHealth
	now    func() time.Time
}

func NewComboHealth() *ComboHealth {
	return &ComboHealth{models: map[uint]*modelHealth{}, now: time.Now}
}

// Resting says whether the model is currently being rested.
func (h *ComboHealth) Resting(modelID uint) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.models[modelID]
	return state != nil && h.now().Before(state.restTill)
}

// Success clears the model's failures.
func (h *ComboHealth) Success(modelID uint) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.models, modelID)
}

// Failure counts a failure that says the model is unavailable (not one that says the
// request was bad). After a few in a row the model rests, for longer each time.
func (h *ComboHealth) Failure(modelID uint) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.models[modelID]
	if state == nil {
		state = &modelHealth{}
		h.models[modelID] = state
	}
	state.failures++
	if state.failures >= comboFailuresBeforeRest {
		rest := comboRestBase
		for i := comboFailuresBeforeRest; i < state.failures && rest < comboRestMax; i++ {
			rest *= 2
		}
		if rest > comboRestMax {
			rest = comboRestMax
		}
		state.restTill = h.now().Add(rest)
	}
}

// ---------------------------------------------------------------------------
// Order: which model to try first, and in what order the rest follow.
// ---------------------------------------------------------------------------

// RoundRobin hands out the next start position per combo.
type RoundRobin struct {
	mu       sync.Mutex
	counters map[uint]uint64
}

func NewRoundRobin() *RoundRobin { return &RoundRobin{counters: map[uint]uint64{}} }

func (r *RoundRobin) next(comboID uint) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.counters[comboID]
	r.counters[comboID] = n + 1
	return n
}

// OrderTargets returns the models to try, first to last.
//
//   - fallback keeps the members' order;
//   - round_robin starts at the next model in turn (a model with weight 3 owns three
//     turns out of the cycle) and then follows the members' order, so a failed first
//     choice still falls back.
//
// Models that are resting move to the back instead of being dropped, so a combo whose
// members are all resting still tries them.
func OrderTargets(strategy string, comboID uint, members []ComboTarget, rr *RoundRobin, health *ComboHealth) []ComboTarget {
	ordered := append([]ComboTarget(nil), members...)
	if len(ordered) == 0 {
		return ordered
	}
	if strategy == models.ComboStrategyRoundRobin && rr != nil {
		slots := make([]int, 0, len(ordered))
		for index, member := range ordered {
			weight := member.Weight
			if weight < 1 {
				weight = 1
			}
			for i := 0; i < weight; i++ {
				slots = append(slots, index)
			}
		}
		first := slots[int(rr.next(comboID)%uint64(len(slots)))]
		rotated := make([]ComboTarget, 0, len(ordered))
		rotated = append(rotated, ordered[first])
		for index, member := range ordered {
			if index != first {
				rotated = append(rotated, member)
			}
		}
		ordered = rotated
	}
	return restingLast(ordered, health)
}

// restingLast moves models that are resting to the back, keeping the order otherwise.
func restingLast(ordered []ComboTarget, health *ComboHealth) []ComboTarget {
	if health == nil {
		return ordered
	}
	ready := make([]ComboTarget, 0, len(ordered))
	resting := make([]ComboTarget, 0)
	for _, member := range ordered {
		if health.Resting(member.ModelDBID) {
			resting = append(resting, member)
		} else {
			ready = append(ready, member)
		}
	}
	return append(ready, resting...)
}

// ---------------------------------------------------------------------------
// Sticky: one task keeps the model that answered it.
// ---------------------------------------------------------------------------

type stickyKey struct{ user, combo, task uint }

type stickyEntry struct {
	model uint
	at    time.Time
}

// StickyChoices remembers, per user, combo and task, which model answered last, so a
// round-robin combo does not change model in the middle of one conversation.
type StickyChoices struct {
	mu      sync.Mutex
	entries map[stickyKey]stickyEntry
	ttl     time.Duration
	max     int
	now     func() time.Time
}

func NewStickyChoices() *StickyChoices {
	return &StickyChoices{entries: map[stickyKey]stickyEntry{}, ttl: 6 * time.Hour, max: 2000, now: time.Now}
}

// Get returns the model chosen for the task, if one was chosen recently.
func (c *StickyChoices) Get(userID, comboID, taskID uint) (uint, bool) {
	if taskID == 0 {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[stickyKey{userID, comboID, taskID}]
	if !ok || c.now().Sub(entry.at) > c.ttl {
		return 0, false
	}
	return entry.model, true
}

// Set records the model that answered the task.
func (c *StickyChoices) Set(userID, comboID, taskID, modelID uint) {
	if taskID == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.entries) >= c.max {
		for key, entry := range c.entries {
			if now.Sub(entry.at) > c.ttl {
				delete(c.entries, key)
			}
		}
		// Still full of recent tasks: forget the oldest quarter.
		if len(c.entries) >= c.max {
			type aged struct {
				key stickyKey
				at  time.Time
			}
			all := make([]aged, 0, len(c.entries))
			for key, entry := range c.entries {
				all = append(all, aged{key, entry.at})
			}
			sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
			for _, item := range all[:len(all)/4+1] {
				delete(c.entries, item.key)
			}
		}
	}
	c.entries[stickyKey{userID, comboID, taskID}] = stickyEntry{model: modelID, at: now}
}

// OrderTargetsForTask is OrderTargets for a request that belongs to a task. A round-robin
// combo keeps using the model that answered the task before, as long as it is still a member
// and not resting, and then does not use up a turn of the rotation. The rest follow as usual,
// so a failure still falls back.
func OrderTargetsForTask(strategy string, comboID uint, members []ComboTarget, rr *RoundRobin, health *ComboHealth, sticky *StickyChoices, userID, taskID uint) []ComboTarget {
	if strategy == models.ComboStrategyRoundRobin && sticky != nil {
		if modelID, ok := sticky.Get(userID, comboID, taskID); ok && (health == nil || !health.Resting(modelID)) {
			for index, member := range members {
				if member.ModelDBID != modelID {
					continue
				}
				ordered := make([]ComboTarget, 0, len(members))
				ordered = append(ordered, member)
				ordered = append(ordered, members[:index]...)
				ordered = append(ordered, members[index+1:]...)
				return restingLast(ordered, health)
			}
		}
	}
	return OrderTargets(strategy, comboID, members, rr, health)
}

// ---------------------------------------------------------------------------
// Run
// ---------------------------------------------------------------------------

// ComboLimits bounds one request.
type ComboLimits struct {
	// MaxAttempts is how many models one request may try.
	MaxAttempts int
	// Budget is the time one request may take over all its attempts. Each attempt gets a
	// share of what is left (see AttemptTimeout), so a model that hangs is cut off in time
	// and the next model still gets its turn.
	Budget time.Duration
}

// ComboMinAttemptTime is the least time an attempt is given while budget is left, so a
// model is not cut off before it could reasonably answer.
const ComboMinAttemptTime = 30 * time.Second

// AttemptTimeout is how long the next attempt may take: what is left of the budget shared
// over the attempts that can still be made, but at least ComboMinAttemptTime (and never
// more than what is left). The last possible attempt gets everything that is left. Zero
// means no budget, so the caller's own timeout applies.
func AttemptTimeout(budget, elapsed time.Duration, attemptsLeft int) time.Duration {
	if budget <= 0 {
		return 0
	}
	remaining := budget - elapsed
	if remaining <= 0 {
		return 0
	}
	if attemptsLeft < 1 {
		attemptsLeft = 1
	}
	share := remaining / time.Duration(attemptsLeft)
	if share < ComboMinAttemptTime {
		share = ComboMinAttemptTime
	}
	if share > remaining {
		share = remaining
	}
	return share
}

// ErrComboExhausted means every model that was tried failed.
var ErrComboExhausted = errors.New("semua model dalam combo gagal menjawab")

// RunCombo tries the ordered models until one answers. A fatal failure (bad request,
// bad key) moves on to the next model without counting against the model's health;
// a retryable one does count. It always returns the attempts, also on failure.
// RunCombo tries the targets in order until one answers. call receives how long that
// attempt may take (0: no limit from the combo) and must stop by then.
func RunCombo(ordered []ComboTarget, limits ComboLimits, health *ComboHealth, call func(ComboTarget, time.Duration) (string, error)) (ComboOutcome, error) {
	outcome := ComboOutcome{}
	maxAttempts := limits.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	start := time.Now()
	tried := 0
	for index, target := range ordered {
		if tried >= maxAttempts {
			break
		}
		if limits.Budget > 0 && tried > 0 && time.Since(start) >= limits.Budget {
			outcome.Attempts = append(outcome.Attempts, ComboAttempt{ModelDBID: target.ModelDBID, Name: target.Name, Status: "skipped", Reason: "waktu combo habis"})
			break
		}
		attemptsLeft := maxAttempts - tried
		if left := len(ordered) - index; left < attemptsLeft {
			attemptsLeft = left
		}
		timeout := AttemptTimeout(limits.Budget, time.Since(start), attemptsLeft)
		tried++
		began := time.Now()
		answer, err := call(target, timeout)
		attempt := ComboAttempt{ModelDBID: target.ModelDBID, Name: target.Name, LatencyMS: time.Since(began).Milliseconds()}
		if err == nil {
			attempt.Status = "ok"
			outcome.Attempts = append(outcome.Attempts, attempt)
			if health != nil {
				health.Success(target.ModelDBID)
			}
			outcome.Answer = answer
			outcome.Used = target
			return outcome, nil
		}
		attempt.Status = "failed"
		attempt.ErrorClass = ClassifyProviderError(err)
		attempt.Reason = shortReason(err)
		outcome.Attempts = append(outcome.Attempts, attempt)
		if health != nil && attempt.ErrorClass == ErrorRetryable {
			health.Failure(target.ModelDBID)
		}
	}
	return outcome, fmt.Errorf("%w (%s)", ErrComboExhausted, summarizeAttempts(outcome.Attempts))
}

func shortReason(err error) string {
	message := strings.TrimSpace(err.Error())
	if len(message) > 160 {
		message = message[:160] + "..."
	}
	return message
}

func summarizeAttempts(attempts []ComboAttempt) string {
	parts := make([]string, 0, len(attempts))
	for _, attempt := range attempts {
		switch attempt.Status {
		case "failed":
			parts = append(parts, fmt.Sprintf("%s: %s", attempt.Name, attempt.Reason))
		case "skipped":
			parts = append(parts, fmt.Sprintf("%s: dilewati, %s", attempt.Name, attempt.Reason))
		}
	}
	return strings.Join(parts, "; ")
}
