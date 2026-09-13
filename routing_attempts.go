package main

import (
	"context"
	"net/http"
	"time"
)

type routingAttempt struct {
	rule   *PinRule
	target string
}

// defaultAttempts 是没有钉扎时的上游尝试次数。
//
// Cline 网关自己会在渠道之间回退，但它偶尔会整体返回空响应
// （HTTP 500 "empty response content"，实测为瞬时故障）。
// 只发一次的话，这种瞬时抖动会直接变成客户端可见的 500，并被 CPA 记成
// 一次凭据失败 —— 累积几次就把 cline 账号熔断成 auth_unavailable，
// 于是所有 cline-pass 模型一起 503。多给两次尝试，把抖动吸收在插件内部。
const defaultAttempts = 3

func routingAttempts(rule *PinRule, mode string) ([]routingAttempt, error) {
	if rule.empty() {
		attempts := make([]routingAttempt, 0, defaultAttempts)
		for i := 0; i < defaultAttempts; i++ {
			attempts = append(attempts, routingAttempt{})
		}
		return attempts, nil
	}
	selected := cleanList(append(append([]string{}, rule.Only...), rule.Order...))
	wanted := withoutChannels(selected, rule.Exclude)
	if len(selected) > 0 && len(wanted) == 0 {
		return nil, errString("所选渠道已全部被排除，请调整选择")
	}
	if len(rule.Exclude) > 0 && (mode == PinModePreferred || len(wanted) == 0) {
		if len(rule.Channels) == 0 {
			return nil, errString("请先探测渠道，再保存排除规则")
		}
		if len(withoutChannels(rule.Channels, rule.Exclude)) == 0 {
			return nil, errString("不能排除所有可用渠道")
		}
	}
	if len(wanted) == 0 {
		return []routingAttempt{{rule: rule}}, nil
	}
	attempts := make([]routingAttempt, 0, len(wanted))
	for _, channel := range wanted {
		copy := *rule
		copy.Only, copy.Order = nil, nil
		if mode == PinModePreferred {
			copy.Order = append([]string{channel}, withoutChannels(wanted, []string{channel})...)
		} else {
			copy.Only = []string{channel}
		}
		attempts = append(attempts, routingAttempt{rule: &copy, target: channel})
	}
	return attempts, nil
}

type callTrace struct {
	Target   string
	Account  string
	Attempts int
	MS       int64
}

// A stream may retry before returning its response. Once SSE delivery begins,
// the caller never replays it, so tool calls and content cannot be duplicated.
func executeRoutedCall(ctx context.Context, cfg PluginConfig, firstKey, sessionID string,
	body map[string]any, rule *PinRule, mode string) (*http.Response, callTrace, error) {
	trace := callTrace{}
	started := time.Now()
	attempts, err := routingAttempts(rule, mode)
	if err != nil {
		return nil, trace, err
	}
	keyConfig := cfg
	keyConfig.AccountMode = "single"
	keys := cleanList(append([]string{firstKey}, resolveConfiguredKeys(keyConfig)...))
	if len(keys) == 0 {
		return nil, trace, errString("尚未接入 Cline 账号")
	}
	var lastErr error
	for attemptIndex, attempt := range attempts {
		payload := copyMapValue(body)
		applyPin(payload, attempt.rule, cfg.PinStyle, mode)
		for keyIndex, key := range keys {
			if err := ctx.Err(); err != nil {
				return nil, trace, err
			}
			trace.Attempts++
			trace.Target, trace.Account = attempt.target, accountLabel(cfg, key)
			req, err := buildClineRequest(ctx, cfg, key, sessionID, payload)
			if err != nil {
				return nil, trace, err
			}
			resp, err := doCline(req)
			trace.MS = time.Since(started).Milliseconds()
			if err != nil {
				lastErr = err
				continue
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				rememberCredential(key)
				return resp, trace, nil
			}
			// 只有可重试的错误才值得换渠道再试；4xx 这类客户端错误重试没有意义。
			retryable := retryCredential(resp.StatusCode)
			moreKeys := retryable && keyIndex+1 < len(keys)
			moreChannels := retryable && attemptIndex+1 < len(attempts)
			if !moreKeys && !moreChannels {
				return resp, trace, nil // Preserve the real final HTTP status and body.
			}
			resp.Body.Close()
			if !moreKeys {
				break
			}
		}
	}
	return nil, trace, lastErr
}

func retryCredential(status int) bool {
	return status == 401 || status == 403 || status == 408 || status == 429 || status >= 500
}

func accountLabel(cfg PluginConfig, key string) string {
	for _, account := range cfg.Accounts {
		if account.Key == key {
			return account.Name
		}
	}
	return "Cline"
}
