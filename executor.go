package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"cline-channel/sdk/pluginapi"
)

const (
	maxResponseBytes = 32 << 20
	defaultMaxTokens = 128000
)

type rpcExecutorRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcExecutorStreamResponse struct {
	Headers http.Header                     `json:"headers,omitempty"`
	Chunks  []pluginapi.ExecutorStreamChunk `json:"chunks,omitempty"`
}

func prepareUpstreamCall(req rpcExecutorRequest, stream bool) (map[string]any, string, string, string, error) {
	cfg := getConfig()
	model := resolveUpstreamModel(req.Model, cfg.ModelPrefix)
	if model == "" || !modelAvailable(model) {
		return nil, "", "", "", errString("请求的模型不在当前 Cline 模型列表中")
	}
	var body map[string]any
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &body); err != nil {
			return nil, "", "", "", errString("请求体不是合法 JSON")
		}
	}
	if body == nil {
		body = map[string]any{}
	}
	sessionID := firstNonEmpty(str(body["session_id"]), newSessionID())
	body["model"], body["stream"], body["session_id"] = model, stream, sessionID
	if !stream {
		delete(body, "stream_options")
	}
	if _, ok := body["max_tokens"]; !ok {
		if budget, present := body["max_completion_tokens"]; present {
			body["max_tokens"] = budget
		} else {
			body["max_tokens"] = defaultMaxTokens
		}
	}
	delete(body, "max_completion_tokens")
	key := resolveAPIKey(req, cfg)
	if key == "" {
		return nil, "", "", "", errString("尚未接入 Cline 账号")
	}
	return body, model, sessionID, key, nil
}

func handleExecutorExecute(request []byte) ([]byte, error) {
	var req rpcExecutorRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	body, model, sessionID, key, err := prepareUpstreamCall(req, false)
	if err != nil {
		return errorEnvelope("cline_request_error", err.Error()), nil
	}
	cfg := getConfig()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()
	started := time.Now()
	resp, trace, err := executeRoutedCall(ctx, cfg, key, sessionID, body, effectivePin(model, cfg), pinModeFor(model, cfg))
	record := requestRecord{Model: model, Kind: "请求", Target: trace.Target, Account: trace.Account, Attempts: trace.Attempts}
	defer func() { record.MS = time.Since(started).Milliseconds(); recordRequest(record) }()
	if err != nil {
		record.Status, record.Error = 502, err.Error()
		return errorEnvelope("cline_network_error", err.Error()), nil
	}
	defer resp.Body.Close()
	record.Status = resp.StatusCode
	raw, err := readAllLimited(resp.Body, maxResponseBytes)
	if err != nil {
		record.Status, record.Error = 502, err.Error()
		return errorEnvelope("cline_read_error", err.Error()), nil
	}
	if route := observeUpstreamRoute(raw, model); route != nil {
		record.Provider = route.Provider
	}
	if resp.StatusCode >= 400 {
		record.Error = extractErrorMessage(raw)
		return upstreamErrorEnvelope(resp.StatusCode, raw), nil
	}
	if message := extractErrorMessage(raw); message != "" {
		record.Status, record.Error = 502, message
		return upstreamErrorEnvelope(502, raw), nil
	}
	raw = unwrapClineResponse(raw, qualifyModelID(model, cfg.ModelPrefix))
	return okEnvelope(pluginapi.ExecutorResponse{
		Payload: raw, Headers: http.Header{"Content-Type": {"application/json"}},
	})
}

func effectivePin(model string, cfg PluginConfig) *PinRule {
	rule := resolvePin(model, cfg)
	if rule == nil {
		return nil
	}
	copy := *rule
	probe := cachedProbe(model)
	if len(probe.Channels) > 0 {
		copy.Channels = probe.Channels
		copy.Only = keepKnownChannels(copy.Only, probe.Channels)
		copy.Order = keepKnownChannels(copy.Order, probe.Channels)
		if len(copy.Only) == 0 && len(copy.Order) == 0 && len(copy.Exclude) == 0 && copy.Sort == "" {
			// A cached pin can outlive Cline's channel list. Fail open to Cline's
			// default router instead of sending an impossible strict constraint.
			return nil
		}
	}
	if copy.Style == "" || copy.Style == PinStyleAuto {
		copy.Style = firstNonEmpty(probe.Style, cfg.PinStyle)
	}
	return &copy
}

func keepKnownChannels(selected, known []string) []string {
	if len(selected) == 0 || len(known) == 0 {
		return selected
	}
	result := make([]string, 0, len(selected))
	for _, value := range selected {
		for _, candidate := range known {
			if normalizeProvider(value) == normalizeProvider(candidate) {
				result = append(result, candidate)
				break
			}
		}
	}
	return cleanList(result)
}

func unwrapClineResponse(raw []byte, requestedModel string) []byte {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return raw
	}
	if inner, ok := root["data"].(map[string]any); ok && inner["choices"] != nil {
		root = inner
	}
	if root["choices"] == nil {
		return raw
	}
	if requestedModel != "" {
		root["model"] = requestedModel
	}
	return mustJSON(root)
}

func handleExecutorCountTokens(request []byte) ([]byte, error) {
	var req rpcExecutorRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	return okEnvelope(pluginapi.ExecutorResponse{
		Payload: mustJSON(map[string]any{"total_tokens": len(req.Payload)/4 + 1}),
		Headers: http.Header{"Content-Type": {"application/json"}},
	})
}
