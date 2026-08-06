// Command golden-cloudinit regenerates the golden fixtures
// candy/plugin-egress/testdata/cloudinit_egress_golden_userdata.yaml and
// candy/plugin-egress/testdata/cloudinit_egress_golden_metadata.yaml that
// candy/plugin-egress/egress_test.go's TestGoldenCloudInit_OutputValidatesAgainstSchema loads via
// plain os.ReadFile — no sdk import needed there anymore.
//
// WHY THIS TOOL EXISTS (#55 final-tail closure, mirroring tools/golden-compile's precedent
// exactly): the test used to call sdk/vmshared.RenderCloudInit directly, in-process, which meant
// charly/egress_test.go imported github.com/opencharly/sdk/vmshared — a violation of charly-core's
// import-purity target. The test's OWN assertion is "the real renderer's real output satisfies
// charly's real egress gate end-to-end" — sdk/vmshared's OWN test suite stubs
// ValidateEgress to a permissive no-op (egress_seam_test.go), so it never proves this. This tool
// computes the SAME render this test used to drive live (identical VmSpec/CloudInitRuntimeParams
// fixture), with vmshared.ValidateEgress wired to the SAME permissive stub sdk/vmshared's own
// suite uses (the render's internal gate is a pass-through here — capturing content, not
// re-validating it), and writes the resulting user-data / meta-data documents to checked-in
// golden files. The plugin-egress test then re-validates those golden bytes against the REAL
// egress schema (the egress family relocated there with the shim, K-wave 2 cone R2) — proving
// the identical invariant, driven from a golden fixture instead of a live sdk call.
//
// DETERMINISM: RenderCloudInit is a pure function of (VmSpec, CloudInitRuntimeParams) given a
// permissive egress stub — no I/O, no plugin RPC.
//
// REGENERATE with: go run ./tools/golden-cloudinit   (run from the repo root, or pass -repo)
// whenever sdk/vmshared's RenderCloudInit (or its VmSpec/CloudInitRuntimeParams inputs) changes
// in a way that alters the rendered cloud-init documents.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/vmshared"
)

const (
	userDataGoldenRelPath = "candy/plugin-egress/testdata/cloudinit_egress_golden_userdata.yaml"
	metaDataGoldenRelPath = "candy/plugin-egress/testdata/cloudinit_egress_golden_metadata.yaml"
)

// testPubKey mirrors charly/egress_test.go's own fixture constant exactly (kept in lockstep by
// inspection — both are tiny, stable literals).
const testPubKey = "ssh-ed25519 AAAATESTKEY user@host"

func main() {
	repo := flag.String("repo", ".", "path to the opencharly superproject root")
	flag.Parse()

	// A permissive stub — mirrors sdk/vmshared/egress_seam_test.go's own wiring exactly. This
	// tool captures RENDERED CONTENT; the charly-side test is what re-validates it for real.
	vmshared.ValidateEgress = func(kind, label string, data []byte) error { return nil }

	spec := &vmshared.VmSpec{Source: vmshared.VmSource{Kind: "cloud_image", Distro: "arch", BaseUser: "arch"}}
	rt := vmshared.CloudInitRuntimeParams{
		SSHPublicKey:          testPubKey,
		InjectKeyViaCloudInit: true,
		InstanceID:            "iid-xyz",
		Hostname:              "egress-vm",
	}
	userData, metaData, _, err := vmshared.RenderCloudInit(spec, rt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "golden-cloudinit: RenderCloudInit: %v\n", err)
		os.Exit(1)
	}
	// RenderCloudInit's internal egress gate validates the user-data document BEFORE
	// prepending the "#cloud-config\n" header (cloud_init_render.go: ValidateEgress runs on
	// userBytes, the header is written to the builder afterward) — strip it here so the golden
	// fixture matches EXACTLY the bytes the real gate validates, byte for byte.
	userData = strings.TrimPrefix(userData, "#cloud-config\n")

	writeGolden(filepath.Join(*repo, userDataGoldenRelPath), userData)
	writeGolden(filepath.Join(*repo, metaDataGoldenRelPath), metaData)
	fmt.Printf("golden-cloudinit: wrote %s + %s\n", userDataGoldenRelPath, metaDataGoldenRelPath)
}

func writeGolden(path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "golden-cloudinit: mkdir %s: %v\n", filepath.Dir(path), err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "golden-cloudinit: write %s: %v\n", path, err)
		os.Exit(1)
	}
}
