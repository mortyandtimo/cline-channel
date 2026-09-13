package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cline-channel/sdk/pluginapi"
)

// pinState 是面板里为每个模型选择的钉扎配置，落盘持久化。
//
// 插件的配置来自 config.yaml（只读），而「钉住哪个上游」是需要随时调整的选择，
// 因此单独存一份可写状态文件，路径优先取宿主上报的 auth-dir。
type pinState struct {
	Version   int                  `json:"version"`
	UpdatedAt time.Time            `json:"updatedAt"`
	Pins      map[string]*pinEntry `json:"pins"`
}

// pinEntry 是单个模型的钉扎选择。
type pinEntry struct {
	Only      []string  `json:"only,omitempty"`
	Order     []string  `json:"order,omitempty"`
	Exclude   []string  `json:"exclude,omitempty"`
	Channels  []string  `json:"channels,omitempty"`
	Sort      string    `json:"sort,omitempty"`
	Style     string    `json:"style,omitempty"`
	Mode      string    `json:"mode,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (e *pinEntry) empty() bool {
	return e == nil || (len(e.Only) == 0 && len(e.Order) == 0 && len(e.Exclude) == 0 && e.Sort == "")
}

// toRule 把选择转成运行时使用的规则。
func (e *pinEntry) toRule(model string) *PinRule {
	if e.empty() {
		return nil
	}
	rule := &PinRule{
		Match:    model,
		Only:     append([]string(nil), e.Only...),
		Order:    append([]string(nil), e.Order...),
		Exclude:  append([]string(nil), e.Exclude...),
		Channels: append([]string(nil), e.Channels...),
		Sort:     e.Sort,
		Style:    e.Style,
	}
	rule.normalize()
	return rule
}

var (
	pinMu          sync.RWMutex
	pins           = &pinState{Version: 1, Pins: map[string]*pinEntry{}}
	pinsLoaded     bool
	pinsModTime    time.Time
	pinsLoadedPath string
	hostAuthDir    string
)

// captureHostConfig 记录宿主上报的目录信息。
// model.static 在启动阶段一定会被调用，因此这里是拿 auth-dir 最稳的时机。
func captureHostConfig(host pluginapi.HostConfigSummary) {
	if strings.TrimSpace(host.AuthDir) == "" {
		return
	}
	pinMu.Lock()
	changed := hostAuthDir != host.AuthDir
	hostAuthDir = host.AuthDir
	pinMu.Unlock()
	if changed {
		hostLog("info", pluginID+": 已捕获 auth-dir = "+host.AuthDir)
	}
}

// pinStatePath 决定钉扎状态文件的位置。
func pinStatePath() string {
	if p := strings.TrimSpace(getConfig().PinStateFile); p != "" {
		return p
	}
	pinMu.RLock()
	dir := hostAuthDir
	pinMu.RUnlock()
	if dir != "" {
		return filepath.Join(dir, "cline-channel-pins.json")
	}
	// 兜底：容器内 CPA 的默认 auth-dir
	return "/root/.cli-proxy-api/cline-channel-pins.json"
}

// loadPins 读入状态文件。
//
// 首次调用会真正读盘；之后每次调用只比对文件的修改时间，
// 这样用户在宿主机上直接编辑状态文件也能被自动识别并重载，
// 不必重启 CPA。stat 比 read 便宜得多，放在请求路径上可以接受。
func loadPins() {
	pinMu.Lock()
	defer pinMu.Unlock()

	path := pinStatePathLocked()
	if pinsLoadedPath != path {
		pinsLoadedPath = path
		pinsLoaded = false
		pinsModTime = time.Time{}
		pins = &pinState{Version: 1, Pins: map[string]*pinEntry{}}
	}

	var modTime time.Time
	if info, errStat := os.Stat(path); errStat == nil {
		modTime = info.ModTime()
	}

	if pinsLoaded && (modTime.IsZero() || !modTime.After(pinsModTime)) {
		return
	}

	pinsLoaded = true
	pinsModTime = modTime

	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		return
	}
	var loaded pinState
	if errUnmarshal := json.Unmarshal(raw, &loaded); errUnmarshal != nil {
		hostLog("warn", pluginID+": 钉扎状态文件解析失败，已忽略: "+errUnmarshal.Error())
		return
	}
	if loaded.Pins == nil {
		loaded.Pins = map[string]*pinEntry{}
	}
	if loaded.Version == 0 {
		loaded.Version = 1
	}
	pins = &loaded
	hostLog("info", pluginID+": 已载入渠道选择 "+itoa(len(loaded.Pins))+" 条 ("+path+")")
}

func pinStatePathLocked() string {
	if p := strings.TrimSpace(getConfig().PinStateFile); p != "" {
		return p
	}
	if hostAuthDir != "" {
		return filepath.Join(hostAuthDir, "cline-channel-pins.json")
	}
	return "/root/.cli-proxy-api/cline-channel-pins.json"
}

// savePinsLocked 落盘，调用方需持有 pinMu。
func savePinsLocked() error {
	pins.UpdatedAt = time.Now()
	raw, errMarshal := json.MarshalIndent(pins, "", "  ")
	if errMarshal != nil {
		return errMarshal
	}
	path := pinStatePathLocked()
	if errMkdir := os.MkdirAll(filepath.Dir(path), 0o755); errMkdir != nil {
		return errMkdir
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil {
		pinsModTime = info.ModTime()
	}
	return nil
}

// resolvePin 决定某个模型最终用哪条钉扎规则。
//
// 优先级：面板里保存的选择 > config.yaml 里的 pin_rules。
// 面板是「所见即所得」的即时选择，应当压过静态配置。
func resolvePin(model string, cfg PluginConfig) *PinRule {
	loadPins()

	pinMu.RLock()
	entry, ok := pins.Pins[model]
	pinMu.RUnlock()

	if ok {
		if rule := entry.toRule(model); rule != nil {
			return rule
		}
		// 面板条目为空（理论上不会持久化）→ 视为没有选择
		return nil
	}

	// 面板未覆盖该模型时，回落到 config.yaml 里的静态规则
	if len(cfg.PinRules) > 0 {
		return matchPinRule(cfg.PinRules, model)
	}
	return nil
}

// pinModeFor 返回该模型生效的钉扎模式。
func pinModeFor(model string, cfg PluginConfig) string {
	loadPins()
	pinMu.RLock()
	entry, ok := pins.Pins[model]
	pinMu.RUnlock()
	if ok && entry.Mode != "" {
		return entry.Mode
	}
	if cfg.PinMode != "" {
		return cfg.PinMode
	}
	return PinModeStrict
}

// setPin 保存一个模型的钉扎选择。
func setPin(model string, entry *pinEntry) error {
	loadPins()
	pinMu.Lock()
	defer pinMu.Unlock()

	previous, existed := pins.Pins[model]
	if entry == nil || entry.empty() {
		delete(pins.Pins, model)
	} else {
		entry.UpdatedAt = time.Now()
		entry.Only = cleanList(entry.Only)
		entry.Order = cleanList(entry.Order)
		entry.Exclude = cleanList(entry.Exclude)
		entry.Channels = cleanList(entry.Channels)
		pins.Pins[model] = entry
	}
	if err := savePinsLocked(); err != nil {
		if existed {
			pins.Pins[model] = previous
		} else {
			delete(pins.Pins, model)
		}
		return err
	}
	return nil
}

// removePin 取消某个模型的钉扎。
func removePin(model string) error {
	loadPins()
	pinMu.Lock()
	defer pinMu.Unlock()
	previous, existed := pins.Pins[model]
	delete(pins.Pins, model)
	if err := savePinsLocked(); err != nil {
		if existed {
			pins.Pins[model] = previous
		}
		return err
	}
	return nil
}

// listPins 返回当前全部选择，供面板展示。
func listPins() map[string]*pinEntry {
	loadPins()
	pinMu.RLock()
	defer pinMu.RUnlock()

	out := make(map[string]*pinEntry, len(pins.Pins))
	for k, v := range pins.Pins {
		if v == nil {
			continue
		}
		copied := *v
		copied.Only = append([]string(nil), v.Only...)
		copied.Order = append([]string(nil), v.Order...)
		copied.Exclude = append([]string(nil), v.Exclude...)
		copied.Channels = append([]string(nil), v.Channels...)
		out[k] = &copied
	}
	return out
}

// pinsSummary 给面板用的排序后列表。
func pinsSummary() []map[string]any {
	all := listPins()
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		e := all[k]
		out = append(out, map[string]any{
			"model":     k,
			"only":      e.Only,
			"order":     e.Order,
			"exclude":   e.Exclude,
			"channels":  e.Channels,
			"sort":      e.Sort,
			"style":     e.Style,
			"mode":      e.Mode,
			"updatedAt": e.UpdatedAt,
		})
	}
	return out
}
