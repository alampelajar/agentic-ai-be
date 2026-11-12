package services

import "testing"

var routeCandidates = []RouteCandidate{
	{ModelDBID: 1, ModelID: "agnes-2.5-flash", Name: "Agnes 2.5 Flash", Provider: "NaraRouter"},
	{ModelDBID: 2, ModelID: "cohere/north-mini-code:free", Name: "cohere/north-mini-code:free", Provider: "OpenRouter"},
	{ModelDBID: 3, ModelID: "gemini-3.8-flash", Name: "gemini-3.8-flash", Provider: "Gemini"},
	{ModelDBID: 4, ModelID: "openai/gpt-oss-120b", Name: "GPT-OSS-120b", Provider: "Groq"},
	{ModelDBID: 5, ModelID: "nvidia/nemotron-3.5-lightning:free", Name: "nvidia/nemotron-3.5-lightning:free", Provider: "OpenRouter"},
	{ModelDBID: 6, ModelID: "nvidia/nemotron-3.5-super-vl:free", Name: "nvidia/nemotron-3.5-super-vl:free", Provider: "OpenRouter"},
	{ModelDBID: 7, ModelID: "liquid/lfm-3-1.7b:free", Name: "lfm", Provider: "OpenRouter"},
}

func ids(choices []RouteChoice) []uint {
	out := make([]uint, len(choices))
	for i, choice := range choices {
		out[i] = choice.ModelDBID
	}
	return out
}

func TestAPhotoGoesOnlyToModelsThatReadImages(t *testing.T) {
	ranked := RankModels(routeCandidates, NeedVision)
	for _, choice := range ranked {
		if !CanReadImages(choice.ModelID, choice.Name) {
			t.Fatalf("%s cannot read images but was offered", choice.ModelID)
		}
	}
	if got := ids(ranked); len(got) != 2 || got[0] != 3 && got[0] != 6 {
		t.Fatalf("vision ranking = %v, want gemini and the -vl model only", got)
	}
	if len(RankModels(routeCandidates[:2], NeedVision)) != 0 {
		t.Fatal("with no model that reads images there is nothing to offer")
	}
}

func TestEachNeedPutsTheFittingModelFirst(t *testing.T) {
	if first := RankModels(routeCandidates, NeedCoding)[0]; first.ModelDBID != 4 && first.ModelDBID != 2 {
		t.Errorf("coding should start with a coding model, got %s", first.ModelID)
	}
	if first := RankModels(routeCandidates, NeedFast)[0]; first.Provider != "Groq" {
		t.Errorf("a plain question should start with the fast provider, got %s (%s)", first.ModelID, first.Provider)
	}
	fast := RankModels(routeCandidates[:2], NeedFast) // a paid flash model and a free mini one
	if fast[0].ModelDBID != 1 {
		t.Errorf("a model on the shared free quota should come after an equal paid one, got %v", ids(fast))
	}
	long := RankModels(routeCandidates, NeedLong)
	if long[len(long)-1].ModelDBID != 7 {
		t.Errorf("a tiny model must come last for long documents, got %v", ids(long))
	}
	if long[0].Reason == "" {
		t.Error("the choice must say why")
	}
}

func TestFailingAndRestingModelsGoDownTheList(t *testing.T) {
	candidates := []RouteCandidate{
		{ModelDBID: 1, ModelID: "openai/gpt-oss-120b", Provider: "Groq", Resting: true},
		{ModelDBID: 2, ModelID: "agnes-2.5-flash", Provider: "NaraRouter", FailRate: 0.9},
		{ModelDBID: 3, ModelID: "gemini-3.8-flash", Provider: "Gemini"},
	}
	got := ids(RankModels(candidates, NeedFast))
	if got[0] != 3 || got[2] != 1 {
		t.Fatalf("ranking = %v, want the healthy model first and the resting one last", got)
	}
}

func TestWhatAMessageNeeds(t *testing.T) {
	cases := []struct {
		images, workspace, documents bool
		length                       int
		want                         TaskNeed
	}{
		{true, true, true, 10, NeedVision},
		{false, true, true, 10, NeedCoding},
		{false, false, true, 10, NeedLong},
		{false, false, false, 5000, NeedLong},
		{false, false, false, 40, NeedFast},
	}
	for _, c := range cases {
		if got := NeedFor(c.images, c.workspace, c.documents, c.length); got != c.want {
			t.Errorf("%+v: got %s", c, got)
		}
	}
}
