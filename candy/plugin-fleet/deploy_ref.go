package fleet

// deploy_ref.go — the PLUGIN-SIDE box/candy reference resolver for `charly fleet add <name>
// <ref>` / `--add-candy <ref>` (K4-C shape-2 port of the former host charly/deploy_ref.go). The
// classification is byte-faithful to the host resolver EXCEPT its local-NAME arm: box-vs-candy
// presence now resolves off the RESOLVED-PROJECT ENVELOPE (rp.Boxes / rp.Candies) instead of the
// host's LoadUnified + os.Stat(candy/<name>/) — the last host-only piece of the deploy compile,
// so the walk's per-node compile step runs entirely plugin-side (killing the plugin→host→plugin
// double-bounce). The three env-independent arms are UNCHANGED and portable:
//
//   1. Local box/candy NAME    "fedora-coder" / "ripgrep"  → rp.Boxes / rp.Candies presence.
//   2. Local YAML path         "./x.yml" | "/abs/x.yaml"   → plugin file-read + classifyYAMLFile.
//   3. Remote repo ref         "github.com/o/r[/box|candy/n][@ref]" / "@…" → spec.ParseRemoteRef.
//
// Disambiguation rules match the host verbatim: a `candy/<n>` subpath → candy, a `box/<n>` subpath
// → box, a YAML with `base:`/`box:`/`defaults:` → box, with a candy marker → candy; a local name in
// BOTH kinds resolves by the CALLER's preferKind (box for the primary <ref>, candy for
// --add-candy).

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// RefKind classifies a DeployRef.
type RefKind string

const (
	RefKindBox   RefKind = "box"
	RefKindCandy RefKind = "candy"
)

// RefSource classifies where the ref's content lives.
type RefSource string

const (
	RefSourceLocalName RefSource = "local-name"
	RefSourceLocalPath RefSource = "local-path"
	RefSourceRemote    RefSource = "remote"
)

// DeployRef is a parsed `<box-or-candy-ref>`. The plugin resolver populates Raw/Kind/Source/Name
// (and Remote for a remote ref) — the compile is off the envelope, so no host-side Path load is
// needed (the host resolver's Path field is deliberately unused here).
type DeployRef struct {
	Raw    string          // original input
	Kind   RefKind         // box or candy
	Source RefSource       // local-name | local-path | remote
	Name   string          // resolved short name (ripgrep, fedora-coder, …)
	Path   string          // absolute path (local-path only; unset for local-name)
	Remote *spec.ParsedRef // populated for remote refs
}

// resolveDeployRef is the plugin box-first resolver (the primary `<ref>` positional almost always
// means "deploy this box"). rp is the resolved-project envelope for local-name classification.
func resolveDeployRef(rp *spec.ResolvedProject, ref, projectDir string) (*DeployRef, error) {
	return resolveDeployRefWithPref(rp, ref, projectDir, RefKindBox)
}

// resolveDeployRefAsCandy is the candy-first sibling for `--add-candy <ref>` — if a name exists as
// both a box and a candy, candy wins.
func resolveDeployRefAsCandy(rp *spec.ResolvedProject, ref, projectDir string) (*DeployRef, error) {
	return resolveDeployRefWithPref(rp, ref, projectDir, RefKindCandy)
}

func resolveDeployRefWithPref(rp *spec.ResolvedProject, ref, projectDir string, preferKind RefKind) (*DeployRef, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("ResolveDeployRef: empty ref")
	}

	// Form 4a (legacy): @host/org/repo/path:version.
	if strings.HasPrefix(ref, "@") {
		return resolveRemoteRef(ref)
	}
	// Form 4b: host/org/repo/path[@ref] — new syntax, no leading @.
	if looksLikeRemoteRef(ref) {
		return resolveRemoteRef("@" + translateAtVersion(ref))
	}
	// Form 3: local YAML path.
	if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "../") ||
		strings.HasSuffix(ref, ".yml") || strings.HasSuffix(ref, ".yaml") {
		return resolveLocalPath(ref, projectDir)
	}
	// Forms 1 + 2: local name — off the envelope.
	return resolveLocalName(rp, ref, preferKind)
}

// knownRemoteHosts / looksLikeRemoteRef / translateAtVersion / refSubPathHas / resolveRemoteRef —
// verbatim ports of the host resolver (env-independent, sdk-portable).
var knownRemoteHosts = regexp.MustCompile(
	`^(github\.com|gitlab\.com|codeberg\.org|bitbucket\.org)(/|$)`,
)

func looksLikeRemoteRef(ref string) bool { return knownRemoteHosts.MatchString(ref) }

func translateAtVersion(ref string) string {
	idx := strings.LastIndex(ref, "@")
	if idx < 0 {
		return ref
	}
	return ref[:idx] + ":" + ref[idx+1:]
}

func refSubPathHas(subPath, segment string) bool {
	return strings.Contains(subPath, "/"+segment+"/") || strings.HasPrefix(subPath, segment+"/")
}

func resolveRemoteRef(ref string) (*DeployRef, error) {
	parsed := spec.ParseRemoteRef(ref)
	var kind RefKind
	switch {
	case refSubPathHas(parsed.SubPath, "candy") || refSubPathHas(parsed.SubPath, "layers"):
		kind = RefKindCandy
	case refSubPathHas(parsed.SubPath, "box") || refSubPathHas(parsed.SubPath, "images"):
		kind = RefKindBox
	default:
		// A bare repo ref (no candy//box/ subpath) defaults to the project's charly.yml (box-shaped).
		kind = RefKindBox
	}
	return &DeployRef{Raw: ref, Kind: kind, Source: RefSourceRemote, Name: parsed.Name, Remote: parsed}, nil
}

// resolveLocalPath handles `./path.yml`, `/abs/path.yaml`, etc. Reads the file's top-level keys to
// classify box vs candy — a plugin-side file read (portable; the deploy plugin runs on the host fs).
func resolveLocalPath(ref, projectDir string) (*DeployRef, error) {
	path := ref
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectDir, ref)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("ResolveDeployRef: cannot stat %s: %w", path, err)
	}
	if info.IsDir() {
		path = filepath.Join(path, unifiedFileName)
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("ResolveDeployRef: directory %s has no %s", ref, unifiedFileName)
		}
	}
	kind, err := classifyYAMLFile(path)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if kind == RefKindCandy && filepath.Base(path) == unifiedFileName {
		name = filepath.Base(filepath.Dir(path))
	}
	return &DeployRef{Raw: ref, Kind: kind, Source: RefSourceLocalPath, Name: name, Path: path}, nil
}

// unifiedFileName is the ONE project filename (charly/UnifiedFileName's plugin-side twin — a plain
// const, no charly-core import).
const unifiedFileName = "charly.yml"

// classifyYAMLFile reads the file's top-level keys and decides box vs candy — verbatim port of the
// host resolver.
func classifyYAMLFile(path string) (RefKind, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return "", fmt.Errorf("parsing %s: %w", path, err)
	}
	for _, k := range []string{"box", "base", "defaults"} {
		if _, ok := top[k]; ok {
			return RefKindBox, nil
		}
	}
	for _, k := range []string{"rpm", "deb", "pac", "aur", "tasks", "services", "service", "system_services", "candy", "depends", "env", "path_append", "description"} {
		if _, ok := top[k]; ok {
			return RefKindCandy, nil
		}
	}
	return "", fmt.Errorf("ResolveDeployRef: %s has no recognized box or candy keys", path)
}

// resolveLocalName classifies a local name off the resolved-project envelope. rp.Boxes is
// namespace-keyed by FULLKEY (root boxes bare, namespaced as `ns.name`), so a direct map hit on the
// user's ref resolves BOTH a bare and a qualified box ref — the SAME result the host resolver's
// uf.ProjectConfig().ResolveBoxRef(name) descent produced (a bare leaf that lives ONLY in a
// namespace is absent from rp.Boxes under its bare key, exactly as ResolveBoxRef returns false for
// it — the two agree). rp.Candies is BARE-ref-keyed (root + namespaced candies folded in), so the
// candy presence check is a BareRef map hit. Cross-kind name reuse is permitted; preferKind decides
// ties.
func resolveLocalName(rp *spec.ResolvedProject, name string, preferKind RefKind) (*DeployRef, error) {
	inBoxes := false
	if rp != nil {
		_, inBoxes = rp.Boxes[name]
	}
	inCandies := false
	if rp != nil {
		_, inCandies = rp.Candies[deploykit.BareRef(name)]
	}

	imageRef := func() *DeployRef {
		return &DeployRef{Raw: name, Kind: RefKindBox, Source: RefSourceLocalName, Name: name}
	}
	candyRef := func() *DeployRef {
		return &DeployRef{Raw: name, Kind: RefKindCandy, Source: RefSourceLocalName, Name: name}
	}

	switch {
	case inBoxes && inCandies:
		if preferKind == RefKindCandy {
			return candyRef(), nil
		}
		return imageRef(), nil
	case inBoxes:
		return imageRef(), nil
	case inCandies:
		return candyRef(), nil
	}
	return nil, fmt.Errorf("ResolveDeployRef: %q not found as a box or candy in the resolved-project envelope", name)
}
