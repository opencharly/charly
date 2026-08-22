package spec

import "testing"

// config_test.go — sdk-level coverage for Config's box-accessor + namespace-resolution methods
// relocated out of charly/uf_box_generic.go + charly/namespace.go (FLOOR-SLIM Unit 5). charly's
// own (much larger) config_test.go / namespace_test.go / resolver_unify_test.go already exercise
// these live through LoadUnified fixtures; this file proves them LIVE in the sdk gate too,
// independent of charly.

func TestConfig_BoxAccessors(t *testing.T) {
	cfg := &Config{}
	if cfg.HasBox("app") {
		t.Error("HasBox(app) should be false on an empty Config")
	}
	cfg.SetBox("app", BoxConfig{Base: "quay.io/fedora/fedora:43"})
	if !cfg.HasBox("app") {
		t.Error("HasBox(app) should be true after SetBox")
	}
	got, ok := cfg.BoxConfig("app")
	if !ok || got.Base != "quay.io/fedora/fedora:43" {
		t.Errorf("BoxConfig(app) = %+v, %v; want Base=quay.io/fedora/fedora:43, true", got, ok)
	}
	if _, ok := cfg.BoxConfig("missing"); ok {
		t.Error("BoxConfig(missing) should be false")
	}
}

func TestConfig_AllBoxNamesAndEachBox(t *testing.T) {
	cfg := &Config{}
	cfg.SetBox("zebra", BoxConfig{})
	cfg.SetBox("app", BoxConfig{})
	names := cfg.AllBoxNames()
	if len(names) != 2 || names[0] != "app" || names[1] != "zebra" {
		t.Errorf("AllBoxNames() = %v, want sorted [app, zebra]", names)
	}
	var seen []string
	cfg.EachBox(func(name string, _ BoxConfig) bool {
		seen = append(seen, name)
		return true
	})
	if len(seen) != 2 {
		t.Errorf("EachBox visited %d boxes, want 2", len(seen))
	}
	// Early-stop via a false return.
	var stopped []string
	cfg.EachBox(func(name string, _ BoxConfig) bool {
		stopped = append(stopped, name)
		return false
	})
	if len(stopped) != 1 {
		t.Errorf("EachBox should stop after the first false return, got %v", stopped)
	}
}

func TestConfig_BoxNames_FiltersDisabled(t *testing.T) {
	disabled := false
	cfg := &Config{}
	cfg.SetBox("on", BoxConfig{})
	cfg.SetBox("off", BoxConfig{Enabled: &disabled})
	names := cfg.BoxNames()
	if len(names) != 1 || names[0] != "on" {
		t.Errorf("BoxNames() = %v, want [on] (disabled excluded)", names)
	}
}

func TestConfig_ResolveBoxRef_NamespaceDescent(t *testing.T) {
	sub := &Config{}
	sub.SetBox("widget", BoxConfig{Base: "quay.io/fedora/fedora:43"})
	root := &Config{Namespaces: map[string]*Config{"sub": sub}}
	root.SetBox("app", BoxConfig{})

	if _, _, ok := root.ResolveBoxRef("app"); !ok {
		t.Error("ResolveBoxRef(app) should resolve in root")
	}
	img, cfg, ok := root.ResolveBoxRef("sub.widget")
	if !ok {
		t.Fatal("ResolveBoxRef(sub.widget) should resolve via namespace descent")
	}
	if img.Base != "quay.io/fedora/fedora:43" {
		t.Errorf("resolved base = %q", img.Base)
	}
	if cfg != sub {
		t.Error("ResolveBoxRef should return the SUB-namespace Config, not root")
	}
	if _, _, ok := root.ResolveBoxRef("nope.widget"); ok {
		t.Error("ResolveBoxRef(nope.widget) should fail: no such namespace")
	}
}

func TestConfig_FindBoxByLeaf(t *testing.T) {
	sub := &Config{}
	sub.SetBox("widget", BoxConfig{})
	root := &Config{Namespaces: map[string]*Config{"sub": sub}}
	root.SetBox("app", BoxConfig{})

	if got, ok := root.FindBoxByLeaf("app"); !ok || got != "app" {
		t.Errorf("FindBoxByLeaf(app) = %q,%v; want app,true (root hit)", got, ok)
	}
	if got, ok := root.FindBoxByLeaf("widget"); !ok || got != "sub.widget" {
		t.Errorf("FindBoxByLeaf(widget) = %q,%v; want sub.widget,true (namespaced hit)", got, ok)
	}
	if _, ok := root.FindBoxByLeaf("absent"); ok {
		t.Error("FindBoxByLeaf(absent) should be false")
	}
}

func TestConfig_WalkBaseChain_StopsAtNamespaceBoundary(t *testing.T) {
	sub := &Config{}
	sub.SetBox("widget", BoxConfig{})
	root := &Config{Namespaces: map[string]*Config{"sub": sub}}
	root.SetBox("parent", BoxConfig{})
	root.SetBox("child", BoxConfig{Base: "parent"})
	root.SetBox("nschild", BoxConfig{Base: "sub.widget"})

	chain := root.WalkBaseChain("child")
	if len(chain) != 2 || chain[0].Name != "child" || chain[1].Name != "parent" {
		t.Errorf("WalkBaseChain(child) = %+v, want [child, parent]", chain)
	}
	// A namespace-qualified base is NOT descended (BoxConfig("sub.widget") looks up a LITERAL
	// key in root's flat map, which doesn't exist there — so the walk stops after the leaf).
	nsChain := root.WalkBaseChain("nschild")
	if len(nsChain) != 1 || nsChain[0].Name != "nschild" {
		t.Errorf("WalkBaseChain(nschild) = %+v, want [nschild] (stops at namespace boundary)", nsChain)
	}
}

func TestConfig_WalkBaseChainDistroAndBuild_InheritThroughChain(t *testing.T) {
	root := &Config{}
	root.SetBox("fedora", BoxConfig{Base: "quay.io/fedora/fedora:43", Distro: []string{"fedora:43", "fedora"}, Build: []string{"rpm"}})
	root.SetBox("child", BoxConfig{Base: "fedora"})
	root.SetBox("grandchild", BoxConfig{Base: "child"})

	if got := root.WalkBaseChainDistro("grandchild"); len(got) != 2 || got[0] != "fedora:43" {
		t.Errorf("WalkBaseChainDistro(grandchild) = %v, want [fedora:43 fedora] (inherited through chain)", got)
	}
	if got := root.WalkBaseChainBuild("grandchild"); len(got) != 1 || got[0] != "rpm" {
		t.Errorf("WalkBaseChainBuild(grandchild) = %v, want [rpm] (inherited through chain)", got)
	}
}

func TestBoxMapHelpers(t *testing.T) {
	m := BoxMap{}
	m["b"] = EncodeBox(BoxConfig{Base: "x"})
	m["a"] = EncodeBox(BoxConfig{Base: "y"})
	names := BoxNamesOf(m)
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("BoxNamesOf() = %v, want sorted [a, b]", names)
	}
	got, ok := BoxConfigFrom(m, "a")
	if !ok || got.Base != "y" {
		t.Errorf("BoxConfigFrom(a) = %+v, %v; want Base=y, true", got, ok)
	}
	if _, ok := DecodeBox(nil); ok {
		t.Error("DecodeBox(nil) should be false")
	}
}
