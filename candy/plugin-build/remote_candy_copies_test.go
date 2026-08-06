package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// remote_candy_copies_test.go — migrated from charly/deploy_target_pod_test.go's
// TestCreateRemoteCandyCopies_StagesRemoteCandySource (K-wave 2 cone R1). The host's own copy of
// createRemoteCandyCopies had been production-dead since the K3 host-prep move — every live caller
// already reached host_prep.go's twin here — but its ONLY regression test still lived over there, so
// deleting the dead core function would have silently dropped the guard for a real build failure.
// The test moves with the code, exercising the surviving implementation.

// TestCreateRemoteCandyCopies_StagesRemoteCandySource guards the remote-candy staging: for a REMOTE
// candy, createRemoteCandyCopies must place the candy's source tree under
// .build/_candy/<name>.<version>/ so the candy's `FROM scratch AS <name>` + `COPY <src>/ /`
// resolves. Without it the real overlay build fails at
// `COPY .build/_candy/<name>.<version>/: no such file or directory`.
func TestCreateRemoteCandyCopies_StagesRemoteCandySource(t *testing.T) {
	ctxRoot := t.TempDir() // the build-context root (the project dir)

	// Simulate a fetched REMOTE add_candy candy cache dir carrying a copy: source file.
	remoteSrc := filepath.Join(ctxRoot, "remote-cache", "marker")
	if err := os.MkdirAll(remoteSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remoteSrc, "copied.dat"), []byte("POD-ADDCANDY-COPIED-OK\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const ver = "2026.181.1430"
	candies := loaderkit.FinalizeScannedCandies(map[string]spec.ScannedCandy{
		"marker": {
			Model: spec.CandyModel{Name: "marker", Version: ver, SourceDir: remoteSrc},
			View:  spec.CandyView{Name: "marker", Remote: true, RepoPath: "github.com/x/y", SubPathPrefix: "candy/"},
		},
	}, nil)

	buildDir := filepath.Join(ctxRoot, ".build")
	if err := createRemoteCandyCopies(buildDir, candies); err != nil {
		t.Fatalf("createRemoteCandyCopies: %v", err)
	}

	staged := filepath.Join(buildDir, "_candy", "marker."+ver, "copied.dat")
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("remote candy source not staged at %s (the per-candy scratch stage's COPY would fail): %v", staged, err)
	}
}
