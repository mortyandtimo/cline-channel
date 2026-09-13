// Package catalog owns Cline Pass discovery independently of the CPA host.
package catalog

import "time"

type Model struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type Snapshot struct {
	Models    []Model   `json:"models"`
	Source    string    `json:"source"`
	FetchedAt time.Time `json:"fetchedAt"`
	Stale     bool      `json:"stale"`
	Error     string    `json:"error,omitempty"`
}

// Fallback is the exact default subscription list in cline-pass-switcher 1.2.0.
// It is used only when neither the official endpoint nor a successful cache exists.
func Fallback() []Model {
	ids := []string{
		"glm-5.3-flash", "kimi-k3", "deepseek-v4-flash", "deepseek-v4.1-flash",
		"qwen3.8-max", "minimax-m3", "glm-5.3", "glm-5.2", "deepseek-v4-pro",
		"mimo-v2.5-pro", "mimo-v2.5", "kimi-k2.6", "qwen3.7-plus",
		"kimi-k2.7-code", "qwen3.7-max",
	}
	models := make([]Model, 0, len(ids))
	for _, id := range ids {
		models = append(models, Model{ID: "cline-pass/" + id, Name: id})
	}
	return models
}
