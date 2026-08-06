package marketplace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// emit_hooks.go — the .claude/hooks/* emission + the .claude/settings.json plugin-owned-key
// merge. The hook scripts are emitted as files (mode applied by applyHookModes after write);
// the settings merge drops the plugin-owned keys (enabledPlugins charly-*, the marketplace's
// extraKnownMarketplaces entry, the wired hook entries) and regenerates them from the
// marketplace + hook entities, preserving every other key (permissions, env, teammateMode, …).

func emitHooks(em emissions, ks *kindSet, root string) error {
	for _, h := range ks.Hooks {
		em[".claude/hooks/"+h.Name] = []byte(h.Content)
	}
	merged, err := mergeSettings(root, ks)
	if err != nil {
		return err
	}
	em[".claude/settings.json"] = merged
	return nil
}

// applyHookModes sets the executable bits on the emitted hook scripts (0755 for .sh by default,
// or the hook's declared mode) AFTER writeAll — the emissions map carries bytes only.
func applyHookModes(root string, ks *kindSet) {
	for _, h := range ks.Hooks {
		mode := os.FileMode(0o644)
		switch h.Mode {
		case "0755":
			mode = 0o755 // an explicit executable declaration applies regardless of extension
		case "":
			if strings.HasSuffix(h.Name, ".sh") {
				mode = 0o755 // the sensible default for gate scripts
			}
		case "0644":
			mode = 0o644
		}
		_ = os.Chmod(filepath.Join(root, ".claude", "hooks", h.Name), mode)
	}
}

// mergeSettings reads the existing .claude/settings.json, drops the plugin-owned keys, and
// regenerates them from the marketplace + hook entities. Returns the full new file bytes.
func mergeSettings(root string, ks *kindSet) ([]byte, error) {
	path := filepath.Join(root, ".claude", "settings.json")
	existing := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &existing); err != nil {
			return nil, fmt.Errorf("parse .claude/settings.json: %w", err)
		}
	}
	name := ks.Marketplace.Name

	// 1. enabledPlugins: drop the charly-*@<name> entries, regenerate per family.
	enabled := map[string]any{}
	if v, ok := existing["enabledPlugins"].(map[string]any); ok {
		for k, val := range v {
			if !strings.HasSuffix(k, "@"+name) {
				enabled[k] = val // preserve non-marketplace plugins (the official ones)
			}
		}
	}
	enabledPlugins := ks.Marketplace.Settings.EnabledPlugins
	for _, f := range familyNamesOf(ks) {
		if len(enabledPlugins) > 0 && !containsStr(enabledPlugins, "charly-"+f) {
			continue
		}
		enabled["charly-"+f+"@"+name] = true
	}
	if len(enabled) > 0 {
		existing["enabledPlugins"] = enabled
	} else {
		delete(existing, "enabledPlugins")
	}

	// 2. extraKnownMarketplaces: drop the <name> entry, regenerate from settings.source_path.
	if m, ok := existing["extraKnownMarketplaces"].(map[string]any); ok {
		delete(m, name)
		existing["extraKnownMarketplaces"] = m
	} else if existing["extraKnownMarketplaces"] != nil {
		delete(existing, "extraKnownMarketplaces")
	}
	sourcePath := ks.Marketplace.Settings.SourcePath
	if sourcePath == "" {
		sourcePath = "./plugins"
	}
	ekm := map[string]any{}
	if v, ok := existing["extraKnownMarketplaces"].(map[string]any); ok {
		for k, val := range v {
			ekm[k] = val
		}
	}
	ekm[name] = map[string]any{
		"source": map[string]any{"source": "directory", "path": sourcePath},
	}
	existing["extraKnownMarketplaces"] = ekm

	// 3. hooks: drop the wired entries (commands referencing a hook entity's file) and
	//    regenerate from the trigger-bearing hook entities, preserving hand-authored hooks.
	hooksOut := map[string]any{}
	if v, ok := existing["hooks"].(map[string]any); ok {
		for trigger, entries := range v {
			kept := dropWiredHookEntries(entries, ks)
			if len(kept) > 0 {
				hooksOut[trigger] = kept
			}
		}
	}
	for trigger, wired := range buildHookWiring(ks) {
		existing := asEntryList(hooksOut[trigger])
		hooksOut[trigger] = append(existing, wired...)
	}
	if len(hooksOut) > 0 {
		existing["hooks"] = hooksOut
	} else {
		delete(existing, "hooks")
	}

	b, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal .claude/settings.json: %w", err)
	}
	return append(b, '\n'), nil
}

// dropWiredHookEntries removes settings.json hook entries whose command references a hook entity
// file (those are regenerated), preserving hand-authored entries.
func dropWiredHookEntries(entries any, ks *kindSet) []any {
	var out []any
	list, _ := entries.([]any)
	for _, e := range list {
		if entryReferencesHook(e, ks) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// entryReferencesHook reports whether a settings hook entry (its nested "hooks":[].command)
// references a .claude/hooks/<name> file belonging to a hook entity.
func entryReferencesHook(entry any, ks *kindSet) bool {
	obj, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	inner, _ := obj["hooks"].([]any)
	for _, h := range inner {
		ho, _ := h.(map[string]any)
		cmd, _ := ho["command"].(string)
		if cmd != "" {
			for name := range ks.Hooks {
				if strings.Contains(cmd, ".claude/hooks/"+name) {
					return true
				}
			}
		}
	}
	return false
}

// buildHookWiring groups the trigger-bearing hook entities by (trigger, matcher) into the
// settings.json hooks entries: {"<trigger>": [{"matcher": X, "hooks": [{"type":"command",
// "command": "bash $CLAUDE_PROJECT_DIR/.claude/hooks/<name>"}]}]}.
func buildHookWiring(ks *kindSet) map[string][]any {
	type group struct {
		matcher string
		entries []any
	}
	byKey := map[string]*group{}
	var keys []string
	var hookNames []string
	for name := range ks.Hooks {
		hookNames = append(hookNames, name)
	}
	sort.Strings(hookNames) // deterministic order — ks.Hooks is a map, and map iteration is random
	for _, name := range hookNames {
		h := ks.Hooks[name]
		if h.Trigger == "" {
			continue // an AUX file (gitcmd.py, gate_test.py) — emitted, not wired
		}
		matcher := h.Matcher
		cmd := "$CLAUDE_PROJECT_DIR/.claude/hooks/" + h.Name // h.Name is the file stem incl. extension
		if strings.HasSuffix(h.Name, ".sh") {
			cmd = "bash " + cmd
		}
		key := h.Trigger + "\x00" + matcher
		g, ok := byKey[key]
		if !ok {
			g = &group{matcher: matcher}
			byKey[key] = g
			keys = append(keys, key)
		}
		g.entries = append(g.entries, map[string]any{"type": "command", "command": cmd})
	}
	sort.Strings(keys)
	out := map[string][]any{}
	for _, key := range keys {
		g := byKey[key]
		trigger := strings.SplitN(key, "\x00", 2)[0]
		entry := map[string]any{"matcher": g.matcher, "hooks": g.entries}
		out[trigger] = append(out[trigger], entry)
	}
	return out
}

func asEntryList(v any) []any {
	list, _ := v.([]any)
	return list
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// familyNamesOf returns the sorted marketplace family names.
func familyNamesOf(ks *kindSet) []string {
	out := make([]string, 0, len(ks.Marketplace.Families))
	for name := range ks.Marketplace.Families {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
