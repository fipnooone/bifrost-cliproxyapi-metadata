package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func modelResponse(models ...schemas.Model) *schemas.BifrostResponse {
	return &schemas.BifrostResponse{ListModelsResponse: &schemas.BifrostListModelsResponse{Data: models, NextPageToken: "preserved-page"}}
}
func fixtureIndex(t *testing.T) map[string]metadataModel {
	t.Helper()
	models, _, err := decodeCatalog(strings.NewReader(string(fixture(t))))
	if err != nil {
		t.Fatal(err)
	}
	return models
}

func TestExactMappingAvailabilityAndOtherProviders(t *testing.T) {
	index := fixtureIndex(t)
	other := schemas.Model{ID: "openai/gpt-5.6-sol", Name: ptr("other name"), ContextLength: ptr(100), Architecture: &schemas.Architecture{InputModalities: []string{"audio"}}}
	missing := schemas.Model{ID: "cliproxyapi/not-in-rich-catalog", Name: ptr("original")}
	original := modelResponse(
		schemas.Model{ID: "cliproxyapi/gpt-5.6-sol", Created: ptr(int64(1783616400)), OwnedBy: ptr("openai"), MaxInputTokens: ptr(12345), Pricing: &schemas.Pricing{Prompt: ptr("0.1")}},
		other, missing,
		schemas.Model{ID: "cliproxyapi/claude-opus-4-6-thinking"},
		schemas.Model{ID: "cliproxyapi/gpt-oss-120b-medium"},
	)
	result, stats := enrichResponse(original, "cliproxyapi/", index)
	if result == original || result.ListModelsResponse == original.ListModelsResponse {
		t.Fatal("expected response/list copy")
	}
	if len(result.ListModelsResponse.Data) != 5 || stats.Matched != 3 || stats.MaxOutput != 3 || stats.Missing.Count != 1 {
		t.Fatal("wrong availability or stats")
	}
	gpt := result.ListModelsResponse.Data[0]
	if gpt.ID != "cliproxyapi/gpt-5.6-sol" || *gpt.ContextLength != 272000 || *gpt.MaxOutputTokens != 128000 || *gpt.MaxInputTokens != 12345 {
		t.Fatal("wrong context/output mapping or changed identity/input limit")
	}
	if *gpt.Name != "GPT 5.6 Sol" || *gpt.Description != "Our most capable model yet..." {
		t.Fatal("name or description not trimmed/mapped")
	}
	if !reflect.DeepEqual(gpt.Architecture.InputModalities, []string{"text", "image"}) {
		t.Fatal("wrong input modalities")
	}
	if !reflect.DeepEqual(gpt.Reasoning.SupportedEfforts, []string{"low", "medium", "high", "xhigh", "max", "ultra"}) || *gpt.Reasoning.DefaultEffort != "low" {
		t.Fatal("wrong reasoning mapping")
	}
	if *gpt.Created != 1783616400 || *gpt.OwnedBy != "openai" || *gpt.Pricing.Prompt != "0.1" {
		t.Fatal("changed identity/pricing")
	}
	if !reflect.DeepEqual(result.ListModelsResponse.Data[1], other) || !reflect.DeepEqual(result.ListModelsResponse.Data[2], missing) {
		t.Fatal("changed other/missing model")
	}
	if result.ListModelsResponse.Data[3].Reasoning != nil {
		t.Fatal("invented Claude reasoning; hidden model should still be present")
	}
	if !reflect.DeepEqual(result.ListModelsResponse.Data[4].Architecture.InputModalities, []string{"text"}) {
		t.Fatal("text-only mapping")
	}
	if original.ListModelsResponse.Data[0].Name != nil || original.ListModelsResponse.Data[0].ContextLength != nil {
		t.Fatal("mutated original response")
	}
	if result.ListModelsResponse.NextPageToken != "preserved-page" {
		t.Fatal("dropped pagination")
	}
}

func TestNullAbsentAndNonpositiveLimitsPreserveExisting(t *testing.T) {
	for _, part := range []string{"", `,"context_window":null,"max_tokens":null`, `,"context_window":0,"max_tokens":0`, `,"context_window":-1,"max_tokens":-1`} {
		models, _, err := decodeCatalog(strings.NewReader(`{"models":[{"slug":"a","max_context_window":872000` + part + `}]}`))
		if err != nil {
			t.Fatal(err)
		}
		original := modelResponse(schemas.Model{ID: "cliproxyapi/a", ContextLength: ptr(999), MaxInputTokens: ptr(111), MaxOutputTokens: ptr(222)})
		result, stats := enrichResponse(original, "cliproxyapi/", models)
		got := result.ListModelsResponse.Data[0]
		if *got.ContextLength != 999 || *got.MaxInputTokens != 111 || *got.MaxOutputTokens != 222 || stats.MaxOutput != 0 {
			t.Fatal("nonpositive or absent limit cleared existing value")
		}
		empty, _ := enrichResponse(modelResponse(schemas.Model{ID: "cliproxyapi/a"}), "cliproxyapi/", models)
		if empty.ListModelsResponse.Data[0].ContextLength != nil || empty.ListModelsResponse.Data[0].MaxInputTokens != nil {
			t.Fatal("used max_context_window as fallback")
		}
	}
}

func TestArchitecturePreservesOtherFieldsAndUnsupportedOnly(t *testing.T) {
	original := schemas.Model{ID: "cliproxyapi/a", Architecture: &schemas.Architecture{
		Modality: ptr("original"), Tokenizer: ptr("tokenizer"), InstructType: ptr("instruct"),
		InputModalities: []string{"audio"}, OutputModalities: []string{"audio", "text"},
	}}
	for _, tc := range []struct {
		source, want []string
		copy         bool
	}{
		{[]string{" TEXT ", "Image", "TEXT", "audio", "video", "file"}, []string{"text", "image"}, true},
		{[]string{"text"}, []string{"text"}, true},
		{[]string{"audio", "video", "file"}, []string{"audio"}, false},
		{nil, []string{"audio"}, false},
	} {
		m := original
		applyMetadata(&m, metadataModel{InputModalities: tc.source})
		if !reflect.DeepEqual(m.Architecture.InputModalities, tc.want) || *m.Architecture.Modality != "original" || *m.Architecture.Tokenizer != "tokenizer" || *m.Architecture.InstructType != "instruct" || !reflect.DeepEqual(m.Architecture.OutputModalities, []string{"audio", "text"}) {
			t.Fatal("architecture not selectively enriched")
		}
		if tc.copy && m.Architecture == original.Architecture {
			t.Fatal("modified shared architecture")
		}
		if !reflect.DeepEqual(original.Architecture.InputModalities, []string{"audio"}) {
			t.Fatal("original architecture changed")
		}
	}
}

func TestReasoningPreservationAndArbitraryEfforts(t *testing.T) {
	original := schemas.Model{ID: "cliproxyapi/a", Reasoning: &schemas.ModelReasoning{
		Mandatory: ptr(true), DefaultEnabled: ptr(false), SupportedEfforts: []string{"old"}, DefaultEffort: ptr("old"),
	}}
	m := original
	applyMetadata(&m, metadataModel{SupportedReasoningLevels: []reasoningLevel{{"None"}, {" LOW "}, {"low"}, {"Ultra"}, {"new-effort"}}, DefaultReasoningLevel: ptr(" HIGH ")})
	if !reflect.DeepEqual(m.Reasoning.SupportedEfforts, []string{"none", "low", "ultra", "new-effort"}) || *m.Reasoning.DefaultEffort != "high" || !*m.Reasoning.Mandatory || *m.Reasoning.DefaultEnabled {
		t.Fatal("reasoning mapping changed semantics")
	}
	if m.Reasoning == original.Reasoning || original.Reasoning.SupportedEfforts[0] != "old" || *original.Reasoning.DefaultEffort != "old" {
		t.Fatal("modified original reasoning")
	}
	for _, source := range []metadataModel{{}, {SupportedReasoningLevels: []reasoningLevel{{" "}}, DefaultReasoningLevel: ptr(" ")}} {
		m = original
		applyMetadata(&m, source)
		if !reflect.DeepEqual(m, original) || m.Reasoning != original.Reasoning {
			t.Fatal("empty reasoning should leave existing metadata untouched")
		}
	}
	m = original
	applyMetadata(&m, metadataModel{DefaultReasoningLevel: ptr("none")})
	if !reflect.DeepEqual(m.Reasoning.SupportedEfforts, []string{"old"}) || *m.Reasoning.DefaultEffort != "none" {
		t.Fatal("default-only mapping")
	}
	m = original
	applyMetadata(&m, metadataModel{SupportedReasoningLevels: []reasoningLevel{{"ultra"}}})
	if *m.Reasoning.DefaultEffort != "old" {
		t.Fatal("effort-only mapping cleared existing default")
	}
}

func TestAliasExactFallbackAndNoHeuristics(t *testing.T) {
	index := fixtureIndex(t)
	index["a"] = metadataModel{Slug: "a", DisplayName: "primary"}
	original := modelResponse(
		schemas.Model{ID: "cliproxyapi/bifrost-alias", Alias: ptr("gemini-pro-agent")},
		schemas.Model{ID: "cliproxyapi/GEMINI-PRO-AGENT"},
		schemas.Model{ID: "cliproxyapi/gemini-pro-agent-thinking"},
		schemas.Model{ID: "cliproxyapi/other", Alias: ptr("cliproxyapi/gemini-pro-agent")},
		schemas.Model{ID: "cliproxyapi/a", Alias: ptr("gemini-pro-agent")},
		schemas.Model{ID: "cliproxyapi-other/gemini-pro-agent"},
	)
	result, stats := enrichResponse(original, "cliproxyapi/", index)
	if stats.Matched != 2 || stats.Missing.Count != 3 {
		t.Fatal("did not restrict to exact prefix/identities")
	}
	if *result.ListModelsResponse.Data[0].ContextLength != 1048576 || *result.ListModelsResponse.Data[0].Alias != "gemini-pro-agent" || result.ListModelsResponse.Data[0].ID != "cliproxyapi/bifrost-alias" {
		t.Fatal("explicit alias fallback failed")
	}
	if *result.ListModelsResponse.Data[4].Name != "primary" {
		t.Fatal("alias overrode primary exact identity")
	}
	for _, i := range []int{1, 2, 3, 5} {
		if !reflect.DeepEqual(result.ListModelsResponse.Data[i], original.ListModelsResponse.Data[i]) {
			t.Fatal("heuristic or cross-provider enrichment")
		}
	}
}

func TestEnrichedValuesDoNotAliasCache(t *testing.T) {
	index := fixtureIndex(t)
	result, _ := enrichResponse(modelResponse(schemas.Model{ID: "cliproxyapi/gpt-5.6-sol"}), "cliproxyapi/", index)
	m := &result.ListModelsResponse.Data[0]
	*m.ContextLength = 1
	*m.MaxOutputTokens = 1
	*m.Name = "mutated"
	m.Architecture.InputModalities[0] = "audio"
	m.Reasoning.SupportedEfforts[0] = "mutated"
	*m.Reasoning.DefaultEffort = "mutated"
	source := index["gpt-5.6-sol"]
	if *source.ContextWindow != 272000 || *source.MaxTokens != 128000 || strings.TrimSpace(source.DisplayName) != "GPT 5.6 Sol" || source.InputModalities[0] != "text" || source.SupportedReasoningLevels[0].Effort != "low" || *source.DefaultReasoningLevel != "low" {
		t.Fatal("response can poison metadata cache")
	}
}

func TestBlankNamesDoNotClearExisting(t *testing.T) {
	m := schemas.Model{Name: ptr("name"), Description: ptr("description")}
	applyMetadata(&m, metadataModel{DisplayName: " \t ", Description: "\n"})
	if *m.Name != "name" || *m.Description != "description" {
		t.Fatal("blank values cleared existing metadata")
	}
}
