package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"cline-channel/sdk/pluginapi"
)

type pinRequest struct {
	Model   string   `json:"model"`
	Only    []string `json:"only"`
	Order   []string `json:"order"`
	Exclude []string `json:"exclude"`
	Sort    string   `json:"sort"`
	Style   string   `json:"style"`
	Mode    string   `json:"mode"`
}

func readPinRequest(req pluginapi.ManagementRequest) (pinRequest, error) {
	var body pinRequest
	if len(req.Body) > 0 {
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return body, errString("请求体不是合法 JSON")
		}
	} else {
		body = pinRequest{
			Model: req.Query.Get("model"), Only: orderedChannels(req.Query.Get("only")),
			Order: orderedChannels(req.Query.Get("order")), Exclude: orderedChannels(req.Query.Get("exclude")),
			Sort: req.Query.Get("sort"), Style: req.Query.Get("style"), Mode: req.Query.Get("mode"),
		}
	}
	body.Model = resolveUpstreamModel(body.Model, getConfig().ModelPrefix)
	if body.Model == "" || !modelAvailable(body.Model) {
		return body, errString("请选择当前 Cline 模型列表中的模型")
	}
	return body, nil
}

func handlePin(req pluginapi.ManagementRequest) ([]byte, error) {
	body, err := readPinRequest(req)
	if err != nil {
		return managementFailure(http.StatusBadRequest, err.Error())
	}
	entry := &pinEntry{
		Only: cleanList(body.Only), Order: cleanList(body.Order), Exclude: cleanList(body.Exclude),
		Sort: strings.ToLower(strings.TrimSpace(body.Sort)), Style: strings.ToLower(strings.TrimSpace(body.Style)),
		Mode: firstNonEmpty(body.Mode, PinModeStrict),
	}
	if entry.Mode != PinModeStrict && entry.Mode != PinModePreferred {
		return managementFailure(http.StatusBadRequest, "不支持的渠道模式")
	}
	if entry.Mode == PinModePreferred {
		entry.Order = cleanList(append(entry.Order, entry.Only...))
		entry.Only = nil
	} else {
		entry.Only = cleanList(append(entry.Only, entry.Order...))
		entry.Order = nil
	}
	if entry.Sort != "" && entry.Sort != "cost" && entry.Sort != "ttft" && entry.Sort != "tps" {
		return managementFailure(http.StatusBadRequest, "不支持的渠道排序")
	}
	if !validPinStyle(entry.Style) {
		return managementFailure(http.StatusBadRequest, "不支持的渠道写法")
	}
	probe := cachedProbe(body.Model)
	entry.Channels = append([]string(nil), probe.Channels...)
	if len(entry.Channels) == 0 {
		if previous := listPins()[body.Model]; previous != nil {
			entry.Channels = append([]string(nil), previous.Channels...)
		}
	}
	if entry.Style == "" || entry.Style == PinStyleAuto {
		entry.Style = firstNonEmpty(probe.Style, PinStyleAuto)
	}
	if _, err := routingAttempts(entry.toRule(body.Model), entry.Mode); err != nil {
		return managementFailure(http.StatusBadRequest, err.Error())
	}
	if err := setPin(body.Model, entry); err != nil {
		return managementFailure(http.StatusInternalServerError, "保存失败："+err.Error())
	}
	return managementJSON(http.StatusOK, map[string]any{"ok": true, "model": body.Model, "pin": entry})
}

func handleUnpin(req pluginapi.ManagementRequest) ([]byte, error) {
	body, err := readPinRequest(req)
	if err != nil {
		return managementFailure(http.StatusBadRequest, err.Error())
	}
	if err := removePin(body.Model); err != nil {
		return managementFailure(http.StatusInternalServerError, err.Error())
	}
	return managementJSON(http.StatusOK, map[string]any{"ok": true, "model": body.Model})
}

func managementFailure(status int, message string) ([]byte, error) {
	return managementJSON(status, map[string]any{"ok": false, "error": message})
}

func orderedChannels(value string) []string {
	return cleanList(strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' || r == '，' }))
}

func validPinStyle(style string) bool {
	switch style {
	case "", PinStyleAuto, PinStyleVercel, PinStyleOpenRouter, PinStyleBoth, PinStyleNone:
		return true
	}
	return false
}
