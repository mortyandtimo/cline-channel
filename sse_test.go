package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStreamUnwrapsKeepsToolCallsAndClineIdentity(t *testing.T) {
	stream := ": keepalive\r\n\r\nevent: message\r\ndata: {\"data\":{\"model\":\"z-ai/internal\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call-1\",\"function\":{\"name\":\"search\"}}]},\"index\":0}],\"provider\":\"Novita\"}}\r\n\r\ndata: [DONE]\r\n\r\n"
	var route *observedRoute
	chunks, err := collectModelStream(strings.NewReader(stream), "cline-pass/test", "cline-pass/test", func(value *observedRoute) { route = value })
	if err != nil || len(chunks) != 1 {
		t.Fatalf("bad SSE framing: %v %v", chunks, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(chunks[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "cline-pass/test" || nestedMap(payload, "choices", 0, "delta")["tool_calls"] == nil {
		t.Fatal("tool calls or model identity lost")
	}
	if route == nil || route.CanonicalSlug != "z-ai/internal" || route.Provider != "Novita" {
		t.Fatalf("actual upstream observation lost: %+v", route)
	}
}

func TestStreamErrorIsNotSilentSuccess(t *testing.T) {
	_, err := collectStream(strings.NewReader("data: {\"error\":{\"message\":\"limited\"}}\n\n"))
	if err == nil || !strings.Contains(err.Error(), "limited") {
		t.Fatalf("stream error lost: %v", err)
	}
}

func TestDirectAndPlannerProbeFormats(t *testing.T) {
	for _, test := range []struct {
		raw, pipeline string
		count         int
	}{
		{`{"error":{"metadata":{"available_providers":["novita","deepinfra"]}}}`, pipelineDirect, 2},
		{`{"error":"{\"message\":\"Available providers are: alibaba, azure, novita.\"}"}`, pipelinePlanner, 3},
	} {
		channels, pipeline, _ := parseProbeResponse([]byte(test.raw))
		if len(channels) != test.count || pipeline != test.pipeline {
			t.Fatalf("probe failed: %v %s", channels, pipeline)
		}
	}
	if channels, _, _ := parseProbeResponse([]byte(`{"error":"temporary failure"}`)); len(channels) != 0 {
		t.Fatal("invented channels")
	}
}

func TestPlainResponseAlsoRestoresExposedModel(t *testing.T) {
	for _, raw := range []string{`{"model":"internal","choices":[]}`, `{"data":{"model":"internal","choices":[]},"success":true}`} {
		var value map[string]any
		_ = json.Unmarshal(unwrapClineResponse([]byte(raw), "cline-pass/test"), &value)
		if value["model"] != "cline-pass/test" || value["data"] != nil {
			t.Fatalf("bad completion: %+v", value)
		}
	}
}
