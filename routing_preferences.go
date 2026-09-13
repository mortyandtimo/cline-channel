package main

// injectRouting matches cline-pass-switcher's two gateway dialects. In
// particular, exclusions become an allowlist because ignore is not enforced.
func injectRouting(body map[string]any, rule *PinRule, defaultStyle, mode string) (string, bool) {
	if rule.empty() {
		return PinStyleNone, false
	}
	style := rule.Style
	if style == "" || style == PinStyleAuto {
		style = defaultStyle
	}
	if style == "" || style == PinStyleAuto {
		style = PinStyleBoth
	}
	if style == PinStyleNone {
		return style, false
	}
	only, order := withoutChannels(rule.Only, rule.Exclude), withoutChannels(rule.Order, rule.Exclude)
	if mode == PinModePreferred && len(order) == 0 && len(only) > 0 {
		order, only = only, nil
	}
	if len(rule.Exclude) > 0 && (mode == PinModePreferred || len(only) == 0) {
		only = withoutChannels(rule.Channels, rule.Exclude)
	}
	if style == PinStyleBoth || style == PinStyleVercel {
		// Planner/Vercel requests must not carry the direct OpenRouter provider
		// block; Cline may consume it before forwarding providerOptions.gateway.
		if style == PinStyleVercel {
			delete(body, "provider")
		}
		options := copyMapValue(body["providerOptions"])
		options["gateway"] = routingBlock(options["gateway"], only, order, rule.Sort)
		body["providerOptions"] = options
	}
	if style == PinStyleBoth || style == PinStyleOpenRouter {
		if style == PinStyleOpenRouter {
			delete(body, "providerOptions")
		}
		sort := map[string]string{"cost": "price", "ttft": "latency", "tps": "throughput"}[rule.Sort]
		provider := routingBlock(body["provider"], only, order, firstNonEmpty(sort, rule.Sort))
		provider["allow_fallbacks"] = mode != PinModeStrict || len(only) != 1
		body["provider"] = provider
	}
	return style, true
}

func routingBlock(existing any, only, order []string, sort string) map[string]any {
	block := copyMapValue(existing)
	for _, key := range []string{"only", "order", "ignore", "allow_fallbacks"} {
		delete(block, key)
	}
	if len(only) > 0 {
		block["only"] = only
	}
	if len(order) > 0 {
		block["order"] = order
	}
	if sort != "" {
		block["sort"] = sort
	}
	return block
}

func copyMapValue(value any) map[string]any {
	result := map[string]any{}
	if original, ok := value.(map[string]any); ok {
		for key, item := range original {
			result[key] = item
		}
	}
	return result
}

func withoutChannels(channels, excluded []string) []string {
	result := make([]string, 0, len(channels))
	for _, channel := range cleanList(channels) {
		blocked := false
		for _, exclusion := range excluded {
			if normalizeProvider(channel) == normalizeProvider(exclusion) {
				blocked = true
				break
			}
		}
		if !blocked {
			result = append(result, channel)
		}
	}
	return result
}
