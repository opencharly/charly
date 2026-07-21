package migrate

// reshape_compact.go — the compact-node-form reshaper: the first real goHooks
// entry (the schema-compaction cutover's `apply:` migration). Transforms one
// node-form document from the RETIRED named child-node shape to the compact
// grammar:
//   - every `<entity>-<key>: {<key>: <value>}` data child folds INLINE into the
//     kind value (a duplicate data key is a hard-error panic — nothing ever
//     relied on the old silent-replace);
//   - every step child folds into the kind value's ordered `plan:` list in
//     document order (an auto `<name>-step-N` key is dropped; a meaningful key
//     with no `id:` becomes the step's id);
//   - the internal plugin envelope rewrites to the authored sugar
//     (`plugin: W` + `plugin_input: M` → `W: M`, scalar-collapsed when M is
//     exactly {<primary>: <scalar>});
//   - a live-verb step's exclusive sibling fields fold into the verb's input
//     map ({method: <m>, ...}), applying the frozen per-verb renames
//     (dbus method→member; cdp/appium/libvirt http-method→http_method);
//   - a box/deploy `env:` KEY=VALUE list becomes the unified map form;
//   - member children (sub-entities) are preserved and recursed.
//
// EVERY vocabulary below is a FROZEN SNAPSHOT of the pre-cutover schema —
// migrations replay against arbitrarily old configs forever, so nothing here
// may read a live registry or the current spec vocab. Proven on the full
// 312-file corpus by the S1 spike (losslessness via assembled-body equality,
// byte-level idempotency).

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---- frozen pre-cutover vocabulary ----

var reshapeDataKeys = setOfWords(
	"add_candy", "alias", "apk", "artifact", "bake_plugin", "candy", "capability",
	"data", "distro", "env", "env_accept", "env_provide", "env_require",
	"ephemeral", "extract", "hook", "install_opts", "iterate", "libvirt",
	"localpkg", "mcp_accept", "mcp_provide", "mcp_require", "package",
	"path_append", "plugin", "port", "port_relay", "preemptible", "require",
	"requires_capability", "requires_exclusive", "requires_shared", "route",
	"secret", "secret_accept", "secret_require", "security", "service", "shell",
	"ssh_arg", "tunnel", "var", "volume",
)

var reshapeStepKeywords = setOfWords("run", "check", "agent-run", "agent-check", "include")

var reshapeKindWords = setOfWords(
	"pod", "vm", "k8s", "local", "android", "group", "bundle", "candy",
	"distro", "builder", "init", "resource", "sidecar", "agent", "check",
	// the retired Calamares kinds still classify so an old config reshapes
	// cleanly before the loader reports the kind itself as removed
	"target", "module", "package-group",
)

var reshapeLiveVerbs = setOfWords(
	"cdp", "wl", "dbus", "vnc", "mcp", "record", "spice", "libvirt", "kube", "adb", "appium",
)

// step-level fields that STAY on the step in the compact form (the post-cutover
// authored #Op vocabulary, frozen).
var reshapeRetainedStepFields = setOfWords(
	"run", "check", "agent-run", "agent-check", "include",
	"id", "description", "context", "timeout", "eventually", "retry_interval",
	"exit_status", "stdout", "stderr", "tag", "depends_on", "skip",
	"exclude_distro", "uninstall", "comment", "cache", "env",
	"run_as", "mode", "to", "content", "extract", "extract_include",
	"strip_components", "target", "caps",
	"mkdir", "copy", "write", "link", "download", "setcap", "build",
)

// frozen primary map for scalar-collapse of single-primary inputs.
var reshapePrimaryOf = map[string]string{
	"file": "file", "command": "command", "package": "package", "service": "service",
	"addr": "addr", "port": "port", "http": "http", "process": "process",
	"dns": "dns", "user": "user", "unix_group": "unix_group", "mount": "mount",
	"kernel-param": "kernel-param", "interface": "interface", "matching": "matching",
	"cdp": "method", "wl": "method", "dbus": "method", "vnc": "method",
	"mcp": "method", "record": "method", "spice": "method", "libvirt": "method",
	"kube": "method", "adb": "method", "appium": "method",
}

// reshapeVerbAlsoFold names RETAINED shared #Op fields that nonetheless fold
// into a SPECIFIC verb's input map — the converted plugin reads them from its
// input, not the step (appium's capabilities JSON collides with setcap's caps;
// wl's window target collides with link's target).
// reshapeFoldVerbs: probe verbs (non-live) whose steps also fold non-retained
// siblings into their input map — they absorbed formerly-shared #Op modifiers.
var reshapeFoldVerbs = setOfWords("http")

var reshapeVerbAlsoFold = map[string]map[string]bool{
	"appium": {"caps": true},
	"wl":     {"target": true},
}

// frozen per-verb renames of shared modifiers absorbed into the verb input map.
var reshapeVerbFieldRenames = map[string]map[string]string{
	"dbus":    {"method": "member"},
	"vnc":     {"method": "http_method"},
	"cdp":     {"method": "http_method"},
	"appium":  {"method": "http_method"},
	"libvirt": {"method": "http_method"},
}

// the deleted harness surface — dropped from steps when present.
var reshapeDroppedStepFields = setOfWords(
	"summarize", "kill", "signal", "over_id", "metric", "emit_id",
	"p50", "p95", "p99", "max", "mean", "capture", "capture_extract",
	"parallel", "count", "index_var",
)

var reshapeAutoStepKey = regexp.MustCompile(`-step-\d+$`)

// the pre-cutover reserved document directives (frozen; matches the stable
// #NodeDoc set — new directives added later never appear in an old config).
var reshapeDocDirectives = setOfWords(
	"version", "repo", "import", "discover", "defaults", "provides", "providers",
	"compiled_plugins", "context_ignore_baseline", "install_hints", "ovmf_paths",
	"device_descriptions", "device_patterns", "gpu_vendors", "pci_class_labels",
	"distro_package_managers", "distro_family_map", "ovmf_distro_aliases",
)

func setOfWords(ws ...string) map[string]bool {
	m := make(map[string]bool, len(ws))
	for _, w := range ws {
		m[w] = true
	}
	return m
}

// compactNodeForm is the per-document transform (the goHooks contract). It
// panics on a structural impossibility (duplicate data key / unclassifiable
// child) — the engine treats a panic as the migration failing loudly, which is
// the intended behavior for a config the reshape cannot faithfully convert.
func compactNodeForm(doc *yaml.Node) bool {
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return false
	}
	changed := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		name := root.Content[i].Value
		if reshapeDocDirectives[name] {
			continue
		}
		val := root.Content[i+1]
		if val.Kind != yaml.MappingNode {
			continue
		}
		// Per-DOCUMENT manifest gate: the engine's candidate walk sweeps every
		// .yml under candy/ + box/ + the root siblings — Taskfiles, service
		// configs, fixtures included. Only a NODE-SHAPED entity (a mapping whose
		// keys include a frozen kind word, or whose children classify as the old
		// data/step shape) is a charly manifest entity; anything else is skipped
		// untouched (never reshaped, never a panic).
		if !reshapeLooksLikeEntity(val) {
			continue
		}
		c, err := reshapeCompactEntity(name, val)
		if err != nil {
			panic(fmt.Sprintf("compact-node-form migration: entity %q: %v", name, err))
		}
		changed = changed || c
	}
	return changed
}

// reshapeClassify returns "disc", "data", "step", or "member" for an entity
// child, or an error for an unclassifiable one.
func reshapeClassify(k string, v *yaml.Node) (string, error) {
	if v.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(v.Content); i += 2 {
			if reshapeStepKeywords[v.Content[i].Value] {
				return "step", nil
			}
		}
	}
	if reshapeKindWords[k] {
		return "disc", nil
	}
	if v.Kind == yaml.MappingNode && len(v.Content) == 2 && reshapeDataKeys[v.Content[0].Value] {
		return "data", nil
	}
	if v.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(v.Content); i += 2 {
			if reshapeKindWords[v.Content[i].Value] {
				return "member", nil
			}
		}
	}
	// a plugin-served kind this frozen snapshot cannot know: a mapping/scalar
	// child that is neither data, step, nor member-shaped is the discriminator
	if v.Kind == yaml.MappingNode || v.Kind == yaml.ScalarNode {
		return "disc", nil
	}
	return "", fmt.Errorf("unclassifiable child %q (yaml kind %v)", k, v.Kind)
}

// reshapeCompactEntity folds one entity's children into its kind value in place.
func reshapeCompactEntity(name string, val *yaml.Node) (bool, error) {
	var discKey string
	var discVal *yaml.Node
	changed := false
	for i := 0; i+1 < len(val.Content); i += 2 {
		k, v := val.Content[i].Value, val.Content[i+1]
		cls, err := reshapeClassify(k, v)
		if err != nil {
			return false, err
		}
		if cls == "disc" {
			if discKey != "" {
				return false, fmt.Errorf("two discriminators %q and %q", discKey, k)
			}
			discKey, discVal = k, v
		}
	}
	if discKey == "" {
		return false, fmt.Errorf("no kind discriminator found")
	}
	if discVal.Kind == yaml.ScalarNode {
		if len(val.Content) > 2 {
			return false, fmt.Errorf("scalar %q discriminator with sibling children — manual review", discKey)
		}
		return false, nil
	}
	newContent := make([]*yaml.Node, 0, len(val.Content))
	var planSeq *yaml.Node
	for i := 0; i+1 < len(val.Content); i += 2 {
		kn, v := val.Content[i], val.Content[i+1]
		k := kn.Value
		if k == discKey && v == discVal {
			newContent = append(newContent, kn, v)
			continue
		}
		cls, _ := reshapeClassify(k, v)
		switch cls {
		case "data":
			dk, dv := v.Content[0], v.Content[1]
			if existing := reshapeMapValue(discVal, dk.Value); existing != nil {
				return false, fmt.Errorf("duplicate data key %q (child %q collides with a value field)", dk.Value, k)
			}
			if kn.HeadComment != "" && dk.HeadComment == "" {
				dk.HeadComment = kn.HeadComment
			}
			discVal.Content = append(discVal.Content, dk, dv)
			changed = true
		case "step":
			if planSeq == nil {
				planSeq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			}
			if err := reshapeTransformStep(v); err != nil {
				return false, fmt.Errorf("step %q: %w", k, err)
			}
			if !reshapeAutoStepKey.MatchString(k) && reshapeMapValue(v, "id") == nil {
				reshapeSetMapEntry(v, "id", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k})
			}
			if kn.HeadComment != "" && len(v.Content) > 0 && v.Content[0].HeadComment == "" {
				v.Content[0].HeadComment = kn.HeadComment
			}
			planSeq.Content = append(planSeq.Content, v)
			changed = true
		case "member":
			mc, err := reshapeCompactEntity(k, v)
			if err != nil {
				return false, fmt.Errorf("member %q: %w", k, err)
			}
			newContent = append(newContent, kn, v)
			changed = changed || mc
		}
	}
	if planSeq != nil {
		if existing := reshapeMapValue(discVal, "plan"); existing != nil {
			existing.Content = append(existing.Content, planSeq.Content...)
		} else {
			reshapeSetMapEntry(discVal, "plan", planSeq)
		}
	}
	val.Content = newContent
	if err := reshapeTransformBody(discVal); err != nil {
		return false, err
	}
	// transformStep mutates in place; compare a serialized snapshot to report
	// the change (a compact file whose only delta is a step fold must still be
	// WRITTEN by the engine).
	if p := reshapeMapValue(discVal, "plan"); p != nil && p.Kind == yaml.SequenceNode {
		beforeBytes, _ := yaml.Marshal(p)
		for _, st := range p.Content {
			if err := reshapeTransformStep(st); err != nil {
				return false, err
			}
		}
		afterBytes, _ := yaml.Marshal(p)
		if !bytes.Equal(beforeBytes, afterBytes) {
			changed = true
		}
	}
	return changed, nil
}

// reshapeTransformBody applies value-level rewrites: env KEY=VALUE list → map.
func reshapeTransformBody(body *yaml.Node) error {
	for i := 0; i+1 < len(body.Content); i += 2 {
		k, v := body.Content[i], body.Content[i+1]
		if k.Value == "env" && v.Kind == yaml.SequenceNode {
			m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			for _, item := range v.Content {
				if item.Kind != yaml.ScalarNode {
					return fmt.Errorf("env list item is not scalar")
				}
				key, val, ok := strings.Cut(item.Value, "=")
				if !ok {
					return fmt.Errorf("env entry %q has no '='", item.Value)
				}
				m.Content = append(m.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key, HeadComment: item.HeadComment},
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: val})
			}
			body.Content[i+1] = m
		}
	}
	return nil
}

// reshapeTransformStep rewrites one step map in place: dropped harness fields,
// envelope → sugar, live-verb sibling fold with the frozen renames.
func reshapeTransformStep(st *yaml.Node) error {
	if st.Kind != yaml.MappingNode {
		return fmt.Errorf("step is not a mapping")
	}
	// drop the deleted harness fields
	filtered := st.Content[:0]
	for i := 0; i+1 < len(st.Content); i += 2 {
		if reshapeDroppedStepFields[st.Content[i].Value] {
			continue
		}
		filtered = append(filtered, st.Content[i], st.Content[i+1])
	}
	st.Content = filtered

	// envelope → sugar
	pluginIdx, inputIdx := -1, -1
	for i := 0; i+1 < len(st.Content); i += 2 {
		switch st.Content[i].Value {
		case "plugin":
			pluginIdx = i
		case "plugin_input":
			inputIdx = i
		}
	}
	if pluginIdx >= 0 {
		word := st.Content[pluginIdx+1].Value
		var input *yaml.Node
		if inputIdx >= 0 {
			input = st.Content[inputIdx+1]
		} else {
			input = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		if prim, ok := reshapePrimaryOf[word]; ok && input.Kind == yaml.MappingNode &&
			len(input.Content) == 2 && input.Content[0].Value == prim &&
			input.Content[1].Kind == yaml.ScalarNode {
			input = input.Content[1]
		}
		nc := make([]*yaml.Node, 0, len(st.Content))
		for i := 0; i+1 < len(st.Content); i += 2 {
			switch i {
			case pluginIdx:
				nc = append(nc, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: word,
					HeadComment: st.Content[i].HeadComment}, input)
			case inputIdx:
				// folded into the sugar key's value
			default:
				nc = append(nc, st.Content[i], st.Content[i+1])
			}
		}
		st.Content = nc
	}

	// verb fold: verb key + non-retained siblings → verb input map. Covers the
	// 11 live verbs AND the probe verbs that absorbed formerly-shared #Op
	// modifiers (http absorbed method/request_body).
	for i := 0; i+1 < len(st.Content); i += 2 {
		word := st.Content[i].Value
		if !reshapeLiveVerbs[word] && !reshapeFoldVerbs[word] {
			continue
		}
		v := st.Content[i+1]
		var extras [][2]*yaml.Node
		alsoFold := reshapeVerbAlsoFold[word]
		nc := make([]*yaml.Node, 0, len(st.Content))
		for j := 0; j+1 < len(st.Content); j += 2 {
			k2 := st.Content[j].Value
			if j != i && ((!reshapeRetainedStepFields[k2] && !reshapeLiveVerbs[k2]) || alsoFold[k2]) {
				extras = append(extras, [2]*yaml.Node{st.Content[j], st.Content[j+1]})
				continue
			}
			nc = append(nc, st.Content[j], st.Content[j+1])
		}
		if len(extras) == 0 {
			break // the scalar method form stays (the primary shorthand)
		}
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		switch v.Kind {
		case yaml.ScalarNode:
			// the scalar shorthand expands via the verb's PRIMARY (method for a
			// live verb; e.g. http for the http probe verb)
			wrapKey := reshapePrimaryOf[word]
			if wrapKey == "" {
				wrapKey = "method"
			}
			m.Content = append(m.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: wrapKey}, v)
		case yaml.MappingNode:
			m.Content = append(m.Content, v.Content...)
		}
		renames := reshapeVerbFieldRenames[word]
		for _, kv := range extras {
			if nn, ok := renames[kv[0].Value]; ok {
				kv[0] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: nn,
					HeadComment: kv[0].HeadComment}
			}
			m.Content = append(m.Content, kv[0], kv[1])
		}
		for j := 0; j+1 < len(nc); j += 2 {
			if nc[j].Value == word {
				nc[j+1] = m
			}
		}
		st.Content = nc
		break
	}
	return nil
}

// ---- yaml.Node helpers (local; the plugin module has no core helpers) ----

func reshapeMapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func reshapeSetMapEntry(m *yaml.Node, key string, val *yaml.Node) {
	if existing := reshapeMapValue(m, key); existing != nil {
		*existing = *val
		return
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
}

// reshapeLooksLikeEntity reports whether a top-level mapping value is a charly
// node-form entity: it carries a frozen kind word as a key, or at least one
// old-shape data/step child.
func reshapeLooksLikeEntity(val *yaml.Node) bool {
	for i := 0; i+1 < len(val.Content); i += 2 {
		k, v := val.Content[i].Value, val.Content[i+1]
		if reshapeKindWords[k] {
			return true
		}
		if cls, err := reshapeClassify(k, v); err == nil && (cls == "data" || cls == "step") {
			return true
		}
	}
	return false
}
