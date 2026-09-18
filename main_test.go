package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func stateFor(t *testing.T, url string) *pluginState {
	t.Helper()
	return &pluginState{cfg: config{ProviderID: "cliproxyapi", BaseURL: url, TimeoutMS: 1000}, client: clientFor(t, url)}
}

func TestNonListAndNoTargetPassthroughWithoutHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	s := stateFor(t, server.URL)
	bfErr := &schemas.BifrostError{}
	for _, resp := range []*schemas.BifrostResponse{nil, {}, modelResponse(), modelResponse(schemas.Model{ID: "openai/gpt-5.6-sol"}), modelResponse(schemas.Model{ID: "cliproxyapi2/gpt-5.6-sol"})} {
		result, e, err := s.postLLMHook(nil, resp, bfErr)
		if result != resp || e != bfErr || err != nil {
			t.Fatal("passthrough changed response or error")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("made HTTP request without relevant models")
	}
}

func TestHookRefreshFallbackAndNoCacheFailOpen(t *testing.T) {
	for _, failure := range []string{"http500", "network", "invalid", "empty"} {
		t.Run(failure, func(t *testing.T) {
			var bad atomic.Bool
			raw := fixture(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !bad.Load() {
					_, _ = w.Write(raw)
					return
				}
				switch failure {
				case "invalid":
					_, _ = w.Write([]byte("{invalid"))
				case "empty":
					_, _ = w.Write([]byte(`{"models":[]}`))
				default:
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			s := stateFor(t, server.URL)
			original := modelResponse(schemas.Model{ID: "cliproxyapi/gpt-5.6-sol"})
			bfErr := &schemas.BifrostError{}
			first, e, err := s.postLLMHook(nil, original, bfErr)
			if err != nil || e != bfErr || first.ListModelsResponse.Data[0].ContextLength == nil {
				t.Fatal("initial enrichment failed")
			}
			bad.Store(true)
			if failure == "network" {
				server.Close()
			}
			second, e, err := s.postLLMHook(nil, original, bfErr)
			if err != nil || e != bfErr || *second.ListModelsResponse.Data[0].ContextLength != 272000 {
				t.Fatal("fallback did not preserve response/error")
			}
			fresh := stateFor(t, server.URL)
			unchanged, e, err := fresh.postLLMHook(nil, original, bfErr)
			if err != nil || e != bfErr || unchanged != original {
				t.Fatal("failure without cache was not exact passthrough")
			}
		})
	}
}

func TestHookLogsAreAggregatedBoundedAndRedacted(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	body := `{"models":[{"slug":"a"},{"slug":"a"},{"slug":"` + testToken + `"},{"slug":"` + testToken + `"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer server.Close()
	s := stateFor(t, server.URL)
	models := []schemas.Model{{ID: "cliproxyapi/a"}}
	for i := 0; i < 12; i++ {
		models = append(models, schemas.Model{ID: fmt.Sprintf("cliproxyapi/%s-%d", testToken, i)})
	}
	_, _, err := s.postLLMHook(nil, modelResponse(models...), nil)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if strings.Contains(output, testToken) {
		t.Fatal("secret appeared in logs")
	}
	if strings.Count(output, "metadata missing for") != 1 || !strings.Contains(output, "(+2 more)") || strings.Count(output, "duplicate CLIProxyAPI") != 1 {
		t.Fatal("diagnostics not aggregated/bounded")
	}
}

func TestLifecycleAndExportedHook(t *testing.T) {
	_ = Cleanup()
	t.Cleanup(func() { _ = Cleanup() })
	t.Setenv(defaultKeyEnv, testToken)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"models":[{"slug":"a","context_window":10}]}`))
	}))
	defer server.Close()
	if GetName() != "cliproxyapi-model-metadata" {
		t.Fatal("wrong plugin identity")
	}
	cfg := map[string]any{"base_url": server.URL}
	if err := Init(cfg); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("Init fetched metadata")
	}
	good := state.Load()
	if err := Init(map[string]any{}); err == nil || state.Load() != good {
		t.Fatal("invalid reinitialization replaced working state")
	}
	request := &schemas.BifrostRequest{}
	if err := PreRequestHook(nil, request); err != nil {
		t.Fatal(err)
	}
	r, short, err := PreLLMHook(nil, request)
	if r != request || short != nil || err != nil {
		t.Fatal("pre-hook changed request")
	}
	original := modelResponse(schemas.Model{ID: "cliproxyapi/a"})
	result, _, err := PostLLMHook(nil, original, nil)
	if err != nil || *result.ListModelsResponse.Data[0].ContextLength != 10 {
		t.Fatal("exported post hook did not enrich")
	}
	if err := Cleanup(); err != nil {
		t.Fatal(err)
	}
	result, _, err = PostLLMHook(nil, original, nil)
	if result != original || err != nil {
		t.Fatal("uninitialized/cleaned hook must be fail-open")
	}
}

func TestConcurrentHooksUseIndependentCopies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"slug":"a","context_window":10,"input_modalities":["text"]}]}`))
	}))
	defer server.Close()
	s := stateFor(t, server.URL)
	original := modelResponse(schemas.Model{ID: "cliproxyapi/a", Architecture: &schemas.Architecture{OutputModalities: []string{"text"}}})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, _, err := s.postLLMHook(nil, original, nil)
			if err != nil || *out.ListModelsResponse.Data[0].ContextLength != 10 {
				t.Error("concurrent hook failure")
			}
			out.ListModelsResponse.Data[0].Architecture.InputModalities[0] = "mutated"
		}()
	}
	wg.Wait()
	if original.ListModelsResponse.Data[0].ContextLength != nil || len(original.ListModelsResponse.Data[0].Architecture.InputModalities) != 0 {
		t.Fatal("shared response mutated")
	}
}
