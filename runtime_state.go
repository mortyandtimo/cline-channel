package main

import (
	"sync"
	"time"
)

type probeResult struct {
	OK        bool      `json:"ok"`
	Model     string    `json:"model"`
	Channels  []string  `json:"channels"`
	Pipeline  string    `json:"pipeline,omitempty"`
	Style     string    `json:"style,omitempty"`
	Canonical string    `json:"canonicalSlug,omitempty"`
	Status    int       `json:"status"`
	Error     string    `json:"error,omitempty"`
	At        time.Time `json:"at"`
}

type requestRecord struct {
	Model    string    `json:"model"`
	Kind     string    `json:"kind"`
	Target   string    `json:"target,omitempty"`
	Provider string    `json:"provider,omitempty"`
	Account  string    `json:"account,omitempty"`
	Status   int       `json:"status"`
	MS       int64     `json:"ms"`
	Stream   bool      `json:"stream"`
	Attempts int       `json:"attempts"`
	Error    string    `json:"error,omitempty"`
	At       time.Time `json:"at"`
}

var runtimeState = struct {
	sync.RWMutex
	probes  map[string]probeResult
	history []requestRecord
}{probes: make(map[string]probeResult)}

func cachedProbe(model string) probeResult {
	runtimeState.RLock()
	defer runtimeState.RUnlock()
	result := runtimeState.probes[model]
	result.Channels = append([]string(nil), result.Channels...)
	return result
}

func rememberProbe(result probeResult) {
	runtimeState.Lock()
	runtimeState.probes[result.Model] = result
	runtimeState.Unlock()
}

func recordRequest(record requestRecord) {
	record.At = time.Now()
	runtimeState.Lock()
	runtimeState.history = append([]requestRecord{record}, runtimeState.history...)
	if len(runtimeState.history) > 100 {
		runtimeState.history = runtimeState.history[:100]
	}
	runtimeState.Unlock()
}

func runtimeSummary() map[string]any {
	runtimeState.RLock()
	defer runtimeState.RUnlock()
	probes := make(map[string]probeResult, len(runtimeState.probes))
	for id, probe := range runtimeState.probes {
		probe.Channels = append([]string(nil), probe.Channels...)
		probes[id] = probe
	}
	history := append([]requestRecord{}, runtimeState.history...)
	return map[string]any{"probes": probes, "history": history}
}
