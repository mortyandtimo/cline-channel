package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"cline-channel/sdk/pluginabi"
	"cline-channel/sdk/pluginapi"
)

func consumeSSE(reader io.Reader, upstreamModel, outputModel string, onRoute func(*observedRoute), emit func([]byte) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	var lines []string
	flush := func() error {
		if len(lines) == 0 {
			return nil
		}
		raw := []byte(strings.Join(lines, "\n"))
		lines = nil
		if isSSEDone(raw) {
			return nil
		}
		if !json.Valid(raw) {
			return errString("Cline 返回了无效的流式数据")
		}
		if onRoute != nil {
			onRoute(observeUpstreamRoute(raw, upstreamModel))
		}
		if message := extractErrorMessage(raw); message != "" {
			return errString(message)
		}
		return emit(unwrapClineResponse(raw, outputModel))
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
		} else if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			// Cline normally sends a blank line after each event, but some
			// gateway paths omit it. Each data line is a complete JSON event.
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

func collectModelStream(reader io.Reader, upstreamModel, outputModel string, onRoute func(*observedRoute)) ([]pluginapi.ExecutorStreamChunk, error) {
	chunks := []pluginapi.ExecutorStreamChunk{}
	size := 0
	err := consumeSSE(reader, upstreamModel, outputModel, onRoute, func(payload []byte) error {
		size += len(payload)
		if size > maxResponseBytes {
			return errString("流式响应超过收集上限")
		}
		chunks = append(chunks, pluginapi.ExecutorStreamChunk{Payload: payload})
		return nil
	})
	return chunks, err
}

func collectStream(reader io.Reader) ([]pluginapi.ExecutorStreamChunk, error) {
	return collectModelStream(reader, "", "", nil)
}

func isSSEDone(raw []byte) bool { return strings.TrimSpace(string(raw)) == "[DONE]" }

func emitHostStream(streamID string, payload []byte) error {
	raw := mustJSON(map[string]any{"stream_id": streamID, "payload": payload})
	if _, ok := callHost(pluginabi.MethodHostStreamEmit, raw); !ok {
		return errString("host.stream.emit 调用失败")
	}
	return nil
}

func closeHostStream(streamID, message string) {
	payload := map[string]any{"stream_id": streamID}
	if message != "" {
		payload["error"] = message
	}
	_, _ = callHost(pluginabi.MethodHostStreamClose, mustJSON(payload))
}
