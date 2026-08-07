package fleet

// deploy_ref_test.go — the K4-C shape-2 ref-classification parity golden (the plugin half of the
// former charly/deploy_ref_test.go). It proves the box-vs-candy classification resolveDeployRef /
// resolveDeployRefAsCandy now do OFF THE RESOLVED-PROJECT ENVELOPE (rp.Boxes / rp.Candies) is
// byte-faithful to the former host LoadUnified + os.Stat resolver across every ref form:
// local-name (box-first / candy-first preference + namespace-qualified fullKey + cross-kind reuse),
// remote (@github box/candy/bare subpath), and local-path (classifyYAMLFile). NON-VACUOUS: the rp
// is a HAND-BUILT envelope independent of the resolver — a resolver that ignored rp would fail the
// box-only / candy-only / not-found cases.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/spec"
)

// testEnvelope hand-builds a resolved-project envelope with one box-only name, one candy-only name,
// one name in BOTH kinds (cross-kind reuse), and one NAMESPACE-QUALIFIED box (fullKey-keyed).
func testEnvelope() *spec.ResolvedProject {
	return &spec.ResolvedProject{
		Boxes: map[string]spec.ResolvedBoxView{
			"fedora-coder":   {},
			"charly.jupyter": {}, // namespace-qualified fullKey (root has no bare "jupyter")
			"dup":            {}, // cross-kind reuse — also a candy below
		},
		Candies: map[string]spec.CandyView{
			"ripgrep": {},
			"dup":     {}, // cross-kind reuse
		},
	}
}

func TestResolveDeployRef_LocalNameBox(t *testing.T) {
	got, err := resolveDeployRef(testEnvelope(), "fedora-coder", "")
	if err != nil {
		t.Fatalf("resolveDeployRef: %v", err)
	}
	if got.Kind != RefKindBox || got.Source != RefSourceLocalName || got.Name != "fedora-coder" {
		t.Fatalf("box name: got %+v, want box/local-name/fedora-coder", got)
	}
}

func TestResolveDeployRef_LocalNameCandy(t *testing.T) {
	got, err := resolveDeployRef(testEnvelope(), "ripgrep", "")
	if err != nil {
		t.Fatalf("resolveDeployRef: %v", err)
	}
	if got.Kind != RefKindCandy || got.Source != RefSourceLocalName {
		t.Fatalf("candy name: got %+v, want candy/local-name", got)
	}
}

func TestResolveDeployRef_NamespaceQualifiedBox(t *testing.T) {
	// rp.Boxes is fullKey-keyed, so a qualified ref is a direct map hit (byte-exact to the host
	// ResolveBoxRef descent). A bare leaf that lives ONLY in a namespace is absent under its bare
	// key — the resolver reports not-found, exactly as ResolveBoxRef returns false for it.
	got, err := resolveDeployRef(testEnvelope(), "charly.jupyter", "")
	if err != nil {
		t.Fatalf("qualified box: %v", err)
	}
	if got.Kind != RefKindBox || got.Name != "charly.jupyter" {
		t.Fatalf("qualified box: got %+v, want box/charly.jupyter", got)
	}
	if _, err := resolveDeployRef(testEnvelope(), "jupyter", ""); err == nil {
		t.Fatal("bare leaf of a namespaced-only box must be not-found (matching ResolveBoxRef)")
	}
}

func TestResolveDeployRef_CrossKindPreference(t *testing.T) {
	// "dup" is BOTH a box and a candy — preferKind decides. The primary <ref> resolver is box-first;
	// the --add-candy resolver is candy-first.
	if got, err := resolveDeployRef(testEnvelope(), "dup", ""); err != nil || got.Kind != RefKindBox {
		t.Fatalf("primary dup: got %+v err=%v, want box-first", got, err)
	}
	if got, err := resolveDeployRefAsCandy(testEnvelope(), "dup", ""); err != nil || got.Kind != RefKindCandy {
		t.Fatalf("add-candy dup: got %+v err=%v, want candy-first", got, err)
	}
}

func TestResolveDeployRef_NotFound(t *testing.T) {
	if _, err := resolveDeployRef(testEnvelope(), "nope", ""); err == nil {
		t.Fatal("unknown name must error")
	}
}

func TestResolveDeployRef_RemoteSubpath(t *testing.T) {
	cases := []struct {
		ref  string
		kind RefKind
	}{
		{"@github.com/opencharly/charly/candy/ripgrep:v2026.1.0", RefKindCandy},
		{"@github.com/opencharly/charly/box/fedora-coder:v2026.1.0", RefKindBox},
		{"github.com/opencharly/charly/candy/ripgrep@v2026.1.0", RefKindCandy}, // no leading @
		{"@github.com/opencharly/charly", RefKindBox},                          // bare repo → box
	}
	for _, c := range cases {
		got, err := resolveDeployRef(testEnvelope(), c.ref, "")
		if err != nil {
			t.Fatalf("remote %q: %v", c.ref, err)
		}
		if got.Kind != c.kind || got.Source != RefSourceRemote {
			t.Fatalf("remote %q: got %s/%s, want %s/remote", c.ref, got.Kind, got.Source, c.kind)
		}
	}
}

func TestResolveDeployRef_LocalPath(t *testing.T) {
	dir := t.TempDir()
	boxPath := filepath.Join(dir, "mybox.yml")
	if err := os.WriteFile(boxPath, []byte("base: quay.io/fedora/fedora:43\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	candyPath := filepath.Join(dir, "mycandy.yml")
	if err := os.WriteFile(candyPath, []byte("rpm:\n  packages: [ripgrep]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveDeployRef(testEnvelope(), boxPath, ""); err != nil || got.Kind != RefKindBox || got.Source != RefSourceLocalPath {
		t.Fatalf("local box path: got %+v err=%v", got, err)
	}
	if got, err := resolveDeployRef(testEnvelope(), candyPath, ""); err != nil || got.Kind != RefKindCandy || got.Source != RefSourceLocalPath {
		t.Fatalf("local candy path: got %+v err=%v", got, err)
	}
}
