package main

// deploy_nodeform.go — the legacy-body → node-form deploy writer, core's live
// per-host deploy-state serializer. saveDeployState marshals a BundleNode to a
// legacy-shaped body and runs it through migrateDeployEntity to emit the unified
// name-first node-form the per-host overlay (~/.config/charly/charly.yml) is read
// back in. This is RUNTIME writer machinery, NOT a migration — it relocated into
// core from the retired migration module (which fronted the historical unified-node
// migration) when that module folded away; the shape it emits is the current
// node-form, so the two can no longer drift because there is only one copy.
//
// The transform: scalars (disposable/host/box/vm/…) stay in the kind value; nested/
// peer members become resource child nodes (recursive); every other non-scalar
// (env/port/add_candy/iterate/…) + each plan step become child nodes; the legacy
// `target:` key is dropped, its classification preserved by bundleDiscForEntity.
// Comment-preserving (yaml.v3 node API). Reuses core's scalarNode/mapValue/
// findMappingValue/dataKeySet helpers.

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// bundleCrossRefKeys are the bundle-value scalar keys that NAME another top-level
// entity (the key equals the referenced entity's kind).
var bundleCrossRefKeys = map[string]bool{
	"box": true, "vm": true, "k8s": true, "local": true, "android": true,
}

// migrateDeployEntity rewrites a deploy/check entity body into a node-form node:
// scalars stay in the value; nested/peer members become resource child nodes
// (recursive; discriminator inferred by bundleDiscForEntity); other non-scalars +
// plan steps become child nodes. The legacy `target:` key is dropped.
func migrateDeployEntity(name string, body *yaml.Node) *yaml.Node {
	content := &yaml.Node{Kind: yaml.MappingNode}
	value := &yaml.Node{Kind: yaml.MappingNode}
	disc := bundleDiscForEntity(body)
	content.Content = append(content.Content, scalarNode(disc), value)
	used := map[string]bool{disc: true}
	for i := 0; i+1 < len(body.Content); i += 2 {
		k, v := body.Content[i], body.Content[i+1]
		if k.Value == "nested" || k.Value == "peer" {
			for j := 0; j+1 < len(v.Content); j += 2 {
				mn, mb := v.Content[j], v.Content[j+1]
				content.Content = append(content.Content, scalarNode(mn.Value), migrateDeployEntity(mn.Value, mb))
				used[mn.Value] = true // a member name reserves a child key
			}
		}
	}
	explodeFields(name, body, value, content, used, map[string]bool{"target": true, "nested": true, "peer": true})
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

// explodeFields splits an entity body: SCALAR fields append to value; every
// dataKeySet field becomes a child `<name>-<key>: {<key>: <value>}`; each plan step
// becomes a child step node; `name` and any skip key are dropped. used tracks the
// child-node names already taken so a data-child name and a step id can't collide.
func explodeFields(name string, body, value, content *yaml.Node, used, skip map[string]bool) {
	for i := 0; i+1 < len(body.Content); i += 2 {
		k, v := body.Content[i], body.Content[i+1]
		switch {
		case k.Value == "name" || (skip != nil && skip[k.Value]):
			// dropped (name → node key; target/nested/peer handled elsewhere)
		case k.Value == "plan":
			appendStepChildren(name, v, content, used)
		case dataKeySet[k.Value]:
			child := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{k, v}}
			content.Content = append(content.Content, scalarNode(uniqueChildName(used, name+"-"+k.Value)), child)
		default:
			value.Content = append(value.Content, k, v) // scalar
		}
	}
}

// appendStepChildren turns each plan step into a child step node, keyed by the
// step's id when present, else `<entity>-step-<index>`, disambiguated against used.
func appendStepChildren(entity string, plan, content *yaml.Node, used map[string]bool) {
	if plan == nil || plan.Kind != yaml.SequenceNode {
		return
	}
	for idx, step := range plan.Content {
		content.Content = append(content.Content, scalarNode(uniqueChildName(used, stepNodeName(entity, step, idx))), step)
	}
}

// stepNodeName names a step child: its `id` if set, else `<entity>-step-<index>`.
func stepNodeName(entity string, step *yaml.Node, idx int) string {
	if id := mapValue(step, "id"); id != nil && id.Value != "" {
		return id.Value
	}
	return fmt.Sprintf("%s-step-%d", entity, idx)
}

// uniqueChildName returns desired if free under the parent, else the first free
// `desired-2` / `desired-3` / … variant, recording the choice in used.
func uniqueChildName(used map[string]bool, desired string) string {
	if !used[desired] {
		used[desired] = true
		return desired
	}
	for i := 2; ; i++ {
		c := fmt.Sprintf("%s-%d", desired, i)
		if !used[c] {
			used[c] = true
			return c
		}
	}
}

// scalarFieldValue returns the scalar value of key in m, or "" when absent / non-scalar.
func scalarFieldValue(m *yaml.Node, key string) string {
	if v := findMappingValue(m, key); v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}
