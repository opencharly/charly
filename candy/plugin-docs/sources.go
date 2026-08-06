package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opencharly/sdk/candywalk"
	"gopkg.in/yaml.v3"
)

// unifiedFileName is the one entity filename (the project rulebook's "one filename charly.yml").
const unifiedFileName = candywalk.UnifiedFileName

// entity is one DEFINED candy or box, read straight off disk.
//
// Definedness is the whole point. The resolver's own surfaces cannot be trusted for a catalog:
// `charly box list boxes` lists ENABLED boxes only, and it resolves through main's `import:`
// closure — which pulls arch, cachyos and fedora but NOT debian or ubuntu, so ten checked-out
// box definitions (every debian.* and ubuntu.*) are invisible to it, while ten more appear as
// transitive namespace aliases (cachyos.arch.X duplicating arch.X). A catalog built on that
// surface would silently omit the entire Debian and Ubuntu families. So this walks each repo as
// its own root and unions the results — the defined set, never the default-active one.
type entity struct {
	Name      string // entity name as authored (the top-level charly.yml key)
	Namespace string // "" for the superproject, else the box/<distro> submodule name
	Dir       string // directory holding this entity's charly.yml, relative to the repo root
	IsBox     bool   // a candy: node carrying base:/from: is an IMAGE; otherwise a layer
	Candy     *candyView
	Box       *boxView
}

// PathSegment is the entity's stable page path — namespaced by DIRECTORY so a submodule box
// cannot collide with a superproject candy of the same name.
//
// The separator is a slash rather than a dot because Astro strips dots when deriving a route
// slug: a `fedora.check-pod.md` file would be served at `/fedoracheck-pod/`, which is both ugly
// and a collision waiting to happen. A directory keeps `fedora/check-pod` intact.
func (e entity) PathSegment() string {
	if e.Namespace == "" {
		return e.Name
	}
	return e.Namespace + "/" + e.Name
}

// Slug is the entity's display name, namespace-qualified.
func (e entity) Slug() string {
	if e.Namespace == "" {
		return e.Name
	}
	return e.Namespace + "." + e.Name
}

// Description returns the entity's authored prose (ADE mandates a non-empty one).
func (e entity) Description() string {
	if e.IsBox && e.Box != nil {
		return e.Box.Description
	}
	if e.Candy != nil {
		return e.Candy.Description
	}
	return ""
}

// Version returns the entity's authored CalVer identity.
func (e entity) Version() string {
	if e.IsBox && e.Box != nil {
		return string(e.Box.Version)
	}
	if e.Candy != nil {
		return string(e.Candy.Version)
	}
	return ""
}

// repoRoot is one project tree to walk: the superproject, or a box/<distro> submodule.
type repoRoot struct {
	Namespace string // "" for the superproject
	Dir       string // absolute path
}

// repoRoots enumerates the superproject plus every box/<distro> submodule that is actually
// checked out (delegates the walk to the shared sdk/candywalk kit — R3, the ONE discovery
// abstraction the docs + marketplace generators share).
func repoRoots(root string) ([]repoRoot, error) {
	cr, err := candywalk.RepoRoots(root)
	if err != nil {
		return nil, err
	}
	out := make([]repoRoot, 0, len(cr))
	for _, r := range cr {
		out = append(out, repoRoot{Namespace: r.Namespace, Dir: r.Dir})
	}
	return out, nil
}

// collectEntities reads every DEFINED candy and box across every repo root, via the shared
// sdk/candywalk kit, and projects the `candy:` kind nodes onto this generator's entity/candyView/
// boxView (routing base:/from: exactly as the loader does).
func collectEntities(roots []repoRoot) ([]entity, error) {
	cr := make([]candywalk.Root, 0, len(roots))
	for _, r := range roots {
		cr = append(cr, candywalk.Root{Namespace: r.Namespace, Dir: r.Dir})
	}
	ents, err := candywalk.CollectEntities(cr)
	if err != nil {
		return nil, err
	}
	var out []entity
	for _, e := range ents {
		if e.Kind != "candy" {
			continue // this generator documents the `candy:` kind only
		}
		// Route on base:/from: exactly as the loader does.
		var router struct {
			Base string `yaml:"base"`
			From string `yaml:"from"`
		}
		if err := e.Value.Decode(&router); err != nil {
			continue
		}
		ent := entity{Name: e.Name, Namespace: e.Namespace, Dir: e.Dir}
		if router.Base != "" || router.From != "" {
			var b boxView
			if err := e.Value.Decode(&b); err != nil {
				return nil, fmt.Errorf("decode box %q in %s: %w", e.Name, e.Dir, err)
			}
			ent.IsBox, ent.Box = true, &b
		} else {
			var c candyView
			if err := e.Value.Decode(&c); err != nil {
				return nil, fmt.Errorf("decode candy %q in %s: %w", e.Name, e.Dir, err)
			}
			ent.Candy = &c
		}
		out = append(out, ent)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug() < out[j].Slug() })
	return out, nil
}

// compiledPlugins reads charly/charly.yml's compiled_plugins list — the plugin candies compiled
// INTO the charly binary. Membership is READ here rather than restated in prose, so a plugin's
// documented placement cannot drift when the list changes. A plugin absent from this list still
// loads out-of-process over gRPC when a plan references its word (the coexist path).
func compiledPlugins(root string) (map[string]bool, error) {
	raw, err := os.ReadFile(filepath.Join(root, "charly", unifiedFileName))
	if err != nil {
		return nil, fmt.Errorf("read charly/charly.yml: %w", err)
	}
	var doc struct {
		CompiledPlugins []string `yaml:"compiled_plugins"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse charly/charly.yml: %w", err)
	}
	set := make(map[string]bool, len(doc.CompiledPlugins))
	for _, p := range doc.CompiledPlugins {
		set[strings.TrimSpace(p)] = true
	}
	return set, nil
}
