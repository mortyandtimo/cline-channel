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
	streamID := strings.TrimSpace(req.StreamID)
	hostLog("info", pluginID+": execute_stream 入口 stream_id=["+streamID+"] model="+req.Model)
	body, model, sessionID, key, err := prepareUpstreamCall(req, true)
	if err != nil {
		hostLog("warn", pluginID+": execute_stream 准备失败: "+err.Error())
		return errorEnvelope("cline_request_error", err.Error()), nil
	}
	cfg := getConfig()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSeconds)*time.Second)
	started := time.Now()
	hostLog("info", pluginID+": execute_stream 发起上游 model="+model+" session="+sessionID)
	resp, trace, err := executeRoutedCall(ctx, cfg, key, sessionID, body, effectivePin(model, cfg), pinModeFor(model, cfg))
	if err != nil {
		cancel()
		hostLog("warn", pluginID+": execute_stream 上游失败: "+err.Error())
		recordRequest(requestRecord{Model: model, Kind: "请求", Stream: true, Target: trace.Target,
			Account: trace.Account, Attempts: trace.Attempts, Status: 502, Error: err.Error(),
			MS: time.Since(started).Milliseconds()})
		return errorEnvelope("cline_network_error", err.Error()), nil
	}
	hostLog("info", pluginID+": execute_stream 上游返回 status="+itoa(resp.StatusCode)+" ct="+resp.Header.Get("Content-Type"))

	if resp.StatusCode >= 400 || !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		defer cancel()
		defer resp.Body.Close()
		raw, _ := readAllLimited(resp.Body, 1<<20)
		message := firstNonEmpty(extractErrorMessage(raw), "Cline 未返回有效流式响应")
		status := resp.StatusCode
		if status < 400 {
			status = 502
		}
		hostLog("warn", pluginID+": execute_stream 非流式响应: "+message)
		recordRequest(requestRecord{Model: model, Kind: "请求", Stream: true, Target: trace.Target,
			Account: trace.Account, Attempts: trace.Attempts, Status: status, Error: message,
			MS: time.Since(started).Milliseconds()})
		return upstreamErrorEnvelope(status, raw), nil
	}

	header := http.Header{"Content-Type": {"text/event-stream"}}
	outputModel := qualifyModelID(model, cfg.ModelPrefix)

	// 推送式转发：宿主通过 stream_id 把这条流转发给下游客户端。
	//
	// 这里必须异步。host.stream.emit 是同步宿主回调，它要往下游连接写数据，
	// 而 CPA 要等本方法返回之后才把响应头交给客户端 —— 顺序同步执行就是死锁：
	//
	//   插件等 emit 返回 → emit 等下游连接可写 → 客户端在等响应头 → CPA 在等插件返回
	//
	// 所以先把响应头交回宿主，再把上游流交给后台 goroutine 边收边推；
	// context 与响应体的生命周期一并交给那个 goroutine，否则本方法返回时
	// 外层 defer 会取消请求、关闭连接，推送立刻断掉。
	if useStreamEmit(cfg) && streamID != "" {
		go func(upstream *http.Response, cancelUpstream context.CancelFunc, startedAt time.Time) {
			defer cancelUpstream()
			// 注意关的是 Body：http.Response 上另有一个 Close bool 字段表示 body 是否已关闭。
			defer upstream.Body.Close()
			record := requestRecord{Model: model, Kind: "请求", Stream: true,
				Target: trace.Target, Account: trace.Account, Attempts: trace.Attempts}
			defer func() {
				record.MS = time.Since(startedAt).Milliseconds()
				recordRequest(record)
			}()
			onRoute := func(route *observedRoute) {
				if route != nil {
					record.Provider = route.Provider
				}
			}
			emitted := 0
			errEmit := consumeSSE(upstream.Body, model, outputModel, onRoute, func(payload []byte) error {
				emitted++
				return emitHostStream(streamID, payload)
			})
			record.Status = upstream.StatusCode
			if errEmit != nil {
				hostLog("warn", pluginID+": execute_stream 推送中断 emitted="+itoa(emitted)+" err="+errEmit.Error())
				record.Error = errEmit.Error()
				closeHostStream(streamID, errEmit.Error())
				return
			}
			hostLog("info", pluginID+": execute_stream 推送完成 emitted="+itoa(emitted)+
				" 耗时ms="+itoa(int(time.Since(startedAt).Milliseconds())))
			closeHostStream(streamID, "")
		}(resp, cancel, started)
		return okEnvelope(rpcExecutorStreamResponse{Headers: header})
	}

	// 收集式转发：把上游整条流收完再一次性交回。首字节等于完整生成时间，
	// 但对端拿到的仍是标准流式响应。作为宿主推送不可用时的退路。
	defer cancel()
	defer resp.Body.Close()
	record := requestRecord{Model: model, Kind: "请求", Stream: true, Target: trace.Target,
		Account: trace.Account, Attempts: trace.Attempts}
	onRoute := func(route *observedRoute) {
		if route != nil {
			record.Provider = route.Provider
		}
	}
	hostLog("info", pluginID+": execute_stream 走收集模式")
	chunks, errCollect := collectModelStream(resp.Body, model, outputModel, onRoute)
	record.Status = resp.StatusCode
	hostLog("info", pluginID+": execute_stream 收集结束 chunks="+itoa(len(chunks))+" err="+errText(errCollect)+
		" 耗时ms="+itoa(int(time.Since(started).Milliseconds())))
	if errCollect != nil {
		record.Status, record.Error = 502, errCollect.Error()
		recordRequest(record)
		return errorEnvelope("cline_stream_error", errCollect.Error()), nil
	}
	recordRequest(record)
	return okEnvelope(rpcExecutorStreamResponse{Headers: header, Chunks: chunks})
}

// 流式转发模式。
const (
	// StreamModeCollect 收完整条上游流再一次性交回宿主。
	StreamModeCollect = "collect"
	// StreamModeEmit 用 host.stream.emit 边收边推，首字节即上游首字节。
	StreamModeEmit = "emit"
)

// useStreamEmit 决定是否使用宿主推送式流式转发。
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
