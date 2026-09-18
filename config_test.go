package main

import (
	"strings"
	"testing"
)

const testToken = "not-a-real-credential"

func TestConfigDefaultsAndNormalization(t *testing.T) {
	t.Setenv(defaultKeyEnv, testToken)
	cfg, key, err := parseConfig(map[string]any{"base_url": "https://example.com///"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProviderID != defaultProviderID || cfg.APIKeyEnv != defaultKeyEnv || cfg.TimeoutMS != 5000 || cfg.BaseURL != "https://example.com" || key != testToken {
		t.Fatal("incorrect defaults or URL normalization")
	}
	for _, u := range []string{"http://127.0.0.1:1234", "http://[::1]:8080/"} {
		if _, _, err := parseConfig(map[string]any{"base_url": u}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfigRejectsInvalidValuesWithoutLeaking(t *testing.T) {
	t.Setenv(defaultKeyEnv, testToken)
	cases := []struct {
		name string
		raw  any
	}{
		{"nil", nil}, {"array", []any{}}, {"not-json", make(chan int)},
		{"base-missing", map[string]any{}},
		{"relative", map[string]any{"base_url": "/server"}},
		{"userinfo", map[string]any{"base_url": "https://user:" + testToken + "@example.com"}},
		{"query", map[string]any{"base_url": "https://example.com?key=" + testToken}},
		{"fragment", map[string]any{"base_url": "https://example.com#" + testToken}},
		{"path", map[string]any{"base_url": "https://example.com/v1"}},
		{"protocol", map[string]any{"base_url": "file:///etc/passwd"}},
		{"bad-percent", map[string]any{"base_url": "https://example.com/%xy" + testToken}},
		{"unknown", map[string]any{"base_url": "https://example.com", testToken: testToken}},
		{"slash-provider", map[string]any{"base_url": "https://example.com", "provider_id": "wrong/"}},
		{"empty-provider", map[string]any{"base_url": "https://example.com", "provider_id": ""}},
		{"bad-env", map[string]any{"base_url": "https://example.com", "api_key_env": "a=b"}},
		{"zero", map[string]any{"base_url": "https://example.com", "timeout_ms": 0}},
		{"negative", map[string]any{"base_url": "https://example.com", "timeout_ms": -1}},
		{"too-large", map[string]any{"base_url": "https://example.com", "timeout_ms": 30001}},
		{"fractional", map[string]any{"base_url": "https://example.com", "timeout_ms": 5.1}},
		{"wrong-type", map[string]any{"base_url": "https://example.com", "timeout_ms": testToken}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseConfig(tc.raw)
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), testToken) {
				t.Fatal("secret leaked in config error")
			}
		})
	}
}

func TestConfigKeyEnvironment(t *testing.T) {
	for _, token := range []string{"", " ", "line\nbreak", "has space", "has\tTAB", "unicode-é"} {
		t.Setenv(defaultKeyEnv, token)
		if _, _, err := parseConfig(map[string]any{"base_url": "https://example.com"}); err == nil {
			t.Fatal("accepted missing/invalid key")
		}
	}
	t.Setenv("PLUGIN_TEST_ALTERNATE_KEY", testToken)
	cfg, key, err := parseConfig(map[string]any{"base_url": "https://example.com", "api_key_env": "PLUGIN_TEST_ALTERNATE_KEY", "provider_id": "my-provider", "timeout_ms": 30000})
	if err != nil || key != testToken || cfg.ProviderID != "my-provider" {
		t.Fatal("custom configuration rejected")
	}
}
