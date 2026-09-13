package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"testing"

	"cline-channel/sdk/pluginabi"
	"cline-channel/sdk/pluginapi"
)

func configureTestPlugin(t *testing.T) PluginConfig {
	t.Helper()
	previous := getConfig()
	t.Cleanup(func() { setConfig(previous) })
	cfg := defaultConfig()
	cfg.Models = []string{"cline-pass/test"}
	cfg.PinStateFile = filepath.Join(t.TempDir(), "pins.json")
	setConfig(cfg)
	return cfg
}

func decodeManagement(t *testing.T, raw []byte, err error) pluginapi.ManagementResponse {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var envelope pluginabi.Envelope
	if json.Unmarshal(raw, &envelope) != nil || !envelope.OK {
		t.Fatalf("bad envelope: %s", raw)
	}
	var response pluginapi.ManagementResponse
	if err := json.Unmarshal(envelope.Result, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestPanelMutationsRequireSessionAndUnknownModelsStayOut(t *testing.T) {
	configureTestPlugin(t)
	req := pluginapi.ManagementRequest{Method: "GET", Path: "/v0/resource/plugins/cline-channel/pin", Query: url.Values{"model": {"cline-pass/test"}, "only": {"third,first"}}}
	raw, err := handleManagementHandle(mustJSON(req))
	if response := decodeManagement(t, raw, err); response.StatusCode != 403 {
		t.Fatalf("unprotected resource mutation: %d", response.StatusCode)
	}
	req.Headers = http.Header{"X-Cline-Panel-Token": {panelSessionToken}}
	raw, err = handleManagementHandle(mustJSON(req))
	if response := decodeManagement(t, raw, err); response.StatusCode != 200 {
		t.Fatalf("save failed: %s", response.Body)
	}
	if pin := listPins()["cline-pass/test"]; pin == nil || !reflect.DeepEqual(pin.Only, []string{"third", "first"}) {
		t.Fatalf("channel order was lost: %+v", pin)
	}
	req.Query.Set("model", "openai/not-authorized")
	raw, err = handleManagementHandle(mustJSON(req))
	if response := decodeManagement(t, raw, err); response.StatusCode != 400 {
		t.Fatalf("foreign model accepted: %s", response.Body)
	}
}

func TestPinPersistenceRetainsExclusionsAcrossRestart(t *testing.T) {
	cfg := configureTestPlugin(t)
	entry := &pinEntry{Order: []string{"novita"}, Exclude: []string{"blocked"}, Channels: []string{"novita", "blocked", "fallback"}, Mode: PinModePreferred}
	if err := setPin("cline-pass/test", entry); err != nil {
		t.Fatal(err)
	}
	pinMu.Lock()
	pinsLoaded = false
	pins = &pinState{Pins: map[string]*pinEntry{}}
	pinMu.Unlock()
	rule := resolvePin("cline-pass/test", cfg)
	if rule == nil || len(rule.Channels) != 3 || len(rule.Exclude) != 1 {
		t.Fatalf("exclusion knowledge lost on reload: %+v", rule)
	}
	body := map[string]any{}
	applyPin(body, rule, PinStyleBoth, PinModePreferred)
	if !reflect.DeepEqual(nestedMap(body, "providerOptions", "gateway")["only"], []string{"novita", "fallback"}) {
		t.Fatal("restart disabled exclusions")
	}
	copy := listPins()["cline-pass/test"]
	copy.Order[0] = "mutated"
	if listPins()["cline-pass/test"].Order[0] != "novita" {
		t.Fatal("panel read mutated active routing")
	}
}

func TestAllHostRegistrationPathsUseSameScope(t *testing.T) {
	configureTestPlugin(t)
	for _, method := range []string{pluginabi.MethodModelRegister, pluginabi.MethodModelStatic, pluginabi.MethodModelForAuth} {
		raw, err := handleModels(method, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var envelope pluginabi.Envelope
		_ = json.Unmarshal(raw, &envelope)
		var result pluginapi.ModelRegistrationResponse
		_ = json.Unmarshal(envelope.Result, &result)
		if result.Provider != "cline" || len(result.Models) != 1 || result.Models[0].ID != "cline-pass/test" {
			t.Fatalf("inconsistent %s: %s", method, raw)
		}
	}
}
