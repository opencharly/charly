package spec

import (
	"reflect"
	"strings"
	"testing"
)

// TestNoKindWordAsStepVerb is the EDGE-INHERIT machine-check (the "kinds only at
// the edges" invariant, cutover A): NO step-Op VERB discriminator may be spelled
// like a reserved KIND word. A kind word belongs at a config EDGE (a top-level or
// tree-child node discriminator), never in the MIDDLE of a step Op. Before this
// invariant the `group:` (unix group) and `k8s:` (cluster) probe verbs collided
// with the `group` (Calamares) and `k8s` (deploy substrate) kinds; they are now
// `unix_group:` / `kube:`. This test fails the moment a future verb reintroduces a
// kind word — forcing the author to pick a non-colliding spelling.
//
// The companion cross-reference half of the invariant — no deploy VALUE def may
// carry a kind word as a non-opening cross-ref field (the `bundle:{box:X}` shape)
// — lands with cutover B (bundle: elimination), where the cross-ref fields are
// deleted in favour of from:/image:/tree-position.
func TestNoKindWordAsStepVerb(t *testing.T) {
	kind := make(map[string]bool, len(KindWords))
	for _, k := range KindWords {
		kind[k] = true
	}
	for _, verb := range OpVerbs {
		if kind[verb] {
			t.Errorf("step verb %q is a reserved KIND word — kinds live only at config edges, "+
				"never as a step verb. Rename the verb (e.g. group→unix_group, k8s→kube).", verb)
		}
	}
}

// TestNoKindWordAsOpModifier extends the invariant to the step Op's MODIFIER
// fields: a modifier key must not be spelled like a reserved KIND word either.
// The sole documented exemption is `target` — the adb/appium/wl app/element
// target modifier, a homonym of the niche Calamares `target` kind (an installer
// sub-kind, never a deploy substrate). The two never co-occur in one document and
// the collision predates EDGE-INHERIT; it is tracked, not silently ignored. Any
// NEW modifier collision fails here.
func TestNoKindWordAsOpModifier(t *testing.T) {
	exempt := map[string]bool{"target": true} // Op UI-target modifier vs Calamares `target` kind (homonym, tracked)
	kind := make(map[string]bool, len(KindWords))
	for _, k := range KindWords {
		kind[k] = true
	}
	rt := reflect.TypeOf(Op{})
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("yaml")
		name := strings.TrimSuffix(strings.SplitN(tag, ",", 2)[0], "")
		if name == "" || name == "-" {
			continue
		}
		if kind[name] && !exempt[name] {
			t.Errorf("Op modifier %q is a reserved KIND word — kinds live only at config edges. "+
				"Rename the modifier, or (if a genuine tracked homonym) add it to the exemption set with a reason.", name)
		}
	}
}
