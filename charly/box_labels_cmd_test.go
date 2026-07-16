package main

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

func TestCanonicalLabelKey_ExpandsShorthand(t *testing.T) {
	cases := map[string]string{
		"init":                      "ai.opencharly.init",
		"version":                   "ai.opencharly.version",
		"ai.opencharly.description": "ai.opencharly.description",
		"org.opencontainers.x":      "org.opencontainers.x",
	}
	for in, want := range cases {
		if got := canonicalLabelKey(in); got != want {
			t.Errorf("canonicalLabelKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSortedLabelKeys_FiltersToContractUnlessAll(t *testing.T) {
	labels := map[string]string{
		"ai.opencharly.version": "2026.001.0001",
		"ai.opencharly.init":    "supervisord",
		"maintainer":            "someone",
	}
	if got := sortedLabelKeys(labels, false); !reflect.DeepEqual(got, []string{"ai.opencharly.init", "ai.opencharly.version"}) {
		t.Errorf("contract-only keys = %v", got)
	}
	if got := sortedLabelKeys(labels, true); !reflect.DeepEqual(got, []string{"ai.opencharly.init", "ai.opencharly.version", "maintainer"}) {
		t.Errorf("all keys = %v", got)
	}
}

func TestApplySecretRefresh_NamedAllAndUnmatched(t *testing.T) {
	base := []deploykit.CollectedSecret{
		{Name: "charly-app-db-password", SecretName: "db-password"},
		{Name: "charly-app-api-key", SecretName: "api-key"},
	}

	out, unmatched := ApplySecretRefresh(append([]deploykit.CollectedSecret(nil), base...), nil)
	if len(unmatched) != 0 || out[0].RotateOnConfig || out[1].RotateOnConfig {
		t.Fatal("no-op refresh must not rotate or report unmatched")
	}

	out, unmatched = ApplySecretRefresh(append([]deploykit.CollectedSecret(nil), base...), []string{"db-password", "nope"})
	if !out[0].RotateOnConfig || out[1].RotateOnConfig {
		t.Errorf("named refresh rotated wrong set: %+v", out)
	}
	if !reflect.DeepEqual(unmatched, []string{"nope"}) {
		t.Errorf("unmatched = %v, want [nope]", unmatched)
	}

	out, unmatched = ApplySecretRefresh(append([]deploykit.CollectedSecret(nil), base...), []string{"all"})
	if !out[0].RotateOnConfig || !out[1].RotateOnConfig || len(unmatched) != 0 {
		t.Errorf("'all' refresh must rotate everything: %+v unmatched=%v", out, unmatched)
	}
}

// NOTE: TestParsePS_PodmanRowsCarryImageRef (formerly here) moved to the
// enginekit package with the parsePS engine-parsing code it exercised (chunk 1
// relocated parsePS to sdk/enginekit as an unexported symbol).
