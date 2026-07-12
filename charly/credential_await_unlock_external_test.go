package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestCredentialAwaitUnlock_ExternalEndToEnd is the LIVE proof that the externalized
// keyring-unlock waiter runs OUT-OF-PROCESS (the godbus dep-shed cutover). It builds the
// REAL candy/plugin-secrets module, stages the binary + its `.providers` manifest under a
// CHARLY_PLUGIN_DIR (the baked-plugin layout a host install / deployed container uses),
// points XDG_CONFIG_HOME at a temp config holding a CONFIG-backend `charly/enc` credential
// (no Secret Service), then drives the SAME path production takes:
//
//	discoverBakedPluginWords()  →  pluginCredentialStore{}.awaitUnlock(ctx, service, key)
//
// awaitUnlock RPCs verb:credential `await-unlock` to the out-of-process plugin. Because there
// is no Secret Service in the test env, the plugin's awaitUnlock fast-path re-probes
// resolveStoreChain, finds the config-backend credential, and RETURNS it across the gRPC
// boundary — proving the externalized waiter dispatches out-of-process and returns the
// resolved passphrase. The full keyring-LOCKED → unlock transition needs a live Secret
// Service and is exercised on the disposable R10 bed; this is its unit-level live proof.
func TestCredentialAwaitUnlock_ExternalEndToEnd(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds + execs the plugin-secrets binary (slow)")
	}
	ctx := context.Background()

	srcDir, err := filepath.Abs("../candy/plugin-secrets")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "go.mod")); err != nil {
		t.Fatalf("plugin-secrets module not found at %s: %v", srcDir, err)
	}

	// 1. Host-build the provider binary (the loader's buildPluginBinary step).
	bin, err := buildPluginBinary(ctx, srcDir, "plugin-secrets")
	if err != nil {
		t.Fatalf("buildPluginBinary: %v", err)
	}

	// 2. Stage it as a BAKED plugin: the binary at <dir>/plugin-secrets plus the
	//    <dir>/plugin-secrets.providers manifest discoverBakedPluginWords reads
	//    (one class:word per line).
	pluginDir := t.TempDir()
	staged := filepath.Join(pluginDir, "plugin-secrets")
	if err := copyFileBytes(bin, staged); err != nil {
		t.Fatalf("stage plugin binary: %v", err)
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		t.Fatalf("chmod staged plugin binary: %v", err)
	}
	if err := os.WriteFile(staged+".providers", []byte("verb:credential\ncommand:secrets\n"), 0o644); err != nil {
		t.Fatalf("write .providers manifest: %v", err)
	}

	// 3. A temp XDG_CONFIG_HOME holding a CONFIG-backend charly/enc credential (no keyring).
	//    The plugin reads ~/.config/charly/config.yml via os.UserConfigDir (honours XDG), and
	//    charly/enc/<box> credentials live under vnc_passwords composite keys (config_store.go).
	const boxName = "await-unlock-testimg"
	const secret = "await-unlock-passphrase-xyz"
	cfgDir := filepath.Join(t.TempDir(), "charly")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgYAML := "secret_backend: config\nvnc_passwords:\n  charly/enc/" + boxName + ": " + secret + "\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yml"), []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	// 4. Point the host AND the inherited-env plugin subprocess at the staged plugin + config,
	//    and force the config backend (no Secret Service in the test env). t.Setenv restores.
	t.Setenv("CHARLY_PLUGIN_DIR", pluginDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(cfgDir))
	t.Setenv("CHARLY_SECRET_BACKEND", "config")

	// Snapshot/restore the process-global baked map and tear down the spawned subprocess.
	prev := make(map[string]string, len(bakedPluginBinaries))
	for k, v := range bakedPluginBinaries {
		prev[k] = v
	}
	t.Cleanup(func() {
		for k := range bakedPluginBinaries {
			delete(bakedPluginBinaries, k)
		}
		for k, v := range prev {
			bakedPluginBinaries[k] = v
		}
	})
	t.Cleanup(func() { _ = providerRegistry.Close() })

	// 5. Discover the baked verb word (verb:credential → staged binary), then drive the REAL
	//    core adapter: awaitUnlock RPCs verb:credential `await-unlock` out-of-process.
	discoverBakedPluginWords()
	if _, ok := bakedPluginBinaries[provKey(ClassVerb, "credential")]; !ok {
		t.Fatalf("discoverBakedPluginWords did not map verb:credential (baked map: %v)", bakedPluginBinaries)
	}

	value, source, err := pluginCredentialStore{}.awaitUnlock(ctx, "charly/enc", boxName)
	if err != nil {
		t.Fatalf("awaitUnlock (out-of-proc verb:credential): %v", err)
	}
	if value != secret {
		t.Fatalf("awaitUnlock value = %q, want %q (config-backend credential returned across the gRPC boundary)", value, secret)
	}
	if source != "config" {
		t.Fatalf("awaitUnlock source = %q, want %q", source, "config")
	}
}
