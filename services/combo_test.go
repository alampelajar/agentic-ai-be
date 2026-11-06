package services

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"agentic-ai-backend/models"
)

func targets(names ...string) []ComboTarget {
	result := make([]ComboTarget, 0, len(names))
	for i, name := range names {
		result = append(result, ComboTarget{ModelDBID: uint(i + 1), Name: name, Weight: 1})
	}
	return result
}

func names(list []ComboTarget) string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, item.Name)
	}
	return strings.Join(out, ",")
}

func TestClassifyProviderError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ProviderErrorClass
	}{
		{"rate limit", &ProviderHTTPError{Status: http.StatusTooManyRequests, Message: "x"}, ErrorRetryable},
		{"provider outage", &ProviderHTTPError{Status: http.StatusBadGateway, Message: "x"}, ErrorRetryable},
		{"timeout status", &ProviderHTTPError{Status: http.StatusRequestTimeout, Message: "x"}, ErrorRetryable},
		{"bad request", &ProviderHTTPError{Status: http.StatusBadRequest, Message: "x"}, ErrorFatal},
		{"bad key", &ProviderHTTPError{Status: http.StatusUnauthorized, Message: "x"}, ErrorFatal},
		{"unknown model", &ProviderHTTPError{Status: http.StatusNotFound, Message: "x"}, ErrorFatal},
		{"wrapped status", errors.Join(errors.New("ctx"), &ProviderHTTPError{Status: http.StatusBadRequest, Message: "x"}), ErrorFatal},
		{"network", &net.DNSError{IsTimeout: true}, ErrorRetryable},
		{"plain error", errors.New("response kosong"), ErrorRetryable},
	}
	for _, tc := range cases {
		if got := ClassifyProviderError(tc.err); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestOrderTargetsFallbackKeepsTheMembersOrder(t *testing.T) {
	got := OrderTargets(models.ComboStrategyFallback, 1, targets("a", "b", "c"), NewRoundRobin(), NewComboHealth())
	if names(got) != "a,b,c" {
		t.Fatalf("got %s", names(got))
	}
}

func TestOrderTargetsRoundRobinTakesTurnsAndKeepsFallbackAfterTheFirst(t *testing.T) {
	rr := NewRoundRobin()
	members := targets("a", "b", "c")
	var firsts, orders []string
	for i := 0; i < 6; i++ {
		got := OrderTargets(models.ComboStrategyRoundRobin, 7, members, rr, nil)
		firsts = append(firsts, got[0].Name)
		orders = append(orders, names(got))
	}
	if strings.Join(firsts, "") != "abcabc" {
		t.Fatalf("firsts = %v", firsts)
	}
	if orders[1] != "b,a,c" {
		t.Fatalf("the rest must follow in member order after the first choice, got %s", orders[1])
	}
}

func TestOrderTargetsRoundRobinHonoursWeights(t *testing.T) {
	members := targets("a", "b")
	members[0].Weight = 3
	rr := NewRoundRobin()
	count := map[string]int{}
	for i := 0; i < 8; i++ {
		count[OrderTargets(models.ComboStrategyRoundRobin, 1, members, rr, nil)[0].Name]++
	}
	if count["a"] != 6 || count["b"] != 2 {
		t.Fatalf("weight 3:1 over two cycles should give 6:2, got %v", count)
	}
}

func TestRoundRobinCountersAreSeparatePerCombo(t *testing.T) {
	rr := NewRoundRobin()
	members := targets("a", "b")
	OrderTargets(models.ComboStrategyRoundRobin, 1, members, rr, nil)
	if got := OrderTargets(models.ComboStrategyRoundRobin, 2, members, rr, nil)[0].Name; got != "a" {
		t.Fatalf("another combo must start at its own beginning, got %s", got)
	}
}

func TestHealthRestsAModelAfterRepeatedFailuresAndForgivesOnSuccess(t *testing.T) {
	health := NewComboHealth()
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	health.now = func() time.Time { return clock }

	health.Failure(1)
	health.Failure(1)
	if health.Resting(1) {
		t.Fatal("two failures must not rest the model yet")
	}
	health.Failure(1)
	if !health.Resting(1) {
		t.Fatal("three failures in a row must rest the model")
	}
	clock = clock.Add(61 * time.Second)
	if health.Resting(1) {
		t.Fatal("the rest must end after a minute")
	}
	health.Failure(1)
	clock = clock.Add(61 * time.Second)
	if !health.Resting(1) {
		t.Fatal("the next rest must be longer (two minutes)")
	}
	health.Success(1)
	if health.Resting(1) {
		t.Fatal("a success must clear the rest")
	}
}

func TestOrderTargetsMovesRestingModelsToTheBackButKeepsThem(t *testing.T) {
	health := NewComboHealth()
	for i := 0; i < 3; i++ {
		health.Failure(1)
	}
	got := OrderTargets(models.ComboStrategyFallback, 1, targets("a", "b", "c"), NewRoundRobin(), health)
	if names(got) != "b,c,a" {
		t.Fatalf("got %s", names(got))
	}
}

func TestRunComboFallsBackAndReportsWhoAnswered(t *testing.T) {
	health := NewComboHealth()
	ordered := targets("a", "b", "c")
	calls := []string{}
	outcome, err := RunCombo(ordered, ComboLimits{MaxAttempts: 3}, health, func(target ComboTarget) (string, error) {
		calls = append(calls, target.Name)
		if target.Name == "c" {
			return "jawaban", nil
		}
		return "", &ProviderHTTPError{Status: http.StatusTooManyRequests, Message: "rate limited"}
	})
	if err != nil || outcome.Answer != "jawaban" || outcome.Used.Name != "c" {
		t.Fatalf("outcome = %+v err = %v", outcome, err)
	}
	if strings.Join(calls, "") != "abc" || len(outcome.Attempts) != 3 || outcome.Attempts[0].Status != "failed" || outcome.Attempts[2].Status != "ok" {
		t.Fatalf("attempts = %+v", outcome.Attempts)
	}
}

func TestRunComboStopsAtMaxAttemptsAndReturnsTheTrace(t *testing.T) {
	calls := 0
	outcome, err := RunCombo(targets("a", "b", "c"), ComboLimits{MaxAttempts: 2}, nil, func(ComboTarget) (string, error) {
		calls++
		return "", errors.New("boom")
	})
	if !errors.Is(err, ErrComboExhausted) || calls != 2 || len(outcome.Attempts) != 2 {
		t.Fatalf("calls = %d err = %v attempts = %+v", calls, err, outcome.Attempts)
	}
	if !strings.Contains(err.Error(), "a: boom") || !strings.Contains(err.Error(), "b: boom") {
		t.Fatalf("the error must say what happened to each model: %v", err)
	}
}

func TestRunComboFatalFailuresDoNotRestTheModel(t *testing.T) {
	health := NewComboHealth()
	for i := 0; i < 5; i++ {
		_, _ = RunCombo(targets("a"), ComboLimits{MaxAttempts: 1}, health, func(ComboTarget) (string, error) {
			return "", &ProviderHTTPError{Status: http.StatusBadRequest, Message: "bad"}
		})
	}
	if health.Resting(1) {
		t.Fatal("a bad request says nothing about whether the model is up")
	}
}

func TestRunComboStopsStartingAttemptsAfterTheBudget(t *testing.T) {
	calls := 0
	_, err := RunCombo(targets("a", "b"), ComboLimits{MaxAttempts: 3, Budget: time.Millisecond}, nil, func(ComboTarget) (string, error) {
		calls++
		time.Sleep(5 * time.Millisecond)
		return "", errors.New("slow failure")
	})
	if calls != 1 || err == nil || !strings.Contains(err.Error(), "waktu combo habis") {
		t.Fatalf("calls = %d err = %v", calls, err)
	}
}
