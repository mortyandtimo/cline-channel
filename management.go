package main

import (
	"encoding/json"
	"net/http"
	"path"
	"strings"

	"cline-channel/sdk/pluginapi"
)

type panelAction struct {
	name   string
	write  bool
	handle func(pluginapi.ManagementRequest) ([]byte, error)
}

// Routes are exact and share a dispatcher; unknown paths never render the panel.
var panelActions = []panelAction{
	{"status", false, handleStatus},
	{"models", false, handleModelList},
	{"refresh-models", true, handleModelRefresh},
	{"pins", false, handleListPins},
	{"observed", false, handleObserved},
	{"key", false, handleKeyInfo},
	{"key-save", true, handleKeySave},
	{"capabilities", false, handleCapabilities},
	{"probe", true, handleProbe},
	{"test", true, handleTest},
	{"pin", true, handlePin},
	{"unpin", true, handleUnpin},
}

func handleManagementRegister(_ []byte) ([]byte, error) {
	// No Menu entry: the standalone Cline panel is not duplicated in CPA's generic UI.
	resources := []pluginapi.ResourceRoute{{Path: "/panel", Menu: "模型与渠道", Description: "Cline Pass 模型、渠道探测与路由设置"}}
	for _, asset := range panelAssetNames {
		resources = append(resources, pluginapi.ResourceRoute{Path: "/" + asset})
	}
	routes := make([]pluginapi.ManagementRoute, 0, len(panelActions))
	for _, action := range panelActions {
		resources = append(resources, pluginapi.ResourceRoute{Path: "/" + action.name})
		method := http.MethodGet
		if action.write {
			method = http.MethodPost
		}
		routes = append(routes, pluginapi.ManagementRoute{Method: method, Path: "/cline-channel/" + action.name})
	}
	return okEnvelope(map[string]any{"resources": resources, "routes": routes})
}

func handleManagementHandle(request []byte) ([]byte, error) {
	var req pluginapi.ManagementRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	name := path.Base(req.Path)
	if name == "panel" {
		return panelResponse("text/html; charset=utf-8", []byte(renderPanelHTML()))
	}
	if content, mime, found := panelAsset(name); found {
		return panelResponse(mime, content)
	}
	for _, action := range panelActions {
		if action.name != name {
			continue
		}
		if action.write && !authorizedPanelAction(req) {
			return managementJSON(http.StatusForbidden, map[string]any{
				"ok": false, "error": "页面会话已过期，请刷新 Cline 面板后重试",
			})
		}
		return action.handle(req)
	}
	return managementJSON(http.StatusNotFound, map[string]any{"ok": false, "error": "unknown Cline route"})
}

func authorizedPanelAction(req pluginapi.ManagementRequest) bool {
	if req.Method == http.MethodPost && (strings.HasPrefix(req.Path, "/cline-channel/") ||
		strings.HasPrefix(req.Path, "/v0/management/cline-channel/")) {
		return true // Already authenticated by CPA.
	}
	return panelTokenMatches(req.Headers.Get("X-Cline-Panel-Token"))
}

func handleStatus(_ pluginapi.ManagementRequest) ([]byte, error) {
	cfg := getConfig()
	// 面板每 15 秒拉一次状态，这里顺带给出模糊化凭据，够用户分辨是哪把 key。
	credential := resolveManagementCredential()
	return managementJSON(http.StatusOK, map[string]any{
		"ok": true, "version": pluginVersion, "catalog": modelsInfo(),
		"pins": pinsSummary(), "observed": observedSummary(), "runtime": runtimeSummary(),
		"accountReady": credential != "", "accountMode": cfg.AccountMode,
		"accountMasked": maskKey(credential),
		"accountSource": credentialSource(cfg, credential),
		"authFiles":     clineAuthFileNames(),
		"baseURL":       cfg.BaseURL,
	})
}

func handleModelList(_ pluginapi.ManagementRequest) ([]byte, error) {
	return managementJSON(http.StatusOK, modelsInfo())
}

func handleModelRefresh(_ pluginapi.ManagementRequest) ([]byte, error) {
	info := modelInfoSnapshot(true)
	if stale, _ := info["stale"].(bool); !stale {
		ids, _ := info["models"].([]string)
		updated, err := refreshHostCatalog(ids)
		info["refreshedAccounts"] = updated
		info["registrySynced"] = err == nil
		if err != nil {
			info["registryError"] = err.Error()
		}
	}
	return managementJSON(http.StatusOK, info)
}

func handleObserved(_ pluginapi.ManagementRequest) ([]byte, error) {
	return managementJSON(http.StatusOK, map[string]any{"ok": true, "observed": observedSummary()})
}

func handleListPins(_ pluginapi.ManagementRequest) ([]byte, error) {
	return managementJSON(http.StatusOK, map[string]any{"ok": true, "pins": pinsSummary()})
}

func managementJSON(status int, payload any) ([]byte, error) {
	return okEnvelope(pluginapi.ManagementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": {"application/json; charset=utf-8"}, "Cache-Control": {"no-store"}},
		Body:       mustJSON(payload),
	})
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"ok":false,"error":"encode failed"}`)
	}
	return raw
}
