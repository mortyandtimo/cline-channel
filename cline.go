package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Cline 网关的客户端标识。
//
// 注意：实测表明 Cline 只校验 Content-Type 与 Authorization，
// 伪造 cline-cli 指纹（User-Agent / X-CLIENT-TYPE / X-CORE-VERSION 等）
// 反而会被判定为异常客户端并返回 401。
// 下面这些常量仅用于「看起来像 Cline 客户端」的默认值，默认不主动发送。
const (
	clineUserAgent       = "Cline/3.0.47"
	clineClientType      = "cline-cli"
	clineClientVersion   = "3.0.47"
	clineCoreVersion     = "0.0.66"
	clinePlatform        = "terminal"
	clinePlatformVersion = "3.0.47"
	clineReferer         = "https://cline.bot"
)

// newSessionID 生成一次会话标识。
// session_id 是 Cline 网关的**必需参数**：缺失时即使鉴权通过也会返回
// "empty response content"（HTTP 500）。
func newSessionID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return "sess_" + hex.EncodeToString(id[:])
}

// buildClineRequest 组装发往 Cline 的请求。
func buildClineRequest(ctx context.Context, cfg PluginConfig, apiKey, sessionID string, body map[string]any) (*http.Request, error) {
	body = copyMapValue(body)
	if str(body["session_id"]) == "" {
		body["session_id"] = sessionID
	}
	raw, errMarshal := json.Marshal(body)
	if errMarshal != nil {
		return nil, errMarshal
	}

	endpoint := strings.TrimRight(cfg.BaseURL, "/")
	// 允许用户把 base_url 写成带 /v1 的形式，避免拼成 /v1/v1
	if strings.HasSuffix(endpoint, "/v1") {
		endpoint += "/chat/completions"
	} else {
		endpoint += "/v1/chat/completions"
	}

	req, errNew := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if errNew != nil {
		return nil, errNew
	}

	// 只发网关真正需要的两个头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if stream, _ := body["stream"].(bool); stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}

	// 用户可以按需补头部（例如未来 Cline 变更校验规则时）
	for k, v := range cfg.ExtraHeaders {
		if strings.TrimSpace(k) != "" {
			req.Header.Set(k, v)
		}
	}

	return req, nil
}

// doCline 发送请求并返回响应，调用方负责关闭 Body。
func doCline(req *http.Request) (*http.Response, error) {
	return clineHTTPClient.Do(req)
}

// clineErrorPayload 把上游错误统一成 OpenAI 风格的错误体，
// 这样 CPA 的响应翻译层能原样处理。
func clineErrorPayload(status int, body []byte) []byte {
	message := extractErrorMessage(body)
	if message == "" {
		message = fmt.Sprintf("cline upstream returned %d: %s", status, truncate(string(body), 500))
	}
	out, errMarshal := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "upstream_error",
			"code":    status,
		},
	})
	if errMarshal != nil {
		return []byte(`{"error":{"message":"upstream error","type":"upstream_error"}}`)
	}
	return out
}

// extractErrorMessage 尽量从上游错误体里抠出可读信息。
func extractErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var probe map[string]any
	if errUnmarshal := json.Unmarshal(body, &probe); errUnmarshal != nil {
		text := strings.TrimSpace(string(body))
		if len(text) > 0 && len(text) < 500 {
			return text
		}
		return ""
	}

	switch e := probe["error"].(type) {
	case string:
		return e
	case map[string]any:
		if m, ok := e["message"].(string); ok && m != "" {
			return m
		}
	}
	if m, ok := probe["message"].(string); ok && m != "" {
		return m
	}
	if m, ok := probe["error_description"].(string); ok && m != "" {
		return m
	}
	return ""
}

// readAllLimited 读取响应体，防止异常上游返回超大内容撑爆内存。
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err == nil && int64(len(raw)) > limit {
		return nil, fmt.Errorf("upstream response exceeds %d bytes", limit)
	}
	return raw, err
}
