package main

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

var (
	availableProvidersRe = regexp.MustCompile(`"available_providers"\s*:\s*\[([^\]]*)\]`)
	plannerProvidersRe   = regexp.MustCompile(`(?i)Available providers are:\s*([a-z0-9_,. /-]+)`)
	openRouterModelRe    = regexp.MustCompile(`failed to invoke model '([^']+)'`)
)

func parseProbeResponse(raw []byte) ([]string, string, string) {
	text := unwrapErrorText(raw)
	canonical := ""
	if match := openRouterModelRe.FindStringSubmatch(text); len(match) > 1 {
		canonical = match[1]
	}
	if match := availableProvidersRe.FindStringSubmatch(text); len(match) > 1 {
		return splitChannelList(match[1]), pipelineDirect, canonical
	}
	if match := plannerProvidersRe.FindStringSubmatch(text); len(match) > 1 {
		return splitChannelList(match[1]), pipelinePlanner, canonical
	}
	return nil, "", canonical
}

func splitChannelList(value string) []string {
	parts := strings.Split(value, ",")
	for i, part := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(part), `"'.`)
	}
	result := cleanList(parts)
	sort.Strings(result)
	return result
}

func unwrapErrorText(raw []byte) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	var walk func(any, int) string
	walk = func(v any, depth int) string {
		if depth > 6 {
			return ""
		}
		switch typed := v.(type) {
		case string:
			var decoded any
			if json.Unmarshal([]byte(typed), &decoded) == nil {
				return walk(decoded, depth+1)
			}
			return typed
		case map[string]any:
			text, _ := json.Marshal(typed)
			out := string(text)
			for _, item := range typed {
				out += "\n" + walk(item, depth+1)
			}
			return out
		case []any:
			text, _ := json.Marshal(typed)
			return string(text)
		}
		return ""
	}
	return walk(value, 0)
}
