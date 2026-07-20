package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/spec"
)

// namespace.go — the Go-inspired hierarchical-namespace resolver.
//
// The `import:` statement (see unified.go) mounts another project under a child
// namespace (`import: [{cachyos: '@github.com/opencharly/distro-cachyos:vTAG'}]`).
// Entries in that project are then referenced QUALIFIED — `base: cachyos.cachyos`,
// `builder: {pixi: charly.arch-builder}` — rather than flat-merged into the importing
// project's global per-kind maps.
//
// Resolution is namespace-relative (Go package-member semantics): a bare ref
// inside namespace `cachyos` resolves within cachyos first; a qualified ref
// `charly.arch` inside cachyos descends into cachyos's own `charly` namespace. The
// resolver below walks Config.Namespaces (projected from UnifiedFile.Namespaces).
//
// Inheritance across a namespace boundary:
//   - distro:/build: are VALUES (tags, formats) → inherited across namespaces.
//   - builder: is a map of REFS relative to the base's namespace → NOT copied
//     across a boundary (a base-namespace-relative ref like `charly.arch-builder`
//     would dangle in a consumer where that namespace doesn't exist).
//     Instead, the consumer's builder is resolved DISTRO-KEYED in
//     ResolveBox (charly/config.go:distroBuilderMap): an image's resolved
//     distro (which DOES cross the boundary) selects the builder map of the
//     root-namespace image that owns that distro (e.g. base.yml's `arch` →
//     arch-builder), whose bare refs resolve in the importing namespace. So a
//     cachyos/Arch image auto-gets arch-builder with no per-image declaration,
//     and no namespace-relative ref ever leaks.

// splitNamespaceRef/leafName are now sdk/spec.SplitNamespaceRef / spec.LeafName
// (the generic namespace-ref VOCAB — pure string predicates with a consumer
// set wider than the loader cone: config.go, k8s_deploy_preresolve.go,
// refs.go). leafName is paired with resolveBoxRef's returned namespace
// Config: together they give the key under which the resolved entity lives
// in that Config's Box map.

// resolveBoxRef resolves a (possibly qualified) box name to its BoxConfig
// and the Config (namespace context) it lives in. Bare names resolve in c;
// `ns.name` descends into c.Namespaces[ns] recursively.
func (c *Config) resolveBoxRef(ref string) (spec.BoxConfig, *Config, bool) {
	if ns, rest, ok := spec.SplitNamespaceRef(ref); ok {
		sub, ok := c.Namespaces[ns]
		if !ok {
			return spec.BoxConfig{}, nil, false
		}
		return sub.resolveBoxRef(rest)
	}
	img, ok := c.BoxConfig(ref)
	if !ok {
		return spec.BoxConfig{}, nil, false
	}
	return img, c, true
}

// findBoxByLeaf searches for a box whose leaf (unqualified) name equals
// `leaf` — first in c's own root box map, then recursively in each imported
// namespace (deterministic alias order). Returns the fully-qualified ref under
// which the box is reachable from c (bare for a root hit, `ns.<...>` for a
// namespaced hit), or ok=false when no box with that leaf exists anywhere in
// the namespace tree.
//
// This is the DISCOVERY dual of resolveBoxRef: resolveBoxRef resolves a
// KNOWN qualified ref by descent, whereas findBoxByLeaf discovers the
// qualification for a bare leaf that may live in a namespace. ensure-image's
// build-fallback needs it because it only has the basename of a full registry
// ref (e.g. `arch-builder` extracted from
// `ghcr.io/opencharly/arch-builder:<tag>`) and must find that the box lives
// under the `charly` namespace to build it locally.
func (c *Config) findBoxByLeaf(leaf string) (string, bool) {
	if leaf == "" {
		return "", false
	}
	if _, ok := c.Box[leaf]; ok {
		return leaf, true
	}
	aliases := make([]string, 0, len(c.Namespaces))
	for ns := range c.Namespaces {
		aliases = append(aliases, ns)
	}
	sort.Strings(aliases)
	for _, ns := range aliases {
		if q, ok := c.Namespaces[ns].findBoxByLeaf(leaf); ok {
			return ns + "." + q, true
		}
	}
	return "", false
}

// resolveLocalRef resolves a (possibly qualified) kind:local template ref.
func (c *Config) resolveLocalRef(ref string) (*ResolvedLocal, bool) {
	if ns, rest, ok := spec.SplitNamespaceRef(ref); ok {
		sub, ok := c.Namespaces[ns]
		if !ok {
			return nil, false
		}
		return sub.resolveLocalRef(rest)
	}
	body, ok := c.Local[ref]
	if !ok {
		return nil, false
	}
	r, err := resolveLocalViaPlugin(body)
	if err != nil || r == nil {
		return nil, false
	}
	return r, true
}

// resolveNamespacedBases pulls every namespace-qualified base referenced by the
// already-resolved local image set into `out` (keyed by the fully-qualified
// name), resolving each within its own namespace context. Iterates to a fixpoint
// because a pulled-in image may itself reference a (deeper) namespaced base.
func (c *Config) resolveNamespacedBases(out map[string]*buildkit.ResolvedBox, calverTag, dir string, opts ResolveOpts) error {
	for {
		var todo []string
		add := func(ref string) {
			if _, ok := out[ref]; ok {
				return
			}
			if _, _, qualified := spec.SplitNamespaceRef(ref); qualified {
				todo = append(todo, ref)
			}
		}
		for _, ri := range out {
			if !ri.IsExternalBase {
				add(ri.Base)
			}
			// Qualified builder refs (e.g. a submodule image's
			// `builder: {pixi: charly.arch-builder}`) are pulled in so the generator
			// can resolve the builder stage's FROM — but ONLY for images that
			// actually have candies to build. A candyless base (e.g. cachyos.cachyos)
			// needs no builder, and its builder map is namespace-relative to ITS
			// own namespace (so the ref would not resolve from the root context).
			// A local/layered image's builder refs ARE relative to the config
			// being resolved, so they resolve correctly.
			if len(ri.Candy) > 0 {
				for _, b := range ri.Builder.AllBuilder() {
					add(b)
				}
			}
		}
		if len(todo) == 0 {
			return nil
		}
		for _, qref := range todo {
			if _, ok := out[qref]; ok {
				continue
			}
			if err := c.pullNamespacedBox(c, qref, "", calverTag, dir, opts, out); err != nil {
				return err
			}
		}
	}
}

// pullNamespacedBox resolves `ref` (possibly qualified, relative to base
// Config `from`) and stores it in `out` under its fully-qualified key
// (keyPrefix + descended namespaces + leaf). Re-keys the entry's own internal
// base so the build graph references the fully-qualified ancestor, and recurses
// to pull that ancestor too.
func (c *Config) pullNamespacedBox(from *Config, ref, keyPrefix, calverTag, dir string, opts ResolveOpts, out map[string]*buildkit.ResolvedBox) error {
	cur := from
	var curPrefix strings.Builder
	curPrefix.WriteString(keyPrefix)
	for {
		ns, rest, qualified := spec.SplitNamespaceRef(ref)
		if !qualified {
			break
		}
		child, ok := cur.Namespaces[ns]
		if !ok {
			return fmt.Errorf("import namespace %q not found (resolving %q)", ns, keyPrefix+ref)
		}
		cur = child
		curPrefix.WriteString(ns + ".")
		ref = rest
	}
	fullKey := curPrefix.String() + ref
	if _, ok := out[fullKey]; ok {
		return nil
	}
	if _, ok := cur.Box[ref]; !ok {
		return fmt.Errorf("imported image %q not found in namespace", fullKey)
	}
	ri, err := cur.ResolveBox(ref, calverTag, dir, opts)
	if err != nil {
		return fmt.Errorf("resolving imported image %q: %w", fullKey, err)
	}
	// Re-qualify EVERY by-name ref to another image that the build graph later
	// resolves. A pulled image's refs are namespace-relative to `cur` (the
	// namespace it was authored in); left untouched they get re-resolved from
	// the ROOT config — where `cur`'s own namespaces (e.g. `charly`) don't exist —
	// yielding `import namespace "charly" not found`. Prefixing with curPrefix
	// (`charly.arch-builder` → `selkies.charly.arch-builder`) makes each ref resolvable
	// from root AND matches the key pullNamespacedBox stores the target under.
	//
	// The set of by-name image refs is the SINGLE source of truth in
	// boxDirectDeps (graph.go): base + format builders + bootstrap builder.
	// Keep this re-qualification in lockstep with that function — any new
	// by-name image ref added there must be re-qualified here too.
	requalify := func(r string) string {
		if r == "" {
			return r
		}
		return curPrefix.String() + r
	}
	if len(ri.Builder) > 0 {
		rk := make(buildkit.BuilderMap, len(ri.Builder))
		for format, b := range ri.Builder {
			rk[format] = requalify(b)
		}
		ri.Builder = rk
	}
	ri.BootstrapBuilderImage = requalify(ri.BootstrapBuilderImage)
	if ri.IsExternalBase {
		out[fullKey] = ri
		return nil
	}
	// Internal base within `cur` — re-qualify it (drives the recursion below,
	// so handled here rather than in the generic block above), store, recurse.
	origBase := ri.Base
	ri.Base = requalify(origBase)
	out[fullKey] = ri
	return c.pullNamespacedBox(cur, origBase, curPrefix.String(), calverTag, dir, opts, out)
}
