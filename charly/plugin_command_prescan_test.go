package main

import (
	"context"
	"testing"
)

// TestCommandPrescan_RegisterAndCollect proves the prescan→grammar path: a declared external
// command word (registerDeclaredExternalCommand, as the byte-gated prescanPluginManifest does)
// surfaces in declaredExternalCommandIdentities AND collectExternalCommandPlugins builds a
// grammar holder + a dispatch entry for it — so `charly <word>` parses before the binary is
// resolved (the resolve + syscall.Exec are deferred to dispatch). A NESTED command (a non-empty
// parent) nests under its parent's holder instead, proving the prescan carries the parent.
func TestCommandPrescan_RegisterAndCollect(t *testing.T) {
	registerDeclaredExternalCommand("zzprescancmd", "")
	registerDeclaredExternalCommand("zznestedcmd", "zzparent")
	ids := declaredExternalCommandIdentities()
	if _, ok := ids[providerKey(ClassCommand, "zzprescancmd", "")]; !ok {
		t.Fatal("declaredExternalCommandIdentities missing the prescanned top-level word")
	}
	if p, ok := ids[providerKey(ClassCommand, "zznestedcmd", "zzparent")]; !ok || p != "zzparent" {
		t.Fatalf("declaredExternalCommandIdentities nested entry = %q, %v; want parent zzparent", p, ok)
	}
	top, nested, table := collectExternalCommandPlugins()
	d, ok := table["zzprescancmd"]
	if !ok {
		t.Fatal("collectExternalCommandPlugins built no dispatch entry for the prescanned word")
	}
	if d.word != "zzprescancmd" || d.parent != "" {
		t.Fatalf("top-level dispatch entry = %+v, want word zzprescancmd parent empty", d)
	}
	if d.holder == nil {
		t.Fatal("top-level dispatch entry has no grammar holder")
	}
	if _, ok := nested["zzparent"]; !ok {
		t.Fatal("a prescanned NESTED command did not nest under its parent")
	}
	nd, ok := table["zzparent zznestedcmd"]
	if !ok || nd.parent != "zzparent" {
		t.Fatalf("nested dispatch entry = %+v, %v; want parent zzparent", nd, ok)
	}
	_ = top
}

// TestResolveCommandPluginBinary_Baked proves dispatch resolves a command word to its BAKED
// provider binary directly (the deployed-container path: discoverBakedPluginWords mapped the
// word → binary from the `.providers` manifest, so no project scan is needed). This is the
// path `charly mcp serve` takes inside the charly-mcp service container.
func TestResolveCommandPluginBinary_Baked(t *testing.T) {
	const word = "zzbakedcmd"
	bakedPluginBinaries[providerKey(ClassCommand, word, "")] = "/usr/lib/charly/plugins/" + word
	defer delete(bakedPluginBinaries, providerKey(ClassCommand, word, ""))

	bin, err := resolveCommandPluginBinary(context.Background(), word, "")
	if err != nil {
		t.Fatalf("resolveCommandPluginBinary (baked): %v", err)
	}
	if want := "/usr/lib/charly/plugins/" + word; bin != want {
		t.Fatalf("resolveCommandPluginBinary = %q, want the baked binary %q", bin, want)
	}
}

// TestScanDirFlag covers the pre-parse -C/--dir project-dir scan (both spaced and = forms).
func TestScanDirFlag(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"charly", "examplecommand"}, ""},
		{[]string{"charly", "-C", "/p", "examplecommand"}, "/p"},
		{[]string{"charly", "--dir", "/q", "x"}, "/q"},
		{[]string{"charly", "-C=/r", "x"}, "/r"},
		{[]string{"charly", "--dir=/s", "x"}, "/s"},
		{[]string{"charly", "-C"}, ""}, // dangling flag, no value
	}
	for _, tc := range cases {
		if got := scanDirFlag(tc.args); got != tc.want {
			t.Errorf("scanDirFlag(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
