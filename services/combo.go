package services

import (
	"errors"
	"fmt"
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
// Run
// ---------------------------------------------------------------------------

// ComboLimits bounds one request.
type ComboLimits struct {
	// MaxAttempts is how many models one request may try.
	MaxAttempts int
	// Budget stops new attempts once this much time has passed (a call already
	// started is not cancelled).
	Budget time.Duration
}

// ErrComboExhausted means every model that was tried failed.
var ErrComboExhausted = errors.New("semua model dalam combo gagal menjawab")

// RunCombo tries the ordered models until one answers. A fatal failure (bad request,
// bad key) moves on to the next model without counting against the model's health;
// a retryable one does count. It always returns the attempts, also on failure.
func RunCombo(ordered []ComboTarget, limits ComboLimits, health *ComboHealth, call func(ComboTarget) (string, error)) (ComboOutcome, error) {
	outcome := ComboOutcome{}
	maxAttempts := limits.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	start := time.Now()
	tried := 0
	for _, target := range ordered {
		if tried >= maxAttempts {
			break
		}
		if limits.Budget > 0 && tried > 0 && time.Since(start) > limits.Budget {
			outcome.Attempts = append(outcome.Attempts, ComboAttempt{ModelDBID: target.ModelDBID, Name: target.Name, Status: "skipped", Reason: "waktu combo habis"})
			break
		}
		tried++
		began := time.Now()
		answer, err := call(target)
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
