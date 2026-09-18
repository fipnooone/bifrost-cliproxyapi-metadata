package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const defaultProviderID = "cliproxyapi"
const defaultKeyEnv = "CLIPROXYAPI_API_KEY"
const maxTimeoutMS = 30000

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type config struct {
	ProviderID string `json:"provider_id"`
	BaseURL    string `json:"base_url"`
	APIKeyEnv  string `json:"api_key_env"`
	TimeoutMS  int    `json:"timeout_ms"`
}

// Parse only the documented configuration. Never echo a supplied value in errors:
// malformed configuration can accidentally contain a pasted credential.
func parseConfig(raw any) (config, string, error) {
	cfg := config{ProviderID: defaultProviderID, APIKeyEnv: defaultKeyEnv, TimeoutMS: 5000}
	b, err := json.Marshal(raw)
	if err != nil {
		return cfg, "", errors.New("plugin config must be a JSON object")
	}
	if len(b) == 0 || b[0] != '{' {
		return cfg, "", errors.New("plugin config must be a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil {
		return cfg, "", errors.New("invalid plugin config: check field names and types")
	}
	if !identifierPattern.MatchString(cfg.ProviderID) {
		return cfg, "", errors.New("provider_id must be a non-empty identifier without slashes or whitespace")
	}
	if !envPattern.MatchString(cfg.APIKeyEnv) {
		return cfg, "", errors.New("api_key_env must be an environment variable name")
	}
	if cfg.TimeoutMS < 1 || cfg.TimeoutMS > maxTimeoutMS {
		return cfg, "", errors.New("timeout_ms must be an integer from 1 through 30000")
	}
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u == nil || u.Hostname() == "" ||
		(u.Scheme != "http" && u.Scheme != "https") || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" ||
		strings.Trim(u.Path, "/") != "" || u.RawPath != "" {
		return cfg, "", errors.New("base_url is required and must be an HTTP(S) root URL without credentials, query, or fragment")
	}
	// Retain the parsed host, including any port or bracketed IPv6 address.
	u.Path, u.RawPath = "", ""
	cfg.BaseURL = u.String()
	key, ok := os.LookupEnv(cfg.APIKeyEnv)
	if !ok || strings.TrimSpace(key) == "" {
		return cfg, "", errors.New("API key environment variable named by api_key_env is missing or empty")
	}
	for _, r := range key {
		if r <= ' ' || r >= 127 {
			return cfg, "", errors.New("API key environment variable must contain a printable ASCII token without whitespace")
		}
	}
	return cfg, key, nil
}

func (c config) timeout() time.Duration { return time.Duration(c.TimeoutMS) * time.Millisecond }
