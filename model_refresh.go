package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cline-channel/sdk/pluginabi"
	"cline-channel/sdk/pluginapi"
)

// CPA keeps an auth-bound model registry. Its supported auth-save callback
// re-registers that auth's models. Only a catalog revision changes; credential
// values, enabled state and other providers are preserved as raw JSON.
func refreshHostCatalog(ids []string) (int, error) {
	pinMu.RLock()
	dir := hostAuthDir
	pinMu.RUnlock()
	if dir == "" {
		return 0, errString("等待 CPA 提供账号目录")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	hash := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	revision := hex.EncodeToString(hash[:])
	updated := 0
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		var provider, kind, previous string
		var disabled bool
		_ = json.Unmarshal(fields["provider"], &provider)
		_ = json.Unmarshal(fields["type"], &kind)
		_ = json.Unmarshal(fields["disabled"], &disabled)
		_ = json.Unmarshal(fields["cline_model_revision"], &previous)
		if disabled || (provider != providerKey && kind != providerKey) || previous == revision {
			continue
		}
		fields["cline_model_revision"], _ = json.Marshal(revision)
		updatedJSON, err := json.Marshal(fields)
		if err != nil {
			return updated, err
		}
		request := pluginapi.HostAuthSaveRequest{Name: file.Name(), JSON: updatedJSON}
		response, ok := callHost(pluginabi.MethodHostAuthSave, mustJSON(request))
		if !ok {
			return updated, errString("CPA 账号模型刷新暂不可用")
		}
		var result pluginabi.Envelope
		if json.Unmarshal(response, &result) != nil || !result.OK {
			return updated, errString("CPA 未确认客户端模型目录刷新")
		}
		updated++
	}
	return updated, nil
}
