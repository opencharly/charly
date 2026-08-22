package spec

import "testing"

func TestSplitNamespaceRef(t *testing.T) {
	cases := []struct {
		ref      string
		wantNs   string
		wantRest string
		wantOk   bool
	}{
		{"charly.arch-builder", "charly", "arch-builder", true},
		{"a.b.c", "a", "b.c", true},
		{"fedora", "", "", false},
		{".fedora", "", "", false},
		{"fedora.", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		ns, rest, ok := SplitNamespaceRef(c.ref)
		if ns != c.wantNs || rest != c.wantRest || ok != c.wantOk {
			t.Errorf("SplitNamespaceRef(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.ref, ns, rest, ok, c.wantNs, c.wantRest, c.wantOk)
		}
	}
}

func TestLeafName(t *testing.T) {
	cases := map[string]string{
		"charly.arch-builder": "arch-builder",
		"a.b.c":               "c",
		"fedora":              "fedora",
		"":                    "",
	}
	for ref, want := range cases {
		if got := LeafName(ref); got != want {
			t.Errorf("LeafName(%q) = %q, want %q", ref, got, want)
		}
	}
}
