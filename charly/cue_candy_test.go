package main

// Core RDD loop: validate every real candy charly.yml against the embedded
// #CandyFile CUE schema. Iterate the schema (schema/candy.cue) until all pass.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCandyCUESchema_Corpus(t *testing.T) {
	const root = "../candy"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var total, ok int
	var fails []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(root, e.Name(), "charly.yml")
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		total++
		if verr := validateCandyManifestCUE(p, data); verr != nil {
			fails = append(fails, verr.Error())
		} else {
			ok++
		}
	}
	t.Logf("candy CUE validation: %d/%d passed", ok, total)
	for i, f := range fails {
		if i >= 6 {
			t.Logf("... and %d more failures", len(fails)-6)
			break
		}
		t.Logf("FAIL #%d:\n%s", i+1, f)
	}
	if ok != total {
		t.Fatalf("%d/%d candies failed CUE validation", total-ok, total)
	}
}
