package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func clientFor(t *testing.T, url string) *metadataClient {
	t.Helper()
	c := newMetadataClient(config{BaseURL: url, TimeoutMS: 1000}, testToken)
	t.Cleanup(c.close)
	return c
}
func TestHTTPContractAndNoStartupFetch(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.RequestURI() != "/v1/models?client_version=pi" || r.Header.Get("Authorization") != "Bearer "+testToken || r.Header.Get("Accept") != "application/json" {
			t.Error("unexpected HTTP request contract")
		}
		_, _ = w.Write([]byte(`{"models":[{"slug":"a"}]}`))
	}))
	defer server.Close()
	c := clientFor(t, server.URL)
	if calls.Load() != 0 {
		t.Fatal("startup fetch")
	}
	for i := int32(1); i <= 2; i++ {
		cache, _, err := c.refresh(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if calls.Load() != i || len(cache.Models) != 1 || cache.FetchedAt.IsZero() {
			t.Fatal("not freshly fetching each operation")
		}
	}
}
func TestFailedRefreshUsesLastGoodCache(t *testing.T) {
	failures := []struct {
		name string
		code int
		body string
	}{
		{"http500", 500, testToken}, {"http401", 401, testToken},
		{"invalid-json", 200, `{"models":` + testToken}, {"empty-models", 200, `{"models":[]}`},
		{"zero-usable", 200, `{"models":[{},null,{"slug":""}]}`},
		{"wrong-type", 200, `{"models":[{"slug":"a","max_tokens":"` + testToken + `"}]}`},
		{"trailing", 200, `{"models":[{"slug":"new"}]} invalid`},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			var bad atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if bad.Load() {
					w.WriteHeader(tc.code)
					_, _ = w.Write([]byte(tc.body))
					return
				}
				_, _ = w.Write([]byte(`{"models":[{"slug":"good","context_window":272000}]}`))
			}))
			defer server.Close()
			c := clientFor(t, server.URL)
			first, _, err := c.refresh(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			bad.Store(true)
			second, _, err := c.refresh(context.Background())
			if err == nil || strings.Contains(err.Error(), testToken) || len(second.Models) != 1 || second.Models["good"].ContextWindow == nil || !second.FetchedAt.Equal(first.FetchedAt) {
				t.Fatal("failed refresh corrupted cache or exposed credential")
			}
		})
	}
}
func TestNetworkFailureAndNoCache(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"models":[{"slug":"good"}]}`)) }))
	c := clientFor(t, server.URL)
	first, _, err := c.refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	second, _, err := c.refresh(context.Background())
	if err == nil || len(second.Models) != 1 || !first.FetchedAt.Equal(second.FetchedAt) {
		t.Fatal("network failure did not use cache")
	}
	fresh := clientFor(t, server.URL)
	empty, _, err := fresh.refresh(context.Background())
	if err == nil || len(empty.Models) != 0 || !empty.FetchedAt.IsZero() {
		t.Fatal("failure without cache should leave it empty")
	}
}
func TestSuccessfulRefreshReplacesWholeCache(t *testing.T) {
	var generation atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"models":[{"slug":"generation-%d"}]}`, generation.Add(1))
	}))
	defer server.Close()
	c := clientFor(t, server.URL)
	_, _, err := c.refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cache, _, err := c.refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cache.Models) != 1 || identity(cache.Models["generation-2"]) != "generation-2" {
		t.Fatal("did not replace whole catalog")
	}
}
func TestRedirectNeverForwardsCredential(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	c := clientFor(t, redirect.URL)
	_, _, err := c.refresh(context.Background())
	if err == nil || reached.Load() != 0 {
		t.Fatal("followed metadata redirect")
	}
}
func TestCancellationAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	c := clientFor(t, server.URL)
	c.http.Timeout = 25 * time.Millisecond
	start := time.Now()
	_, _, err := c.refresh(context.Background())
	if err == nil || time.Since(start) > time.Second {
		t.Fatal("client timeout not applied")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = c.refresh(ctx)
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatal("cancellation not propagated")
	}
}
func TestBodyLimit(t *testing.T) {
	for _, declared := range []bool{false, true} {
		t.Run(fmt.Sprint(declared), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if declared {
					w.Header().Set("Content-Length", fmt.Sprint(maxCatalogBytes+100))
					return
				}
				w.(http.Flusher).Flush()
				_, _ = fmt.Fprintf(w, `{"models":[{"slug":"a","base_instructions":"%s"}]}`, strings.Repeat("x", int(maxCatalogBytes)))
			}))
			defer server.Close()
			c := clientFor(t, server.URL)
			cache, _, err := c.refresh(context.Background())
			if err == nil || len(cache.Models) > 0 {
				t.Fatal("accepted oversized catalog")
			}
		})
	}
}
func TestConcurrentRefreshIsRaceSafe(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1)%4 == 0 {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"slug":"a","context_window":272000,"max_tokens":128000}]}`))
	}))
	defer server.Close()
	c := clientFor(t, server.URL)
	if _, _, err := c.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache, _, _ := c.refresh(context.Background())
			if len(cache.Models) != 1 || *cache.Models["a"].ContextWindow != 272000 {
				t.Error("bad concurrent snapshot")
			}
		}()
	}
	wg.Wait()
}
