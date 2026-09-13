package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestNativeModelIdentityAndClineGroups(t *testing.T) {
	cfg := defaultConfig()
	cfg.normalize()
	for _, test := range []struct{ id, exposed, group string }{
		{"cline-pass/deepseek-v4-flash", "cline-pass/deepseek-v4-flash", "Cline Pass"},
		{"openai/shared", "cline/openai/shared", "Cline"},
		{"cline-free/shared", "cline-free/shared", "Cline Free"},
	} {
		info := buildModelInfo(test.id, cfg)
		if info.ID != test.exposed || info.OwnedBy != "cline" || !strings.HasPrefix(info.DisplayName, test.group+" · ") {
			t.Errorf("unisolated model: %+v", info)
		}
		if got := resolveUpstreamModel(info.ID, cfg.ModelPrefix); got != test.id {
			t.Errorf("upstream identity corrupted: %s", got)
		}
	}
	if got := resolveUpstreamModel("cline-pass/test", "cline/"); got != "cline-pass/test" {
		t.Fatal(got)
	}
}

func TestExclusionUsesAllowlistAndPreservesOtherOptions(t *testing.T) {
	body := map[string]any{
		"providerOptions": map[string]any{"anthropic": map[string]any{"cache": true}, "gateway": map[string]any{"user": "keep"}},
		"provider":        map[string]any{"data_collection": "deny", "only": []string{"obsolete"}},
	}
	rule := &PinRule{Order: []string{"novita"}, Exclude: []string{"blocked"}, Channels: []string{"novita", "blocked", "fallback"}, Sort: "cost"}
	applyPin(body, rule, PinStyleBoth, PinModePreferred)
	gw := nestedMap(body, "providerOptions", "gateway")
	provider := nestedMap(body, "provider")
	for _, part := range []map[string]any{gw, provider} {
		if !reflect.DeepEqual(part["only"], []string{"novita", "fallback"}) {
			t.Errorf("exclusion not enforced: %+v", part)
		}
		if _, ok := part["ignore"]; ok {
			t.Fatal("unsupported ignore was used")
		}
	}
	if gw["sort"] != "cost" || provider["sort"] != "price" {
		t.Fatal("sort dialect not translated")
	}
	if gw["user"] != "keep" || provider["data_collection"] != "deny" || nestedMap(body, "providerOptions", "anthropic")["cache"] != true {
		t.Fatal("unrelated provider options were lost")
	}
}

func TestOrderedRoutingAndExclusionGuards(t *testing.T) {
	rule := &PinRule{Only: []string{"third", "blocked", "first"}, Exclude: []string{"blocked"}}
	attempts, err := routingAttempts(rule, PinModeStrict)
	if err != nil || len(attempts) != 2 || attempts[0].target != "third" || attempts[1].target != "first" {
		t.Fatalf("order changed: %+v %v", attempts, err)
	}
	for _, attempt := range attempts {
		body := map[string]any{}
		applyPin(body, attempt.rule, PinStyleBoth, PinModeStrict)
		if !reflect.DeepEqual(nestedMap(body, "provider")["only"], []string{attempt.target}) {
			t.Fatal("strict pin allowed an unselected channel")
		}
	}
	for _, invalid := range []*PinRule{
		{Only: []string{"blocked"}, Exclude: []string{"blocked"}},
		{Exclude: []string{"unknown"}},
		{Exclude: []string{"only"}, Channels: []string{"only"}},
	} {
		if _, err := routingAttempts(invalid, PinModePreferred); err == nil {
			t.Errorf("unsafe rule accepted: %+v", invalid)
		}
	}
	if got := orderedChannels("third,first,third"); !reflect.DeepEqual(got, []string{"third", "first"}) {
		t.Fatalf("request order sorted: %v", got)
	}
}

func TestStreamingAndPlainCallsShareAccountFailover(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			var keys []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				keys = append(keys, r.Header.Get("Authorization"))
				var payload map[string]any
				if json.NewDecoder(r.Body).Decode(&payload) != nil || str(payload["session_id"]) == "" {
					t.Error("missing session")
				}
				if r.Header.Get("Authorization") == "Bearer first-key" {
					http.Error(w, "quota", 429)
					return
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("data: {\"choices\":[]}\n\n"))
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"choices":[]}`))
				}
			}))
			defer server.Close()
			cfg := defaultConfig()
			cfg.BaseURL = server.URL + "/v1"
			cfg.Accounts = []ClineAccount{{Name: "first", Key: "first-key"}, {Name: "second", Key: "second-key"}}
			resp, trace, err := executeRoutedCall(context.Background(), cfg, "first-key", "session", map[string]any{"model": "cline-pass/test", "stream": stream}, nil, PinModeStrict)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 || trace.Attempts != 2 || trace.Account != "second" || !reflect.DeepEqual(keys, []string{"Bearer first-key", "Bearer second-key"}) {
				t.Fatalf("failover incorrect: %+v %v", trace, keys)
			}
		})
	}
}

func TestChannelFailoverAndFinalHTTPStatus(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		only := nestedMap(payload, "providerOptions", "gateway")["only"].([]any)
		seen = append(seen, only[0].(string))
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":{"message":"limited"}}`))
	}))
	defer server.Close()
	cfg := defaultConfig()
	cfg.BaseURL = server.URL + "/v1"
	rule := &PinRule{Only: []string{"second", "first"}}
	resp, trace, err := executeRoutedCall(context.Background(), cfg, "account-key", "session", map[string]any{}, rule, PinModeStrict)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 429 || !strings.Contains(string(raw), "limited") || trace.Attempts != 2 || !reflect.DeepEqual(seen, []string{"second", "first"}) {
		t.Fatalf("final upstream error or channel order lost: %d %s %v", resp.StatusCode, raw, seen)
	}
}

func TestDownstreamAuthorizationNeverBecomesClineCredential(t *testing.T) {
	req := rpcExecutorRequest{}
	req.Headers = http.Header{"Authorization": {"Bearer downstream-only"}}
	if got := resolveAPIKey(req, defaultConfig()); got != "" {
		t.Fatalf("used downstream credential: %s", got)
	}
	req.StorageJSON = []byte(`{"api_key":"cline-account-key"}`)
	if got := resolveAPIKey(req, defaultConfig()); got != "cline-account-key" {
		t.Fatal("auth storage ignored")
	}
}
