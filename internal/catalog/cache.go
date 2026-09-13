package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Cache struct {
	mu          sync.Mutex
	baseURL     string
	path        string
	nextRefresh time.Time
	snapshot    Snapshot
	ready       bool
}

type diskSnapshot struct {
	BaseURL string `json:"baseURL"`
	Snapshot
}

// Load replaces a successful list, including removals. A failed refresh keeps
// the last success and retries after a minute without adding any catalog models.
func (c *Cache) Load(ctx context.Context, client HTTPClient, baseURL, cachePath string, force bool) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.baseURL != baseURL || c.path != cachePath {
		c.baseURL, c.path = baseURL, cachePath
		c.ready, c.nextRefresh = false, time.Time{}
		c.snapshot = Snapshot{}
		if raw, err := os.ReadFile(cachePath); err == nil {
			var saved diskSnapshot
			if json.Unmarshal(raw, &saved) == nil && saved.BaseURL == baseURL && saved.Models != nil {
				c.snapshot = saved.Snapshot
				c.snapshot.Source, c.snapshot.Stale = "cache", true
			}
		}
	}
	if c.ready && !force && time.Now().Before(c.nextRefresh) {
		return clone(c.snapshot)
	}
	c.ready = true
	models, err := fetch(ctx, client, baseURL)
	if err != nil {
		if c.snapshot.Models == nil {
			c.snapshot = Snapshot{Models: Fallback(), Source: "switcher-snapshot"}
		}
		c.snapshot.Stale, c.snapshot.Error = true, err.Error()
		c.nextRefresh = time.Now().Add(time.Minute)
		return clone(c.snapshot)
	}
	c.snapshot = Snapshot{Models: models, Source: "cline-official", FetchedAt: time.Now()}
	c.nextRefresh = time.Now().Add(10 * time.Minute)
	if cachePath != "" {
		c.save(cachePath)
	}
	return clone(c.snapshot)
}

func (c *Cache) save(path string) {
	raw, err := json.Marshal(diskSnapshot{BaseURL: c.baseURL, Snapshot: c.snapshot})
	if err != nil {
		return
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		c.snapshot.Error = "模型已同步，但缓存目录无法写入"
		return
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, raw, 0o600); err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		c.snapshot.Error = "模型已同步，但缓存文件无法保存"
	}
}

func clone(snapshot Snapshot) Snapshot {
	models := make([]Model, len(snapshot.Models))
	copy(models, snapshot.Models)
	snapshot.Models = models
	return snapshot
}
