package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestRepresentativeCatalog(t *testing.T) {
	models, duplicates, err := decodeCatalog(strings.NewReader(string(fixture(t))))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 4 || duplicates.Count != 0 {
		t.Fatal("wrong catalog size")
	}
	m := models["gpt-5.6-sol"]
	if m.ID != "" || m.ContextWindow == nil || *m.ContextWindow != 272000 || *m.MaxTokens != 128000 {
		t.Fatal("incorrect decoded metadata")
	}
	b, err := json.Marshal(models)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"base_instructions", "visibility", "service_tiers", "max_context_window"} {
		if strings.Contains(string(b), field) {
			t.Fatalf("retained out-of-scope field %s", field)
		}
	}
}
func TestCatalogShapesIdentityAndDuplicates(t *testing.T) {
	cases := []struct {
		name, raw  string
		keys       []string
		duplicates int
	}{
		{"models-slug-null-id", `{"models":[{"slug":"a","id":null}]}`, []string{"a"}, 0},
		{"data-id", `{"data":[{"id":"a"}]}`, []string{"a"}, 0},
		{"slug-primary", `{"models":[{"slug":"a","id":"b"}]}`, []string{"a"}, 0},
		{"blank-slug-id-fallback", `{"models":[{"slug":" ","id":"b"}]}`, []string{"b"}, 0},
		{"exact-not-normalized", `{"models":[{"slug":"Case/Suffix "}]}`, []string{"Case/Suffix "}, 0},
		{"duplicates", `{"models":[{},null,{"slug":"a","context_window":1},{"slug":"a","context_window":2},{"id":"a"}]}`, []string{"a"}, 2},
		{"unknown-envelope", `{"other":{"ignored":[1,true,null,{"x":1}]},"models":[{"slug":"a"}]}`, []string{"a"}, 0},
		{"models-wins", `{"data":[{"id":"b"}],"models":[{"slug":"a"}]}`, []string{"a"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			models, dups, err := decodeCatalog(strings.NewReader(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if len(models) != len(tc.keys) || dups.Count != tc.duplicates {
				t.Fatal("wrong index/duplicate count")
			}
			for _, key := range tc.keys {
				if _, ok := models[key]; !ok {
					t.Fatalf("missing exact identity %q", key)
				}
			}
			if tc.duplicates > 0 && *models["a"].ContextWindow != 1 {
				t.Fatal("duplicate overwrote first entry")
			}
		})
	}
}
func TestCatalogRejectsEmptyMalformedAndTrailingData(t *testing.T) {
	for _, raw := range []string{
		``, `null`, `[]`, `{}`, `{"models":[]}`, `{"models":null}`, `{"models":[{},null,{"slug":" "}]}`,
		`{"models":[],"data":[{"id":"must-not-rescue-empty-models"}]}`,
		`{"models":[{"slug":"a"}]`, `{"models":42}`, `{"models":["a"]}`,
		`{"models":[{"slug":"a","max_tokens":"wrong"}]}`,
		`{"models":[{"slug":"a","context_window":1.5}]}`,
		`{"models":[{"slug":"a","context_window":99999999999999999999999}]}`,
		`{"models":[{"slug":"a"}]} true`, `{"models":[{"slug":"a"}]} {}`,
		`{"models":[{"slug":"a"}]} garbage`, `{"models":[],"models":[{"slug":"b"}]}`,
		`{"data":[],"data":[{"id":"b"}]}`,
	} {
		if _, _, err := decodeCatalog(strings.NewReader(raw)); err == nil {
			t.Errorf("accepted invalid/empty document: %s", raw)
		}
	}
}
func TestNormalizeAndReasoning(t *testing.T) {
	got := normalize([]string{" TEXT ", "Image", "text", "audio", "video", "file", "", " image "}, true)
	if !reflect.DeepEqual(got, []string{"text", "image"}) {
		t.Fatal(got)
	}
	if got := normalize([]string{"Audio", ""}, true); len(got) != 0 {
		t.Fatal(got)
	}
	got = reasoningEfforts([]reasoningLevel{{"None"}, {" LOW "}, {"low"}, {"Ultra"}, {""}, {" Future Effort "}})
	if !reflect.DeepEqual(got, []string{"none", "low", "ultra", "future effort"}) {
		t.Fatal(got)
	}
}
func TestBoundedDiagnosticsAndRedaction(t *testing.T) {
	c := &metadataClient{key: testToken}
	var ids boundedIDs
	for i := 0; i < 20; i++ {
		ids.add(fmt.Sprintf("%s\n%d", testToken, i))
	}
	msg := ids.summary(c.redact)
	if ids.Count != 20 || len(ids.First) != 10 || !strings.Contains(msg, "(+10 more)") || strings.Contains(msg, testToken) || strings.Contains(msg, "\n") {
		t.Fatal("unbounded or unsafe diagnostics")
	}
	var long boundedIDs
	long.add(strings.Repeat("x", 155) + testToken)
	if strings.Contains(long.summary(c.redact), "not-a") {
		t.Fatal("truncation leaked token prefix")
	}
}
