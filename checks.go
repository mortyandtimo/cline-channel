package main

import (
	"context"
	"net/http"
	"time"

	"cline-channel/sdk/pluginapi"
)

func handleTest(req pluginapi.ManagementRequest) ([]byte, error) {
	input, err := readPinRequest(req)
	if err != nil {
		return managementFailure(http.StatusBadRequest, err.Error())
	}
	key := resolveManagementCredential()
	if key == "" {
		return managementFailure(http.StatusBadRequest, "尚未接入 Cline 账号")
	}
	cfg := getConfig()
	rule, mode := effectivePin(input.Model, cfg), pinModeFor(input.Model, cfg)
	if len(input.Only) > 0 {
		if len(input.Only) != 1 {
			return managementFailure(http.StatusBadRequest, "单次测试只能指定一个渠道")
		}
		probe := cachedProbe(input.Model)
		rule = &PinRule{Only: input.Only, Style: firstNonEmpty(input.Style, probe.Style, PinStyleAuto)}
		mode = PinModeStrict
	}
	body := map[string]any{
		"model":      input.Model,
		"messages":   []any{map[string]any{"role": "user", "content": "Reply with OK."}},
		"max_tokens": 32, "stream": false,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	started := time.Now()
	resp, trace, err := executeRoutedCall(ctx, cfg, key, newSessionID(), body, rule, mode)
	record := requestRecord{Model: input.Model, Kind: "测试", Target: trace.Target, Account: trace.Account, Attempts: trace.Attempts}
	defer func() { record.MS = time.Since(started).Milliseconds(); recordRequest(record) }()
	if err != nil {
		record.Status, record.Error = 502, err.Error()
		return managementFailure(http.StatusOK, err.Error())
	}
	defer resp.Body.Close()
	record.Status = resp.StatusCode
	raw, err := readAllLimited(resp.Body, 1<<20)
	if err != nil {
		record.Status, record.Error = 502, err.Error()
		return managementFailure(http.StatusOK, err.Error())
	}
	if route := observeUpstreamRoute(raw, input.Model); route != nil {
		record.Provider = route.Provider
	}
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300 && extractErrorMessage(raw) == ""
	verified := ok && record.Target != "" && normalizeProvider(record.Target) == normalizeProvider(record.Provider)
	message := "请求成功"
	if !ok {
		message = firstNonEmpty(extractErrorMessage(raw), "Cline 返回 HTTP "+itoa(resp.StatusCode))
		record.Error = message
	} else if record.Target != "" && !verified {
		message = "请求成功，但本次未确认命中所选渠道"
		if record.Provider != "" {
			message = "实际命中 " + record.Provider + "，与所选渠道不同"
		}
	} else if verified {
		message = "已确认命中 " + record.Provider
	}
	return managementJSON(http.StatusOK, map[string]any{
		"ok": ok, "model": input.Model, "target": record.Target, "provider": record.Provider,
		"verified": verified, "status": record.Status, "ms": time.Since(started).Milliseconds(),
		"message": message, "error": record.Error,
	})
}
