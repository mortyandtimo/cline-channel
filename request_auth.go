package main

import "strings"
import "encoding/json"

func resolveUpstreamModel(raw, prefix string) string {
	model := strings.TrimSpace(raw)
	if strings.HasPrefix(model, "cline-pass/") || strings.HasPrefix(model, "cline-free/") ||
		strings.HasPrefix(model, "cline-cloud/") {
		return model
	}
	if prefix != "" && strings.HasPrefix(model, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(model, prefix))
	}
	return strings.TrimPrefix(model, "cline/")
}

func resolveAPIKey(req rpcExecutorRequest, cfg PluginConfig) string {
	if len(cfg.Accounts) > 0 {
		if keys := resolveConfiguredKeys(cfg); len(keys) > 0 {
			return keys[0]
		}
	}
	if key := apiKeyFromAuth(req.StorageJSON); key != "" {
		return key
	}
	if key := apiKeyFromAuthMetadata(req.AuthMetadata); key != "" {
		return key
	}
	if key := strings.TrimSpace(req.Headers.Get("X-Cline-Api-Key")); key != "" {
		return key
	}
	// Authorization belongs to the downstream CPA client and is never a Cline key.
	return strings.TrimSpace(cfg.APIKey)
}

func apiKeyFromAuth(storage []byte) string {
	var root map[string]any
	if json.Unmarshal(storage, &root) != nil {
		return ""
	}
	if key := apiKeyFromAuthMetadata(root); key != "" {
		return key
	}
	for _, nested := range []string{"auth", "credentials", "cline"} {
		if child, ok := root[nested].(map[string]any); ok {
			if key := apiKeyFromAuthMetadata(child); key != "" {
				return key
			}
		}
	}
	return ""
}

func apiKeyFromAuthMetadata(meta map[string]any) string {
	return pickString(meta, "api_key", "apiKey", "key", "access_token", "accessToken", "token")
}

func pickString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type errString string

func (e errString) Error() string { return string(e) }
