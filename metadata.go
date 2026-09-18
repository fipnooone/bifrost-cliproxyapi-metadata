package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// These are the only upstream fields retained. In particular, instructions,
// visibility, pricing, service tiers and max_context_window are not modeled.
type metadataModel struct {
	Slug                     string           `json:"slug"`
	ID                       string           `json:"id"`
	DisplayName              string           `json:"display_name"`
	Description              string           `json:"description"`
	ContextWindow            *int             `json:"context_window"`
	MaxTokens                *int             `json:"max_tokens"`
	InputModalities          []string         `json:"input_modalities"`
	SupportedReasoningLevels []reasoningLevel `json:"supported_reasoning_levels"`
	DefaultReasoningLevel    *string          `json:"default_reasoning_level"`
}

type reasoningLevel struct {
	Effort string `json:"effort"`
}

// boundedIDs stores at most ten diagnostic identifiers, never a catalog body.
type boundedIDs struct {
	Count int
	First []string
}

func (b *boundedIDs) add(id string) {
	b.Count++
	if len(b.First) < 10 {
		b.First = append(b.First, id)
	}
}
func (b boundedIDs) summary(redact func(string) string) string {
	ids := make([]string, len(b.First))
	for i, id := range b.First {
		// Redact BEFORE truncating, so truncation cannot expose a partial token.
		id = redact(id)
		if len(id) > 160 {
			id = id[:160] + "..."
		}
		ids[i] = strconv.QuoteToASCII(id)
	}
	result := strings.Join(ids, ", ")
	if b.Count > len(b.First) {
		result += fmt.Sprintf(" (+%d more)", b.Count-len(b.First))
	}
	return result
}

var errCatalogJSON = errors.New("invalid rich catalog JSON or field types")
var errEmptyCatalog = errors.New("rich catalog has zero usable model identities")

func identity(m metadataModel) string {
	// Whitespace-only identities are unusable, but do not alter valid identities:
	// matching is exact, not trimming/normalizing upstream model names.
	if strings.TrimSpace(m.Slug) != "" {
		return m.Slug
	}
	if strings.TrimSpace(m.ID) != "" {
		return m.ID
	}
	return ""
}

// Read one model at a time rather than decoding the whole document into a map
// or RawMessage. The JSON decoder skips unmodeled per-model fields; no raw
// catalog or instruction fields are retained in the cache.
func decodeModels(d *json.Decoder) (map[string]metadataModel, boundedIDs, error) {
	models := make(map[string]metadataModel)
	var duplicates boundedIDs
	t, err := d.Token()
	if err != nil {
		return nil, duplicates, errCatalogJSON
	}
	if t == nil {
		return models, duplicates, nil
	}
	if t != json.Delim('[') {
		return nil, duplicates, errCatalogJSON
	}
	for d.More() {
		var m metadataModel
		if err := d.Decode(&m); err != nil {
			return nil, duplicates, errCatalogJSON
		}
		id := identity(m)
		if id == "" {
			continue
		}
		if _, exists := models[id]; exists {
			duplicates.add(id)
			continue
		}
		models[id] = m
	}
	t, err = d.Token()
	if err != nil || t != json.Delim(']') {
		return nil, duplicates, errCatalogJSON
	}
	return models, duplicates, nil
}

// Ignore unrelated envelope fields without retaining them. Nesting is bounded
// independently of the body-size bound to avoid recursive stack exhaustion.
func skipJSON(d *json.Decoder, depth int) error {
	if depth > 128 {
		return errCatalogJSON
	}
	t, err := d.Token()
	if err != nil {
		return errCatalogJSON
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		for d.More() {
			if _, err := d.Token(); err != nil {
				return errCatalogJSON
			}
			if err := skipJSON(d, depth+1); err != nil {
				return err
			}
		}
		t, err = d.Token()
		if err != nil || t != json.Delim('}') {
			return errCatalogJSON
		}
	case '[':
		for d.More() {
			if err := skipJSON(d, depth+1); err != nil {
				return err
			}
		}
		t, err = d.Token()
		if err != nil || t != json.Delim(']') {
			return errCatalogJSON
		}
	default:
		return errCatalogJSON
	}
	return nil
}

func decodeCatalog(r io.Reader) (map[string]metadataModel, boundedIDs, error) {
	d := json.NewDecoder(r)
	d.UseNumber()
	var duplicates boundedIDs
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return nil, duplicates, errCatalogJSON
	}
	var primary, fallback map[string]metadataModel
	var primaryDup, fallbackDup boundedIDs
	var sawPrimary, sawFallback bool
	for d.More() {
		t, err = d.Token()
		if err != nil {
			return nil, duplicates, errCatalogJSON
		}
		key, ok := t.(string)
		if !ok {
			return nil, duplicates, errCatalogJSON
		}
		switch key {
		case "models":
			if sawPrimary {
				return nil, duplicates, errCatalogJSON
			}
			sawPrimary = true
			primary, primaryDup, err = decodeModels(d)
		case "data":
			if sawFallback {
				return nil, duplicates, errCatalogJSON
			}
			sawFallback = true
			fallback, fallbackDup, err = decodeModels(d)
		default:
			err = skipJSON(d, 0)
		}
		if err != nil {
			return nil, duplicates, errCatalogJSON
		}
	}
	t, err = d.Token()
	if err != nil || t != json.Delim('}') {
		return nil, duplicates, errCatalogJSON
	}
	// Reject appended documents or trailing garbage, without decoding them into
	// a large interface{}. A clean EOF (after whitespace) is the only success.
	if _, err = d.Token(); err != io.EOF {
		return nil, duplicates, errCatalogJSON
	}
	models := fallback
	duplicates = fallbackDup
	if sawPrimary {
		models, duplicates = primary, primaryDup
	}
	if len(models) == 0 {
		return nil, duplicates, errEmptyCatalog
	}
	return models, duplicates, nil
}

func normalize(values []string, modalities bool) []string {
	var out []string
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		if modalities && value != "text" && value != "image" {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func reasoningEfforts(levels []reasoningLevel) []string {
	values := make([]string, 0, len(levels))
	for _, level := range levels {
		values = append(values, level.Effort)
	}
	return normalize(values, false)
}
