package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"cline-channel/sdk/pluginabi"
)

const (
	// providerKey 是插件在 CPA 中的 provider 标识。
	// 它会出现在 auth 文件的 provider 字段、executor.identifier 返回值里，
	// 也是模型路由到本插件的依据。
	providerKey = "cline"

	// pluginID 必须与动态库文件名（去掉扩展名）一致，否则 CPA 不会把
	// plugins.configs 下的配置交给本插件。
	pluginID = "cline-channel"

	defaultBaseURL = "https://api.cline.bot/api/v1"

	// Native cline-pass/ IDs are retained; explicit non-Cline IDs use this prefix.
	defaultModelPrefix = "cline/"

	defaultTimeoutSeconds = 300
)

// PluginConfig 对应 config.yaml 里 plugins.configs.cline-channel 的内容。
type PluginConfig struct {
	Enabled  bool `yaml:"enabled"`
	Priority int  `yaml:"priority"`

	// BaseURL 是 Cline 网关地址，默认 https://api.cline.bot/api/v1
	BaseURL string `yaml:"base_url"`

	// APIKey 是可选的直连兜底：没配 auth 文件时用这里的 key。
	APIKey string `yaml:"api_key"`

	// Accounts 是可轮换的 Cline Pass 账号池。APIKey 作为旧配置兼容的首选账号。
	Accounts      []ClineAccount `yaml:"accounts"`
	AccountMode   string         `yaml:"account_mode"` // single | roundrobin
	ActiveAccount int            `yaml:"active_account"`

	// ModelPrefix 是对外暴露的模型前缀，默认 cline/
	ModelPrefix string `yaml:"model_prefix"`

	// PinMode 决定钉扎严格程度：
	//   strict    只走钉住的渠道，失败也不换
	//   preferred 优先钉住的渠道，异常时由网关自行回退
	PinMode string `yaml:"pin_mode"`

	// PinStyle 决定钉扎指令用哪种写法注入：
	//   auto       按规则内的 style，缺省为 both
	//   vercel     顶层 providerOptions.gateway
	//   openrouter 顶层 provider
	//   both       两种都写（经中转网关时最稳）
	//   none       不注入
	PinStyle string `yaml:"pin_style"`

	// PinRules 是模型到上游渠道的钉扎规则，按顺序匹配。
	// 面板里保存的选择优先级更高，这里的规则只作为未选择时的默认值。
	PinRules []PinRule `yaml:"pin_rules"`

	// PinStateFile 是面板选择的落盘路径。
	// 留空时自动使用 <auth-dir>/cline-channel-pins.json。
	PinStateFile string `yaml:"pin_state_file"`

	// Models 是手动指定的模型清单；为空时由 defaultModels 兜底。
	Models []string `yaml:"models"`

	// TimeoutSeconds 是单次上游请求超时。
	TimeoutSeconds int `yaml:"timeout_seconds"`

	// ExtraHeaders 会附加到发往 Cline 的请求上。
	ExtraHeaders map[string]string `yaml:"extra_headers"`

	// Debug 打开后会通过 host.log 输出路由与钉扎详情。
	Debug bool `yaml:"debug"`

	// StreamMode 决定流式转发怎么把上游 SSE 交回宿主：
	//   collect  收完整条流再一次性交回（默认，当前 CPA 宿主下唯一可用）
	//   emit     用 host.stream.emit 边收边推；该回调会与响应头交付互等而死锁，
	//            仅在宿主修好之后才有意义
	StreamMode string `yaml:"stream_mode"`
}

type ClineAccount struct {
	Name    string `yaml:"name"`
	Key     string `yaml:"key"`
	Enabled *bool  `yaml:"enabled"`
}

var (
	cfgMu          sync.RWMutex
	activeCfg      = defaultConfig()
	registerRaw    []byte // 最近一次注册用的原始配置，供管理面板展示
	accountCounter uint64
)

func defaultConfig() PluginConfig {
	return PluginConfig{
		Enabled:        true,
		BaseURL:        defaultBaseURL,
		ModelPrefix:    defaultModelPrefix,
		// 优先渠道模式避免单个上游容量错误触发 CPA 账号失效状态；
		// 需要严格诊断时可在插件设置中显式改回 strict。
		PinMode:        PinModePreferred,
		PinStyle:       PinStyleAuto,
		TimeoutSeconds: defaultTimeoutSeconds,
		StreamMode:     StreamModeCollect,
	}
}

func getConfig() PluginConfig {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return activeCfg
}

func setConfig(c PluginConfig) {
	c.normalize()
	cfgMu.Lock()
	activeCfg = c
	cfgMu.Unlock()
}

func (c *PluginConfig) normalize() {
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if c.BaseURL == "" {
		c.BaseURL = defaultBaseURL
	}
	// 地址可以只填主机名，补上默认 scheme
	if !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
		c.BaseURL = "https://" + c.BaseURL
	}

	c.ModelPrefix = strings.TrimSpace(c.ModelPrefix)
	if c.ModelPrefix == "" {
		c.ModelPrefix = defaultModelPrefix
	}
	if !strings.HasSuffix(c.ModelPrefix, "/") {
		c.ModelPrefix += "/"
	}
	if !strings.HasPrefix(c.ModelPrefix, "cline/") {
		c.ModelPrefix = "cline/" + c.ModelPrefix
	}

	if c.PinMode != PinModePreferred {
		c.PinMode = PinModeStrict
	}
	switch strings.ToLower(strings.TrimSpace(c.PinStyle)) {
	case PinStyleVercel, PinStyleOpenRouter, PinStyleBoth, PinStyleNone, PinStyleAuto:
		c.PinStyle = strings.ToLower(strings.TrimSpace(c.PinStyle))
	default:
		c.PinStyle = PinStyleAuto
	}

	if c.TimeoutSeconds <= 0 {
		c.TimeoutSeconds = defaultTimeoutSeconds
	}
	switch strings.ToLower(strings.TrimSpace(c.StreamMode)) {
	case StreamModeEmit:
		c.StreamMode = StreamModeEmit
	default:
		c.StreamMode = StreamModeCollect
	}
	if c.AccountMode != "roundrobin" {
		c.AccountMode = "single"
	}
	if c.ActiveAccount < 0 {
		c.ActiveAccount = 0
	}
	for i := range c.Accounts {
		c.Accounts[i].Key = strings.TrimSpace(c.Accounts[i].Key)
		if c.Accounts[i].Name == "" {
			c.Accounts[i].Name = "account-" + itoa(i+1)
		}
	}
	for i := range c.PinRules {
		c.PinRules[i].normalize()
	}
}

// rpcLifecycleRequest 是 plugin.register / plugin.reconfigure 的入参。
// config_yaml 以 base64 传输（JSON 对 []byte 的默认编码）。
type rpcLifecycleRequest struct {
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
}

func handleRegistration(request []byte) ([]byte, error) {
	var req rpcLifecycleRequest
	if len(request) > 0 {
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return nil, errUnmarshal
		}
	}

	raw := req.ConfigYAML
	// 宿主理论上直接给原始字节；个别版本会以 base64 字符串形式传递，这里两种都兜住。
	if len(raw) > 0 && !looksLikeYAML(raw) {
		if decoded, errDecode := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw))); errDecode == nil {
			raw = decoded
		}
	}

	cfg := defaultConfig()
	if len(raw) > 0 {
		if errUnmarshal := yaml.Unmarshal(raw, &cfg); errUnmarshal != nil {
			hostLog("error", pluginID+": 配置解析失败，使用默认配置: "+errUnmarshal.Error())
		}
	}
	setConfig(cfg)

	cfgMu.Lock()
	registerRaw = append([]byte(nil), raw...)
	cfgMu.Unlock()

	hostLog("info", pluginID+": 已注册, base_url="+cfg.BaseURL+
		" prefix="+cfg.ModelPrefix+
		" pin_mode="+cfg.PinMode+
		" pin_style="+cfg.PinStyle+
		" rules="+itoa(len(cfg.PinRules)))

	return okEnvelope(registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata:      pluginMetadata(),
		Capabilities:  pluginCapabilities(),
	})
}

func looksLikeYAML(raw []byte) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return true
	}
	// base64 不会出现 ':' 或换行缩进，而 YAML 配置必然有 key: value
	return strings.Contains(trimmed, ":")
}
