package initkind

// render_service_hooks_test.go — R8 coverage for the exec_start_pre/exec_start_post service hooks.
//
// The sibling matrix test (render_service_unit_test.go) renders against test-local template
// constants, which proves the render PLUMBING but would keep passing if the SHIPPED systemd
// template never emitted the directives. So this file renders the REAL template — read out of
// charly/charly.yml's systemd init vocabulary — and asserts the emitted unit text. An artifact
// assertion has to read the artifact's own source, not a copy of it (R8).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// shippedSystemdServiceSchema reads the systemd init system's service_schema straight out of the
// repo's init vocabulary, so the assertions below run against the template charly actually ships.
func shippedSystemdServiceSchema(t *testing.T) *spec.InitServiceSchema {
	t.Helper()
	path := filepath.Join("..", "..", "charly", "charly.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the init vocabulary at %s: %v", path, err)
	}
	// Decode lazily per top-level key: charly.yml mixes scalars (version:), sequences and
	// entity nodes at the top level, so only the systemd node can be given a concrete shape.
	var doc map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	node, ok := doc["systemd"]
	if !ok {
		t.Fatalf("%s has no top-level systemd entity", path)
	}
	var entry struct {
		Init struct {
			ServiceSchema *spec.InitServiceSchema `yaml:"service_schema"`
		} `yaml:"init"`
	}
	if err := node.Decode(&entry); err != nil {
		t.Fatalf("decoding the systemd init node from %s: %v", path, err)
	}
	if entry.Init.ServiceSchema == nil {
		t.Fatalf("%s has no systemd init service_schema", path)
	}
	if strings.TrimSpace(entry.Init.ServiceSchema.ServiceTemplate) == "" {
		t.Fatalf("%s systemd service_schema has an empty service_template", path)
	}
	return entry.Init.ServiceSchema
}

func renderWithShippedSystemd(t *testing.T, entry *spec.ServiceEntry) string {
	t.Helper()
	schema := shippedSystemdServiceSchema(t)
	def := withRaw(&spec.ResolvedInit{ManagementTool: "systemctl", ServiceSchema: schema})
	rendered, err := renderService(entry, def, spec.ServiceRenderContext{
		Candy:         "k3s-server",
		SystemUnitDir: "/etc/systemd/system",
	})
	if err != nil {
		t.Fatalf("renderService against the shipped systemd template: %v", err)
	}
	return rendered.UnitText
}

// TestShippedSystemdTemplateEmitsStartHooks is the R8 assertion: the directives must appear, in
// authored order, in the unit text the shipped template produces.
func TestShippedSystemdTemplateEmitsStartHooks(t *testing.T) {
	unit := renderWithShippedSystemd(t, &spec.ServiceEntry{
		Name:          "k3s",
		Exec:          "/usr/local/bin/k3s server",
		Restart:       "always",
		Scope:         "system",
		Enable:        true,
		ExecStartPre:  []string{"/usr/local/bin/k3s-cpuset-ensure.sh"},
		ExecStartPost: []string{"-/usr/local/bin/k3s-crd-heal.sh %n"},
	})

	for _, want := range []string{
		"ExecStartPre=/usr/local/bin/k3s-cpuset-ensure.sh",
		"ExecStart=/usr/local/bin/k3s server",
		"ExecStartPost=-/usr/local/bin/k3s-crd-heal.sh %n",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("shipped systemd unit is missing %q; got:\n%s", want, unit)
		}
	}

	// Ordering matters for readability of the emitted unit (systemd itself orders by directive
	// type, but a pre-hook printed below ExecStart reads as a defect to anyone inspecting it).
	pre := strings.Index(unit, "ExecStartPre=")
	start := strings.Index(unit, "\nExecStart=")
	post := strings.Index(unit, "ExecStartPost=")
	if pre >= start || start >= post {
		t.Errorf("expected ExecStartPre before ExecStart before ExecStartPost; got indices pre=%d start=%d post=%d in:\n%s", pre, start, post, unit)
	}
}

// TestShippedSystemdTemplateMultipleHooksInOrder covers the list form: one directive per element,
// authored order preserved.
func TestShippedSystemdTemplateMultipleHooksInOrder(t *testing.T) {
	unit := renderWithShippedSystemd(t, &spec.ServiceEntry{
		Name:          "multi",
		Exec:          "/usr/bin/daemon",
		Scope:         "system",
		ExecStartPre:  []string{"/bin/first", "/bin/second"},
		ExecStartPost: []string{"/bin/third", "-/bin/fourth"},
	})
	first := strings.Index(unit, "ExecStartPre=/bin/first")
	second := strings.Index(unit, "ExecStartPre=/bin/second")
	third := strings.Index(unit, "ExecStartPost=/bin/third")
	fourth := strings.Index(unit, "ExecStartPost=-/bin/fourth")
	if first < 0 || second < 0 || third < 0 || fourth < 0 {
		t.Fatalf("expected all four hook directives; got:\n%s", unit)
	}
	if first >= second || third >= fourth {
		t.Errorf("hooks rendered out of authored order (pre %d,%d post %d,%d):\n%s", first, second, third, fourth, unit)
	}
}

// TestShippedSystemdTemplateOmitsHooksWhenUnset guards the negative: a service that declares no
// hooks must not gain empty ExecStartPre=/ExecStartPost= lines (an empty one makes systemd fail
// the unit).
func TestShippedSystemdTemplateOmitsHooksWhenUnset(t *testing.T) {
	unit := renderWithShippedSystemd(t, &spec.ServiceEntry{
		Name:    "plain",
		Exec:    "/usr/bin/plain",
		Restart: "always",
		Scope:   "system",
	})
	if strings.Contains(unit, "ExecStartPre") || strings.Contains(unit, "ExecStartPost") {
		t.Errorf("a hookless entry must emit no start-hook directives; got:\n%s", unit)
	}
}

// TestSupervisordIgnoresStartHooks pins the systemd-only scope. supervisord has no equivalent
// directive, so its template simply does not reference the fields — exactly as it already ignores
// wanted_by/before. Declaring hooks must therefore render cleanly rather than error, so a candy
// carrying them stays deployable on a supervisord venue.
func TestSupervisordIgnoresStartHooks(t *testing.T) {
	rendered, err := renderService(&spec.ServiceEntry{
		Name:          "svc",
		Exec:          "/usr/bin/svc",
		Restart:       "always",
		ExecStartPre:  []string{"/bin/pre"},
		ExecStartPost: []string{"/bin/post"},
	}, testSupervisordInitDef(), spec.ServiceRenderContext{Candy: "demo"})
	if err != nil {
		t.Fatalf("supervisord render with hooks set: %v", err)
	}
	if strings.Contains(rendered.UnitText, "/bin/pre") || strings.Contains(rendered.UnitText, "/bin/post") {
		t.Errorf("supervisord fragment must not carry systemd start hooks; got:\n%s", rendered.UnitText)
	}
	if !strings.Contains(rendered.UnitText, "command=/usr/bin/svc") {
		t.Errorf("supervisord fragment lost its command line; got:\n%s", rendered.UnitText)
	}
}
