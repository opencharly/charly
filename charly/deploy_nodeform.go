package main

// deploy_nodeform.go — the internal-body → compact node-form deploy writer,
// core's live per-host deploy-state serializer. saveDeployState marshals a
// BundleNode to an internal-shaped body and this file emits the COMPACT
// name-first node the per-host overlay (~/.config/charly/charly.yml) is read
// back in: the kind value carries the FULL body inline (scalars, collections,
// and the `plan:` step list), and only nested/peer members become child nodes
// (their names are load-bearing). Plan steps are RESUGARED (the internal
// plugin/plugin_input pair back to the authored `<word>: <input>` sugar,
// node_desugar.go) so the written file round-trips through the loader's
// parse-time desugar instead of tripping its authored-envelope ban.
//
// The legacy `target:` key is dropped, its classification preserved by
// bundleDiscForEntity. Comment-preserving (yaml.v3 node API).

import (
	"gopkg.in/yaml.v3"
)

// bundleCrossRefKeys are the bundle-value scalar keys that NAME another top-level
// entity (the key equals the referenced entity's kind).
var bundleCrossRefKeys = map[string]bool{
	"box": true, "vm": true, "k8s": true, "local": true, "android": true,
}

// migrateDeployEntity rewrites a deploy/check entity body into a compact
// node-form node: the whole body stays INLINE in the kind value (plan steps
// resugared); nested/peer members become child nodes (recursive; discriminator
// inferred by bundleDiscForEntity). The legacy `target:` key is dropped.
func migrateDeployEntity(name string, body *yaml.Node) *yaml.Node {
	content := &yaml.Node{Kind: yaml.MappingNode}
	value := &yaml.Node{Kind: yaml.MappingNode}
	disc := bundleDiscForEntity(body)
	content.Content = append(content.Content, scalarNode(disc), value)
	for i := 0; i+1 < len(body.Content); i += 2 {
		k, v := body.Content[i], body.Content[i+1]
		switch {
		case k.Value == "nested" || k.Value == "peer":
			for j := 0; j+1 < len(v.Content); j += 2 {
				mn, mb := v.Content[j], v.Content[j+1]
				content.Content = append(content.Content, scalarNode(mn.Value), migrateDeployEntity(mn.Value, mb))
			}
		case k.Value == "name" || k.Value == "target":
			// dropped (name → the node key; target → the discriminator)
		case k.Value == "plan":
			resugarPlan(v)
			value.Content = append(value.Content, k, v)
		default:
			value.Content = append(value.Content, k, v)
		}
	}
	return content
}

// bundleDiscForEntity picks the node-form discriminator for a deploy/check entity
// whose `target:` key is about to be dropped. A same-kind cross-ref (box/vm/local/
// k8s/android) uses `bundle:` (buildBundleNode infers the workload target from it);
// the SAVE path marshals BundleNode.Target, so the disc is that target — an empty
// target with a POD-WORKLOAD indicator (image/resolved_port/port) is a POD (the
// DEFAULT substrate), and an empty target with NO workload is a targetless deploy
// GROUP (`host` is the pre-rename spelling of `local`).
func bundleDiscForEntity(body *yaml.Node) string {
	if body != nil {
		for i := 0; i+1 < len(body.Content); i += 2 {
			if bundleCrossRefKeys[body.Content[i].Value] {
				return "bundle"
			}
		}
	}
	switch t := scalarFieldValue(body, "target"); t {
	case "host":
		return "local"
	case "":
		// An empty target with a POD-WORKLOAD indicator (an image: field, a resolved pod-port
		// map, or an authored port:) is a POD — the DEFAULT substrate — NOT a targetless group.
		// A `group:` deploy carries only MEMBERS and no own workload; misclassifying an
		// image-backed pod as a group writes its pod-only resolved_port under `group:`, which
		// #GroupInput rejects at the next load (the 2026-07 `charly config <image-ref>` config
		// corruption). A truly targetless deploy (members only, no workload) stays a group.
		if findMappingValue(body, "image") != nil ||
			findMappingValue(body, "resolved_port") != nil ||
			findMappingValue(body, "port") != nil {
			return "pod"
		}
		return "group"
	default:
		return t // pod | vm | k8s | local | android
	}
}

// scalarFieldValue returns the scalar value of key in m, or "" when absent / non-scalar.
func scalarFieldValue(m *yaml.Node, key string) string {
	if v := findMappingValue(m, key); v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}
