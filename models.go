package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"cline-channel/internal/catalog"
	"cline-channel/sdk/pluginabi"
	"cline-channel/sdk/pluginapi"
)

var modelCatalog catalog.Cache

// All host registration paths use the same subscription snapshot.
func handleModels(method string, request []byte) ([]byte, error) {
	if method == pluginabi.MethodModelForAuth {
		var req pluginapi.AuthModelRequest
		if err := json.Unmarshal(request, &req); len(request) > 0 && err != nil {
			return nil, err
		}
		captureHostConfig(req.Host)
		rememberCredential(apiKeyFromAuth(req.StorageJSON))
	} else {
		var req pluginapi.StaticModelRequest
		if err := json.Unmarshal(request, &req); len(request) > 0 && err != nil {
			return nil, err
		}
		captureHostConfig(req.Host)
	}
	return okEnvelope(pluginapi.ModelRegistrationResponse{
		Provider: providerKey,
		Models:   loadModels(getConfig(), ""),
	})
}

func subscriptionSnapshot(cfg PluginConfig, force bool) catalog.Snapshot {
	if len(cfg.Models) > 0 {
		models := make([]catalog.Model, 0, len(cfg.Models))
		for _, id := range cleanList(cfg.Models) {
			id = resolveUpstreamModel(id, cfg.ModelPrefix)
			models = append(models, catalog.Model{ID: id, Name: id})
		}
		return catalog.Snapshot{Models: models, Source: "config"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cachePath := filepath.Join(filepath.Dir(pinStatePath()), "cline-channel-models-cache.json")
	return modelCatalog.Load(ctx, clineHTTPClient, cfg.BaseURL, cachePath, force)
}

// apiKey is retained for compatibility with the original facade. Discovery is public.
func loadModels(cfg PluginConfig, _ string) []pluginapi.ModelInfo {
	snapshot := subscriptionSnapshot(cfg, false)
	models := make([]pluginapi.ModelInfo, 0, len(snapshot.Models))
	for _, entry := range snapshot.Models {
		model := buildModelInfo(entry.ID, cfg)
		model.Description = entry.Description
		models = append(models, model)
	}
	return models
}

func modelGroup(id string) string {
	groups := []struct{ prefix, name string }{
		{"cline-pass/", "Cline Pass"},
		{"cline-free/", "Cline Free"},
		{"cline-cloud/", "Cline Cloud"},
	}
	for _, group := range groups {
		if strings.HasPrefix(id, group.prefix) {
			return group.name
		}
	}
	return "Cline"
}

func buildModelInfo(id string, cfg PluginConfig) pluginapi.ModelInfo {
	name := id[strings.LastIndex(id, "/")+1:]
	context, output := limitsFor(id)
	return pluginapi.ModelInfo{
		ID: qualifyModelID(id, cfg.ModelPrefix), Object: "model", OwnedBy: providerKey,
		Type: "chat", Name: id, DisplayName: modelGroup(id) + " · " + name,
		SupportedGenerationMethods: []string{"chat"},
		UserDefined:                len(cfg.Models) > 0,
		// 窗口规格来自 OpenRouter 目录的逐条映射（见 model_limits.go）。
		// 未收录的模型保持 0，交给客户端用默认值，不塞猜测数字。
		ContextLength:       context,
		MaxCompletionTokens: output,
		InputTokenLimit:     context,
		OutputTokenLimit:    output,
	}
}

// Native subscription names already carry the Cline namespace. Explicit catalog
// entries get cline/ so an OpenAI, WorkBuddy or other provider cannot claim them.
func qualifyModelID(id, prefix string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, "cline-pass/") || strings.HasPrefix(id, "cline-free/") ||
		strings.HasPrefix(id, "cline-cloud/") || strings.HasPrefix(id, "cline/") {
		return id
	}
	if prefix == "" {
		prefix = defaultModelPrefix
	}
	if strings.HasPrefix(id, prefix) {
		return id
	}
	return prefix + id
}

func modelAvailable(id string) bool {
	cfg := getConfig()
	id = resolveUpstreamModel(id, cfg.ModelPrefix)
	for _, entry := range subscriptionSnapshot(cfg, false).Models {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func modelsInfo() map[string]any { return modelInfoSnapshot(false) }

func modelInfoSnapshot(force bool) map[string]any {
	cfg := getConfig()
	snapshot := subscriptionSnapshot(cfg, force)
	ids := make([]string, 0, len(snapshot.Models))
	entries := make([]map[string]any, 0, len(snapshot.Models))
	for _, model := range snapshot.Models {
		info := buildModelInfo(model.ID, cfg)
		context, output := limitsFor(model.ID)
		ids = append(ids, info.ID)
		entries = append(entries, map[string]any{
			"id": info.ID, "upstreamId": model.ID, "name": info.DisplayName,
			"group": modelGroup(model.ID), "owned_by": providerKey, "description": model.Description,
			"context_length": context, "max_completion_tokens": output,
		})
	}
	return map[string]any{
		"ok": true, "count": len(ids), "models": ids, "entries": entries,
		"source": snapshot.Source, "fetchedAt": snapshot.FetchedAt,
		"stale": snapshot.Stale, "error": snapshot.Error,
		"sourceURL": cfg.BaseURL + catalog.RecommendedPath,
	}
}
