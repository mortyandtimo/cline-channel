package main

import (
	"context"
	"net/http"
	"time"

	"cline-channel/sdk/pluginapi"
)

const (
	pipelineDirect  = "direct"
	pipelinePlanner = "planner"
)

func handleProbe(req pluginapi.ManagementRequest) ([]byte, error) {
	body, err := readPinRequest(req)
	if err != nil {
		return managementFailure(http.StatusBadRequest, err.Error())
	}
	key := resolveManagementCredential()
	if key == "" {
		return managementFailure(http.StatusBadRequest, "尚未接入 Cline 账号")
	}
	result := probeModel(getConfig(), key, body.Model)
	rememberProbe(result)
	return managementJSON(http.StatusOK, result)
}

// Probe the Cline gateway itself. An OpenRouter directory entry is not evidence
// that the user's Cline account can use or pin that channel.
func probeModel(cfg PluginConfig, key, model string) probeResult {
	result := probeResult{Model: model, At: time.Now(), Channels: []string{}}
	for _, style := range []string{PinStyleVercel, PinStyleOpenRouter} {
		raw, status, err := probeRequest(cfg, key, model, style)
		result.Status = status
		if err != nil {
			result.Error = err.Error()
			continue
		}
		channels, pipeline, canonical := parseProbeResponse(raw)
		if len(channels) > 0 {
			result.OK, result.Channels, result.Pipeline, result.Canonical = true, channels, pipeline, canonical
			result.Style = PinStyleOpenRouter
			if pipeline == pipelinePlanner {
				result.Style = PinStyleVercel
			}
			result.Error = ""
			return result
		}
		result.Error = extractErrorMessage(raw)
		if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests {
			break
		}
	}
	if result.Error == "" {
		result.Error = "Cline 本次未返回可选渠道，请稍后重试；不能据此认定该模型无法切换"
	}
	result.Error = truncate(result.Error, 500)
	return result
}

func probeRequest(cfg PluginConfig, key, model, style string) ([]byte, int, error) {
	body := map[string]any{
		"model": model, "messages": []any{map[string]any{"role": "user", "content": "ping"}},
		"max_tokens": 16, "stream": false,
	}
	applyPin(body, &PinRule{Only: []string{"__cline_channel_probe__"}, Style: style}, style, PinModeStrict)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	req, err := buildClineRequest(ctx, cfg, key, newSessionID(), body)
	if err != nil {
		return nil, 0, err
	}
	resp, err := doCline(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := readAllLimited(resp.Body, 1<<20)
	return raw, resp.StatusCode, err
}
