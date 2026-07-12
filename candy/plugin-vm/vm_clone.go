package vm

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// vm_clone.go — clone-from-snapshot build path.
//
// Two entry points:
//
//   - BuildClone(spec) — invoked by `charly vm build` when source.kind ==
//     "clone". Materializes a fresh per-VM qcow2 with the source
//     snapshot's external disk as backing chain. Regenerates the
//     cloud-init seed ISO with a fresh InstanceID (cloud-init MUST see
//     a new instance-id, otherwise it skips first-boot tasks). When
//     the clone declaration carries cloud_init_clean: true, the user-
//     data also injects `runcmd: cloud-init clean --machine-id --logs`
//     so the guest re-runs identity setup on first boot.
//
//   - writeVmCloneDeclaration — invoked by `charly vm clone` to persist a
//     kind:vm entry into charly.yml. Pure config-file
//     mutation; no disk operations.

// BuildClone is the source.kind == "clone" build path.
//
// vmName is the new VM (the clone target). spec is its VmSpec
// (source.from_vm and source.from_snapshot fully populated). outputDir
// is where output/qcow2/disk.qcow2 + output/qcow2/seed.iso will be
// written, mirroring the cloud_image build path's conventions.
func BuildClone(vmName string, spec *VmSpec, _, vmStateDir string) error {
	if spec.Source.Kind != "clone" {
		return fmt.Errorf("BuildClone called with source.kind == %q (want clone)", spec.Source.Kind)
	}
	if spec.Source.FromVm == "" {
		return fmt.Errorf("vm %q: source.from_vm is required for clone", vmName)
	}
	if spec.Source.FromSnapshot == "" {
		return fmt.Errorf("vm %q: source.from_snapshot is required for clone", vmName)
	}

	// Look up the source snapshot. Refuses if not found; auto-promotes
	// internal-mode snapshots to external before cloning.
	parentEntry, err := LookupSnapshot(spec.Source.FromVm, spec.Source.FromSnapshot)
	if err != nil {
		return err
	}
	if parentEntry.Mode == "internal" {
		fmt.Fprintf(os.Stderr, "note: snapshot %s@%s is mode=internal; auto-promoting to external for clone backing\n",
			spec.Source.FromVm, spec.Source.FromSnapshot)
		parentEntry, err = PromoteSnapshot(spec.Source.FromVm, spec.Source.FromSnapshot)
		if err != nil {
			return fmt.Errorf("auto-promoting %s@%s: %w", spec.Source.FromVm, spec.Source.FromSnapshot, err)
		}
	}
	if parentEntry.DiskPath == "" {
		return fmt.Errorf("vm %q: parent snapshot %s@%s has no disk path", vmName, spec.Source.FromVm, spec.Source.FromSnapshot)
	}

	// Materialize the clone overlay using the existing primitive.
	clonePath := filepath.Join(vmDiskDir(vmName), "disk.qcow2")
	if err := os.MkdirAll(filepath.Dir(clonePath), 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}
	if err := qemuImgCreateOverlay(parentEntry.DiskPath, clonePath); err != nil {
		return fmt.Errorf("clone overlay create: %w", err)
	}

	// Increment the parent snapshot's refcount. The decrement happens
	// at ephemeral / clone teardown.
	if err := IncrementSnapshotRefcount(spec.Source.FromVm, spec.Source.FromSnapshot); err != nil {
		// Don't fail the build if refcount bookkeeping is off-by-one;
		// log and proceed.
		fmt.Fprintf(os.Stderr, "warning: incrementing snapshot refcount for %s@%s: %v\n",
			spec.Source.FromVm, spec.Source.FromSnapshot, err)
	}

	// Regenerate the cloud-init seed ISO with a fresh InstanceID.
	// Pass nil for existingState — that's the path that auto-generates
	// a new UUIDv4 (see vm_cloud_image.go:164-169).
	seedPath := filepath.Join(vmDiskDir(vmName), "seed.iso")
	if spec.CloudInit != nil || spec.SSH != nil {
		// If cloud_init_clean is set, inject the clean runcmd so
		// machine-id and ssh host keys regenerate on first boot.
		if spec.Source.CloudInitClean {
			if spec.CloudInit == nil {
				spec.CloudInit = &VmCloudInit{}
			}
			spec.CloudInit.RunCmd = appendCloudInitClean(spec.CloudInit.RunCmd)
		}
		if err := RegenerateSeedISO(spec, seedPath, vmStateDir, nil); err != nil {
			return fmt.Errorf("regenerating seed ISO for clone: %w", err)
		}
	}
	return nil
}

// appendCloudInitClean adds the cloud-init clean runcmd entry to a
// runcmd list (idempotent — won't duplicate if the entry's already
// there).
func appendCloudInitClean(existing []string) []string {
	const cleanCmd = "cloud-init clean --machine-id --logs"
	if slices.Contains(existing, cleanCmd) {
		return existing
	}
	return append(existing, cleanCmd)
}

// writeVmCloneDeclaration persists a kind:vm entry for a clone into
// charly.yml, preserving comments + key order via the yaml.v3 Node
// API.
//
// Schema v4 (2026-05) makes charly.yml the only canonical authoring
// target. If charly.yml is missing, errors with a remediation hint
// pointing at `charly box new project` / `charly migrate`.
func writeVmCloneDeclaration(name, srcVm, srcSnap string, cloudInitClean bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	target := filepath.Join(cwd, UnifiedFileName)
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("charly.yml not found in %s; run `charly box new project .` first", cwd)
		}
		return fmt.Errorf("stat charly.yml: %w", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("reading %s: %w", target, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parsing %s: %w", target, err)
	}

	// Walk to the `vm:` mapping (creating it on demand). Append the new
	// entry. Re-marshal preserving comments via the Node API.
	if root.Kind == 0 {
		// Empty file — synthesize a fresh document root.
		root.Kind = yaml.DocumentNode
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: top-level YAML is not a mapping (cannot append vm: entry)", target)
	}
	topMap := root.Content[0]

	vmMap := findOrCreateMapEntry(topMap, "vm")
	if alreadyHas(vmMap, name) {
		return fmt.Errorf("%s: vm entry %q already exists; pick a different name or remove the existing entry first", target, name)
	}

	// Build the new entry as a YAML mapping node.
	entry := buildCloneVmNode(srcVm, srcSnap, cloudInitClean)

	// Append name → entry as a key/value pair.
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}
	vmMap.Content = append(vmMap.Content, keyNode, entry)

	// Re-marshal with explicit 4-space indent matching the project's
	// charly.yml canonical style. Default `yaml.Marshal` produces
	// the right indent BUT also re-flows quoted strings and key
	// ordering in ways that pollute the diff; we use the encoder API
	// for stable output. Mirrors migrate_deploy_v3.go which uses
	// SetIndent(4) for the same reason.
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(&root); err != nil {
		return fmt.Errorf("marshaling updated YAML: %w", err)
	}
	_ = enc.Close()
	out := []byte(buf.String())
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	return os.Rename(tmp, target)
}

// findOrCreateMapEntry locates a top-level map key in a mapping node
// and returns its value mapping. If absent, appends a fresh empty
// mapping and returns it.
func findOrCreateMapEntry(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key && parent.Content[i+1].Kind == yaml.MappingNode {
			return parent.Content[i+1]
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, keyNode, valNode)
	return valNode
}

// alreadyHas reports whether a mapping node has the given key.
func alreadyHas(parent *yaml.Node, key string) bool {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return true
		}
	}
	return false
}

// buildCloneVmNode synthesizes a YAML mapping node for a kind:vm
// clone entry: source: { kind: clone, from_vm, from_snapshot,
// cloud_init_clean }.
func buildCloneVmNode(srcVm, srcSnap string, cloudInitClean bool) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	// source: ...
	sourceVal := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addStrPair(sourceVal, "kind", "clone")
	addStrPair(sourceVal, "from_vm", srcVm)
	if srcSnap != "" {
		addStrPair(sourceVal, "from_snapshot", srcSnap)
	}
	if cloudInitClean {
		addBoolPair(sourceVal, "cloud_init_clean", true)
	}

	n.Content = append(n.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "source"},
		sourceVal,
	)
	return n
}

func addStrPair(parent *yaml.Node, key, val string) {
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: val},
	)
}

func addBoolPair(parent *yaml.Node, key string, val bool) {
	v := "false"
	if val {
		v = "true"
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v},
	)
}
