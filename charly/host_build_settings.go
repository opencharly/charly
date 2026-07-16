package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// host_build_settings.go — the generic "settings" F10 host-builder. The externalized `charly settings`
// command plugin (candy/plugin-settings) OWNS the get/set/list/reset/path grammar + output and asks the
// host to run each config-subsystem op via Executor.HostBuild("settings", spec.SettingsRequest{...}).
// The config subsystem (GetConfigValue / SetConfigValue / ListConfigValues / ResetConfigValue /
// RuntimeConfigPath + engine resolution + the credential store) STAYS core — reached via this generic
// action noun, NOT a provider word (F11).
const settingsBuilderKind = "settings"

func hostBuildSettings(_ context.Context, req spec.SettingsRequest, _ buildEngineContext) (spec.SettingsReply, error) {
	switch req.Op {
	case "get":
		val, err := resolveSettingsGet(req.Key)
		if err != nil {
			return spec.SettingsReply{Error: err.Error()}, nil
		}
		return spec.SettingsReply{Value: val}, nil
	case "set":
		if err := SetConfigValue(req.Key, req.Value); err != nil {
			return spec.SettingsReply{Error: err.Error()}, nil
		}
		return spec.SettingsReply{}, nil
	case "list":
		vals, err := ListConfigValues()
		if err != nil {
			return spec.SettingsReply{Error: err.Error()}, nil
		}
		entries := make([]spec.SettingsEntry, 0, len(vals))
		for _, v := range vals {
			entries = append(entries, spec.SettingsEntry{Key: v.Key, Value: v.Value, Source: v.Source})
		}
		return spec.SettingsReply{Entries: entries}, nil
	case "reset":
		if err := ResetConfigValue(req.Key); err != nil {
			return spec.SettingsReply{Error: err.Error()}, nil
		}
		return spec.SettingsReply{}, nil
	case "path":
		path, err := kit.RuntimeConfigPath()
		if err != nil {
			return spec.SettingsReply{Error: err.Error()}, nil
		}
		return spec.SettingsReply{Value: path}, nil
	default:
		return spec.SettingsReply{Error: fmt.Sprintf("settings: unknown op %q", req.Op)}, nil
	}
}

// resolveSettingsGet resolves a config key's value for `charly settings get` — moved from the deleted
// SettingsGetCmd.Run. Special cases: vnc.password.* + hosts.* resolve via GetConfigValue; engine.* via
// ResolveRuntime (shows "podman" not "auto"); secret_backend via the resolved credential store name.
func resolveSettingsGet(key string) (string, error) {
	if strings.HasPrefix(key, "vnc.password.") {
		return GetConfigValue(key)
	}
	switch key {
	case "engine.build", "engine.run", "engine.rootful":
		if rt, err := kit.ResolveRuntime(); err == nil {
			switch key {
			case "engine.build":
				return rt.BuildEngine, nil
			case "engine.run":
				return rt.RunEngine, nil
			case "engine.rootful":
				return fmt.Sprintf("%v", rt.Rootful), nil
			}
		}
		// fall through to ListConfigValues if engine detection fails
	case "secret_backend":
		return DefaultCredentialStore().Name(), nil
	}
	vals, err := ListConfigValues()
	if err != nil {
		return "", err
	}
	for _, v := range vals {
		if v.Key == key {
			return v.Value, nil
		}
	}
	if strings.HasPrefix(key, "hosts.") || strings.HasPrefix(key, "vnc.password.") {
		return GetConfigValue(key)
	}
	return "", fmt.Errorf("unknown config key %q (run 'charly settings list' to see valid keys)", key)
}

var _ = func() bool {
	registerHostBuilder(settingsBuilderKind, typedHostBuilder(settingsBuilderKind, hostBuildSettings))
	return true
}()
