package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// unifiedFileName is the one entity filename (the project rulebook's "one filename charly.yml").
const unifiedFileName = "charly.yml"

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
// checked out. A submodule with no charly.yml is an uninitialised gitlink — skipped rather than
// failed, so the generator still runs in a partially-initialised clone (and the coverage
// assertions in the check bed are what catch a genuinely missing family).
func repoRoots(root string) ([]repoRoot, error) {
	roots := []repoRoot{{Namespace: "", Dir: root}}
	boxDir := filepath.Join(root, "box")
	ents, err := os.ReadDir(boxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return roots, nil
		}
		return nil, fmt.Errorf("read box/: %w", err)
	}
	for _, de := range ents {
		if !de.IsDir() {
			continue
		}
		sub := filepath.Join(boxDir, de.Name())
		if _, err := os.Stat(filepath.Join(sub, unifiedFileName)); err != nil {
			continue // uninitialised submodule
		}
		roots = append(roots, repoRoot{Namespace: de.Name(), Dir: sub})
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].Namespace < roots[j].Namespace })
	return roots, nil
}

// collectEntities reads every candy and box definition across every repo root. Discovery is by
// directory convention (candy/<name>/charly.yml, box/<name>/charly.yml) — the same two discovery
// directories the loader uses — so the result is exactly what an author has written down.
func collectEntities(roots []repoRoot) ([]entity, error) {
	var out []entity
	for _, r := range roots {
		for _, kind := range []string{"candy", "box"} {
			dir := filepath.Join(r.Dir, kind)
			ents, err := os.ReadDir(dir)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("read %s: %w", dir, err)
			}
			for _, de := range ents {
				if !de.IsDir() {
					continue
				}
				path := filepath.Join(dir, de.Name(), unifiedFileName)
				found, err := readEntityFile(path, r, filepath.Join(kind, de.Name()))
				if err != nil {
					return nil, err
				}
				out = append(out, found...)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug() < out[j].Slug() })
	return out, nil
}

// readEntityFile decodes one charly.yml. The file is a map of entity NAME to a single kind key;
// this generator documents the `candy:` kind (the one entity kind — a node carrying base:/from:
// is an image, otherwise a layer) and ignores deploy/vm/check nodes, which are orchestration
// rather than catalog content.
func readEntityFile(path string, r repoRoot, relDir string) ([]entity, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc map[string]map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		// A charly.yml that does not fit the name-first shape (a multi-doc bed, a template) is
		// not catalog content — skip it rather than failing the whole generation.
		return nil, nil //nolint:nilerr // intentionally tolerant: non-catalog shapes are skipped
	}
	var out []entity
	for name, kinds := range doc {
		node, ok := kinds["candy"]
		if !ok {
			continue
		}
		// Route on base:/from: exactly as the loader does.
		var router struct {
			Base string `yaml:"base"`
			From string `yaml:"from"`
		}
		if err := node.Decode(&router); err != nil {
			continue
		}
		e := entity{Name: name, Namespace: r.Namespace, Dir: relDir}
		if router.Base != "" || router.From != "" {
			var b boxView
			if err := node.Decode(&b); err != nil {
				return nil, fmt.Errorf("decode box %q in %s: %w", name, path, err)
			}
			e.IsBox, e.Box = true, &b
		} else {
			var c candyView
			if err := node.Decode(&c); err != nil {
				return nil, fmt.Errorf("decode candy %q in %s: %w", name, path, err)
			}
			e.Candy = &c
		}
		out = append(out, e)
	}
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
