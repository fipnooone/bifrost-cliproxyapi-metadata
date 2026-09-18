package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxCatalogBytes int64 = 16 << 20

type catalogCache struct {
	Models    map[string]metadataModel // immutable after publication
	FetchedAt time.Time
}

type metadataClient struct {
	http     *http.Client
	endpoint string
	key      string // runtime only; never put in a cache entry, request log or error
	mu       sync.RWMutex
	cache    catalogCache
}

func newMetadataClient(cfg config, key string) *metadataClient {
	// An explicit transport avoids relying on a mutable process-global transport
	// inside the Bifrost host. No application retries or redirect following.
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: cfg.timeout(), KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: cfg.timeout(), ResponseHeaderTimeout: cfg.timeout(),
		DisableKeepAlives:      true, // no automatic stale-connection replay of GET requests
		MaxResponseHeaderBytes: 1 << 20,
	}
	return &metadataClient{
		http: &http.Client{Transport: transport, Timeout: cfg.timeout(),
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		endpoint: cfg.BaseURL + "/v1/models?client_version=pi", key: key,
	}
}

func (c *metadataClient) close() { c.http.CloseIdleConnections() }
func (c *metadataClient) redact(s string) string {
	if c.key != "" {
		s = strings.ReplaceAll(s, c.key, "[REDACTED]")
	}
	return s
}

// Transport and decoder errors can contain a URL, response byte or server text.
// Only emit closed, safe classifications, never wrap the original error.
func safeTransportError(err error) error {
	if errors.Is(err, context.Canceled) {
		return errors.New("rich catalog request canceled")
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return errors.New("rich catalog request timed out")
	}
	return errors.New("rich catalog network or TLS request failed")
}

func (c *metadataClient) fetch(ctx context.Context) (map[string]metadataModel, boundedIDs, error) {
	var duplicates boundedIDs
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return nil, duplicates, errors.New("could not construct rich catalog request")
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, duplicates, safeTransportError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, duplicates, fmt.Errorf("rich catalog returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxCatalogBytes {
		return nil, duplicates, errors.New("rich catalog exceeds 16 MiB body limit")
	}
	body := &io.LimitedReader{R: resp.Body, N: maxCatalogBytes + 1}
	models, duplicates, err := decodeCatalog(body)
	if body.N <= 0 {
		return nil, duplicates, errors.New("rich catalog exceeds 16 MiB body limit")
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, duplicates, safeTransportError(ctx.Err())
		}
		return nil, duplicates, err // decoder errors are fixed strings
	}
	return models, duplicates, nil
}

// Each relevant operation attempts a refresh. Failed/empty refreshes never
// publish anything. Callers only read snapshots; enrichment copies values out.
func (c *metadataClient) refresh(ctx context.Context) (catalogCache, boundedIDs, error) {
	models, duplicates, err := c.fetch(ctx)
	if err != nil {
		c.mu.RLock()
		cached := c.cache
		c.mu.RUnlock()
		return cached, duplicates, err
	}
	c.mu.Lock()
	fresh := catalogCache{Models: models, FetchedAt: time.Now().UTC()}
	c.cache = fresh
	c.mu.Unlock()
	return fresh, duplicates, nil
}
