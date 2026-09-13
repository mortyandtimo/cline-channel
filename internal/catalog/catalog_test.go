package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestOnlyOfficialSubscriptionsAreRegistered(t *testing.T) {
	raw := []byte(`{"recommended":[{"id":"openai/gpt-extra"}],"free":[{"id":"cline-free/extra"}],"clineCloud":[{"id":"cline-cloud/extra"}],"clinePass":[{"id":"cline-pass/current"},"cline-pass/second",{"id":"cline-pass/current"},{"id":"openai/injected"},{"id":"cline-pass/invalid/path"}]}`)
	models, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(models); !reflect.DeepEqual(got, []string{"cline-pass/current", "cline-pass/second"}) {
		t.Fatalf("subscription scope leaked: %v", got)
	}
	wrapped := append([]byte(`{"data":`), append(raw, '}')...)
	models, err = Decode(wrapped)
	if err != nil || len(models) != 2 {
		t.Fatalf("nested envelope failed: %v, %v", models, err)
	}
}

func TestInvalidOrMissingSubscriptionListsAreNotSuccesses(t *testing.T) {
	for _, raw := range []string{
		`{"data":[{"id":"openai/directory"}]}`, `{"clinePass":null}`, `{"clinePass":{}}`,
		`{"clinePass":["openai/not-a-subscription"]}`, `{"clinePass":["cline-pass/stale"],"data":{}}`,
	} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Errorf("accepted invalid response: %s", raw)
		}
	}
	models, err := Decode([]byte(`{"clinePass":[]}`))
	if err != nil || models == nil || len(models) != 0 {
		t.Fatalf("authoritative empty list must be honored: %v %v", models, err)
	}
}

func TestCacheKeepsLastSuccessAndRemovesRetiredModels(t *testing.T) {
	var calls atomic.Int32
	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1"+RecommendedPath {
			t.Errorf("wrong discovery endpoint: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("public discovery sent account credentials")
		}
		switch mode.Load() {
		case 0:
			_, _ = w.Write([]byte(`{"clinePass":["cline-pass/old","cline-pass/current"]}`))
		case 1:
			http.Error(w, "offline", 503)
		case 2:
			_, _ = w.Write([]byte(`{"clinePass":["cline-pass/current"]}`))
		case 3:
			_, _ = w.Write([]byte(`{"clinePass":[]}`))
		}
	}))
	defer server.Close()
	cachePath := filepath.Join(t.TempDir(), "models.json")
	var cache Cache
	load := func(force bool) Snapshot {
		return cache.Load(context.Background(), server.Client(), server.URL+"/api/v1", cachePath, force)
	}
	first := load(false)
	if len(first.Models) != 2 || first.Stale || first.Source != "cline-official" {
		t.Fatalf("unexpected discovery: %+v", first)
	}
	first.Models[0].ID = "mutated-by-caller"
	if cached := load(false); cached.Models[0].ID != "cline-pass/old" || calls.Load() != 1 {
		t.Fatal("cache was mutable or ignored TTL")
	}
	mode.Store(1)
	stale := load(true)
	if !stale.Stale || len(stale.Models) != 2 || stale.FetchedAt.IsZero() {
		t.Fatalf("lost last success: %+v", stale)
	}
	var restarted Cache
	restored := restarted.Load(context.Background(), server.Client(), server.URL+"/api/v1", cachePath, false)
	if restored.Source != "cache" || len(restored.Models) != 2 {
		t.Fatalf("restart cache failed: %+v", restored)
	}
	mode.Store(2)
	current := load(true)
	if got := ids(current.Models); !reflect.DeepEqual(got, []string{"cline-pass/current"}) {
		t.Fatalf("retired models survived: %v", got)
	}
	mode.Store(3)
	if empty := load(true); empty.Stale || len(empty.Models) != 0 {
		t.Fatalf("official removals ignored: %+v", empty)
	}
	mode.Store(1)
	if empty := load(true); !empty.Stale || len(empty.Models) != 0 {
		t.Fatalf("empty success incorrectly replaced by fallback: %+v", empty)
	}
}

func TestFallbackCannotMixProvidersOrReuseAnotherGateway(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "offline", 503) }))
	defer server.Close()
	var cache Cache
	snapshot := cache.Load(context.Background(), server.Client(), server.URL, filepath.Join(t.TempDir(), "models.json"), false)
	if !snapshot.Stale || snapshot.Source != "switcher-snapshot" || len(snapshot.Models) != 15 {
		t.Fatalf("bad fallback: %+v", snapshot)
	}
	found := map[string]bool{}
	for _, model := range snapshot.Models {
		if !subscriptionID.MatchString(model.ID) {
			t.Errorf("foreign fallback: %s", model.ID)
		}
		found[model.ID] = true
	}
	if !found["cline-pass/deepseek-v4.1-flash"] {
		t.Error("switcher model missing from fallback")
	}
	other := cache.Load(context.Background(), server.Client(), server.URL+"/different", "", true)
	if other.Source != "switcher-snapshot" {
		t.Fatalf("gateway cache crossed scopes: %+v", other)
	}
}

func ids(models []Model) []string {
	result := make([]string, len(models))
	for i, model := range models {
		result[i] = model.ID
	}
	return result
}
