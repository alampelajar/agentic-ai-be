package services

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestRunPipelinePassesEachDraftOnAndTheLastModelAnswers(t *testing.T) {
	type seen struct {
		name  string
		final bool
		draft string
	}
	calls := []seen{}
	outcome, err := RunPipeline(targets("a", "b", "c"), ComboLimits{}, NewComboHealth(), func(target ComboTarget, final bool, draft string) (string, error) {
		calls = append(calls, seen{target.Name, final, draft})
		return "dari-" + target.Name, nil
	})
	if err != nil || outcome.Answer != "dari-c" || outcome.Used.Name != "c" {
		t.Fatalf("outcome = %+v err = %v", outcome, err)
	}
	want := []seen{{"a", false, ""}, {"b", false, "dari-a"}, {"c", true, "dari-b"}}
	if len(calls) != 3 || calls[0] != want[0] || calls[1] != want[1] || calls[2] != want[2] {
		t.Fatalf("calls = %+v", calls)
	}
	if len(outcome.Attempts) != 3 || outcome.Attempts[0].Stage != PipelineStageDraft || outcome.Attempts[2].Stage != PipelineStageFinal {
		t.Fatalf("attempts = %+v", outcome.Attempts)
	}
}

func TestRunPipelineSkipsADraftStageThatFailsOrRests(t *testing.T) {
	health := NewComboHealth()
	for i := 0; i < comboFailuresBeforeRest; i++ {
		health.Failure(2) // "b" is resting
	}
	drafts := []string{}
	outcome, err := RunPipeline(targets("a", "b", "c", "d"), ComboLimits{}, health, func(target ComboTarget, final bool, draft string) (string, error) {
		drafts = append(drafts, target.Name+":"+draft)
		if target.Name == "c" {
			return "", &ProviderHTTPError{Status: http.StatusBadGateway, Message: "down"}
		}
		return "dari-" + target.Name, nil
	})
	if err != nil || outcome.Used.Name != "d" {
		t.Fatalf("a failing draft stage must not fail the pipeline: %+v %v", outcome, err)
	}
	// b never ran; c failed, so d still got a's draft.
	if strings.Join(drafts, "|") != "a:|c:dari-a|d:dari-a" {
		t.Fatalf("calls = %v", drafts)
	}
	statuses := []string{}
	for _, attempt := range outcome.Attempts {
		statuses = append(statuses, attempt.Name+"="+attempt.Status)
	}
	if strings.Join(statuses, ",") != "a=ok,b=skipped,c=failed,d=ok" {
		t.Fatalf("statuses = %v", statuses)
	}
}

func TestRunPipelineFailsWhenTheLastModelFailsAndKeepsTheTrace(t *testing.T) {
	outcome, err := RunPipeline(targets("a", "b"), ComboLimits{}, NewComboHealth(), func(target ComboTarget, final bool, draft string) (string, error) {
		if final {
			return "", &ProviderHTTPError{Status: http.StatusBadRequest, Message: "no"}
		}
		return "draf", nil
	})
	if !errors.Is(err, ErrComboExhausted) || len(outcome.Attempts) != 2 || outcome.Attempts[1].Status != "failed" {
		t.Fatalf("outcome = %+v err = %v", outcome, err)
	}
}

func TestRunPipelineTreatsAnEmptyAnswerAsFailureAndAnEmptyDraftAsNoDraft(t *testing.T) {
	_, err := RunPipeline(targets("a", "b"), ComboLimits{}, nil, func(target ComboTarget, final bool, draft string) (string, error) {
		if final {
			return "  ", nil
		}
		return "draf", nil
	})
	if !errors.Is(err, ErrComboExhausted) {
		t.Fatalf("an empty final answer must fail, got %v", err)
	}
	var passed string
	_, err = RunPipeline(targets("a", "b"), ComboLimits{}, nil, func(target ComboTarget, final bool, draft string) (string, error) {
		if final {
			passed = draft
			return "ok", nil
		}
		return " ", nil
	})
	if err != nil || passed != "" {
		t.Fatalf("an empty draft must not be passed on: %q %v", passed, err)
	}
}

func TestRunPipelineSingleModelJustAnswers(t *testing.T) {
	outcome, err := RunPipeline(targets("solo"), ComboLimits{}, nil, func(target ComboTarget, final bool, draft string) (string, error) {
		if !final || draft != "" {
			t.Fatalf("a lone model is the final stage with no draft")
		}
		return "jawab", nil
	})
	if err != nil || outcome.Answer != "jawab" {
		t.Fatalf("outcome = %+v err = %v", outcome, err)
	}
}
