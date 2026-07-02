package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// BundleConfig represents per-machine deployment overrides (~/.config/charly/charly.yml).
// Only runtime/deployment fields are supported — build-time fields are structurally excluded.
//
// Schema v4: the top-level map key is `deployment:` (singular, flat). The
// legacy `images:` / `deployments.images.*` nesting is gone — all target
// kinds (host / vm / pod / k8s) live under the single `deployment:` map.
type BundleConfig struct {
	Provides *ProvidesConfig       `yaml:"provides,omitempty" json:"provides,omitempty"`
	Bundle   map[string]BundleNode `yaml:"deploy" json:"deploy"`
	// Sidecar carries the project's sidecar-template library (the embedded
	// default set merged with any project-declared root sidecar: entries).
	// Projected from the UnifiedFile.Sidecars() accessor (over PluginKinds["sidecar"])
	// by ProjectBundleConfig(); deploy-time resolution merges these UNDER each deploy
	// node's own sidecar overrides.
	Sidecar map[string]SidecarDef `yaml:"sidecar,omitempty" json:"sidecar,omitempty"`
}

// ToShellEntry converts a charly.yml overlay into the LabelShell
// ShellEntry shape consumed by MergeDeployShell.
func shellOverlayToEntry(o *DeployShellOverlay) ShellEntry {
	entry := ShellEntry{
		Origin:   o.Origin,
		ID:       o.ID,
		Priority: o.Priority,
	}
	if !o.Skip {
		hasGeneric := o.Init != "" || len(o.PathAppend) > 0 || o.Path != ""
		if hasGeneric {
			entry.Generic = &ShellSpec{
				Init:       o.Init,
				PathAppend: append([]string(nil), o.PathAppend...),
				Path:       o.Path,
			}
		}
		if len(o.ByShell()) > 0 {
			entry.ByShell = make(map[string]*ShellSpec, len(o.ByShell()))
			for k, v := range o.ByShell() {
				if v == nil {
					continue
				}
				entry.ByShell[k] = &ShellSpec{
					Init:       v.Init,
					PathAppend: append([]string(nil), v.PathAppend...),
					Path:       v.Path,
				}
			}
		}
	}
	// Skip == true → leave Generic/ByShell nil; MergeDeployShell's
	// replaceShellEntryByID treats both-nil as the "drop matched entry"
	// signal.
	return entry
}

// WalkPreOrder invokes fn on this node, then recurses into every
// child in sorted key order. Pre-order is the add-order semantic: a
// parent's environment must exist before its children can run inside
// it, so the caller applies deploys root-first.
//
// fn receives the full dotted path to each node (e.g. "stack.web.db").
// The root path argument is prepended; callers pass the node's own
// key as `path`.
//
// When fn returns a non-nil error, traversal stops immediately and
// the error propagates.
func bundleWalkPreOrder(n *BundleNode, path string, fn func(path string, node *BundleNode) error) error {
	if n == nil {
		return nil
	}
	if err := fn(path, n); err != nil {
		return err
	}
	for _, k := range sortedNestedKeys(n.Children) {
		childPath := k
		if path != "" {
			childPath = path + "." + k
		}
		if err := bundleWalkPreOrder(n.Children[k], childPath, fn); err != nil {
			return err
		}
	}
	return nil
}

// WalkPostOrder invokes fn on every child (recursively, post-order)
// before invoking fn on this node. Post-order is the delete-order
// semantic: a child must be torn down while its parent environment
// is still alive, so the caller reverses leaves first.
func bundleWalkPostOrder(n *BundleNode, path string, fn func(path string, node *BundleNode) error) error {
	if n == nil {
		return nil
	}
	for _, k := range sortedNestedKeys(n.Children) {
		childPath := k
		if path != "" {
			childPath = path + "." + k
		}
		if err := bundleWalkPostOrder(n.Children[k], childPath, fn); err != nil {
			return err
		}
	}
	return fn(path, n)
}

// ResolveNodePath walks roots[path0].Children[path1]...[pathN] and
// returns the targeted node plus its parent chain (root-first,
// excluding the target itself). Returns a descriptive error when any
// path segment is missing so the user sees which segment doesn't
// exist.
//
// An empty path is invalid — callers dispatch to
// WalkPreOrder/WalkPostOrder against a named root instead of
// resolving "".
func ResolveNodePath(roots map[string]BundleNode, path string) (*BundleNode, []*BundleNode, error) {
	parts := splitDottedPath(path)
	if len(parts) == 0 {
		return nil, nil, fmt.Errorf("empty or malformed deployment path %q", path)
	}
	rootName := parts[0]
	rootEntry, ok := roots[rootName]
	if !ok {
		return nil, nil, fmt.Errorf("no deployment named %q", rootName)
	}
	current := &rootEntry
	var ancestors []*BundleNode
	for i := 1; i < len(parts); i++ {
		ancestors = append(ancestors, current)
		next, ok := current.Children[parts[i]]
		if !ok {
			prefix := strings.Join(parts[:i], ".")
			return nil, nil, fmt.Errorf("no child %q under %q", parts[i], prefix)
		}
		current = next
	}
	return current, ancestors, nil
}

// splitDottedPath splits a dotted deployment path into segments. An
// empty input or a path with any empty segment (leading/trailing/
// doubled dots) yields nil so callers can flag the error at their
// layer with the original offending path string.
func splitDottedPath(path string) []string {
	if path == "" {
		return nil
	}
	out := strings.Split(path, ".")
	if slices.Contains(out, "") {
		return nil
	}
	return out
}

// sortedNestedKeys returns the keys of a children map in deterministic
// order so traversal produces stable output across runs.
func sortedNestedKeys(children map[string]*BundleNode) []string {
	out := make([]string, 0, len(children))
	for k := range children {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// bedCheckLiveRefs returns the ordered `charly check live` targets for a bed: the
// substrate itself first, then each nested child as a sorted dotted path. This
// is the pure list `charly check run` walks so a nested pod's BAKED candy/box check
// (e.g. the selkies candy's encoder + frame checks on a nested selkies-kde pod)
// is exercised against its real venue through the chain — not just the parent
// substrate. Without the nested entries, `charly check run` deploys nested children
// but never evaluates them. Pure + unit-tested.
func bedCheckLiveRefs(name string, children map[string]*BundleNode) []string {
	refs := []string{name}
	for _, k := range sortedNestedKeys(children) {
		// A nested child gets its own `charly check live <parent>.<child>` hop ONLY
		// if it is an independently-resolvable venue (a pod/vm/local child with
		// its own image/host that the chain can reach). A `target: android`
		// child shares the parent pod's venue and has NO own image — its
		// app-presence checks are baked into the parent's android-emulator-layer
		// and already run in the parent ref. `charly check live` has no android
		// dotted-path branch, so a hop for it would wrongly resolve to a
		// non-existent `charly-<parent>.device` container. Skip android children.
		if c := children[k]; c != nil && c.Target == "android" {
			continue
		}
		refs = append(refs, name+"."+k)
	}
	return refs
}

// Canonical preemption policy values. Stop is the freeing mechanism;
// Restore is when the holder is brought back.
const (
	PreemptStopShutdown   = "shutdown"   // graceful ACPI shutdown / podman stop; disk preserved (only supported value)
	PreemptRestoreAlways  = "always"     // restart the holder regardless of the claim's outcome (default)
	PreemptRestoreSuccess = "on-success" // restart only if the claim released cleanly; leave stopped on failure
)

// EffectiveStop returns the configured stop mechanism with the default.
func preemptEffectiveStop(p *PreemptibleConfig) string {
	if p == nil || p.Stop == "" {
		return PreemptStopShutdown
	}
	return p.Stop
}

// EffectiveRestore returns the configured restore policy with the default.
func preemptEffectiveRestore(p *PreemptibleConfig) string {
	if p == nil || p.Restore == "" {
		return PreemptRestoreAlways
	}
	return p.Restore
}

// ApplyTo merges install_opts settings into an EmitOpts. CLI flags
// still win — charly.yml provides defaults, not overrides. Nil
// receiver is a no-op.
func installOptsApplyTo(o *InstallOptsConfig, opts EmitOpts) EmitOpts {
	if o == nil {
		return opts
	}
	if !opts.WithServices {
		opts.WithServices = o.WithServices
	}
	if !opts.AllowRepoChanges {
		opts.AllowRepoChanges = o.AllowRepoChanges
	}
	if !opts.AllowRootTasks {
		opts.AllowRootTasks = o.AllowRootTasks
	}
	if !opts.SkipIncompatible {
		opts.SkipIncompatible = o.SkipIncompatible
	}
	if !opts.Verify {
		opts.Verify = o.Verify
	}
	if opts.BuilderImageOverride == "" {
		opts.BuilderImageOverride = o.BuilderImage
	}
	return opts
}

// deployKey returns the charly.yml map key for an image, optionally qualified by instance.
// Base images use just the image name; instances use "image/instance".
func deployKey(boxName, instance string) string {
	if instance == "" {
		return boxName
	}
	return boxName + "/" + instance
}

// canonicalizeDeployArg splits Pattern A "<base>/<instance>" CLI positional
// args into their component (image, instance) pair. Idempotent: if the input
// is already split (instance != "") or contains no slash, returns as-is.
// Pattern B (FQ ref containing "/") is identified by presence of "@" or ":"
// suffix on the leftmost segment OR a registry-host pattern (contains "."
// before the first "/") and passed through untouched.
//
// MUST be called at the top of every CLI verb that takes a positional
// deploy-arg (`charly config`, `charly start`, `charly stop`, `charly shell`, `charly logs`,
// `charly update`, `charly status`, `charly remove`) — before any downstream code reads
// c.Image or c.Instance. Without this, Pattern A instance deploys leak
// past the canonicalization boundary and downstream code conflates the
// deploy key with the image short-name (see Bug 2/3 RCA notes —
// MergeDeployOntoMetadata composes wrong key, port/env overlays drop).
func canonicalizeDeployArg(arg, instance string) (box, inst string) {
	if instance != "" || arg == "" {
		return arg, instance
	}
	if !strings.Contains(arg, "/") {
		return arg, ""
	}
	// Registry-qualified ref (Pattern B): contains "." in the first segment
	// (registry host like ghcr.io) or "@" anywhere (digest pin) or the
	// trailing segment carries ":tag". Pass through.
	first := arg
	if before, _, ok := strings.Cut(arg, "/"); ok {
		first = before
	}
	if strings.Contains(first, ".") || strings.Contains(arg, "@") || strings.Contains(arg[strings.LastIndex(arg, "/"):], ":") {
		return arg, ""
	}
	return parseDeployKey(arg)
}

// parseDeployKey splits a charly.yml map key back into image name and instance.
// "selkies-desktop" → ("selkies-desktop", "")
// "selkies-desktop/foo" → ("selkies-desktop", "foo")
func parseDeployKey(key string) (boxName, instance string) {
	if before, after, ok := strings.Cut(key, "/"); ok {
		return before, after
	}
	return key, ""
}

// resolveDeployKeyToBox maps a deploy-key name to the `box:` field of
// its deploy entry. User (~/.config/charly/charly.yml) wins over project
// (charly.yml/check.yml) — the same precedence the check runner and
// `charly config` use. Returns "" when no entry declares a box for the key
// (caller decides the fallback). Implements the Pattern-B (arbitrary
// deploy-key + version-pin) and kind:check-bed (key != box) lookups.
// See /charly-core:deploy "Two supported deploy patterns".
func resolveDeployKeyToBox(key, instance string) string {
	if key == "" {
		return ""
	}
	// User-side first.
	if dc := loadDeployConfigForRead("resolveDeployKeyToBox"); dc != nil {
		if entry, ok := dc.Bundle[deployKey(key, instance)]; ok && entry.Image != "" {
			return entry.Image
		}
		if entry, ok := dc.Bundle[key]; ok && entry.Image != "" {
			return entry.Image
		}
	}
	// Project-level fallback.
	if dir, err := os.Getwd(); err == nil {
		if uf, ok, _ := LoadUnified(dir); ok && uf != nil {
			if pc := uf.ProjectBundleConfig(); pc != nil {
				if entry, ok := pc.Bundle[key]; ok && entry.Image != "" {
					return entry.Image
				}
			}
		}
	}
	return ""
}

// resolveDeployResolvedImage returns the concrete overlay image ref a pod
// deploy's add_candy: overlay build persisted (BundleNode.ResolvedImage), or ""
// when none is recorded. This is charly-written PER-HOST runtime state (written
// by PrepareVenue via saveDeployState, like resolved_port), so it is read ONLY
// from the per-host charly.yml — never the project config, which carries no
// resolved_image. When set, resolveDeployRef deploys THIS exact overlay (with
// the add_candy: layers) instead of re-resolving the base image: short-name by
// a CalVer sort the overlay alias can lose to the base on a same-minute build.
func resolveDeployResolvedImage(key, instance string) string {
	if key == "" {
		return ""
	}
	if dc := loadDeployConfigForRead("resolveDeployResolvedImage"); dc != nil {
		if entry, ok := dc.Bundle[deployKey(key, instance)]; ok && entry.ResolvedImage != "" {
			return entry.ResolvedImage
		}
		if entry, ok := dc.Bundle[key]; ok && entry.ResolvedImage != "" {
			return entry.ResolvedImage
		}
	}
	return ""
}

// findVmDeployNode finds the BundleNode for a vm-target deploy. It is
// THE shared "which deploy entry backs this VM" lookup used by both
// `charly bundle add` (artifact-env collection) and `charly check live` (tests
// overlay), so the two never diverge. Resolution order:
//  1. by deploy NAME (the entry key) — the precise match;
//  2. by the legacy "vm:<name>" key form;
//  3. by scanning for any target:vm entry whose `vm:` field == vmName (or
//     == name) — the fallback when the caller only knows the vm entity.
//
// Keying by the deploy NAME first is load-bearing: a bed whose key differs
// from its vm entity (e.g. check-k3s-vm -> vm: k3s-vm) is found by its key,
// not mis-resolved via the vm entity name.
func findVmDeployNode(deploys map[string]BundleNode, name, vmName string) (BundleNode, bool) {
	if deploys == nil {
		return BundleNode{}, false
	}
	if name != "" {
		if e, ok := deploys[name]; ok && (e.Target == "vm" || e.From != "") {
			return e, true
		}
		if e, ok := deploys["vm:"+name]; ok {
			return e, true
		}
	}
	for _, e := range deploys {
		if e.Target == "vm" && e.From != "" && (e.From == vmName || e.From == name) {
			return e, true
		}
	}
	return BundleNode{}, false
}

// vmEntityForDeploy resolves a vm-target deploy KEY to its `vm:` cross-ref
// (the kind:vm entity name) — operator overlay first, then project config,
// via the shared findVmDeployNode. Returns "" when no entry declares a vm
// entity. THE single deploy-key→vm-entity resolver so `charly update <bed>`
// (the vm lifecycle hook's Rebuild) and any other vm-deploy consumer agree: the deploy KEY
// (e.g. check-k3s-vm) is NOT the vm entity name (k3s-vm) when they differ —
// blindly using the key breaks `charly vm create`/`destroy` for such beds.
func vmEntityForDeploy(deployName string) string {
	if deployName == "" {
		return ""
	}
	if dc := loadDeployConfigForRead("vmEntityForDeploy"); dc != nil {
		if node, ok := findVmDeployNode(dc.Bundle, deployName, ""); ok && node.From != "" {
			return node.From
		}
	}
	if dir, err := os.Getwd(); err == nil {
		if uf, ok, _ := LoadUnified(dir); ok && uf != nil {
			if pc := uf.ProjectBundleConfig(); pc != nil {
				if node, ok := findVmDeployNode(pc.Bundle, deployName, ""); ok && node.From != "" {
					return node.From
				}
			}
		}
	}
	return ""
}

// resolveDeployBoxName is THE single deploy-key→image-name resolver used
// by every deploy-mode command that starts from a deploy key (charly config /
// start / shell / check live). It returns the deploy entry's declared
// `box:` (resolveDeployKeyToBox), falling back to the key itself when
// no entry declares one (the key==image convention). Before this was
// shared, `charly config` resolved key→image but `charly start`/`charly shell`/
// `charly check live` treated the key AS the image — so a kind:check bed
// (check-jupyter-pod → jupyter) or any Pattern-B deploy resolved a
// different (wrong/unresolvable) image per command. `charly update` reaches the
// same value via its already-resolved merged-tree node (node.Image), so it
// reads that directly rather than re-loading config here.
func resolveDeployBoxName(key, instance string) string {
	if img := resolveDeployKeyToBox(key, instance); img != "" {
		return img
	}
	return key
}

// DeployedContainerNames returns hostnames for all deployed images.
// Used to enrich NO_PROXY so Chrome (which doesn't support CIDR) can bypass
// the proxy for container-to-container traffic.
func (dc *BundleConfig) DeployedContainerNames() []string {
	if dc == nil {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	for key := range dc.Bundle {
		img, inst := parseDeployKey(key)
		name := containerNameInstance(img, inst)
		if !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	sort.Strings(names)
	return names
}

// isSameBaseBox returns true if source is the same base image (with or without instance).
func isSameBaseBox(source, boxName string) bool {
	return source == boxName || strings.HasPrefix(source, boxName+"/")
}

// DeployConfigPath returns the path to the deploy overlay file.
// Package-level var for testability (same pattern as RuntimeConfigPath).
var DeployConfigPath = defaultDeployConfigPath

func defaultDeployConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determining config directory: %w", err)
	}
	return filepath.Join(configDir, "charly", "charly.yml"), nil
}

// LoadBundleConfig reads the per-host deploy overlay (~/.config/charly/charly.yml)
// through the unified loader — the SAME LoadUnified path as every project
// charly.yml. Returns nil, nil if the file doesn't exist.
//
// Every transform the old bespoke parser did — the `images:` legacy-key reject,
// the deployment-tree / required-box: / preemptible / ephemeral-naming
// validation, and the ephemeral→disposable auto-promotion — now runs INSIDE
// LoadUnified (its version gate + the deploy-validation
// block subsume the legacy check; the ephemeral/naming validators + promotion
// were consolidated there so a PROJECT charly.yml's inline deploy: entries get
// them too — R3, one path).
func LoadBundleConfig() (*BundleConfig, error) {
	path, err := DeployConfigPath()
	if err != nil {
		return nil, nil
	}
	configDir := filepath.Dir(path)

	// Host-file-existence guard: a host still on the legacy `deploy.yml`
	// filename would otherwise silently lose its overlay (LoadUnified reads
	// charly.yml only when the project is already at HEAD). Fail loud with the
	// migration hint — mirrors the old hasLegacyImagesKey safety.
	if legacy := filepath.Join(configDir, "deploy.yml"); fileExists(legacy) && !fileExists(path) {
		return nil, fmt.Errorf(
			"per-host deploy overlay at %s uses the legacy `deploy.yml` filename — rename it to charly.yml (the unified per-host config)",
			legacy,
		)
	}

	uf, ok, err := LoadUnified(configDir)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	// A present-but-empty config still returns a non-nil BundleConfig (matching
	// the old bespoke parser), so callers that range/index dc.Deploy without a
	// nil guard keep working after an overlay's last entry is removed.
	if dc := uf.ProjectBundleConfig(); dc != nil {
		return dc, nil
	}
	return &BundleConfig{}, nil
}

// OccupiedHostPorts returns the set of host ports already published by
// any deployment in dc except the named one (`excludeKey` is typically
// the deploy key for the entry currently being expanded — we want to
// allow it to keep its old allocations, not avoid them). Used by
// ResolveDeployPorts to keep auto-allocations from colliding across deploys.
func (dc *BundleConfig) OccupiedHostPorts(excludeKey string) map[int]bool {
	out := map[int]bool{}
	if dc == nil {
		return out
	}
	for key, entry := range dc.Bundle {
		if key == excludeKey {
			continue
		}
		// Prefer ResolvedPort over Port (Port may still contain "auto"
		// in another entry that hasn't been expanded yet).
		mappings := entry.ResolvedPort
		if mappings == nil {
			mappings = entry.Port
		}
		for _, m := range mappings {
			if IsAutoPort(m) {
				continue
			}
			if h, err := ParseHostPort(m); err == nil {
				out[h] = true
			}
		}
	}
	return out
}

// MergeDeployOntoMetadata applies a per-host charly.yml entry's overrides (ports,
// env, security, tunnel, secrets, …) onto label-derived image metadata.
// Field-level replace semantics.
//
// The overlay entry is keyed by deployName — the charly.yml key base the caller
// is operating on (the bed / instance / Pattern-B name), NOT meta.Image (the
// baked ai.opencharly.box short-name). For a plain deploy the two coincide,
// but a kind:check bed or a Pattern-B deploy carries a key distinct from its
// image, so the caller MUST pass its own deploy key (typically c.Image). Keying
// off meta.Image would read whichever sibling deploy merely shares the image and
// clobber this entry's explicit port:/env:/security: — e.g. a bed remapping
// 45434:11434 would lose its port to a running same-image deploy on 11434.
//
//nolint:gocyclo // field-by-field conditional overlay merge; every branch is a peer
func MergeDeployOntoMetadata(meta *BoxMetadata, dc *BundleConfig, deployName, instance string) {
	// Volume isolation runs UNCONDITIONALLY (independent of any charly.yml
	// overlay), so every distinctly-named deploy gets its own volume namespace
	// on the very first `charly config` and every run after — see
	// scopeVolumesToDeployKey.
	scopeVolumesToDeployKey(meta, deployName, instance)

	if dc == nil || dc.Bundle == nil || meta == nil {
		return
	}

	overlay, ok := dc.Bundle[deployKey(deployName, instance)]
	if !ok {
		return
	}

	if overlay.Description != "" {
		// A deploy overlay's description is purely informational — it carries no
		// status signal (the maturity rung lives on the candy `status:` field and
		// is baked into the image's ai.opencharly.status label). Keep the baked
		// meta.Status; only refresh the human-facing summary.
		meta.Info = descriptionInfo(overlay.Description)
	}
	if overlay.Tunnel != nil {
		meta.Tunnel = overlay.Tunnel
	}
	if overlay.DNS != "" {
		meta.DNS = overlay.DNS
	}
	if overlay.AcmeEmail != "" {
		meta.AcmeEmail = overlay.AcmeEmail
	}
	// Ports: prefer the persisted ResolvedPort (the auto-allocated /
	// pinned host:container mapping `charly config` computed via
	// ResolveDeployPorts). A deploy `port:` entry is only a PIN INPUT to that
	// resolution — never a wholesale replacement — so it is NOT applied here.
	// If ResolvedPort isn't set yet (deploy not configured), meta.Port keeps the
	// image-label's bare container ports (published 1:1 on 127.0.0.1 until the
	// next charly config resolves them).
	if overlay.ResolvedPort != nil {
		meta.Port = overlay.ResolvedPort
	}
	if overlay.Env != nil {
		meta.Env = overlay.Env
	}
	if overlay.Security != nil {
		// Field-level merge: overlay fields override, unset fields fall
		// through to the label-provided values. A full struct replace would
		// wipe candy defaults like shm_size when a user sets just --memory-max
		// via `charly config`.
		if overlay.Security.Privileged {
			meta.Security.Privileged = true
		}
		if len(overlay.Security.CapAdd) > 0 {
			meta.Security.CapAdd = overlay.Security.CapAdd
		}
		if len(overlay.Security.Devices) > 0 {
			meta.Security.Devices = overlay.Security.Devices
		}
		if len(overlay.Security.SecurityOpt) > 0 {
			meta.Security.SecurityOpt = overlay.Security.SecurityOpt
		}
		if overlay.Security.ShmSize != "" {
			meta.Security.ShmSize = overlay.Security.ShmSize
		}
		if overlay.Security.IpcMode != "" {
			meta.Security.IpcMode = overlay.Security.IpcMode
		}
		if overlay.Security.CgroupNS != "" {
			meta.Security.CgroupNS = overlay.Security.CgroupNS
		}
		if len(overlay.Security.GroupAdd) > 0 {
			meta.Security.GroupAdd = overlay.Security.GroupAdd
		}
		if len(overlay.Security.Mounts) > 0 {
			meta.Security.Mounts = overlay.Security.Mounts
		}
		if overlay.Security.MemoryMax != "" {
			meta.Security.MemoryMax = overlay.Security.MemoryMax
		}
		if overlay.Security.MemoryHigh != "" {
			meta.Security.MemoryHigh = overlay.Security.MemoryHigh
		}
		if overlay.Security.MemorySwapMax != "" {
			meta.Security.MemorySwapMax = overlay.Security.MemorySwapMax
		}
		if overlay.Security.Cpus != "" {
			meta.Security.Cpus = overlay.Security.Cpus
		}
	}
	if overlay.Network != "" {
		meta.Network = overlay.Network
	}
	if overlay.Engine != "" {
		meta.Engine = overlay.Engine
	}
	// Merge charly.yml secrets onto image label secrets
	if overlay.Secret != nil {
		deployByName := make(map[string]DeploySecretConfig, len(overlay.Secret))
		for _, ds := range overlay.Secret {
			deployByName[ds.Name] = ds
		}
		// Override matching secrets from image labels with charly.yml source config
		for i, ls := range meta.Secret {
			if _, ok := deployByName[ls.Name]; ok {
				// Deploy.yml provides this secret — keep the label entry
				// (the source override is used at provisioning time, not in the label)
				_ = i
			}
		}
		// Add deploy-only secrets that aren't in the image labels
		for _, ds := range overlay.Secret {
			found := false
			for _, ls := range meta.Secret {
				if ls.Name == ds.Name {
					found = true
					break
				}
			}
			if !found {
				meta.Secret = append(meta.Secret, LabelSecretEntry{
					Name:   ds.Name,
					Target: "/run/secrets/" + ds.Name,
				})
			}
		}
	}

}

// deployVolumePrefix is the named-volume prefix for a deploy: it equals the
// deploy's container name plus a dash, so EVERY distinctly-named deploy — a base
// deploy, a Pattern-B deploy, a `<base>/<instance>`, or a kind:check bed — gets
// its own volume namespace. Two deploys NEVER share a named volume unless they
// share a container name (which they can't — container names are unique). This
// is the single source of truth for volume naming; ResolveVolumeBacking,
// removeVolumes, and scopeVolumesToDeployKey all key off it.
func deployVolumePrefix(deployKey, instance string) string {
	return containerNameInstance(deployKey, instance) + "-"
}

// deployStorageDir is the per-deploy directory component for bind-auto paths and
// encrypted-volume directories. Like deployVolumePrefix it is unique per deploy
// (base vs instance vs Pattern-B vs bed), so auto-provisioned bind/encrypted
// storage is never shared across differently-named deploys. For a base deploy
// with no instance it is just the deploy key (unchanged from the historical
// layout); an instance appends "-<instance>".
func deployStorageDir(deployKey, instance string) string {
	if instance == "" {
		return deployKey
	}
	return deployKey + "-" + instance
}

// scopeVolumesToDeployKey renames meta's named-volume mounts from the
// image-derived prefix (charly-<image>-) to the deploy's own prefix
// (deployVolumePrefix), so every distinctly-named deploy ALWAYS gets volume
// mounts distinct from any other deploy of the same image — production pods,
// instances, and disposable kind:check beds alike. Before this, names were keyed
// by the baked ai.opencharly.box label, so two deploys of one image (e.g.
// the operator's immich plus a disposable immich bed, or two production pods)
// shared the SAME named volumes and could read or corrupt each other's data.
// No-op when the deploy's prefix already equals the image prefix (the common
// `charly config <image>` base deploy), so that deploy's volume names never change.
// Idempotent: re-running on already-scoped names is a no-op.
func scopeVolumesToDeployKey(meta *BoxMetadata, deployName, instance string) {
	if meta == nil || deployName == "" {
		return
	}
	newPrefix := deployVolumePrefix(deployName, instance)
	oldPrefix := "charly-" + meta.Box + "-"
	if newPrefix == oldPrefix {
		return
	}
	for i := range meta.Volume {
		if rest, ok := strings.CutPrefix(meta.Volume[i].VolumeName, oldPrefix); ok {
			meta.Volume[i].VolumeName = newPrefix + rest
		}
	}
}

// ResolveVolumeBacking splits image volumes into named volumes and bind mounts
// based on charly.yml volume configuration.
// Volumes without a deploy override remain as named volumes.
// Volumes with type=bind or type=encrypted become ResolvedBindMount.
// Deploy-only volumes (with Path set, not in labels) are also supported.
// resolveVolumeHostPath computes the host-side path for a bind/encrypted deploy
// volume. It is the single home for the host-path strategy (R3): ResolveVolumeBacking
// applies it in two passes — label-matched volumes (keyed by the short name) and
// deploy-only volumes (keyed by the config name) — that previously carried
// byte-identical copies of this switch differing only in that name argument.
//
//	encrypted + explicit Host:  <Host>/plain
//	encrypted + default:        <encStoragePath>/<storageDir>/plain  (per-deploy)
//	bind + explicit Host:       <Host>
//	bind + default:             <volumesPath>/<storageDir>/<name>    (per-deploy)
func resolveVolumeHostPath(dv DeployVolumeConfig, name, storageDir, encStoragePath, volumesPath string) string {
	switch {
	case dv.Type == "encrypted":
		if dv.Host != "" {
			return filepath.Join(expandHostHome(dv.Host), "plain")
		}
		return encryptedPlainDir(encStoragePath, storageDir, name)
	case dv.Host != "":
		return expandHostHome(dv.Host)
	default:
		return filepath.Join(volumesPath, storageDir, name)
	}
}

func ResolveVolumeBacking(boxName, instance string, labelVolumes []VolumeMount, deployVolumes []DeployVolumeConfig, home string, encStoragePath string, volumesPath string) ([]VolumeMount, []ResolvedBindMount) {
	// Index deploy volume configs by name
	deployByName := make(map[string]DeployVolumeConfig, len(deployVolumes))
	for _, dv := range deployVolumes {
		deployByName[dv.Name] = dv
	}

	// Track which deploy entries matched a label volume
	matched := make(map[string]bool)

	var volumes []VolumeMount
	var bindMounts []ResolvedBindMount

	for _, vol := range labelVolumes {
		// Extract short name from the deploy-scoped prefix (charly-<deploy>-<name>).
		shortName := strings.TrimPrefix(vol.VolumeName, deployVolumePrefix(boxName, instance))

		dv, hasOverride := deployByName[shortName]
		if hasOverride {
			matched[shortName] = true
		}

		if hasOverride && (dv.Type == "bind" || dv.Type == "encrypted") {
			hostPath := resolveVolumeHostPath(dv, shortName, deployStorageDir(boxName, instance), encStoragePath, volumesPath)
			bindMounts = append(bindMounts, ResolvedBindMount{
				Name:      shortName,
				HostPath:  hostPath,
				ContPath:  vol.ContainerPath,
				Encrypted: dv.Type == "encrypted",
			})
		} else {
			// Default: keep as named volume
			volumes = append(volumes, vol)
		}
	}

	// Add deploy-only volumes (not in any candy, must have Path)
	for _, dv := range deployVolumes {
		if matched[dv.Name] || dv.Path == "" {
			continue
		}
		containerPath := ExpandPath(dv.Path, home)
		if dv.Type == "bind" || dv.Type == "encrypted" {
			hostPath := resolveVolumeHostPath(dv, dv.Name, deployStorageDir(boxName, instance), encStoragePath, volumesPath)
			bindMounts = append(bindMounts, ResolvedBindMount{
				Name:      dv.Name,
				HostPath:  hostPath,
				ContPath:  containerPath,
				Encrypted: dv.Type == "encrypted",
			})
		} else {
			volumes = append(volumes, VolumeMount{
				VolumeName:    deployVolumePrefix(boxName, instance) + dv.Name,
				ContainerPath: containerPath,
			})
		}
	}

	return volumes, bindMounts
}

// LoadDeployFile reads a charly.yml from an arbitrary path.
func LoadDeployFile(path string) (*BundleConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var dc BundleConfig
	if err := yaml.Unmarshal(data, &dc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &dc, nil
}

// SaveBundleConfig writes a BundleConfig to the standard charly.yml
// path. Uses tempfile + os.Rename for atomic write — defense in depth
// against partial writes truncating the prior file (primary guard is
// loadDeployConfigForWrite's error propagation; this catches any
// remaining IO/marshal failure mid-write). The tempfile lives in the
// same directory as the target so rename stays on the same filesystem.
func SaveBundleConfig(dc *BundleConfig) error {
	path, err := DeployConfigPath()
	if err != nil {
		return fmt.Errorf("determining deploy config path: %w", err)
	}
	// FAIL-SAFE (data-safety): refuse to clobber a present-but-currently-
	// unloadable per-host config. A writer that loaded through the
	// error-swallowing loadDeployConfigForRead path holds a DEGRADED (empty)
	// BundleConfig whenever the on-disk file fails the loader gate (e.g. an
	// un-migrated overlay still carrying a legacy `deploy:` map — the exact
	// state the per-host migrate-path bug produced — or a deploy.yml awaiting
	// the charly.yml rename); writing that degraded config would TRUNCATE the
	// user's recoverable deploy state. Re-check the on-disk file here and abort
	// with a `charly migrate` hint instead — the bytes stay on disk for the
	// migration to recover. A clean load, an absent file, and an empty file all
	// return no error, so first-writes and ordinary saves proceed unchanged.
	// This single point protects EVERY caller (R3) — including the read-degraded
	// resolved-port / data-seeded / secret-migration write-backs in
	// config_image.go — on top of the primary loadDeployConfigForWrite gate.
	if _, lerr := LoadBundleConfig(); lerr != nil {
		return fmt.Errorf("refusing to overwrite %s — the existing per-host config fails to load (%w); fix it (or remove it to regenerate) first", path, lerr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if dc == nil {
		dc = &BundleConfig{}
	}
	// Write a unified node-form per-host charly.yml: the HEAD `version:` stamp lets
	// a re-load through LoadUnified pass the schema gate; `provides:` stays a
	// document directive; each deploy entry is a name-first node `<name>: {bundle:
	// <scalars>, <child-nodes>}` — the SAME shape the node-form loader accepts (the
	// only authoring surface). Reuses migrateDeployEntity (the legacy-body →
	// node-form transform) on each entry's marshaled struct body, so the writer can
	// never drift from the migration.
	root := &yaml.Node{Kind: yaml.MappingNode}
	root.Content = append(root.Content, scalarNode("version"), scalarNode(LatestSchemaVersion().String()))
	if dc.Provides != nil {
		pb, perr := yaml.Marshal(dc.Provides)
		if perr != nil {
			return fmt.Errorf("marshaling provides: %w", perr)
		}
		var pd yaml.Node
		if perr := yaml.Unmarshal(pb, &pd); perr != nil {
			return fmt.Errorf("re-parsing provides: %w", perr)
		}
		if len(pd.Content) == 1 {
			root.Content = append(root.Content, scalarNode("provides"), pd.Content[0])
		}
	}
	names := make([]string, 0, len(dc.Bundle))
	for n := range dc.Bundle {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		node := dc.Bundle[name]
		body, merr := marshalBundleNodeLegacy(&node)
		if merr != nil {
			return fmt.Errorf("marshaling deploy %q: %w", name, merr)
		}
		root.Content = append(root.Content, scalarNode(name), migrateDeployEntity(name, body))
	}
	data, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshaling deploy config: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".charly.yml.tmp.*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("renaming %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}

// marshalBundleNodeLegacy yaml-marshals a BundleNode into the LEGACY struct body
// shape — re-injecting the now-yaml:"-" structural fields (`target:`, `nested:`,
// `peer:`) that no longer marshal off the struct. This reproduces exactly the
// input migrateDeployEntity expects (the body it converts into node-form tree
// children), so the per-host overlay writer round-trips the deployment tree even
// though Target/Children/Members are no longer authored/marshaled fields
// (Risk 5a). Recurses so nested children + members at every depth are preserved.
func marshalBundleNodeLegacy(node *BundleNode) (*yaml.Node, error) {
	nb, err := yaml.Marshal(node)
	if err != nil {
		return nil, err
	}
	var nd yaml.Node
	if err := yaml.Unmarshal(nb, &nd); err != nil {
		return nil, err
	}
	if len(nd.Content) != 1 || nd.Content[0].Kind != yaml.MappingNode {
		// Empty/odd body — return an empty mapping so the caller still emits a node.
		return &yaml.Node{Kind: yaml.MappingNode}, nil
	}
	body := nd.Content[0]
	// target: — derived from the node's disc/cross-ref at load; re-emit it so a
	// reload re-derives the same target (also lets a group's empty target stay
	// absent rather than mis-marshaling).
	if node.Target != "" {
		body.Content = append(body.Content, scalarNode("target"), scalarNode(node.Target))
	}
	// nested: + peer: — the recursive tree. Each child/member body is itself
	// marshaled through this helper so its own structural fields survive.
	appendNodeMap := func(key string, m map[string]*BundleNode) error {
		if len(m) == 0 {
			return nil
		}
		mapNode := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range sortedMemberKeys(m) {
			childBody, cerr := marshalBundleNodeLegacy(m[k])
			if cerr != nil {
				return cerr
			}
			mapNode.Content = append(mapNode.Content, scalarNode(k), childBody)
		}
		body.Content = append(body.Content, scalarNode(key), mapNode)
		return nil
	}
	if err := appendNodeMap("nested", node.Children); err != nil {
		return nil, err
	}
	if err := appendNodeMap("peer", node.Members); err != nil {
		return nil, err
	}
	return body, nil
}

// Lookup returns the BundleNode for (deployName, instance), or
// (zero, false) when the entry is absent. Safe to call on a nil
// *BundleConfig — lets callers chain
// `loadDeployConfigForRead(...).Lookup(deployName, instance)` without a
// separate nil check. deployName is the charly.yml key base the caller is
// operating on (typically c.Image), NOT the baked image short-name — for a
// kind:check bed or Pattern-B deploy the two differ. Pass the deploy key, never
// a value derived from an image label (see MergeDeployOntoMetadata).
func (dc *BundleConfig) Lookup(deployName, instance string) (BundleNode, bool) {
	if dc == nil {
		return BundleNode{}, false
	}
	entry, ok := dc.Bundle[deployKey(deployName, instance)]
	return entry, ok
}

// LookupKey looks up a deploy entry by its full charly.yml key (e.g.
// "foo", "foo/instance", "vm:name"). Safe on nil receiver.
func (dc *BundleConfig) LookupKey(key string) (BundleNode, bool) {
	if dc == nil {
		return BundleNode{}, false
	}
	entry, ok := dc.Bundle[key]
	return entry, ok
}

// loadDeployConfigForRead loads charly.yml for read-only consumption.
// Unlike the historical `dc, _ := LoadBundleConfig()` pattern (silently
// discards validation errors → caller proceeds with nil → feature
// degrades invisibly), this helper SURFACES the load error as a stderr
// warning while still returning nil — preserving the existing caller
// nil-check contract but giving the operator visibility into why a
// command behaved as if charly.yml were absent.
//
// Sibling of loadDeployConfigForWrite — the write variant returns an
// error and callers MUST abort; the read variant returns nil and
// callers MAY continue with degraded behavior.
//
// context is a short human-readable label included in the warning
// message so the operator can trace which code path noticed the
// problem (e.g. "charly status", "config injectEnvProvides").
func loadDeployConfigForRead(context string) *BundleConfig {
	dc, err := LoadBundleConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %s: charly.yml unavailable for read: %v\n", context, err)
	}
	// NEVER return nil — a caller dereferences `dc.Deploy[...]` directly (and some
	// assign into it), so an absent config (LoadBundleConfig → (nil, nil)) or a load
	// error both degrade to an EMPTY config with a live map (image-label-driven
	// behavior), not a nil-deref / nil-map-assignment panic.
	if dc == nil {
		return &BundleConfig{Bundle: map[string]BundleNode{}}
	}
	if dc.Bundle == nil {
		dc.Bundle = map[string]BundleNode{}
	}
	return dc
}

// loadDeployConfigForWrite loads charly.yml for mutation. Unlike the
// historical `dc, _ := LoadBundleConfig()` pattern (silently discards
// validation errors → writer constructs an empty config → SaveBundleConfig
// truncates the file), this helper PROPAGATES the load error so writers
// can ABORT instead of destroying data.
//
// Cautionary tale: pre-2026-05-16 the `charly bundle add --disposable` write
// path discarded the load error. The 2026-05-12 require-image schema
// cutover widened the set of conditions under which LoadBundleConfig
// returns an error; once any pre-existing charly.yml entry failed
// validation, the next `charly bundle add` constructed a fresh empty
// BundleConfig containing only the new entry and truncated the on-disk
// file. The user's `provides:` block and unrelated deploy entries
// vanished silently. New write sites MUST use this helper.
//
// context is a short human-readable label included in the error message
// (e.g. "saveDeployState"). Returns (nil, error) when the file exists
// but failed parse/validation; (fresh empty config, nil) when the file
// doesn't exist; (parsed config, nil) on clean load.
func loadDeployConfigForWrite(context string) (*BundleConfig, error) {
	dc, err := LoadBundleConfig()
	if err != nil {
		return nil, fmt.Errorf("%s: refusing to write — charly.yml load failed: %w", context, err)
	}
	if dc == nil {
		dc = &BundleConfig{Bundle: make(map[string]BundleNode)}
	}
	if dc.Bundle == nil {
		dc.Bundle = make(map[string]BundleNode)
	}
	return dc, nil
}

// MergeDeployConfigs merges multiple DeployConfigs left-to-right. Later
// configs take precedence (field-level replace per image). The merge walks
// every yaml-tagged field of BundleNode via reflect: a field copies
// from src → dst when src's value is non-zero (string != "", slice/map/ptr
// not nil, bool != false, numeric != 0). This makes adding a new field to
// BundleNode automatically merge-correct — the pre-2026-05 hand-rolled
// per-field merge silently dropped 19+ fields (ResolvedPort, Description,
// Secret, Sidecar, Shell, Kubernetes, ForwardGpgAgent, ForwardSshAgent,
// Kind, Replica, Restart, Schedule, Resources, Expose, Storage, Probes,
// Cpus, Ram, DiskSize) whenever any merge → save cycle ran.
//
// The yaml tag `-` (currently only BundleNode.Inside, a derived
// runtime field) skips the merge. Untagged fields are also skipped.
func MergeDeployConfigs(configs ...*BundleConfig) *BundleConfig {
	result := &BundleConfig{Bundle: make(map[string]BundleNode)}
	for _, dc := range configs {
		if dc == nil || dc.Bundle == nil {
			continue
		}
		for name, overlay := range dc.Bundle {
			existing := result.Bundle[name]
			result.Bundle[name] = MergeBundleNode(existing, overlay)
		}
	}
	return result
}

// MergeBundleNode applies non-zero fields from `src` onto `dst` and
// returns the merged copy. Walks every yaml-tagged field via reflect; the
// yaml `-` tag (derived/runtime-only fields) is skipped. Same precedence
// rule as the underlying merge: src non-zero wins, otherwise dst passes
// through. Per R3 the single helper replaces the hand-rolled per-field
// merges that previously lived in MergeDeployConfigs (drift-prone — every
// new struct field needed a remembered append, and 19+ were missed).
func MergeBundleNode(dst, src BundleNode) BundleNode {
	dstV := reflect.ValueOf(&dst).Elem()
	srcV := reflect.ValueOf(src)
	t := dstV.Type()
	for i := 0; i < t.NumField(); i++ {
		ft := t.Field(i)
		tag := ft.Tag.Get("yaml")
		// Skip derived fields (yaml:"-") and untagged fields (rare; not
		// part of the persisted schema, so not merge-relevant).
		if tag == "-" || tag == "" {
			continue
		}
		sv := srcV.Field(i)
		if sv.IsZero() {
			continue
		}
		dstV.Field(i).Set(sv)
	}
	// Children/Members/Target are loader-DERIVED (yaml:"-" — not authored) yet are
	// real TREE DATA that must merge across project + per-host overlay. The reflect
	// loop above skips yaml:"-" fields (intended for the genuinely runtime-only
	// MemberOf/Inside/venue), so merge the structural tree fields EXPLICITLY here:
	// src non-zero wins, else dst passes through (same precedence). Without this a
	// project's nested/peer tree + target is dropped on the empty→project merge AND
	// by a nestedless operator overlay (resolveTreeRoot → MergeDeployConfigs).
	if src.Target != "" {
		dst.Target = src.Target
	}
	if len(src.Children) > 0 {
		dst.Children = src.Children
	}
	if len(src.Members) > 0 {
		dst.Members = src.Members
	}
	return dst
}

// isAutoVmDeployEntry reports whether a VM deploy entry carries NOTHING beyond
// the fields saveVmDeployState auto-sets — target: vm, vm:, and vm_state. Such
// an entry is a pure runtime-state record (e.g. a disposable check-bed VM) that
// `charly vm destroy` should delete so it doesn't accumulate. Any OTHER non-zero
// field means operator-authored per-host config (preemptible, env, tunnel,
// port, security, …) that MUST survive a destroy→create cycle. Compares against
// the zero node after blanking the three auto-set fields, so a newly-added
// per-host field is covered automatically (no remembered append — same
// drift-proof discipline as MergeBundleNode).
func isAutoVmDeployEntry(entry BundleNode) bool {
	probe := entry
	probe.VmState = nil
	probe.Target = ""
	probe.From = ""
	return reflect.DeepEqual(probe, BundleNode{})
}

// RemoveBoxDeploy removes an image's entry from a deploy config.
func RemoveBoxDeploy(dc *BundleConfig, boxName string) {
	if dc != nil && dc.Bundle != nil {
		delete(dc.Bundle, boxName)
	}
}

// cleanDeployEntry removes an image's entry from charly.yml (best-effort).
// Also removes global service env vars that were injected by this image.
// If charly.yml becomes empty after removal, the file is deleted.
func cleanDeployEntry(boxName, instance string) {
	// Same shared-file serialization as saveDeployState — a concurrent bed
	// teardown must not race another writer's load→modify→save. See filelock.go.
	unlock, lockErr := acquireDeployConfigLock()
	if lockErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not lock charly.yml for clean: %v\n", lockErr)
		return
	}
	defer func() { _ = unlock() }()
	dc, err := LoadBundleConfig()
	if err != nil || dc == nil {
		return
	}

	key := deployKey(boxName, instance)
	hasImage := false
	if _, ok := dc.Bundle[key]; ok {
		hasImage = true
		RemoveBoxDeploy(dc, key)
	}

	// Remove provides entries injected by this image/instance.
	// For instances: always clean entries sourced from the specific instance (exact match).
	// For base images: only clean ALL provides if no other instances remain deployed.
	removedProvides := false
	if dc.Provides != nil {
		if instance != "" {
			// Instance removal: remove only this instance's provides (exact source match)
			if len(dc.Provides.Env) > 0 {
				cleaned, removed := removeByExactSource(dc.Provides.Env, key)
				if removed {
					dc.Provides.Env = cleaned
					removedProvides = true
					fmt.Fprintf(os.Stderr, "Removed env provides from %s\n", key)
				}
			}
			if len(dc.Provides.MCP) > 0 {
				cleaned, removed := removeByExactSource(dc.Provides.MCP, key)
				if removed {
					dc.Provides.MCP = cleaned
					removedProvides = true
					fmt.Fprintf(os.Stderr, "Removed MCP provides from %s\n", key)
				}
			}
		} else {
			// Base image removal: only remove if no other entries for the same base image remain
			hasOtherEntries := false
			for k := range dc.Bundle {
				base, _ := parseDeployKey(k)
				if base == boxName {
					hasOtherEntries = true
					break
				}
			}
			if !hasOtherEntries {
				if len(dc.Provides.Env) > 0 {
					cleaned, removed := removeBySource(dc.Provides.Env, boxName)
					if removed {
						dc.Provides.Env = cleaned
						removedProvides = true
						fmt.Fprintf(os.Stderr, "Removed env provides from %s\n", boxName)
					}
				}
				if len(dc.Provides.MCP) > 0 {
					cleaned, removed := removeBySource(dc.Provides.MCP, boxName)
					if removed {
						dc.Provides.MCP = cleaned
						removedProvides = true
						fmt.Fprintf(os.Stderr, "Removed MCP provides from %s\n", boxName)
					}
				}
			}
		}
		if len(dc.Provides.MCP) == 0 && len(dc.Provides.Env) == 0 {
			dc.Provides = nil
		}
	}

	if !hasImage && !removedProvides {
		return
	}

	if len(dc.Bundle) == 0 && dc.Provides == nil {
		if path, pathErr := DeployConfigPath(); pathErr == nil {
			_ = os.Remove(path)
		}
	} else if err := SaveBundleConfig(dc); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not clean charly.yml: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "Cleaned charly.yml entry for %s\n", key)
}

// appendOrReplaceEnv adds or replaces an env var entry (KEY=VALUE) in a slice.
// If the key already exists, the value is replaced in-place.
func appendOrReplaceEnv(envs []string, entry string) []string {
	key := envKey(entry)
	for i, e := range envs {
		if envKey(e) == key {
			envs[i] = entry
			return envs
		}
	}
	return append(envs, entry)
}

// envKey extracts the KEY part from a KEY=VALUE string.
func envKey(entry string) string {
	if before, _, ok := strings.Cut(entry, "="); ok {
		return before
	}
	return entry
}

// SaveDeployStateInput holds the deployment parameters to persist.
type SaveDeployStateInput struct {
	Ports []string
	// SetPorts gates whether Ports is written to charly.yml at all.
	// This ensures `charly config <name>`
	// (without --port flags) and `charly update <name>` no longer silently
	// overwrite operator port overrides with image-label defaults.
	// Writing Ports whenever input.Ports != nil would
	// turn every config-recompute into a port-state reset because the
	// caller always computes ports from `meta.Port` (image-label
	// defaults pre-merged with charly.yml). With SetPorts, the caller
	// explicitly opts in to writing only when the operator passed
	// `--port` flags. Same idiom as SetDisposable/SetLifecycle below.
	SetPorts bool
	Env      []string
	CleanEnv bool // true = replace env list; false = merge (upsert by key)
	EnvFile  string
	Network  string
	Security *SecurityConfig
	Volume   []DeployVolumeConfig
	Sidecar  map[string]SidecarDef
	Tunnel   *TunnelYAML

	// SecretNames lists env var names declared as secret_accepts /
	// secret_requires on the image. saveDeployState uses this list to
	// defensively strip any matching KEY=VAL entries from both the input
	// Env and the existing persisted entry.Env before writing. Defense in
	// depth for the §6 / Run() pipeline (MigratePlaintextEnvSecret and
	// scrubSecretCLIEnv are the primary gates). Populated by the Run()
	// call site from meta.SecretAccept/SecretRequires.
	SecretNames []string

	// Disposable + Lifecycle — the classification fields
	// (see /charly-internals:disposable). SetDisposable toggles whether the
	// Disposable field is written at all: when false, saveDeployState
	// leaves any pre-existing value untouched. Same idiom for lifecycle.
	SetDisposable bool
	Disposable    bool
	SetLifecycle  bool
	Lifecycle     string

	// Box + Target — the schema-required fields per the 2026-05-12
	// require-image cutover (validateDeployRequiresBox). Written
	// when non-empty AND when the existing entry doesn't already have
	// a value (don't clobber operator-authored refs on re-config).
	// Without these, `charly bundle add foo bar --disposable` would write
	// an entry that the validator then rejects on the next load —
	// hard-failing every subsequent `charly` invocation.
	Box    string
	Target string

	// ResolvedImage is the concrete overlay image ref produced by a pod
	// deploy's add_candy: overlay build (`<deploy-key>-overlay:<hash>`),
	// persisted by PrepareVenue so config/start deploy EXACTLY that overlay
	// (carrying the add_candy layers) rather than re-resolving the base
	// image: short-name by a CalVer sort the overlay alias can lose to the
	// base on a same-minute build. Written when non-empty (the latest overlay
	// build wins); other callers (charly config/start) leave it "" so they
	// never clobber a persisted overlay ref.
	ResolvedImage string

	// Resource-arbitration axis (the fourth classification, see
	// classification.go + charly/preempt.go): the holder-side Preemptible
	// block and the claimant-side RequiresExclusive / RequiresShared token
	// lists. Persisted so a deploy/bed MEMBER round-trips its arbiter role
	// through the per-host overlay — a member's `charly start` reads the
	// reloaded node and drives acquireResourceForClaimant / the arbiter's
	// holder gather off these fields (start.go / preempt.go). Without them a
	// member carrying requires_exclusive would reload with RequiredExclusive()
	// == [] and the arbiter would silently no-op (the group-member arbiter
	// gap the C9 cutover surfaced). Written when non-empty — the same idiom as
	// Volume/Tunnel: an unset field is a no-op, so a re-config (charly
	// config/start passing zero arbiter fields) never clobbers a seeded role.
	Preemptible       *PreemptibleConfig
	RequiresExclusive []string
	RequiresShared    []string
}

// saveDeployState persists deployment parameters to charly.yml (best-effort).
// Merges onto any existing entry to preserve fields from charly bundle import.
//
// Defense-in-depth: any env entry whose key matches a name in input.SecretNames
// is stripped from both input.Env and the existing persisted entry.Env before
// writing. The primary gates against plaintext-credential leakage are
// MigratePlaintextEnvSecret and scrubSecretCLIEnv in config_image.go:Run();
// this scrub catches anything that slipped through (e.g., a future refactor
// that adds a new code path writing into dc.Env). Matches plan §6.7.
func saveDeployState(boxName, instance string, input SaveDeployStateInput) {
	// Serialize the load→modify→save against concurrent charly processes
	// (parallel check beds, charly config/start). Without it two writers race
	// and silently drop each other's entry — the truncation class the
	// loadDeployConfigForWrite docstring warns about. See filelock.go.
	unlock, lockErr := acquireDeployConfigLock()
	if lockErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not lock charly.yml for write: %v\n", lockErr)
		return
	}
	defer func() { _ = unlock() }()
	dc, err := loadDeployConfigForWrite("saveDeployState")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save to charly.yml: %v\n", err)
		return
	}
	key := deployKey(boxName, instance)
	entry := dc.Bundle[key] // preserve existing fields (tunnel, volumes, etc.)
	if input.Box != "" && entry.Image == "" {
		entry.Image = input.Box
	}
	if input.Target != "" && entry.Target == "" {
		entry.Target = input.Target
	}
	// ResolvedImage: the latest overlay build wins (clobber). Only PrepareVenue
	// sets it (non-empty); charly config/start pass "" and never clobber it.
	if input.ResolvedImage != "" {
		entry.ResolvedImage = input.ResolvedImage
	}
	if input.Volume != nil {
		entry.Volume = input.Volume
	}
	// Ports gated on SetPorts: explicit opt-in required so a recompute
	// path that always-passes computed `meta.Port` doesn't silently
	// overwrite operator overrides. See SaveDeployStateInput.SetPorts
	// docstring.
	if input.SetPorts && input.Ports != nil {
		entry.Port = input.Ports
	}
	// Defensive scrub: drop credential-backed env vars from both input and
	// existing entry before they land in the persisted file.
	if len(input.SecretNames) > 0 {
		input.Env = stripSecretEnvNames(input.Env, input.SecretNames)
		entry.Env = stripSecretEnvNames(entry.Env, input.SecretNames)
	}
	if len(input.Env) > 0 {
		if input.CleanEnv || len(entry.Env) == 0 {
			entry.Env = input.Env
		} else {
			entry.Env = mergeEnvVars(entry.Env, input.Env)
		}
	}
	if input.EnvFile != "" {
		entry.EnvFile = input.EnvFile
	}
	if input.Network != "" {
		entry.Network = input.Network
	}
	if input.Security != nil {
		entry.Security = input.Security
	}
	if len(input.Sidecar) > 0 {
		entry.Sidecar = input.Sidecar
	}
	if input.Tunnel != nil {
		entry.Tunnel = input.Tunnel
	}
	// Classification fields: only written when the caller explicitly
	// opts in via SetDisposable / SetLifecycle. This lets repeated
	// saveDeployState calls from unrelated code paths (charly start, charly
	// config) leave a user-authored `disposable: true` in place.
	if input.SetDisposable {
		v := input.Disposable
		entry.Disposable = &v
	}
	if input.SetLifecycle {
		entry.Lifecycle = input.Lifecycle
	}
	// Resource-arbitration axis: persist the holder-side preemptible block and
	// the claimant-side requires_exclusive / requires_shared token lists so a
	// deploy/bed MEMBER's `charly start` reloads them from the per-host overlay
	// and the arbiter actually fires for it (start.go → acquireResourceForClaimant;
	// preempt.go gather). Write-when-non-empty (the Volume/Tunnel idiom): an unset
	// field never clobbers a previously-seeded role on a re-config.
	if input.Preemptible != nil {
		entry.Preemptible = input.Preemptible
	}
	if len(input.RequiresExclusive) > 0 {
		entry.RequiresExclusive = input.RequiresExclusive
	}
	if len(input.RequiresShared) > 0 {
		entry.RequiresShared = input.RequiresShared
	}
	// Defensive zero-write guard: refuse to persist a fully-zero
	// BundleNode (every field at its Go zero value). A future caller
	// that invokes saveDeployState with an empty SaveDeployStateInput on
	// a key that doesn't yet exist in the user overlay would otherwise
	// write `<key>: {}`, materializing an empty entry that masks any
	// matching entry from the project charly.yml deploy block (see
	// 2026-05 RCA: charly update did NOT directly do this, but the latent
	// shape was real and the user's charly.yml ended up empty by some
	// path we couldn't fully reconstruct — this guard makes the entire
	// regression class structurally impossible).
	if reflect.DeepEqual(entry, BundleNode{}) {
		return
	}
	dc.Bundle[key] = entry
	if err := SaveBundleConfig(dc); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save to charly.yml: %v\n", err)
	}
}

// stripSecretEnvNames removes any KEY=VAL entries from env whose KEY is in
// the blocked list. The blocked list is expected to be short (one entry per
// secret_* declaration on the image), so a linear contains check per entry
// is fine. Preserves the order of surviving entries.
func stripSecretEnvNames(env []string, blocked []string) []string {
	if len(env) == 0 || len(blocked) == 0 {
		return env
	}
	blockedSet := make(map[string]bool, len(blocked))
	for _, name := range blocked {
		blockedSet[name] = true
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key := kv
		if before, _, ok := strings.Cut(kv, "="); ok {
			key = before
		}
		if blockedSet[key] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// mergeEnvVars merges new env vars into existing ones (upsert by key).
// New vars override existing vars with the same key; existing vars with
// unmatched keys are preserved in their original order.
func mergeEnvVars(existing, newVars []string) []string {
	newByKey := make(map[string]string, len(newVars))
	for _, kv := range newVars {
		key, _, _ := strings.Cut(kv, "=")
		newByKey[key] = kv
	}
	result := make([]string, 0, len(existing)+len(newVars))
	seen := make(map[string]bool)
	for _, kv := range existing {
		key, _, _ := strings.Cut(kv, "=")
		if newKV, ok := newByKey[key]; ok {
			result = append(result, newKV)
			seen[key] = true
		} else {
			result = append(result, kv)
		}
	}
	for _, kv := range newVars {
		key, _, _ := strings.Cut(kv, "=")
		if !seen[key] {
			result = append(result, kv)
		}
	}
	return result
}

// ExportAllBox exports all runtime-relevant fields for all enabled images in a Config.
func ExportAllBox(cfg *Config) *BundleConfig {
	dc := &BundleConfig{Bundle: make(map[string]BundleNode)}
	for name, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}
		// Schema v4: Tunnel / DNS / AcmeEmail / Engine no longer sourced
		// from BoxConfig (they're deploy-only).
		entry := BundleNode{
			Version:     img.Version,
			Description: img.Description,
			Env:         img.Env,
			EnvFile:     img.EnvFile,
			Security:    img.Security,
			Network:     img.Network,
		}
		// Only include if at least one field is set. Ports are no longer a box
		// field — they're inherited from candies and auto-allocated at deploy.
		if entry.Version != "" || entry.Description != "" ||
			entry.Env != nil ||
			entry.EnvFile != "" || entry.Security != nil || entry.Network != "" {
			dc.Bundle[name] = entry
		}
	}
	return dc
}
