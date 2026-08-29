package main

// Core RDD loop: validate every candy charly.yml this repo still owns against the embedded
// #CandyFile CUE schema. Iterate the schema (schema/candy.cue) until all pass.
//
// The corpus used to be `../candy`, which the candy de-submodule cutover emptied and
// removed: every deployable candy now lives in its own kind-prefixed repo and is validated
// by that repo's own CI. Two candy manifests remain in charly, for reasons that are not
// "not migrated yet":
//
//   - packaging/charly.yml        — charly's OWN native-package metadata, published as the
//                                   `charly-candy-charly.yml` release asset the six
//                                   per-distro package repos consume as `--candy`.
//   - tools/generate-packages/    — the command-plugin re-export shim, the dev path that
//     charly.yml                    makes `charly generate-packages` resolvable from a
//                                   checkout.
//
// The manifests are DISCOVERED under the roots that hold them, not hand-listed: a new candy
// added under packaging/ or tools/ is picked up automatically, so this test cannot silently
// stop covering something. The roots are named rather than walking the whole repo, because
// charly/testdata/ carries DELIBERATELY INVALID candy fixtures and check/ carries a project
// manifest — neither is a candy this repo ships, and sweeping them in would make the test
// fail on files it was never meant to police.

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// candyManifests returns every candy charly.yml in the repo — that is, every charly.yml
// EXCEPT the repo-root project manifest, which is a project file rather than a candy file
// and is validated by `charly box validate`, not by #CandyFile.
func candyManifests(t *testing.T) []string {
	t.Helper()
	// The roots that hold the candy manifests this repo ships. `../candy` was the only
	// one until the de-submodule cutover removed it.
	roots := []string{"../packaging", "../tools"}
	var out []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if !d.IsDir() && d.Name() == "charly.yml" {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	sort.Strings(out)
	return out
}

func TestCandyCUESchema_Corpus(t *testing.T) {
	manifests := candyManifests(t)
	if len(manifests) == 0 {
		t.Fatal("no candy charly.yml found outside the root project manifest — the layout " +
			"changed and this test would otherwise pass vacuously")
	}
	var total, ok int
	var fails []string
	for _, p := range manifests {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		total++
		if verr := requireProjectLoader().ValidateCandyManifestCUE(p, data, loaderThreaded(), requireLoaderParser()); verr != nil {
			fails = append(fails, strings.TrimSpace(verr.Error()))
		} else {
			ok++
		}
	}
	t.Logf("candy CUE validation: %d/%d passed (%s)", ok, total, strings.Join(manifests, ", "))
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
