package main

import (
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

type enrichmentStats struct {
	Matched   int
	MaxOutput int
	Missing   boundedIDs
}

func ptr[T any](value T) *T { return &value }

func hasTarget(resp *schemas.BifrostResponse, prefix string) bool {
	if resp == nil || resp.ListModelsResponse == nil {
		return false
	}
	for _, model := range resp.ListModelsResponse.Data {
		if strings.HasPrefix(model.ID, prefix) {
			return true
		}
	}
	return false
}

func enrichResponse(resp *schemas.BifrostResponse, prefix string, models map[string]metadataModel) (*schemas.BifrostResponse, enrichmentStats) {
	var stats enrichmentStats
	if !hasTarget(resp, prefix) {
		return resp, stats
	}
	// Own the list slice and modified nested objects. No writes through pointers
	// borrowed from Bifrost or from the immutable metadata cache.
	out := *resp
	list := *resp.ListModelsResponse
	list.Data = append([]schemas.Model(nil), list.Data...)
	out.ListModelsResponse = &list
	for i, model := range list.Data {
		if !strings.HasPrefix(model.ID, prefix) {
			continue
		}
		id := strings.TrimPrefix(model.ID, prefix)
		metadata, ok := models[id]
		if !ok && model.Alias != nil {
			metadata, ok = models[*model.Alias]
		}
		if !ok {
			stats.Missing.add(id)
			continue
		}
		stats.Matched++
		if applyMetadata(&list.Data[i], metadata) {
			stats.MaxOutput++
		}
	}
	return &out, stats
}

// Returns whether positive explicit max-output metadata was applied.
func applyMetadata(model *schemas.Model, source metadataModel) bool {
	if name := strings.TrimSpace(source.DisplayName); name != "" {
		model.Name = ptr(name)
	}
	if description := strings.TrimSpace(source.Description); description != "" {
		model.Description = ptr(description)
	}
	if source.ContextWindow != nil && *source.ContextWindow > 0 {
		model.ContextLength = ptr(*source.ContextWindow)
	}
	outputApplied := source.MaxTokens != nil && *source.MaxTokens > 0
	if outputApplied {
		model.MaxOutputTokens = ptr(*source.MaxTokens)
	}
	if modalities := normalize(source.InputModalities, true); len(modalities) > 0 {
		arch := schemas.Architecture{}
		if model.Architecture != nil {
			arch = *model.Architecture
		}
		arch.InputModalities = modalities
		model.Architecture = &arch
	}
	efforts := reasoningEfforts(source.SupportedReasoningLevels)
	var defaultEffort string
	if source.DefaultReasoningLevel != nil {
		defaultEffort = strings.ToLower(strings.TrimSpace(*source.DefaultReasoningLevel))
	}
	if len(efforts) > 0 || defaultEffort != "" {
		reasoning := schemas.ModelReasoning{}
		if model.Reasoning != nil {
			reasoning = *model.Reasoning
		}
		if len(efforts) > 0 {
			reasoning.SupportedEfforts = efforts
		}
		if defaultEffort != "" {
			reasoning.DefaultEffort = ptr(defaultEffort)
		}
		model.Reasoning = &reasoning
	}
	return outputApplied
}
