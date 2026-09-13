package main

import "strings"

const (
	PinModeStrict    = "strict"
	PinModePreferred = "preferred"

	PinStyleAuto       = "auto"
	PinStyleVercel     = "vercel"
	PinStyleOpenRouter = "openrouter"
	PinStyleBoth       = "both"
	PinStyleNone       = "none"
)

// PinRule 把某个模型钉到指定的上游渠道。
//
// 这是 cline-pass-switcher 那套「钉住上游」能力的等价物，落在插件的转发层：
// Cline Pass 的模型在 Cline 网关之后会分流到两条管道，直连管道背后是 OpenRouter，
// 规划器管道背后是 Vercel AI Gateway，两条管道认的钉扎写法并不相同。
type PinRule struct {
	// Match 是模型名或通配模式，例如 "deepseek-v4-pro"、"deepseek-*"、"*"。
	Match string `yaml:"match"`

	// Only 限制只使用这些上游渠道；Order 指定优先顺序；Sort 按成本/延迟/吞吐排序。
	Only     []string `yaml:"only"`
	Order    []string `yaml:"order"`
	Exclude  []string `yaml:"exclude"`
	Sort     string   `yaml:"sort"`
	Channels []string `yaml:"channels"`

	// Style 覆盖插件级的注入写法。
	Style string `yaml:"style"`
}

func (r *PinRule) normalize() {
	r.Match = strings.TrimSpace(r.Match)
	r.Style = strings.ToLower(strings.TrimSpace(r.Style))
	r.Sort = strings.ToLower(strings.TrimSpace(r.Sort))
	r.Only = cleanList(r.Only)
	r.Order = cleanList(r.Order)
	r.Exclude = cleanList(r.Exclude)
	r.Channels = cleanList(r.Channels)
}

func (r *PinRule) empty() bool {
	if r == nil {
		return true
	}
	return len(r.Only) == 0 && len(r.Order) == 0 && len(r.Exclude) == 0 && r.Sort == ""
}

// matchPinRule 按优先级挑选规则：精确名 > 带前缀通配 > 全局通配；同级以靠前者为准。
func matchPinRule(rules []PinRule, model string) *PinRule {
	model = strings.TrimSpace(model)
	if model == "" || len(rules) == 0 {
		return nil
	}

	best := 0
	var bestRule *PinRule
	for i := range rules {
		rule := &rules[i]
		score := matchScore(rule.Match, model)
		if score > best {
			best = score
			bestRule = rule
		}
	}
	return bestRule
}

// matchScore 给匹配模式打分：
//
//	3 精确匹配  deepseek-v4-pro
//	2 带前缀通配 deepseek-*
//	1 全局通配  *
//	0 不匹配
func matchScore(pattern, model string) int {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || model == "" {
		return 0
	}
	if pattern == model {
		return 3
	}
	if !strings.Contains(pattern, "*") {
		return 0
	}
	if pattern == "*" {
		return 1
	}
	if wildcardMatch(pattern, model) {
		return 2
	}
	return 0
}

// wildcardMatch 支持 * 出现在任意位置。
func wildcardMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		if part == "" {
			continue
		}
		idx := strings.Index(s, part)
		if idx < 0 {
			return false
		}
		s = s[idx+len(part):]
	}
	tail := parts[len(parts)-1]
	if tail == "" {
		return true
	}
	return strings.HasSuffix(s, tail)
}

// applyPin 把渠道钉扎指令写进发往 Cline 的请求体，返回实际使用的风格与是否写入。
//
// mode 决定语义：
//   - strict    用 only 锁死，网关只能走这个渠道
//   - preferred 用 order 表达优先，该渠道不可用时网关可以自行回退
//
// 两种写法的由来（cline-pass-switcher 实测 + Vercel 官方文档）：
//   - 请求经中转网关转发时，顶层 provider 简写会被中转网关自己消费掉，
//     只有 providerOptions.gateway 这种它不认识的扩展字段才会原样透传给下游网关；
//   - 直连 Vercel AI Gateway 时两种写法都生效；
//   - 直连 OpenRouter 时只认顶层 provider。
//
// 因此拿不准链路时用 both —— 代价只是多一个会被忽略的字段。
func applyPin(body map[string]any, rule *PinRule, defaultStyle, mode string) (string, bool) {
	return injectRouting(body, rule, defaultStyle, mode)
}

// describePin 返回用于日志的一句话描述。
func describePin(rule *PinRule, style string) string {
	if rule.empty() {
		return ""
	}
	parts := []string{"style=" + style}
	if len(rule.Only) > 0 {
		parts = append(parts, "only="+strings.Join(rule.Only, ","))
	}
	if len(rule.Order) > 0 {
		parts = append(parts, "order="+strings.Join(rule.Order, ","))
	}
	if len(rule.Exclude) > 0 {
		parts = append(parts, "exclude="+strings.Join(rule.Exclude, ","))
	}
	if rule.Sort != "" {
		parts = append(parts, "sort="+rule.Sort)
	}
	return strings.Join(parts, " ")
}

func cleanList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
