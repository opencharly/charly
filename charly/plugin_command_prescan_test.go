package main

import (
	"context"
	"testing"
)

// TestCommandPrescan_RegisterAndCollect proves the prescan→grammar path: a declared external
// command word (registerDeclaredExternalCommand, as the byte-gated prescanPluginManifest does)
// surfaces in declaredExternalCommandWords AND collectExternalCommandPlugins builds a TOP-LEVEL
// grammar holder + a dispatch entry (word + holder set) for it — so `charly <word>` parses
// before the binary is resolved (the resolve + syscall.Exec are deferred to dispatch).
func TestCommandPrescan_RegisterAndCollect(t *testing.T) {
	registerDeclaredExternalCommand("zzprescancmd")
	found := false
	for _, w := range declaredExternalCommandWords() {
		if w == "zzprescancmd" {
			found = true
		}
	}
	if !found {
		t.Fatal("declaredExternalCommandWords missing the prescanned word")
	}
	_, _, table := collectExternalCommandPlugins()
	d, ok := table["zzprescancmd"]
	if !ok {
		t.Fatal("collectExternalCommandPlugins built no dispatch entry for the prescanned word")
	}
	if d.word != "zzprescancmd" {
		t.Fatalf("dispatch entry word = %q, want zzprescancmd", d.word)
	}
	if d.holder == nil {
		t.Fatal("dispatch entry has no grammar holder")
	}
}

// TestResolveCommandPluginBinary_Baked proves dispatch resolves a command word to its BAKED
// provider binary directly (the deployed-container path: discoverBakedPluginWords mapped the
// word → binary from the `.providers` manifest, so no project scan is needed). This is the
// path `charly mcp serve` takes inside the charly-mcp service container.
func TestResolveCommandPluginBinary_Baked(t *testing.T) {
	const word = "zzbakedcmd"
	bakedPluginBinaries[provKey(ClassCommand, word)] = "/usr/lib/charly/plugins/" + word
	defer delete(bakedPluginBinaries, provKey(ClassCommand, word))

	bin, err := resolveCommandPluginBinary(context.Background(), word)
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
