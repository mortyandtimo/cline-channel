package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"cline-channel/sdk/pluginabi"
	"cline-channel/sdk/pluginapi"
)

// clineAuthDir 返回宿主上报的 auth 目录。
func clineAuthDir() string {
	pinMu.RLock()
	defer pinMu.RUnlock()
	return hostAuthDir
}

// clineAuthFileNames 列出 auth 目录里属于 cline 的凭据文件。
// CPA 只把 provider/type 命中 cline 的 JSON 分派给本插件，这里按同样规则筛选，
// 顺带避开插件自己写在同目录下的 pins / 模型缓存文件。
func clineAuthFileNames() []string {
	dir := clineAuthDir()
	if dir == "" {
		return nil
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, 1)
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		var provider, kind string
		_ = json.Unmarshal(fields["provider"], &provider)
		_ = json.Unmarshal(fields["type"], &kind)
		if provider == providerKey || kind == providerKey {
			names = append(names, file.Name())
		}
	}
	return names
}

// credentialSource 说明当前这把 key 是从哪一层取到的，面板据此提示用户改哪里。
func credentialSource(cfg PluginConfig, key string) string {
	switch {
	case key == "":
		return "none"
	case len(cfg.Accounts) > 0:
		return "accounts"
	case strings.TrimSpace(cfg.APIKey) != "" && key == strings.TrimSpace(cfg.APIKey):
		return "config"
	default:
		return "auth-file"
	}
}

// handleKeyInfo 返回当前 Cline 凭据的模糊化视图。
// 只给头尾各几个字符，够分辨是哪一把 key，明文不进浏览器。
func handleKeyInfo(_ pluginapi.ManagementRequest) ([]byte, error) {
	cfg := getConfig()
	key := resolveManagementCredential()
	return managementJSON(http.StatusOK, map[string]any{
		"ok":           true,
		"hasKey":       key != "",
		"masked":       maskKey(key),
		"length":       len(key),
		"source":       credentialSource(cfg, key),
		"authFiles":    clineAuthFileNames(),
		"accountReady": key != "",
	})
}

// handleKeySave 把新的 Cline API Key 写进 auth 文件。
//
// 走宿主的 auth-save 回调，只改 api_key，其它字段（label / note / 模型版本号等）
// 原样保留；同时清掉 CPA 可能留下的 unusable_key 标记，换 key 之后不该继续背着旧状态。
func handleKeySave(req pluginapi.ManagementRequest) ([]byte, error) {
	var body struct {
		APIKey string `json:"api_key"`
	}
	if len(req.Body) > 0 {
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return managementJSON(http.StatusBadRequest, map[string]any{"ok": false, "error": "请求体不是合法 JSON"})
		}
	}
	if strings.TrimSpace(body.APIKey) == "" {
		body.APIKey = req.Query.Get("api_key")
	}
	key := strings.TrimSpace(body.APIKey)
	if key == "" {
		return managementJSON(http.StatusBadRequest, map[string]any{"ok": false, "error": "api_key 不能为空"})
	}
	// 只要看起来像一把凭据就放行；真正的有效性由 Cline 网关判定。
	if len(key) < 12 || strings.ContainsAny(key, " \t\r\n\"'") {
		return managementJSON(http.StatusBadRequest, map[string]any{"ok": false, "error": "api_key 格式不正确（不应包含空白或引号）"})
	}
	if strings.Contains(key, "****") {
		return managementJSON(http.StatusBadRequest, map[string]any{"ok": false, "error": "这是掩码值，请填写完整 key"})
	}

	dir := clineAuthDir()
	names := clineAuthFileNames()
	if dir == "" || len(names) == 0 {
		return managementJSON(http.StatusBadRequest, map[string]any{
			"ok": false, "error": "没有找到 Cline 凭据文件，请先在 CPA 里接入 Cline 账号",
		})
	}

	updated := 0
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		fields["api_key"], _ = json.Marshal(key)
		fields["has_api_key"], _ = json.Marshal(true)
		delete(fields, "unusable_key")
		payload, err := json.Marshal(fields)
		if err != nil {
			continue
		}
		request := pluginapi.HostAuthSaveRequest{Name: name, JSON: payload}
		response, ok := callHost(pluginabi.MethodHostAuthSave, mustJSON(request))
		if !ok {
			return managementJSON(http.StatusBadGateway, map[string]any{"ok": false, "error": "CPA 账号保存暂不可用"})
		}
		var result pluginabi.Envelope
		if json.Unmarshal(response, &result) != nil || !result.OK {
			return managementJSON(http.StatusBadGateway, map[string]any{"ok": false, "error": "CPA 未确认凭据写入"})
		}
		updated++
	}
	if updated == 0 {
		return managementJSON(http.StatusInternalServerError, map[string]any{"ok": false, "error": "凭据写入失败"})
	}

	rememberCredential(key)
	hostLog("info", pluginID+": 已更新 Cline API Key "+maskKey(key)+"（"+itoa(updated)+" 个凭据文件）")
	return managementJSON(http.StatusOK, map[string]any{
		"ok": true, "masked": maskKey(key), "updated": updated,
	})
}
