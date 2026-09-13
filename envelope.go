package main

import (
	"encoding/json"
	"time"

	"cline-channel/sdk/pluginabi"
	"cline-channel/sdk/pluginapi"
)

// 插件通过 JSON 信封与宿主交换数据：
// 成功 {"ok":true,"result":{...}}，失败 {"ok":false,"error":{...}}。
type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type identifierResponse struct {
	Identifier string `json:"identifier"`
}

func okEnvelope(v any) ([]byte, error) {
	raw, errMarshal := json.Marshal(v)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

// handleMethod 是插件的 RPC 入口，按宿主传入的方法名分发。
func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return handleRegistration(request)

	case pluginabi.MethodExecutorIdentifier:
		return okEnvelope(identifierResponse{Identifier: providerKey})
	case pluginabi.MethodExecutorExecute:
		return handleExecutorExecute(request)
	case pluginabi.MethodExecutorExecuteStream:
		return handleExecutorExecuteStream(request)
	case pluginabi.MethodExecutorCountTokens:
		return handleExecutorCountTokens(request)
	case pluginabi.MethodExecutorHTTPRequest:
		return okEnvelope(pluginapi.ExecutorHTTPResponse{
			StatusCode: 501,
			Headers:    map[string][]string{"content-type": {"application/json"}},
			Body:       []byte(`{"error":{"message":"http_request is not implemented by cline-channel"}}`),
		})

	case pluginabi.MethodModelRegister, pluginabi.MethodModelStatic, pluginabi.MethodModelForAuth:
		return handleModels(method, request)

	case pluginabi.MethodAuthIdentifier:
		return okEnvelope(identifierResponse{Identifier: providerKey})
	case pluginabi.MethodAuthParse:
		return handleAuthParse(request)
	case pluginabi.MethodAuthRefresh:
		return handleAuthRefresh(request)
	case pluginabi.MethodAuthLoginStart:
		// Cline 的 API Key 是长期凭据，没有可自动化的登录流程；
		// 返回一个立即过期的会话，并把引导信息放在 metadata 里。
		return okEnvelope(pluginapi.AuthLoginStartResponse{
			Provider:  providerKey,
			URL:       "https://app.cline.bot",
			State:     "manual",
			ExpiresAt: time.Now().Add(time.Minute),
			Metadata: map[string]any{
				"message": "cline-channel 不需要交互式登录：请在 Cline 账户设置里创建 API Key，然后写入 auths 目录或插件配置的 api_key。",
			},
		})
	case pluginabi.MethodAuthLoginPoll:
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "cline-channel 不需要交互式登录，请把 Cline API Key 写入 auth 文件或插件配置的 api_key",
		})

	case pluginabi.MethodManagementRegister:
		return handleManagementRegister(request)
	case pluginabi.MethodManagementHandle:
		return handleManagementHandle(request)

	case pluginabi.MethodPluginQuiesce, pluginabi.MethodPluginShutdown:
		return okEnvelope(map[string]any{})

	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}
