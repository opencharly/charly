package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk/kit"
)

// Canonical preemption policy values. Stop is the freeing mechanism;
// Restore is when the holder is brought back.
const (
	PreemptStopShutdown   = "shutdown"   // graceful ACPI shutdown / podman stop; disk preserved (only supported value)
	PreemptRestoreAlways  = "always"     // restart the holder regardless of the claim's outcome (default)
	PreemptRestoreSuccess = "on-success" // restart only if the claim released cleanly; leave stopped on failure
)

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
// isSameBaseBox returns true if source is the same base image (with or without instance).
func isSameBaseBox(source, boxName string) bool {
	return source == boxName || strings.HasPrefix(source, boxName+"/")
}

// DeployConfigPath returns the path to the deploy overlay file. Package-level var for
// testability (tests inject a temp path, same pattern as RuntimeConfigPath). The resolver
// body lives in kit.DefaultDeployConfigPath — ONE definition shared with the out-of-module
// candy/plugin-migrate (R3).
var DeployConfigPath = kit.DefaultDeployConfigPath

// DeployConfigEnv overrides the per-host deploy-config PATH. A check bed sets it (via the
// bed runner) to a PER-BED isolated file so CONCURRENT beds never share — and corrupt —
// the operator's ~/.config/charly/charly.yml, and a disposable bed's transient
// resolved_port/quadlet state never pollutes the operator's persistent config. The 2026-07
// maxjobs-load corruption (`node "…": kind:group: #GroupInput.resolved_port: field not
// allowed`) was concurrent beds racing the shared read-modify-write of this one file.
const DeployConfigEnv = kit.DeployConfigEnv

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
		meta.Env = envMapToPairs(overlay.Env)
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

// deployVolumePrefix + deployStorageDir moved to sdk/deploykit as
// DeployVolumePrefix/DeployStorageDir (P13/C15); aliased in
// deploykit_state_aliases.go. ResolveVolumeBacking + resolveVolumeHostPath also
// relocated to sdk/deploykit with the P11 enc-model move (see below); only
// scopeVolumesToDeployKey below stays core (it reads *BoxMetadata, not yet
// spec-sourced — folds with P2B).

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

// ResolveVolumeBacking + resolveVolumeHostPath relocated to sdk/deploykit
// (deploy_volume.go) with the P11 enc-model move, alongside the pure enc-path cluster —
// aliased in deploykit_state_aliases.go so the host call sites (config_image.go / start.go
// / shell.go, and step 3's config-resolve seam) compile unchanged. scopeVolumesToDeployKey
// stays here (it reads *BoxMetadata, folding with the BoxMetadata cutover).

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
		root.Content = append(root.Content, scalarNode(name), migrateDeployEntity(body))
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
	// descent: is a loader-DERIVED venue-hop descriptor (Cutover H) re-stamped on
	// every load by the substrate plugin — NEVER persist it (a stored descent trips
	// #DeployValue's descent?:_|_ on reload, and migrateDeployEntity does not know
	// the key). Drop it from the marshaled body at every recursion level.
	dropMappingKey(body, "descent")
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
	Env      map[string]string
	CleanEnv bool // true = replace env map; false = merge (upsert by key)
	EnvFile  string
	Network  string
	Security *SecurityConfig
	Volume   []DeployVolumeConfig
	Sidecar  map[string]json.RawMessage
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

	// VmState + VmCrossRef — the vm substrate's persisted runtime state (instance-id, ssh_port, disk
	// path) + the kind:vm cross-ref, shipped by the externalized vm plugin's PrepareVenue reply as
	// the generic State patch (the host owns charly.yml, the plugin cannot). VmState is written
	// whenever non-nil (the latest prepare wins); VmCrossRef seeds entry.From only when unset (never
	// clobber an operator-authored cross-ref). saveVmDeployState is a thin wrapper over this path (R3).
	VmState    *VmDeployState
	VmCrossRef string

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
	// Vm runtime state (from the externalized vm plugin's PrepareVenue State patch): write whenever
	// non-nil; seed the kind:vm cross-ref only when entry.From is unset (non-clobber).
	if input.VmState != nil {
		entry.VmState = input.VmState
	}
	if input.VmCrossRef != "" && entry.From == "" {
		entry.From = input.VmCrossRef
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

// ExportAllBox exports all runtime-relevant fields for all enabled images in a Config.
func ExportAllBox(cfg *Config) *BundleConfig {
	dc := &BundleConfig{Bundle: make(map[string]BundleNode)}
	for _, name := range cfg.allBoxNames() {
		img, _ := cfg.BoxConfig(name)
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

// occupiedHostPorts returns the host ports already claimed by other deploys in dc
// (excluding excludeKey). A charly free function (not a BundleConfig method) because
// it reaches charly's ports.go parsers (IsAutoPort/ParseHostPort).
