// Native Bifrost plugin. There is deliberately no HTTP transport hook.
package main

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

const pluginName = "cliproxyapi-model-metadata"

type pluginState struct {
	cfg    config
	client *metadataClient
}

var state atomic.Pointer[pluginState]

func GetName() string { return pluginName }

func Init(raw any) error {
	cfg, key, err := parseConfig(raw)
	if err != nil {
		return err
	}
	next := &pluginState{cfg: cfg, client: newMetadataClient(cfg, key)}
	if previous := state.Swap(next); previous != nil {
		previous.client.close()
	}
	// Init has no Bifrost request context/logger injection in this native ABI.
	log.Printf("INFO [%s] %s", pluginName, next.client.redact(fmt.Sprintf("CLIProxyAPI metadata plugin initialized for provider %q", cfg.ProviderID)))
	return nil
}

func Cleanup() error {
	if previous := state.Swap(nil); previous != nil {
		previous.client.close()
	}
	return nil
}

func PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error { return nil }
func PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	return req, nil, nil
}

func PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	current := state.Load()
	if current == nil {
		return resp, bifrostErr, nil
	}
	return current.postLLMHook(ctx, resp, bifrostErr)
}

func (s *pluginState) postLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	prefix := s.cfg.ProviderID + "/"
	if !hasTarget(resp, prefix) {
		return resp, bifrostErr, nil
	}
	// Preserve client cancellation, but do not let net/http retain a pooled
	// plugin-scoped context after this hook returns. Root is stable for the request.
	var requestCtx context.Context = context.Background()
	if ctx != nil {
		requestCtx = ctx.Root()
	}
	cached, duplicates, err := s.client.refresh(requestCtx)
	if err != nil {
		if len(cached.Models) == 0 {
			s.emit(ctx, "warn", fmt.Sprintf("rich metadata unavailable and no cache exists; returning original model response: %s", err))
			return resp, bifrostErr, nil
		}
		s.emit(ctx, "warn", fmt.Sprintf("rich metadata request failed; using cache from %s: %s", cached.FetchedAt.Format(time.RFC3339Nano), err))
	} else {
		s.emit(ctx, "debug", fmt.Sprintf("fetched %d CLIProxyAPI metadata entries", len(cached.Models)))
		if duplicates.Count > 0 {
			s.emit(ctx, "warn", fmt.Sprintf("ignored %d duplicate CLIProxyAPI model entries (first valid entry wins): %s", duplicates.Count, duplicates.summary(s.client.redact)))
		}
	}
	out, stats := enrichResponse(resp, prefix, cached.Models)
	if stats.Missing.Count > 0 {
		s.emit(ctx, "warn", fmt.Sprintf("CLIProxyAPI metadata missing for %d Bifrost model(s): %s", stats.Missing.Count, stats.Missing.summary(s.client.redact)))
	}
	s.emit(ctx, "debug", fmt.Sprintf("enriched %d %s models; %d models received max output metadata", stats.Matched, s.cfg.ProviderID, stats.MaxOutput))
	return out, bifrostErr, nil
}

func (s *pluginState) emit(ctx *schemas.BifrostContext, level, message string) {
	message = s.client.redact(message)
	if ctx == nil {
		log.Printf("%s [%s] %s", level, pluginName, message)
		return
	}
	if level == "warn" {
		ctx.Log(schemas.LogLevelWarn, message)
	} else {
		ctx.Log(schemas.LogLevelDebug, message)
	}
}
