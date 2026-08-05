package docs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// marketplace is plugins/.claude-plugin/marketplace.json — the authoritative plugin taxonomy.
// Driving the site's recipe section from it means a NEW plugin appears on the site with no
// site-side edit at all: its bucket, description and skill list all follow from the manifest.
type marketplace struct {
	Plugins []marketplacePlugin `json:"plugins"`
}

type marketplacePlugin struct {
	Name        string `json:"name"`   // e.g. "charly-internals"
	Source      string `json:"source"` // e.g. "./internals"
	Description string `json:"description"`
	Category    string `json:"category"` // commands | kind | development | images
}

// Dir is the plugin's directory name under plugins/ (the manifest's source path, minus "./").
func (p marketplacePlugin) Dir() string { return strings.TrimPrefix(p.Source, "./") }

// Ref is the plugin half of a `/charly-<plugin>:<skill>` reference — the manifest name minus the
// `charly-` prefix, which is exactly the directory name.
func (p marketplacePlugin) Ref() string { return strings.TrimPrefix(p.Name, "charly-") }

// skill is one recipe card: a SKILL.md plus any references/*.md split files.
type skill struct {
	PluginDir   string
	PluginName  string
	Name        string
	Title       string
	Description string
	Body        string
	References  []skillReference
}

type skillReference struct {
	Stem string // file stem, e.g. "beds-and-r10"
	Body string
}

// categoryTitle maps a manifest bucket onto the site's section heading.
var categoryTitle = map[string]string{
	"commands":    "Commands — runtime CLI verbs",
	"kind":        "Kinds — schema authoring",
	"development": "Development — contributor internals",
	"images":      "Images — the deployable catalog",
}

// readMarketplace loads the plugin taxonomy.
func readMarketplace(root string) (*marketplace, error) {
	path := filepath.Join(root, "plugins", ".claude-plugin", "marketplace.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read marketplace.json: %w", err)
	}
	var m marketplace
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse marketplace.json: %w", err)
	}
	sort.Slice(m.Plugins, func(i, j int) bool { return m.Plugins[i].Name < m.Plugins[j].Name })
	return &m, nil
}

// collectSkills reads every skill of every plugin named by the manifest. The plugins tree is a
// submodule, so this reads it at whatever gitlink the superproject currently pins — the site
// therefore always matches the plugins version this repo actually ships.
func collectSkills(root string, m *marketplace) ([]skill, error) {
	var out []skill
	for _, p := range m.Plugins {
		skillsDir := filepath.Join(root, "plugins", p.Dir(), "skills")
		ents, err := os.ReadDir(skillsDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", skillsDir, err)
		}
		for _, de := range ents {
			if !de.IsDir() {
				continue
			}
			s, err := readSkill(skillsDir, p, de.Name())
			if err != nil {
				return nil, err
			}
			if s != nil {
				out = append(out, *s)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PluginDir != out[j].PluginDir {
			return out[i].PluginDir < out[j].PluginDir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func readSkill(skillsDir string, p marketplacePlugin, name string) (*skill, error) {
	path := filepath.Join(skillsDir, name, "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	front, body := splitFrontmatter(string(raw))
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if front != "" {
		if err := yaml.Unmarshal([]byte(front), &fm); err != nil {
			return nil, fmt.Errorf("parse frontmatter of %s: %w", path, err)
		}
	}
	title := fm.Name
	if title == "" {
		title = name
	}
	s := skill{
		PluginDir:   p.Dir(),
		PluginName:  p.Name,
		Name:        name,
		Title:       title,
		Description: fm.Description,
		Body:        body,
	}
	refDir := filepath.Join(skillsDir, name, "references")
	refEnts, err := os.ReadDir(refDir)
	if err == nil {
		for _, re := range refEnts {
			if re.IsDir() || !strings.HasSuffix(re.Name(), ".md") {
				continue
			}
			rraw, err := os.ReadFile(filepath.Join(refDir, re.Name()))
			if err != nil {
				return nil, fmt.Errorf("read reference %s: %w", re.Name(), err)
			}
			// Reference files carry no frontmatter by contract — only the entry SKILL.md does.
			s.References = append(s.References, skillReference{
				Stem: strings.TrimSuffix(re.Name(), ".md"),
				Body: string(rraw),
			})
		}
		sort.Slice(s.References, func(i, j int) bool { return s.References[i].Stem < s.References[j].Stem })
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", refDir, err)
	}
	return &s, nil
}

// splitFrontmatter separates a leading `---` YAML block from the markdown body.
func splitFrontmatter(s string) (front, body string) {
	if !strings.HasPrefix(s, "---\n") {
		return "", s
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return "", s
	}
	return rest[:end], strings.TrimPrefix(rest[end+len("\n---\n"):], "\n")
}

// skillPagePath is the content-root-relative file for a skill's card.
func skillPagePath(s skill) string {
	return fmt.Sprintf("recipes/%s/%s.md", s.PluginDir, s.Name)
}

// skillSitePath is the URL Starlight serves that card at.
func skillSitePath(s skill) string {
	return fmt.Sprintf("/recipes/%s/%s/", s.PluginDir, s.Name)
}

func referencePagePath(s skill, stem string) string {
	return fmt.Sprintf("recipes/%s/%s/%s.md", s.PluginDir, s.Name, stem)
}

func referenceSitePath(s skill, stem string) string {
	return fmt.Sprintf("/recipes/%s/%s/%s/", s.PluginDir, s.Name, stem)
}

// buildResolver indexes every skill and reference page so cross-references can be rewritten and
// anything unresolvable can be reported as a build error.
func buildResolver(skills []skill, m *marketplace) linkResolver {
	dirToRef := map[string]string{}
	for _, p := range m.Plugins {
		dirToRef[p.Dir()] = p.Ref()
	}
	lr := linkResolver{
		skillPages: map[string]string{},
		refPages:   map[string]string{},
	}
	for _, s := range skills {
		ref := dirToRef[s.PluginDir]
		lr.skillPages[ref+":"+s.Name] = skillSitePath(s)
		for _, r := range s.References {
			lr.refPages[s.PluginDir+"/"+s.Name+"/"+r.Stem] = referenceSitePath(s, r.Stem)
		}
	}
	return lr
}

// generateSkills emits every recipe card and its reference pages.
func generateSkills(outRoot string, skills []skill, m *marketplace, lr linkResolver) (int, []danglingRef, error) {
	dirToPlugin := map[string]marketplacePlugin{}
	for _, p := range m.Plugins {
		dirToPlugin[p.Dir()] = p
	}
	var dangling []danglingRef
	count := 0
	for _, s := range skills {
		p := dirToPlugin[s.PluginDir]
		body, d := lr.rewrite(s.Body, s.PluginDir, s.Name, skillPagePath(s))
		dangling = append(dangling, d...)

		var b strings.Builder
		fmt.Fprintf(&b, "*Recipe card from the `%s` plugin (%s).*\n\n", p.Name, categoryLabel(p.Category))
		if len(s.References) > 0 {
			b.WriteString("This card has additional detail pages:\n\n")
			for _, r := range s.References {
				fmt.Fprintf(&b, "- [%s](%s)\n", r.Stem, referenceSitePath(s, r.Stem))
			}
			b.WriteString("\n")
		}
		b.WriteString(body)

		if err := (page{
			Path:        skillPagePath(s),
			Title:       s.Title,
			Description: s.Description,
			Body:        b.String(),
		}).write(outRoot); err != nil {
			return 0, nil, err
		}
		count++

		for _, r := range s.References {
			rbody, rd := lr.rewrite(r.Body, s.PluginDir, s.Name, referencePagePath(s, r.Stem))
			dangling = append(dangling, rd...)
			intro := fmt.Sprintf("*Detail page of the [%s](%s) recipe card.*\n\n", s.Title, skillSitePath(s))
			if err := (page{
				Path:        referencePagePath(s, r.Stem),
				Title:       fmt.Sprintf("%s — %s", s.Title, r.Stem),
				Description: fmt.Sprintf("Detail page of the %s recipe card.", s.Title),
				Body:        intro + rbody,
			}).write(outRoot); err != nil {
				return 0, nil, err
			}
			count++
		}
	}
	return count, dangling, nil
}

func categoryLabel(c string) string {
	if t, ok := categoryTitle[c]; ok {
		return t
	}
	return c
}
