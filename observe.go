package main

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type observedRoute struct {
	Model         string    `json:"model"`
	Provider      string    `json:"provider"`
	CanonicalSlug string    `json:"canonicalSlug,omitempty"`
	ResolvedBy    string    `json:"resolvedBy,omitempty"`
	Fallbacks     []string  `json:"fallbacks,omitempty"`
	At            time.Time `json:"at"`
}

var (
	observedMu          sync.RWMutex
	observed            = map[string]*observedRoute{}
	errorProviderRe     = regexp.MustCompile(`"provider_name"\s*:\s*"([^"]+)"`)
	providerPunctuation = regexp.MustCompile(`[^a-z0-9]`)
)

func parseObservedRoute(raw []byte, model string) *observedRoute {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	if inner, ok := root["data"].(map[string]any); ok {
		root = inner
	}
	route := &observedRoute{Model: model, At: time.Now()}
	paths := [][]any{
		{"choices", 0, "message", "provider_metadata", "gateway", "routing"},
		{"choices", 0, "delta", "provider_metadata", "gateway", "routing"},
		{"provider_metadata", "gateway", "routing"},
	}
	for _, path := range paths {
		if routing := nestedMap(root, path...); routing != nil {
			route.Provider = firstNonEmpty(str(routing["finalProvider"]), str(routing["resolvedProvider"]))
			route.CanonicalSlug = str(routing["canonicalSlug"])
			route.ResolvedBy = pipelinePlanner
			if values, ok := routing["fallbacksAvailable"].([]any); ok {
				for _, value := range values {
					if name := str(value); name != "" {
						route.Fallbacks = append(route.Fallbacks, name)
					}
				}
			}
			if route.Provider != "" {
				break
			}
		}
	}
	if route.Provider == "" && str(root["provider"]) != "" {
		route.Provider, route.CanonicalSlug, route.ResolvedBy = str(root["provider"]), str(root["model"]), pipelineDirect
	}
	if route.Provider == "" {
		if match := errorProviderRe.FindStringSubmatch(unwrapErrorText(raw)); len(match) > 1 {
			route.Provider, route.ResolvedBy = match[1], "error"
		}
	}
	if route.Provider == "" {
		return nil
	}
	return route
}

func observeUpstreamRoute(raw []byte, model string) *observedRoute {
	route := parseObservedRoute(raw, model)
	if route != nil && model != "" {
		observedMu.Lock()
		observed[model] = route
		observedMu.Unlock()
	}
	return route
}

func observedSummary() []*observedRoute {
	observedMu.RLock()
	defer observedMu.RUnlock()
	out := make([]*observedRoute, 0, len(observed))
	for _, route := range observed {
		copy := *route
		copy.Fallbacks = append([]string(nil), route.Fallbacks...)
		out = append(out, &copy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

func nestedMap(root map[string]any, keys ...any) map[string]any {
	var current any = root
	for _, key := range keys {
		switch key := key.(type) {
		case string:
			m, ok := current.(map[string]any)
			if !ok {
				return nil
			}
			current = m[key]
		case int:
			list, ok := current.([]any)
			if !ok || key < 0 || key >= len(list) {
				return nil
			}
			current = list[key]
		}
	}
	result, _ := current.(map[string]any)
	return result
}

func normalizeProvider(value string) string {
	return providerPunctuation.ReplaceAllString(strings.ToLower(value), "")
}
