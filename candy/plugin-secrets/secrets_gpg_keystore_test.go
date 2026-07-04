package secrets

import (
	"bytes"
	"errors"
	"testing"

	dbus "github.com/godbus/dbus/v5"
)

// These tests exercise the GPG keystore code path's collection-routing
// behavior against the fakeSSOps from secret_service_test.go. They cover the
// bug class fixed by the 2026-05 cutover that replaced default-alias-only
// `secret-tool lookup`/`store` shell-outs with iteration-capable ssClient
// calls (findItemByAttrsAcrossCollections + resolveTargetCollection).
//
// Naming convention: `TestGpgKeystore_*` so the suite is greppable from
// `go test -run TestGpgKeystore ./charly/...`.

const (
	testKeyID      = "5EA2283B420DE2B3"
	testKeyArmored = "-----BEGIN PGP PRIVATE KEY BLOCK-----\nfake-payload\n-----END PGP PRIVATE KEY BLOCK-----\n"
	testKeygrip    = "A9D1D22305215BD12298FF94FB78BF514685E27B"
	testPassphrase = "hunter2-test"
)

// gpgKeyAttrs returns the canonical attribute map for an org.gnupg.Key entry.
func gpgKeyAttrs(keyid, uid string) map[string]string {
	return map[string]string{
		"xdg:schema": ssSchemaKey,
		"keyid":      keyid,
		"uid":        uid,
	}
}

// gpgPassphraseAttrs returns the canonical attribute map for an
// org.gnupg.Passphrase entry.
func gpgPassphraseAttrs(keygrip string) map[string]string {
	return map[string]string{
		"xdg:schema": ssSchemaPassphrase,
		"keygrip":    keygrip,
	}
}

// TestGpgKeystore_ReadFindsEntryInNonDefaultCollection is the explicit
// regression test for bug A: secret-tool's lookup verb only checked the
// `default`-aliased collection, so entries in any other collection were
// invisible. The fix iterates ALL healthy unlocked collections.
func TestGpgKeystore_ReadFindsEntryInNonDefaultCollection(t *testing.T) {
	f := newFakeSSOps()
	const defaultPath = dbus.ObjectPath("/collections/default-empty")
	const keepassPath = dbus.ObjectPath("/collections/keepass")

	f.aliasMap["default"] = defaultPath
	f.collectionList = []dbus.ObjectPath{defaultPath, keepassPath}
	f.labels[defaultPath] = "Login"
	f.labels[keepassPath] = "opencharly"

	// The GPG key lives in the KeePassXC collection (NOT the default one).
	f.addItemWithAttrs(keepassPath,
		gpgKeyAttrs(testKeyID, "Test User <test@example.com>"),
		"/items/keepass/key1",
		[]byte(testKeyArmored),
		"GPG Key entry")

	item, label, err := findItemByAttrsAcrossCollections(f, gpgKeyAttrs(testKeyID, "Test User <test@example.com>"), "")
	if err != nil {
		t.Fatalf("expected to find entry in non-default collection, got error: %v", err)
	}
	if item != "/items/keepass/key1" {
		t.Errorf("item path = %s, want /items/keepass/key1", item)
	}
	if label != "opencharly" {
		t.Errorf("served-from collection label = %q, want opencharly", label)
	}
}

// TestGpgKeystore_ReadByPartialAttrs ensures subset-style search works:
// passing only {xdg:schema, keyid} (no uid) matches an entry whose stored
// attrs are a superset.
func TestGpgKeystore_ReadByPartialAttrs(t *testing.T) {
	f := newFakeSSOps()
	const path = dbus.ObjectPath("/collections/keepass")
	f.aliasMap["default"] = path
	f.collectionList = []dbus.ObjectPath{path}
	f.labels[path] = "opencharly"
	f.addItemWithAttrs(path,
		gpgKeyAttrs(testKeyID, "Test User <test@example.com>"),
		"/items/k", []byte(testKeyArmored), "label")

	// Search by schema+keyid only — should still find the entry whose
	// attribute map ALSO carries `uid`.
	item, _, err := findItemByAttrsAcrossCollections(f, map[string]string{
		"xdg:schema": ssSchemaKey,
		"keyid":      testKeyID,
	}, "")
	if err != nil {
		t.Fatalf("partial-attr search failed: %v", err)
	}
	if item != "/items/k" {
		t.Errorf("item = %s, want /items/k", item)
	}
}

// TestGpgKeystore_WriteRoutesToDefaultAliased verifies that the write path
// (resolveTargetCollection) lands in the collection aliased as `default`
// when no preferLabel is set.
func TestGpgKeystore_WriteRoutesToDefaultAliased(t *testing.T) {
	f := newFakeSSOps()
	const defaultPath = dbus.ObjectPath("/collections/default")
	const otherPath = dbus.ObjectPath("/collections/other")

	f.aliasMap["default"] = defaultPath
	f.collectionList = []dbus.ObjectPath{defaultPath, otherPath}
	f.labels[defaultPath] = "Login"
	f.labels[otherPath] = "Other"

	target, label, err := resolveTargetCollection(f, "")
	if err != nil {
		t.Fatalf("resolveTargetCollection: %v", err)
	}
	if target != defaultPath {
		t.Errorf("target = %s, want default %s", target, defaultPath)
	}
	if label != "Login" {
		t.Errorf("label = %q, want Login", label)
	}

	// Round-trip: write a key, read it back via findItemByAttrsAcrossCollections.
	itemPath, err := f.createItem(target, gpgKeyAttrs(testKeyID, "uid"), []byte(testKeyArmored), "lbl", true)
	if err != nil {
		t.Fatalf("createItem: %v", err)
	}
	if itemPath == "" {
		t.Fatal("createItem returned empty path")
	}
	read, _, err := findItemByAttrsAcrossCollections(f, gpgKeyAttrs(testKeyID, "uid"), "")
	if err != nil {
		t.Fatalf("read-after-write: %v", err)
	}
	if read != itemPath {
		t.Errorf("read item = %s, want %s", read, itemPath)
	}
	got, err := f.getSecret(read)
	if err != nil {
		t.Fatalf("getSecret: %v", err)
	}
	if !bytes.Equal(got, []byte(testKeyArmored)) {
		t.Errorf("payload mismatch: got %q want %q", got, testKeyArmored)
	}
}

// TestGpgKeystore_WriteRoutesToPreferLabelWhenDefaultBroken is the regression
// test for bug B: when default aliases to an unhealthy collection,
// resolveTargetCollection must fall through to the preferLabel-pinned
// collection (used by charly via runtime_config.KeyringCollectionLabel).
func TestGpgKeystore_WriteRoutesToPreferLabelWhenDefaultBroken(t *testing.T) {
	f := newFakeSSOps()
	const brokenDefault = dbus.ObjectPath("/collections/broken")
	const keepass = dbus.ObjectPath("/collections/keepass")

	f.aliasMap["default"] = brokenDefault
	f.collectionList = []dbus.ObjectPath{brokenDefault, keepass}
	f.labels[brokenDefault] = "broken"
	f.labels[keepass] = "opencharly"
	f.healthErrs[brokenDefault] = errors.New("simulated I/O error")

	target, label, err := resolveTargetCollection(f, "opencharly")
	if err != nil {
		t.Fatalf("resolveTargetCollection: %v", err)
	}
	if target != keepass {
		t.Errorf("target = %s, want keepass %s", target, keepass)
	}
	if label != "opencharly" {
		t.Errorf("label = %q, want opencharly", label)
	}
}

// TestGpgKeystore_AllCollectionsLockedReturnsInteractiveUnlock checks that the
// write path surfaces ErrSSInteractiveUnlockRequired when the user must
// unlock KeePassXC manually — distinct from "all broken" which means
// permanent failure.
func TestGpgKeystore_AllCollectionsLockedReturnsInteractiveUnlock(t *testing.T) {
	f := newFakeSSOps()
	const path = dbus.ObjectPath("/collections/locked")
	f.aliasMap["default"] = path
	f.collectionList = []dbus.ObjectPath{path}
	f.labels[path] = "opencharly"
	f.unlockErrs[path] = ErrSSInteractiveUnlockRequired

	_, _, err := resolveTargetCollection(f, "")
	if !errors.Is(err, ErrSSInteractiveUnlockRequired) {
		t.Errorf("err = %v, want ErrSSInteractiveUnlockRequired", err)
	}
}

// TestGpgKeystore_AllCollectionsBrokenReturnsAllBroken verifies the
// distinguishable error class for permanent failure.
func TestGpgKeystore_AllCollectionsBrokenReturnsAllBroken(t *testing.T) {
	f := newFakeSSOps()
	const path = dbus.ObjectPath("/collections/broken")
	f.collectionList = []dbus.ObjectPath{path}
	f.labels[path] = "broken"
	f.healthErrs[path] = errors.New("simulated I/O error")

	_, _, err := resolveTargetCollection(f, "")
	if !errors.Is(err, ErrSSAllBroken) {
		t.Errorf("err = %v, want ErrSSAllBroken", err)
	}
}

// TestGpgKeystore_DefaultAliasUnsetButPreferLabelMatches: simulates a
// gnome-keyring-not-running case where there is no default alias but a
// label-pinned collection is unlocked and healthy.
func TestGpgKeystore_DefaultAliasUnsetButPreferLabelMatches(t *testing.T) {
	f := newFakeSSOps()
	const keepass = dbus.ObjectPath("/collections/keepass")
	f.collectionList = []dbus.ObjectPath{keepass}
	f.labels[keepass] = "opencharly"
	// no aliasMap entry: default is unset

	target, label, err := resolveTargetCollection(f, "opencharly")
	if err != nil {
		t.Fatalf("resolveTargetCollection: %v", err)
	}
	if target != keepass {
		t.Errorf("target = %s, want keepass %s", target, keepass)
	}
	if label != "opencharly" {
		t.Errorf("label = %q, want opencharly", label)
	}
}

// TestGpgKeystore_PassphraseRoundTrip stores a passphrase via createItem,
// reads it back via findItemByAttrsAcrossCollections + getSecret. Mirrors the
// `charly secrets gpg setup` flow that stores per-keygrip passphrases.
func TestGpgKeystore_PassphraseRoundTrip(t *testing.T) {
	f := newFakeSSOps()
	const path = dbus.ObjectPath("/collections/keepass")
	f.aliasMap["default"] = path
	f.collectionList = []dbus.ObjectPath{path}
	f.labels[path] = "opencharly"

	target, _, err := resolveTargetCollection(f, "")
	if err != nil {
		t.Fatalf("resolveTargetCollection: %v", err)
	}

	attrs := gpgPassphraseAttrs(testKeygrip)
	itemPath, err := f.createItem(target, attrs, []byte(testPassphrase), "GPG Passphrase: keygrip "+testKeygrip[:8], true)
	if err != nil {
		t.Fatalf("createItem: %v", err)
	}

	read, _, err := findItemByAttrsAcrossCollections(f, attrs, "")
	if err != nil {
		t.Fatalf("findItemByAttrsAcrossCollections: %v", err)
	}
	if read != itemPath {
		t.Errorf("read = %s, want %s", read, itemPath)
	}
	got, err := f.getSecret(read)
	if err != nil {
		t.Fatalf("getSecret: %v", err)
	}
	if string(got) != testPassphrase {
		t.Errorf("payload = %q, want %q", got, testPassphrase)
	}
}

// TestGpgKeystore_ReplaceOnSameAttrs verifies that createItem with replace=true
// replaces the existing entry rather than creating a duplicate. Mirrors the
// ssGpgStore behavior used by `charly secrets gpg setup` re-runs.
func TestGpgKeystore_ReplaceOnSameAttrs(t *testing.T) {
	f := newFakeSSOps()
	const path = dbus.ObjectPath("/collections/keepass")
	f.aliasMap["default"] = path
	f.collectionList = []dbus.ObjectPath{path}
	f.labels[path] = "opencharly"

	attrs := gpgPassphraseAttrs(testKeygrip)
	first, err := f.createItem(path, attrs, []byte("old-passphrase"), "GPG Passphrase", true)
	if err != nil {
		t.Fatalf("first createItem: %v", err)
	}
	second, err := f.createItem(path, attrs, []byte("new-passphrase"), "GPG Passphrase", true)
	if err != nil {
		t.Fatalf("second createItem: %v", err)
	}
	// Read should now return the NEW value via the second item path.
	got, err := f.getSecret(second)
	if err != nil {
		t.Fatalf("getSecret: %v", err)
	}
	if string(got) != "new-passphrase" {
		t.Errorf("payload after replace = %q, want new-passphrase", got)
	}
	// Old item should be gone.
	if _, err := f.getSecret(first); err == nil {
		t.Errorf("old item still readable after replace=true")
	}
}

// TestGpgKeystore_DeleteItem verifies the Delete D-Bus operation surface used
// by future passphrase-rotation flows (and required for clean test teardown).
func TestGpgKeystore_DeleteItem(t *testing.T) {
	f := newFakeSSOps()
	const path = dbus.ObjectPath("/collections/keepass")
	f.collectionList = []dbus.ObjectPath{path}
	f.labels[path] = "opencharly"

	attrs := gpgPassphraseAttrs(testKeygrip)
	itemPath, err := f.createItem(path, attrs, []byte(testPassphrase), "lbl", true)
	if err != nil {
		t.Fatalf("createItem: %v", err)
	}
	if err := f.deleteItem(itemPath); err != nil {
		t.Fatalf("deleteItem: %v", err)
	}
	if _, err := f.getSecret(itemPath); err == nil {
		t.Error("item still readable after deleteItem")
	}
}

// TestGpgKeystore_ItemMetadata verifies the label+attrs read-back path used by
// ssGpgSearch.
func TestGpgKeystore_ItemMetadata(t *testing.T) {
	f := newFakeSSOps()
	const path = dbus.ObjectPath("/collections/keepass")
	f.collectionList = []dbus.ObjectPath{path}
	f.labels[path] = "opencharly"

	attrs := gpgKeyAttrs(testKeyID, "uid")
	itemPath, err := f.createItem(path, attrs, []byte(testKeyArmored), "GPG Key entry", true)
	if err != nil {
		t.Fatalf("createItem: %v", err)
	}
	gotLabel, gotAttrs, err := f.itemMetadata(itemPath)
	if err != nil {
		t.Fatalf("itemMetadata: %v", err)
	}
	if gotLabel != "GPG Key entry" {
		t.Errorf("label = %q, want %q", gotLabel, "GPG Key entry")
	}
	if !attrsEqual(attrs, gotAttrs) {
		t.Errorf("attrs = %v, want %v", gotAttrs, attrs)
	}
}

// TestGpgKeystore_SearchItemsByAttrsReturnsAllMatches ensures the plural
// search returns every match in a collection, not just the first. Used by the
// importFromKeystore loop and the doctor's "count backed-up keys" line.
func TestGpgKeystore_SearchItemsByAttrsReturnsAllMatches(t *testing.T) {
	f := newFakeSSOps()
	const path = dbus.ObjectPath("/collections/keepass")
	f.collectionList = []dbus.ObjectPath{path}
	f.labels[path] = "opencharly"

	for i, keyid := range []string{"AAAA1111", "BBBB2222", "CCCC3333"} {
		attrs := gpgKeyAttrs(keyid, "user-"+keyid)
		_, err := f.createItem(path, attrs, []byte("payload-"+string(rune('A'+i))), "lbl", true)
		if err != nil {
			t.Fatalf("createItem #%d: %v", i, err)
		}
	}

	// All entries share the same xdg:schema → 3 matches.
	got, err := f.searchItemsByAttrs(path, map[string]string{"xdg:schema": ssSchemaKey})
	if err != nil {
		t.Fatalf("searchItemsByAttrs: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len(got) = %d, want 3", len(got))
	}

	// One specific keyid → 1 match.
	got, err = f.searchItemsByAttrs(path, gpgKeyAttrs("BBBB2222", "user-BBBB2222"))
	if err != nil {
		t.Fatalf("searchItemsByAttrs (specific): %v", err)
	}
	if len(got) != 1 {
		t.Errorf("specific-keyid len = %d, want 1", len(got))
	}
}
