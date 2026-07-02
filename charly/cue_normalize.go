package main

// cue_normalize.go — the on-disk shorthand canonicalizer for the CUE loader
// switch (Cutover 1). It walks a YAML document node in parallel with the Go
// type graph (reflect) and rewrites every shorthand wire form into the canonical
// struct shape that plain cue.Value.Decode expects — IN PLACE, so comments and
// key order survive (the migration reuses this; see migrate_cue_normalize.go).
//
// Design (see memory cue-loader-switch-design):
//   - A type implementing json.Unmarshaler is OPAQUE: cue.Value.Decode runs its
//     UnmarshalJSON, so its shorthand needs no on-disk change (Matcher, MatcherList,
//     PortScope). The walker neither expands nor recurses into it.
//   - A registered shorthand type (no UnmarshalJSON) is rewritten by its expander
//     (PackageItem, PortSpec, ShellConfig, …).
//   - A scalar bound for a Go string field is force-tagged !!str (yaml.v3 coerced
//     int/bool→string silently; CUE preserves the scalar type, so e.g.
//     `stop_timeout: 30` must become `"30"`).
//
// The expanders PORT — byte-for-byte — the canonical mapping each (about-to-be-
// deleted) UnmarshalYAML produced; the S6 equivalence test is the oracle.

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// shorthandExpander rewrites a shorthand node into its canonical shape in place.
type shorthandExpander func(node *yaml.Node) error

// cueShorthandExpanders maps a canonical Go type to the expander that
// canonicalizes its shorthand wire forms. Keyed by the value type (not pointer).
var cueShorthandExpanders = map[reflect.Type]shorthandExpander{
	reflect.TypeOf(PackageItem{}):              expandPackageItemNode,
	reflect.TypeOf(PortSpec{}):                 expandPortSpecNode,
	reflect.TypeOf(LibvirtGraphicsListeners{}): expandLibvirtListenersNode,
	reflect.TypeOf(PreemptibleConfig{}):        expandPreemptibleNode,
	reflect.TypeOf(TunnelYAML{}):               expandTunnelNode,
}

var jsonUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

// implementsJSONUnmarshaler reports whether t or *t has an UnmarshalJSON method.
func implementsJSONUnmarshaler(t reflect.Type) bool {
	return t.Implements(jsonUnmarshalerType) || reflect.PointerTo(t).Implements(jsonUnmarshalerType)
}

// NormalizeEntityNode canonicalizes a single entity's YAML node against the Go
// type t (the authored struct, e.g. CandyYAML). Mutates node in place.
func NormalizeEntityNode(node *yaml.Node, t reflect.Type) error {
	return normalizeNode(node, t)
}

func normalizeNode(node *yaml.Node, t reflect.Type) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil
		}
		return normalizeNode(node.Content[0], t)
	}
	// Resolve aliases to their anchor target for type-walking purposes.
	if node.Kind == yaml.AliasNode {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	// Opaque self-decoding types (Matcher/MatcherList/PortScope): cue.Value.Decode
	// runs their UnmarshalJSON, so leave the shorthand untouched and do not recurse.
	if implementsJSONUnmarshaler(t) {
		return nil
	}

	// Shorthand expander (turns scalar/seq/dynamic-key shapes into the struct form).
	if exp, ok := cueShorthandExpanders[t]; ok {
		if err := exp(node); err != nil {
			return err
		}
	}

	// Generic scalar→string coercion: a non-string scalar bound for a Go string
	// field must carry the !!str tag so CUE ingests it as a string.
	if t.Kind() == reflect.String && node.Kind == yaml.ScalarNode {
		forceStringTag(node)
		return nil
	}

	switch t.Kind() {
	case reflect.Struct:
		if node.Kind != yaml.MappingNode {
			return nil // shape mismatch — CUE validation reports it precisely
		}
		flat := flattenYamlFields(t)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			ft, ok := flat[key]
			if !ok {
				continue // unknown key — CUE closedness reports it
			}
			if err := normalizeNode(node.Content[i+1], ft); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if node.Kind != yaml.SequenceNode {
			return nil
		}
		for _, el := range node.Content {
			if err := normalizeNode(el, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Map:
		if node.Kind != yaml.MappingNode {
			return nil
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			if err := normalizeNode(node.Content[i+1], t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

// flattenYamlFields maps every yaml key authorable on a struct (following
// inline/anonymous embeds, e.g. Step embeds Op `yaml:",inline"`) to its Go
// field type. A bare field name with no yaml tag maps under its lowercased name
// (yaml.v3's own default). Cached per type.
var flatYamlFieldsCache = map[reflect.Type]map[string]reflect.Type{}

func flattenYamlFields(t reflect.Type) map[string]reflect.Type {
	if m, ok := flatYamlFieldsCache[t]; ok {
		return m
	}
	out := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		// Wire key: the yaml tag. The CUE-generated spec types carry DOUBLED
		// yaml+json tags (cue:gen -mode=retag doubles every json tag into a
		// matching yaml tag), so every generated snake_case field is reachable
		// by its yaml tag — the former json fallback is redundant (R3). The only
		// hand union type with json-only fields, Matcher{Op,Value}, is reached but
		// inert (its wire form is a scalar/operator-map, never op:/value: keys).
		tag := f.Tag.Get("yaml")
		name, opts, _ := strings.Cut(tag, ",")
		inline := strings.Contains(opts, "inline")
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if (f.Anonymous || inline) && ft.Kind() == reflect.Struct {
			for k, v := range flattenYamlFields(ft) {
				if _, exists := out[k]; !exists {
					out[k] = v
				}
			}
			continue
		}
		if name == "-" {
			continue
		}
		if name == "" {
			out[strings.ToLower(f.Name)] = f.Type
			continue
		}
		out[name] = f.Type
	}
	flatYamlFieldsCache[t] = out
	return out
}

// forceStringTag retags a scalar node as !!str so CUE ingests it as a string
// (mirrors yaml.v3's implicit int/bool→string coercion into a string field).
func forceStringTag(node *yaml.Node) {
	if node.Kind != yaml.ScalarNode {
		return
	}
	if node.Tag == "" || node.Tag == "!!int" || node.Tag == "!!bool" || node.Tag == "!!float" {
		node.Tag = "!!str"
		node.Style = yaml.DoubleQuotedStyle
	}
}

// --- expanders (port the canonical mapping from the deleted UnmarshalYAML) ---

// expandPackageItemNode: a bare scalar `nginx` → `{name: nginx}` (PackageItem).
func expandPackageItemNode(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return nil // already mapping form
	}
	nameVal := cloneScalar(node)
	*node = *mappingNodes("name", nameVal)
	return nil
}

// expandPortSpecNode: `8080` → {port: 8080, protocol: http};
// `"tcp:5900"` → {port: 5900, protocol: tcp}; `"8080"` → {port: 8080, protocol: http}.
func expandPortSpecNode(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return nil
	}
	s := node.Value
	port := s
	proto := "http"
	if _, err := strconv.Atoi(s); err != nil {
		if before, after, ok := strings.Cut(s, ":"); ok {
			proto = before
			port = after
		}
	}
	portNode := scalarNode(port)
	portNode.Tag = "!!int"
	*node = *mappingNodes(
		"port", portNode,
		"protocol", scalarNode(proto),
	)
	return nil
}

// expandLibvirtListenersNode: graphics `listen:` shorthand → canonical list of
// {type,address,…}. scalar `127.0.0.1` → [{type:address,address:127.0.0.1}];
// a mapping → [mapping] with type inferred from address/network; a sequence →
// type inferred per element. Ports LibvirtGraphicsListeners.UnmarshalYAML.
func expandLibvirtListenersNode(node *yaml.Node) error {
	inferType := func(m *yaml.Node) {
		if m.Kind != yaml.MappingNode {
			return
		}
		hasType, hasAddr, hasNet := false, false, false
		for i := 0; i+1 < len(m.Content); i += 2 {
			switch m.Content[i].Value {
			case "type":
				hasType = true
			case "address":
				hasAddr = true
			case "network":
				hasNet = true
			}
		}
		if hasType {
			return
		}
		typ := ""
		switch {
		case hasAddr:
			typ = "address"
		case hasNet:
			typ = "network"
		default:
			return // no inferable default — CUE validation will report it
		}
		m.Content = append([]*yaml.Node{scalarNode("type"), scalarNode(typ)}, m.Content...)
	}
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Value == "" {
			*node = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			return nil
		}
		addr := cloneScalar(node)
		*node = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{
			mappingNodes("type", scalarNode("address"), "address", addr),
		}}
	case yaml.MappingNode:
		orig := *node // copy before overwrite (avoid node-in-itself cycle)
		inferType(&orig)
		*node = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{&orig}}
	case yaml.SequenceNode:
		for _, el := range node.Content {
			inferType(el)
		}
	}
	return nil
}

// expandPreemptibleNode: the token-list shorthand `[a, b]` → {holds: [a, b]}
// (PreemptibleConfig); a mapping is already canonical.
func expandPreemptibleNode(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return nil
	}
	seq := *node // copy before overwrite (avoid node-in-itself cycle)
	*node = *mappingNodes("holds", &seq)
	return nil
}

// expandTunnelNode: provider scalar shorthand → {provider, default scope}.
// `tailscale` → {provider: tailscale, private: all}; `cloudflare` →
// {provider: cloudflare, public: all}; any other scalar → {provider: <scalar>}.
// (PortScope's all-ports wire form is the scalar "all".)
func expandTunnelNode(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return nil
	}
	kv := []any{"provider", scalarNode(node.Value)}
	switch node.Value {
	case "tailscale":
		kv = append(kv, "private", scalarNode("all"))
	case "cloudflare":
		kv = append(kv, "public", scalarNode("all"))
	}
	*node = *mappingNodes(kv...)
	return nil
}

// --- yaml.Node construction helpers ---

// scalarNode is defined in migrate_description.go (reused here, R3).

func cloneScalar(n *yaml.Node) *yaml.Node {
	c := *n
	return &c
}

func mappingNodes(kv ...any) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(kv); i += 2 {
		k := kv[i].(string)
		var vn *yaml.Node
		switch v := kv[i+1].(type) {
		case string:
			vn = scalarNode(v)
		case *yaml.Node:
			vn = v
		}
		m.Content = append(m.Content, scalarNode(k), vn)
	}
	return m
}
