package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	"cline-channel/sdk/pluginapi"
)

// resolveConfiguredKeys 返回按配置顺序排列的可用凭据。保留 APIKey 兼容旧配置，
// 轮询模式下每个请求从账号池轮换起点开始尝试，失败时可无缝切换到下一账号。
func resolveConfiguredKeys(cfg PluginConfig) []string {
	keys := make([]string, 0, len(cfg.Accounts)+1)
	seen := map[string]bool{}
	add := func(k string) {
		k = strings.TrimSpace(k)
		if k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	if cfg.AccountMode == "roundrobin" && len(cfg.Accounts) > 0 {
		start := int(atomic.AddUint64(&accountCounter, 1)-1) % len(cfg.Accounts)
		for i := 0; i < len(cfg.Accounts); i++ {
			a := cfg.Accounts[(start+i)%len(cfg.Accounts)]
			if a.Enabled == nil || *a.Enabled {
				add(a.Key)
			}
		}
	} else if len(cfg.Accounts) > 0 {
		idx := cfg.ActiveAccount
		if idx >= 0 && idx < len(cfg.Accounts) {
			a := cfg.Accounts[idx]
			if a.Enabled == nil || *a.Enabled {
				add(a.Key)
			}
		}
		for _, a := range cfg.Accounts {
			if a.Enabled == nil || *a.Enabled {
				add(a.Key)
			}
		}
	}
	add(cfg.APIKey)
	return keys
}

// handleAuthParse 判断一份 auth 文件是否属于本插件，并解析成宿主可用的记录。
//
// 与 workbuddy 等官方插件的 auth 文件结构保持一致：
//
//	{
//	  "provider": "cline",
//	  "type": "cline",
//	  "disabled": false,
//	  "label": "Cline Pass",
//	  "api_key": "sk_...",
//	  "account": {...}   // 可选，插件自用
//	}
func handleAuthParse(request []byte) ([]byte, error) {
	var req pluginapi.AuthParseRequest
	if len(request) > 0 {
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return nil, errUnmarshal
		}
	}

	var raw map[string]any
	if len(req.RawJSON) > 0 {
		_ = json.Unmarshal(req.RawJSON, &raw)
	}

	// 只认领属于本插件的 auth：provider / type 命中，或文件名带 cline
	provider := strings.ToLower(strings.TrimSpace(pickString(raw, "provider")))
	kind := strings.ToLower(strings.TrimSpace(pickString(raw, "type")))
	fileName := strings.ToLower(strings.TrimSpace(req.FileName))

	if provider != providerKey && kind != providerKey && !strings.Contains(fileName, providerKey) {
		return okEnvelope(pluginapi.AuthParseResponse{Handled: false})
	}

	apiKey := apiKeyFromAuth(req.RawJSON)
	if apiKey == "" && provider != providerKey && kind != providerKey {
		// 文件名像 cline，但里面没有可用凭据，交给别的插件处理
		return okEnvelope(pluginapi.AuthParseResponse{Handled: false})
	}

	// 缓存凭据：管理面板的渠道探测没有 auth 上下文，
	// 靠这里在启动阶段就把 key 存下来，否则探测会报「还没有可用的 Cline 凭据」。
	if disabled, _ := raw["disabled"].(bool); !disabled {
		rememberCredential(apiKey)
	}

	label := firstNonEmpty(
		pickString(raw, "label", "name"),
		"cline",
	)
	disabled, _ := raw["disabled"].(bool)

	auth := pluginapi.AuthData{
		Provider:    providerKey,
		ID:          firstNonEmpty(pickString(raw, "id"), providerKey+":"+req.FileName),
		FileName:    req.FileName,
		Label:       label,
		Disabled:    disabled,
		StorageJSON: append([]byte(nil), req.RawJSON...),
		// 注意：这里绝不能放 api_key（哪怕是掩码）。
		// CPA 会把插件返回的 AuthData 合并写回 auth 文件，
		// 掩码值一旦落盘，后续请求就会拿着 "sk_xxx****yyy" 去认证，直接 401。
		//
		// 同理也不写 unusable_key 之类的凭据状态字段：那是 CPA 自己的状态机在管，
		// 插件代它下结论只会互相打架。这里只留插件自己的信息。
		Metadata: map[string]any{
			"type":        providerKey,
			"has_api_key": apiKey != "",
		},
		Attributes: map[string]string{
			"provider": providerKey,
		},
	}

	if key := pickString(raw, "prefix"); key != "" {
		auth.Prefix = key
	}

	return okEnvelope(pluginapi.AuthParseResponse{Handled: true, Auth: auth})
}

// handleAuthRefresh 直接回显凭据。
//
// Cline 的 API Key（sk_ 开头）是长期有效的，没有 refresh 流程；
// 这里返回原样数据只是为了让宿主的刷新调度不会报错。
func handleAuthRefresh(request []byte) ([]byte, error) {
	var req pluginapi.AuthRefreshRequest
	if len(request) > 0 {
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return nil, errUnmarshal
		}
	}

	storage := req.StorageJSON
	var raw map[string]any
	if len(storage) > 0 {
		_ = json.Unmarshal(storage, &raw)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	// 确保 provider 字段存在，否则宿主下一次加载不会再把它分派给本插件
	if pickString(raw, "provider") == "" {
		raw["provider"] = providerKey
	}
	if pickString(raw, "type") == "" {
		raw["type"] = providerKey
	}
	normalized, errMarshal := json.Marshal(raw)
	if errMarshal != nil {
		normalized = storage
	}

	apiKey := apiKeyFromAuth(normalized)

	return okEnvelope(pluginapi.AuthRefreshResponse{
		Auth: pluginapi.AuthData{
			Provider:    providerKey,
			ID:          firstNonEmpty(req.AuthID, providerKey),
			Label:       firstNonEmpty(pickString(raw, "label", "name"), "cline"),
			StorageJSON: normalized,
			// 同上：不放任何密钥值，避免被合并写回文件后污染真实凭据
			Metadata: map[string]any{
				"type":        providerKey,
				"has_api_key": apiKey != "",
			},
			Attributes: map[string]string{"provider": providerKey},
		},
		// 没有真正需要刷新的东西，给一个较长的下次刷新时间避免无谓调度
		NextRefreshAfter: time.Now().Add(24 * time.Hour),
	})
}

// maskKey 只保留头尾，用于展示。
func maskKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 10 {
		return "****"
	}
	return key[:6] + "****" + key[len(key)-4:]
}
