package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

const RecommendedPath = "/ai/cline/recommended-models"

var subscriptionID = regexp.MustCompile(`^cline-pass/[a-zA-Z0-9._-]+$`)

// Decode deliberately reads only clinePass. Recommended, free, cloud and the
// general /models catalog must never leak into subscription model registration.
func Decode(raw []byte) ([]Model, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("invalid official model response: %w", err)
	}
	if nested, ok := root["data"]; ok {
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(nested, &inner); err != nil {
			return nil, fmt.Errorf("invalid official model envelope: %w", err)
		}
		root = inner
	}
	list, ok := root["clinePass"]
	if !ok || string(list) == "null" {
		return nil, fmt.Errorf("official response is missing clinePass")
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(list, &entries); err != nil {
		return nil, fmt.Errorf("invalid clinePass list: %w", err)
	}
	models := make([]Model, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		var model Model
		if err := json.Unmarshal(entry, &model); err != nil {
			if err := json.Unmarshal(entry, &model.ID); err != nil {
				continue
			}
		}
		model.ID = strings.TrimSpace(model.ID)
		if !subscriptionID.MatchString(model.ID) || seen[model.ID] {
			continue
		}
		seen[model.ID] = true
		model.Name = strings.TrimPrefix(model.ID, "cline-pass/")
		models = append(models, model)
	}
	if len(entries) > 0 && len(models) == 0 {
		return nil, fmt.Errorf("official list contains no valid Cline Pass models")
	}
	return models, nil
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

func fetch(ctx context.Context, client HTTPClient, baseURL string) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(baseURL, "/")+RecommendedPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("official model endpoint returned HTTP %d", resp.StatusCode)
	}
	const maxBytes = 2 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBytes {
		return nil, fmt.Errorf("official model response exceeds size limit")
	}
	return Decode(raw)
}
