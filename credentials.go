package main

import (
	"strings"
	"sync"
)

var credentialState struct {
	sync.RWMutex
	key string
}

func rememberCredential(key string) {
	if key = strings.TrimSpace(key); key == "" {
		return
	}
	credentialState.Lock()
	credentialState.key = key
	credentialState.Unlock()
}

func cachedCredential() string {
	credentialState.RLock()
	defer credentialState.RUnlock()
	return credentialState.key
}

func resolveManagementCredential() string {
	cfg := getConfig()
	// A read-only panel refresh must not advance round-robin selection.
	cfg.AccountMode = "single"
	if keys := resolveConfiguredKeys(cfg); len(keys) > 0 {
		return keys[0]
	}
	return cachedCredential()
}
