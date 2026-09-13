package main

import (
	"net/http"

	"cline-channel/sdk/pluginapi"
)

// handleCapabilities 用 CPA 原生 model-definitions 的形状暴露模型窗口能力。
//
// CPA 的 /v0/management/model-definitions/{channel} 只认内置 channel，
// 对插件 provider 一律返回 {"error":"unknown channel"}（cline、workbuddy 实测都一样）。
// 而中间层（cpa-stack 的 compat-proxy）正是靠那个接口给 /v1/models 补上
// context_length / max_completion_tokens —— 没有这两个值，DSH 这类 agent
// 就无法自动换算上下文预算，只能靠用户手填。
//
// 这里按同一形状补一份，用同样的 owner（owned_by=cline）和模型 id 作键，
// 让中间层可以按 provider 取用。
func handleCapabilities(_ pluginapi.ManagementRequest) ([]byte, error) {
	cfg := getConfig()
	snapshot := subscriptionSnapshot(cfg, false)
	models := make([]map[string]any, 0, len(snapshot.Models))
	for _, entry := range snapshot.Models {
		context, output := limitsFor(entry.ID)
		if context == 0 && output == 0 {
			// 规格未知的模型不出现在能力清单里，避免客户端拿到 0 当成"无限制"。
			continue
		}
		item := map[string]any{
			"id":       entry.ID,
			"object":   "model",
			"owned_by": providerKey,
		}
		if context > 0 {
			item["context_length"] = context
		}
		if output > 0 {
			item["max_completion_tokens"] = output
		}
		models = append(models, item)
	}
	return managementJSON(http.StatusOK, map[string]any{
		"channel": providerKey,
		"models":  models,
	})
}
