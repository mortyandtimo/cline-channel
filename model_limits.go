package main

// cline 订阅模型的窗口规格（上下文长度 / 最大输出）。
//
// Cline 自己的 /v1/models 只返回 id/object/created/owned_by，不带任何能力元数据；
// 官方 API 文档也只给出请求参数表，没有窗口数值。但 Cline 的模型 ID 用的是
// OpenRouter 的 `provider/model-name` 命名约定（官方 Models 文档明确说明），
// 因此可以按同样的 slug 去 OpenRouter 公共目录取规格：响应里的 canonicalSlug
// 与 OpenRouter 目录条目一一对应，实测可对上。
//
// 数值取自 https://openrouter.ai/api/v1/models 的 context_length 与
// top_provider.max_completion_tokens，2026-09-13 逐条映射核对。
// 目录里查不到的模型（例如刚上线、OpenRouter 尚未收录的）这里不填，
// 宁可让客户端拿不到数值，也不塞一个猜测值误导 agent 的上下文预算。
var modelLimits = map[string]struct {
	Context int64
	Output  int64
}{
	// deepseek/deepseek-v4.1-flash
	"cline-pass/deepseek-v4.1-flash": {Context: 1048576, Output: 384000},
	// deepseek/deepseek-v4-flash
	"cline-pass/deepseek-v4-flash": {Context: 1048576, Output: 384000},
	// deepseek/deepseek-v4-pro
	"cline-pass/deepseek-v4-pro": {Context: 1048576, Output: 393216},
	// moonshotai/kimi-k3
	"cline-pass/kimi-k3": {Context: 1048576, Output: 943718},
	// moonshotai/kimi-k2.6
	"cline-pass/kimi-k2.6": {Context: 262144, Output: 235929},
	// moonshotai/kimi-k2.7-code
	"cline-pass/kimi-k2.7-code": {Context: 262144, Output: 235929},
	// z-ai/glm-5.2
	"cline-pass/glm-5.2": {Context: 1048576, Output: 182476},
	// z-ai/glm-5.3
	"cline-pass/glm-5.3": {Context: 1310720, Output: 131072},
	// z-ai/glm-5.3-flash
	"cline-pass/glm-5.3-flash": {Context: 1310720, Output: 131072},
	// qwen/qwen3.7-plus
	"cline-pass/qwen3.7-plus": {Context: 1000000, Output: 131072},
	// qwen/qwen3.7-max
	"cline-pass/qwen3.7-max": {Context: 1000000, Output: 131072},
	// qwen/qwen3.8-max-0902 —— OpenRouter 收录的是带日期后缀的条目，
	// Cline 侧名称为 qwen3.8-max。同系列 qwen3.8-flash / 27b / 2.4t-a95b 的
	// context 与 maxOut 与之完全一致，且与上一代 qwen3.7-max 相同，故采用该组数值。
	"cline-pass/qwen3.8-max": {Context: 1000000, Output: 131072},
	// minimax/minimax-m3
	"cline-pass/minimax-m3": {Context: 1048576, Output: 512000},
	// xiaomi/mimo-v2.5
	"cline-pass/mimo-v2.5": {Context: 1050000, Output: 131072},
	// xiaomi/mimo-v2.5-pro
	"cline-pass/mimo-v2.5-pro": {Context: 1050000, Output: 131072},
}

// limitsFor 返回模型的窗口规格；未收录时返回零值。
func limitsFor(id string) (int64, int64) {
	if entry, ok := modelLimits[id]; ok {
		return entry.Context, entry.Output
	}
	return 0, 0
}
