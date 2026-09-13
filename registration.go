package main

import "cline-channel/sdk/pluginapi"

const pluginVersion = "2.3.0"

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

// registrationCapability 的字段名必须与宿主 internal/pluginhost 里的
// rpcCapabilities 完全一致，否则能力不会被识别。
type registrationCapability struct {
	ModelRegistrar bool `json:"model_registrar"`
	ModelProvider  bool `json:"model_provider"`
	AuthProvider   bool `json:"auth_provider"`

	Executor              bool     `json:"executor"`
	ExecutorModelScope    string   `json:"executor_model_scope"`
	ExecutorInputFormats  []string `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string `json:"executor_output_formats,omitempty"`

	ManagementAPI bool `json:"management_api"`
}

func pluginMetadata() pluginapi.Metadata {
	return pluginapi.Metadata{
		Name:    pluginID,
		Version: pluginVersion,
		Author:  "cline-channel",
		// CPA 的 validPlugin 要求 Name / Version / Author / GitHubRepository 四项都非空，
		// 缺任何一项整个插件注册都会被判为无效。
		// 这里不冒充任何线上仓库，用一个明确的本地标识占位。
		GitHubRepository: "local:cline-channel",
		Logo:             "",
		ConfigFields: []pluginapi.ConfigField{
			{
				Name:        "base_url",
				Type:        pluginapi.ConfigFieldTypeString,
				Description: "Cline 网关地址，默认 https://api.cline.bot/api/v1",
			},
			{
				Name:        "api_key",
				Type:        pluginapi.ConfigFieldTypeString,
				Description: "Cline API Key（sk_ 开头）。留空则从 auth 文件读取。",
			},
			{
				Name:        "accounts",
				Type:        pluginapi.ConfigFieldTypeArray,
				Description: "Cline Pass 账号池（name/key/enabled），用于轮询和故障切换。",
			},
			{
				Name:        "account_mode",
				Type:        pluginapi.ConfigFieldTypeEnum,
				EnumValues:  []string{"single", "roundrobin"},
				Description: "账号选择模式：single 优先 active_account，roundrobin 按请求轮换。",
			},
			{
				Name:        "model_prefix",
				Type:        pluginapi.ConfigFieldTypeString,
				Description: "非原生模型使用 cline/ 命名空间；cline-pass/、cline-free/ 原生名称保留。",
			},
			{
				Name:        "pin_mode",
				Type:        pluginapi.ConfigFieldTypeEnum,
				EnumValues:  []string{PinModeStrict, PinModePreferred},
				Description: "strict 只走钉住的渠道；preferred 优先该渠道并允许回退。",
			},
			{
				Name:        "pin_style",
				Type:        pluginapi.ConfigFieldTypeEnum,
				EnumValues:  []string{PinStyleAuto, PinStyleVercel, PinStyleOpenRouter, PinStyleBoth, PinStyleNone},
				Description: "钉扎指令的注入写法。经中转网关转发时只有 vercel 风格能透传。",
			},
			{
				Name:        "pin_rules",
				Type:        pluginapi.ConfigFieldTypeArray,
				Description: "模型到上游渠道的钉扎规则列表（match/only/order/sort/style）。",
			},
			{
				Name:        "pin_state_file",
				Type:        pluginapi.ConfigFieldTypeString,
				Description: "面板渠道选择的落盘路径；留空使用 <auth-dir>/cline-channel-pins.json。",
			},
			{
				Name:        "models",
				Type:        pluginapi.ConfigFieldTypeArray,
				Description: "显式模型白名单；留空从 Cline 官方 clinePass 接口同步订阅模型，不混入完整目录。",
			},
			{
				Name:        "stream_mode",
				Type:        pluginapi.ConfigFieldTypeEnum,
				EnumValues:  []string{StreamModeEmit, StreamModeCollect},
				Description: "emit 边收边推、首字节即上游首字节（默认）；collect 收完整条流再交回，作为退路。",
			},
			{
				Name:        "timeout_seconds",
				Type:        pluginapi.ConfigFieldTypeInteger,
				Description: "单次上游请求超时秒数，默认 300。",
			},
			{
				Name:        "debug",
				Type:        pluginapi.ConfigFieldTypeBoolean,
				Description: "输出路由与钉扎详情日志。",
			},
		},
	}
}

func pluginCapabilities() registrationCapability {
	return registrationCapability{
		ModelRegistrar: true,
		ModelProvider:  true,
		AuthProvider:   true,

		Executor:           true,
		ExecutorModelScope: string(pluginapi.ExecutorModelScopeBoth),
		// 声明只接受 chat-completions：CPA 会把 Claude、Gemini、Codex 等
		// 各种入口协议自动翻译成 OpenAI 格式后再交给本插件，
		// 插件侧因此只需要处理一种协议。
		ExecutorInputFormats:  []string{"chat-completions"},
		ExecutorOutputFormats: []string{"chat-completions"},

		// 面板与探测接口由插件自己的 Cline 风格页面提供；CPA 的通用页面只是承载入口。
		ManagementAPI: true,
	}
}
