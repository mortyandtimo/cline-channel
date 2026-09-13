package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func handleExecutorExecuteStream(request []byte) ([]byte, error) {
	var req rpcExecutorRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	hostLog("info", pluginID+": execute_stream 入口 stream_id=["+req.StreamID+"] model="+req.Model)
	body, model, sessionID, key, err := prepareUpstreamCall(req, true)
	if err != nil {
		hostLog("warn", pluginID+": execute_stream 准备失败: "+err.Error())
		return errorEnvelope("cline_request_error", err.Error()), nil
	}
	cfg := getConfig()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()
	started := time.Now()
	hostLog("info", pluginID+": execute_stream 发起上游 model="+model+" session="+sessionID)
	resp, trace, err := executeRoutedCall(ctx, cfg, key, sessionID, body, effectivePin(model, cfg), pinModeFor(model, cfg))
	record := requestRecord{Model: model, Kind: "请求", Stream: true, Target: trace.Target, Account: trace.Account, Attempts: trace.Attempts}
	defer func() { record.MS = time.Since(started).Milliseconds(); recordRequest(record) }()
	if err != nil {
		hostLog("warn", pluginID+": execute_stream 上游失败: "+err.Error())
		record.Status, record.Error = 502, err.Error()
		return errorEnvelope("cline_network_error", err.Error()), nil
	}
	defer resp.Body.Close()
	record.Status = resp.StatusCode
	hostLog("info", pluginID+": execute_stream 上游返回 status="+itoa(resp.StatusCode)+" ct="+resp.Header.Get("Content-Type"))
	if resp.StatusCode >= 400 || !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		raw, _ := readAllLimited(resp.Body, 1<<20)
		record.Error = firstNonEmpty(extractErrorMessage(raw), "Cline 未返回有效流式响应")
		if record.Status < 400 {
			record.Status = 502
		}
		hostLog("warn", pluginID+": execute_stream 非流式响应: "+record.Error)
		return upstreamErrorEnvelope(record.Status, raw), nil
	}
	header := http.Header{"Content-Type": {"text/event-stream"}}
	onRoute := func(route *observedRoute) {
		if route != nil {
			record.Provider = route.Provider
		}
	}
	outputModel := qualifyModelID(model, cfg.ModelPrefix)
	// host.stream.emit 是同步宿主回调：CPA 要等插件返回 execute_stream 之后才把响应头
	// 交给客户端，而 emit 又要往那条尚未就绪的连接写数据 —— 两者互等，形成死锁
	// （实测：stream_id 非空时请求永久挂起，上游却已经在 2 秒内正常返回 SSE）。
	// 因此统一走收集模式：把上游 SSE 收完再一次性交回，客户端拿到的仍是标准流式响应。
	if useStreamEmit(cfg) && strings.TrimSpace(req.StreamID) != "" {
		hostLog("info", pluginID+": execute_stream 走转发模式，开始逐个 emit")
		emitted := 0
		err = consumeSSE(resp.Body, model, outputModel, onRoute, func(payload []byte) error {
			emitted++
			if errEmit := emitHostStream(req.StreamID, payload); errEmit != nil {
				hostLog("warn", pluginID+": execute_stream emit 第 "+itoa(emitted)+" 块失败: "+errEmit.Error())
				return errEmit
			}
			return nil
		})
		hostLog("info", pluginID+": execute_stream 转发结束 emitted="+itoa(emitted)+" err="+errText(err)+" 耗时ms="+itoa(int(time.Since(started).Milliseconds())))
		if err != nil {
			record.Status, record.Error = 502, err.Error()
			closeHostStream(req.StreamID, err.Error())
			return errorEnvelope("cline_stream_error", err.Error()), nil
		}
		closeHostStream(req.StreamID, "")
		return okEnvelope(rpcExecutorStreamResponse{Headers: header})
	}
	hostLog("info", pluginID+": execute_stream 走收集模式")
	chunks, err := collectModelStream(resp.Body, model, outputModel, onRoute)
	hostLog("info", pluginID+": execute_stream 收集结束 chunks="+itoa(len(chunks))+" err="+errText(err)+" 耗时ms="+itoa(int(time.Since(started).Milliseconds())))
	if err != nil {
		record.Status, record.Error = 502, err.Error()
		return errorEnvelope("cline_stream_error", err.Error()), nil
	}
	return okEnvelope(rpcExecutorStreamResponse{Headers: header, Chunks: chunks})
}

// 流式转发模式。
const (
	// StreamModeCollect 收完整条上游流再一次性交回宿主。
	StreamModeCollect = "collect"
	// StreamModeEmit 用 host.stream.emit 边收边推。
	StreamModeEmit = "emit"
)

// useStreamEmit 决定是否使用宿主推送式流式转发。
//
// 默认关闭：当前 CPA 宿主下 host.stream.emit 会与响应头交付互等而永久阻塞。
// 保留开关是为了宿主修好后能一行配置切回真流式，无需重新编译插件。
func useStreamEmit(cfg PluginConfig) bool {
	return strings.EqualFold(strings.TrimSpace(cfg.StreamMode), StreamModeEmit)
}

// errText 把 error 渲染成日志用的短文本，nil 时给出明确标记。
func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
