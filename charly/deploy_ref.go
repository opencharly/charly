package main

// deploy_ref.go — unified box/candy reference resolver for
// `charly bundle add <name> <ref>`, `--add-candy <ref>`, and charly.yml
// box:/add_candy: fields.
//
// <ref> accepts four forms, auto-detected:
//
//   1. Local box name           "fedora-coder"
//      Matches a top-level entry in charly.yml.
//
//   2. Local candy name         "pre-commit"
//      Matches a directory in candy/.
//
//   3. Local YAML path          "./my-box.yml" | "/abs/path/candy.yml"
//      Starts with "./" or "/"; ends with ".yml" or ".yaml". The file's
//      top-level keys tell us whether it's a box or candy declaration.
//
//   4. Remote repo ref          "github.com/owner/repo[/box/<n>|/candy/<n>][@ref]"
//      Matches "{host}/{org}/{repo}[/sub][@ref]" with a known host. The
//      existing refs.go `@`-prefixed form is also accepted for backward
//      compat with charly.yml import:/candy: already in the tree.
//
// Disambiguation rules (post 2026-06 candy/box rebrand):
//   - Any ref with a "candy/<n>" subpath resolves to a candy ("layers/" legacy).
//   - Any ref with a "box/<n>" subpath resolves to a box ("images/" legacy).
//   - A local name found in BOTH charly.yml and candy/ is permitted —
//     each kind has its own namespace. Precedence is decided by the
//     CALLER's context: ResolveDeployRef defaults to box-first
//     (the primary `<ref>` positional almost always means "deploy
//     this box"); ResolveDeployRefAsCandy prefers candies (used by
//     `--add-candy <ref>` where the user explicitly asked for a candy).
//   - A YAML file with a top-level `base:` or `box:` key is a box;
//     one with `rpm:`/`deb:`/`pac:`/`aur:`/`tasks:`/`services:` is a candy.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/opencharly/sdk/spec"
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

// DeployRef is a parsed `<box-or-candy-ref>` ready to be loaded.
type DeployRef struct {
	Raw    string          // original input
	Kind   RefKind         // box or candy
	Source RefSource       // local-name | local-path | remote
	Name   string          // resolved short name (ripgrep, fedora-coder, etc.)
	Path   string          // absolute path to the relevant YAML; populated for local-name + local-path
	Remote *spec.ParsedRef // populated for remote refs
}

// ResolveDeployRef parses ref using projectDir as the current project
// root for local-name resolution. Returns a DeployRef ready for
// downstream loading (LoadCandyFromFile / LoadImageFromFile / remote
// download). The function does not fetch remote repos — that happens
// at Emit time when the plan actually needs the content.
//
// When the same name exists as BOTH a box and a candy (cross-kind
// name reuse — permitted since 2026-05), this entry point prefers
// box. For the `--add-candy` context where the user asked for a
// candy specifically, use ResolveDeployRefAsCandy instead.
func ResolveDeployRef(ref, projectDir string) (*DeployRef, error) {
	return resolveDeployRefWithPref(ref, projectDir, RefKindBox)
}

// ResolveDeployRefAsCandy is the candy-preferring sibling of
// ResolveDeployRef. Used for `--add-candy <ref>` resolution where the
// user has explicitly asked for a candy overlay; if the same name
// exists as both a box and a candy, candy wins.
func ResolveDeployRefAsCandy(ref, projectDir string) (*DeployRef, error) {
	return resolveDeployRefWithPref(ref, projectDir, RefKindCandy)
}

// resolveDeployRefWithPref is the shared implementation; preferKind
// only affects the local-name codepath when a name resolves to both
// a box and a candy. Remote refs and explicit local paths classify
// themselves unambiguously.
func resolveDeployRefWithPref(ref, projectDir string, preferKind RefKind) (*DeployRef, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("ResolveDeployRef: empty ref")
	}

	// Form 4a (legacy): @host/org/repo/path:version — existing refs.go syntax.
	if strings.HasPrefix(ref, "@") {
		return resolveRemoteRef(ref)
	}

	// Form 4b: host/org/repo/path[@ref] — new syntax, no leading @.
	if looksLikeRemoteRef(ref) {
		// Normalize to the @-prefixed form so ParseRemoteRef handles it.
		normalized := "@" + translateAtVersion(ref)
		return resolveRemoteRef(normalized)
	}

	// Form 3: local YAML path.
	if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "../") ||
		strings.HasSuffix(ref, ".yml") || strings.HasSuffix(ref, ".yaml") {
		return resolveLocalPath(ref, projectDir)
	}

	// Forms 1 + 2: local name. Check charly.yml AND candy/; cross-kind
	// reuse permitted, preferKind decides which wins.
	return resolveLocalName(ref, projectDir, preferKind)
}

// looksLikeRemoteRef returns true if ref resembles a remote repo ref:
// "{host}/{org}/{repo}..." with a recognized host. We match a known set
// of hosts conservatively — adding new hosts is a one-line change.
var knownRemoteHosts = regexp.MustCompile(
	`^(github\.com|gitlab\.com|codeberg\.org|bitbucket\.org)(/|$)`,
)

func looksLikeRemoteRef(ref string) bool {
	return knownRemoteHosts.MatchString(ref)
}

// translateAtVersion rewrites "path@ref" into "path:ref" so ParseRemoteRef
// (which uses the colon delimiter) recognizes version pins in the new
// syntax. If ref has no @, returns it unchanged.
func translateAtVersion(ref string) string {
	idx := strings.LastIndex(ref, "@")
	if idx < 0 {
		return ref
	}
	// Preserve chars up to idx; replace @ with : after.
	return ref[:idx] + ":" + ref[idx+1:]
}

// refSubPathHas reports whether a remote-ref subpath contains a path segment
// either as the leading component ("candy/charly") or an interior one ("x/candy/charly").
func refSubPathHas(subPath, segment string) bool {
	return strings.Contains(subPath, "/"+segment+"/") || strings.HasPrefix(subPath, segment+"/")
}

// resolveRemoteRef parses and classifies an @-prefixed remote ref.
func resolveRemoteRef(ref string) (*DeployRef, error) {
	parsed := spec.ParseRemoteRef(ref)
	var kind RefKind
	switch {
	case refSubPathHas(parsed.SubPath, "candy") || refSubPathHas(parsed.SubPath, "layers"):
		// `candy/<n>` is the post-rebrand candy subpath; `layers/<n>` is the
		// legacy form kept for back-compat with old pins.
		kind = RefKindCandy
	case refSubPathHas(parsed.SubPath, "box") || refSubPathHas(parsed.SubPath, "images"):
		// `box/<n>` is the post-rebrand box subpath; `images/<n>` is legacy.
		kind = RefKindBox
	default:
		// A bare repo ref (no candy//box/ subpath) defaults to the project's
		// charly.yml, which is box-shaped. Existing tooling treats such
		// refs as project imports; we follow suit.
		kind = RefKindBox
	}
	return &DeployRef{
		Raw:    ref,
		Kind:   kind,
		Source: RefSourceRemote,
		Name:   parsed.Name,
		Remote: parsed,
	}, nil
}

// resolveLocalPath handles `./path.yml`, `/abs/path.yaml`, etc. Reads
// the file's top-level keys to classify as box vs candy.
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
		// A directory ref points at a candy directory (candy/<name>/).
		path = filepath.Join(path, UnifiedFileName)
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("ResolveDeployRef: directory %s has no %s", ref, UnifiedFileName)
		}
	}
	kind, err := classifyYAMLFile(path)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if kind == RefKindCandy && filepath.Base(path) == UnifiedFileName {
		name = filepath.Base(filepath.Dir(path))
	}
	return &DeployRef{
		Raw:    ref,
		Kind:   kind,
		Source: RefSourceLocalPath,
		Name:   name,
		Path:   path,
	}, nil
}

// classifyYAMLFile reads the file's top-level keys and decides whether
// it declares a box or a candy.
func classifyYAMLFile(path string) (RefKind, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return "", fmt.Errorf("parsing %s: %w", path, err)
	}
	// Box-shaped: has `box:` (top-level), `base:`, or `defaults:` block.
	for _, k := range []string{"box", "base", "defaults"} {
		if _, ok := top[k]; ok {
			return RefKindBox, nil
		}
	}
	// Candy-shaped: has any candy marker. This list roughly matches
	// the candy manifest's documented top-level fields; a YAML that has none of
	// these keys is an error (we don't try to guess).
	for _, k := range []string{"rpm", "deb", "pac", "aur", "tasks", "services", "service", "system_services", "candy", "depends", "env", "path_append", "description"} {
		if _, ok := top[k]; ok {
			return RefKindCandy, nil
		}
	}
	return "", fmt.Errorf("ResolveDeployRef: %s has no recognized box or candy keys", path)
}

// resolveLocalName checks the unified loader's boxes and candy/ for
// a matching name. Cross-kind name reuse is permitted (since 2026-05);
// preferKind decides precedence when the name exists in both kinds.
func resolveLocalName(name, projectDir string, preferKind RefKind) (*DeployRef, error) {
	imgYml := filepath.Join(projectDir, UnifiedFileName)
	candiesDir := filepath.Join(projectDir, DefaultCandyDir, name)

	inImageYml := false
	resolvedImgPath := imgYml
	// Schema v4: only charly.yml is the entry point. Resolve box
	// names through the unified loader (which pulls in sibling per-kind
	// files transparently). No direct file reads here.
	if uf, ok, err := LoadUnified(projectDir); err == nil && ok && uf != nil {
		// Namespace-aware presence check via the single resolver, so a qualified
		// deploy ref (`charly bundle add charly.<image>`) resolves the same way every
		// other command resolves a box name. Bare names hit the root map
		// exactly as the previous flat `uf.Box[name]` did.
		if _, _, present := uf.ProjectConfig().ResolveBoxRef(name); present {
			inImageYml = true
			resolvedImgPath = imgYml
		}
	}

	inCandies := false
	candyYML := filepath.Join(candiesDir, UnifiedFileName)
	if info, err := os.Stat(candyYML); err == nil && !info.IsDir() {
		inCandies = true
	}

	imageRef := func() *DeployRef {
		return &DeployRef{
			Raw:    name,
			Kind:   RefKindBox,
			Source: RefSourceLocalName,
			Name:   name,
			Path:   resolvedImgPath,
		}
	}
	candyRef := func() *DeployRef {
		return &DeployRef{
			Raw:    name,
			Kind:   RefKindCandy,
			Source: RefSourceLocalName,
			Name:   name,
			Path:   candyYML,
		}
	}

	switch {
	case inImageYml && inCandies:
		// Cross-kind name reuse — preferKind decides. Both kinds
		// remain reachable via explicit paths (./candy/<name>/ or
		// the box config file with a #<name> fragment) or via ResolveDeployRefAsCandy.
		if preferKind == RefKindCandy {
			return candyRef(), nil
		}
		return imageRef(), nil
	case inImageYml:
		return imageRef(), nil
	case inCandies:
		return candyRef(), nil
	}

	return nil, fmt.Errorf("ResolveDeployRef: %q not found as box in %s or candy in %s",
		name, imgYml, candiesDir)
}
