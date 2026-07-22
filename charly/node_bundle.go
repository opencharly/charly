package main

// node_bundle.go — the bundle / resource-member builder for the unified node-form
// model. A `bundle` node (or any resource node carrying resource members) becomes
// a BundleNode tree:
//   - the discriminator VALUE carries the deploy config (box/vm/… cross-ref +
//     port/env/volume/security/disposable/tunnel/…), decoded into the node;
//   - RESOURCE children become members — alongside siblings (Peer) under a
//     `bundle` group, inside-venue children (Nested) under a resource (pod-in-vm);
//   - STEP children become Plan steps whose venue is their position in the tree.
// Cross-member addressing is ${HOST:<member>} (resolved by position; see
// check_members.go). NO data-node children here (deploy config is value-carried),
// so #BundleArm narrows children to resources + steps.

import (
	"fmt"

	"github.com/opencharly/sdk/spec"

	"gopkg.in/yaml.v3"
)

// buildBundleNodeInto builds gn into a BundleNode and registers it in the
// Deploy (bundle) map.
func buildBundleNodeInto(gn *genericNode, uf *UnifiedFile) error {
	dn, err := buildBundleNode(gn)
	if err != nil {
		return err
	}
	ensureMap(&uf.Bundle)
	uf.Bundle[gn.name] = *dn
	return nil
}

// buildBundleNode recursively builds a BundleNode from a bundle/resource node. The
// discriminator value carries the deploy config; inline STEP children (checks) fold
// into the bundle's plan via decodeNodeValue (the assembler); ENTITY children are
// RESOURCE members (deploy-into / alongside).
func buildBundleNode(gn *genericNode) (*spec.BundleNode, error) {
	var dn spec.BundleNode
	if err := decodeNodeValue(gn, &dn); err != nil {
		return nil, err
	}
	// EDGE-INHERIT cutover B: the substrate kind at the EDGE is the target directly
	// (no inference from a cross-ref). group:/host: are targetless venues.
	dn.Target = bundleTargetForDisc(gn.disc)
	// A scalar discriminator value (`vm: pg-vm` / `pod: img`) is the deploy's
	// cross-ref: pod → the image it runs; vm/k8s/local/android → the same-kind
	// template it inherits (`from:`).
	if gn.discValue != nil && gn.discValue.Kind == yaml.ScalarNode {
		setBundleCrossRef(&dn, gn.disc, gn.discValue.Value)
	}

	children, err := buildResourceMemberChildren(gn)
	if err != nil {
		return nil, err
	}
	for name, member := range children {
		// A targetless GROUP (no own workload target) places members ALONGSIDE
		// (shared net → Peer); a WORKLOAD places its resource children INSIDE its
		// venue (deploy-into → Nested).
		if dn.Target == "" {
			if dn.Members == nil {
				dn.Members = map[string]*spec.BundleNode{}
			}
			dn.Members[name] = member
		} else {
			if dn.Children == nil {
				dn.Children = map[string]*spec.BundleNode{}
			}
			dn.Children[name] = member
		}
	}
	return &dn, nil
}

// buildResourceMemberChildren decodes gn's RESOURCE-MEMBER entity children into a
// name→*BundleNode map via the SAME buildBundleNode recursion — the SINGLE source of
// truth for authored member-tree decode (R3). It is called by buildBundleNode (the
// in-proc builtin path, which then partitions into Members/Children by the node's
// Target) AND by runPluginKind (the EXTERNAL structural-kind path — F5 authored-member
// input-threading — which threads this decoded subtree to the plugin's OpLoad via
// op.Env so the plugin attaches it to its spec.Deploy reply). Data + step children are
// NOT members — they fold into the node body via decodeNodeValue. A non-resource entity
// child is a hard error (deploy/resource children must be pod/vm/k8s/local/android/group).
func buildResourceMemberChildren(gn *genericNode) (map[string]*spec.BundleNode, error) {
	var out map[string]*spec.BundleNode
	for _, rk := range gn.children {
		// Data + step children are folded into the node body by decodeNodeValue;
		// only sub-ENTITY children are resource members.
		if rk.discClass != "entity" {
			continue
		}
		if !isResourceDisc(rk.disc) {
			return nil, fmt.Errorf("node %q: a %q child %q is not a resource member (deploy/resource children must be pod/vm/k8s/local/android)", gn.name, rk.disc, rk.name)
		}
		member, err := buildBundleNode(rk)
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = map[string]*spec.BundleNode{}
		}
		out[rk.name] = member
	}
	return out, nil
}

// isResourceDisc reports whether a discriminator names a deploy-substrate kind
// (the markers of a bundle member / bundle-shaped node) — the CUE-derived
// resourceKindSet (#ResourceKind), OR a recognized external DEPLOY substrate word
// (a registered/pre-scanned out-of-process deploy provider, e.g. `exampledeploy`),
// so a deploy whose edge is an external target is built as a bundle node.
func isResourceDisc(d string) bool {
	return resourceKindSet[d] || recognizedDeploySubstrate(d)
}

// bundleTargetForDisc maps a node discriminator to the BundleNode Target — DATA-driven via
// deployTraitsFor (P9's plugin-declared #DeployTraits, the D-clause fact every substrate word
// resolves against), never a kind-word switch (the boundary law's self-test): a word with no
// declared deploy traits is TARGETLESS (`group` — the only such word today; a plugin-declared
// external deploy substrate DOES carry traits, the Venue="none" external-in-place default).
func bundleTargetForDisc(d string) string {
	if deployTraitsFor(d) == nil {
		return "" // targetless (e.g. group — no own workload target)
	}
	return d // pod | vm | k8s | local | android | an external deploy substrate word
}

// setBundleCrossRef sets the deploy's cross-ref from a scalar discriminator value
// (EDGE-INHERIT cutover B): DATA-driven via deployTraitsFor's ImageBacked trait (declared true
// for pod alone, per the canonical #DeployTraits table) rather than a kind-word switch — an
// image-backed substrate's scalar is the IMAGE it runs; every other substrate's scalar is the
// same-kind template it inherits (`from:`). A targetless word (traits == nil) sets neither.
func setBundleCrossRef(dn *spec.BundleNode, disc, ref string) {
	traits := deployTraitsFor(disc)
	if traits == nil {
		return
	}
	if traits.ImageBacked {
		dn.Image = ref
	} else {
		dn.From = ref
	}
}
