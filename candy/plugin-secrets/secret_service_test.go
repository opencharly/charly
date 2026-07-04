package secrets

import (
	"errors"
	"fmt"
	"maps"
	"testing"

	dbus "github.com/godbus/dbus/v5"
)

// fakeItem is a single in-memory entry in fakeSSOps.
type fakeItem struct {
	path   dbus.ObjectPath
	attrs  map[string]string
	secret []byte
	label  string
}

// fakeSSOps is a configurable ssOps implementation for unit tests.
type fakeSSOps struct {
	aliasMap       map[string]dbus.ObjectPath
	aliasErr       map[string]error
	collectionList []dbus.ObjectPath
	collectionsErr error
	labels         map[dbus.ObjectPath]string
	healthErrs     map[dbus.ObjectPath]error
	unlockErrs     map[dbus.ObjectPath]error
	// items: collectionPath -> list of in-memory entries. Each entry's attrs
	// map is matched against incoming searchItemByAttrs calls subset-style:
	// every key/value in the search attrs must equal the entry's attrs. This
	// mirrors libsecret's `Collection.SearchItems(IN attrs)` semantics.
	items map[dbus.ObjectPath][]*fakeItem
	// itemByPath: itemPath -> *fakeItem, owned by the same fake. Lets
	// getSecret/deleteItem look up by path without scanning collections.
	itemByPath map[dbus.ObjectPath]*fakeItem
	searchErrs map[dbus.ObjectPath]error
	createErrs map[dbus.ObjectPath]error
	deleteErrs map[dbus.ObjectPath]error
	// nextItemSeq supplies unique paths to createItem when the test does not
	// set explicit ones via addItem.
	nextItemSeq int
}

func newFakeSSOps() *fakeSSOps {
	return &fakeSSOps{
		aliasMap:   map[string]dbus.ObjectPath{},
		aliasErr:   map[string]error{},
		labels:     map[dbus.ObjectPath]string{},
		healthErrs: map[dbus.ObjectPath]error{},
		unlockErrs: map[dbus.ObjectPath]error{},
		items:      map[dbus.ObjectPath][]*fakeItem{},
		itemByPath: map[dbus.ObjectPath]*fakeItem{},
		searchErrs: map[dbus.ObjectPath]error{},
		createErrs: map[dbus.ObjectPath]error{},
		deleteErrs: map[dbus.ObjectPath]error{},
	}
}

func (f *fakeSSOps) readAlias(name string) (dbus.ObjectPath, error) {
	if err, ok := f.aliasErr[name]; ok {
		return "", err
	}
	return f.aliasMap[name], nil
}

func (f *fakeSSOps) collections() ([]dbus.ObjectPath, error) {
	if f.collectionsErr != nil {
		return nil, f.collectionsErr
	}
	out := make([]dbus.ObjectPath, len(f.collectionList))
	copy(out, f.collectionList)
	return out, nil
}

func (f *fakeSSOps) isCollectionHealthy(path dbus.ObjectPath) error {
	return f.healthErrs[path]
}

func (f *fakeSSOps) collectionLabel(path dbus.ObjectPath) string {
	return f.labels[path]
}

func (f *fakeSSOps) unlock(path dbus.ObjectPath) error {
	return f.unlockErrs[path]
}

// attrsMatch returns true when every key/value in want is present and equal in
// got — subset semantics, mirroring libsecret's SearchItems behavior.
func attrsMatch(want, got map[string]string) bool {
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

func (f *fakeSSOps) searchItemByAttrs(path dbus.ObjectPath, attrs map[string]string) (dbus.ObjectPath, error) {
	results, err := f.searchItemsByAttrs(path, attrs)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "", ErrSSNotFound
	}
	return results[0], nil
}

func (f *fakeSSOps) searchItemsByAttrs(path dbus.ObjectPath, attrs map[string]string) ([]dbus.ObjectPath, error) {
	if err, ok := f.searchErrs[path]; ok && err != nil {
		return nil, err
	}
	var out []dbus.ObjectPath
	for _, it := range f.items[path] {
		if attrsMatch(attrs, it.attrs) {
			out = append(out, it.path)
		}
	}
	return out, nil
}

func (f *fakeSSOps) getSecret(item dbus.ObjectPath) ([]byte, error) {
	it, ok := f.itemByPath[item]
	if !ok {
		return nil, fmt.Errorf("getSecret: item %s not found", item)
	}
	out := make([]byte, len(it.secret))
	copy(out, it.secret)
	return out, nil
}

func (f *fakeSSOps) itemMetadata(item dbus.ObjectPath) (string, map[string]string, error) {
	it, ok := f.itemByPath[item]
	if !ok {
		return "", nil, fmt.Errorf("itemMetadata: item %s not found", item)
	}
	cloned := make(map[string]string, len(it.attrs))
	maps.Copy(cloned, it.attrs)
	return it.label, cloned, nil
}

func (f *fakeSSOps) createItem(path dbus.ObjectPath, attrs map[string]string, secret []byte, label string, replace bool) (dbus.ObjectPath, error) {
	if err, ok := f.createErrs[path]; ok && err != nil {
		return "", err
	}
	if replace {
		// remove any existing entry whose attrs match exactly
		newList := f.items[path][:0]
		for _, it := range f.items[path] {
			if attrsEqual(it.attrs, attrs) {
				delete(f.itemByPath, it.path)
				continue
			}
			newList = append(newList, it)
		}
		f.items[path] = newList
	}
	f.nextItemSeq++
	itemPath := dbus.ObjectPath(fmt.Sprintf("%s/items/auto-%d", path, f.nextItemSeq))
	cloned := make(map[string]string, len(attrs))
	maps.Copy(cloned, attrs)
	clonedSecret := make([]byte, len(secret))
	copy(clonedSecret, secret)
	it := &fakeItem{
		path:   itemPath,
		attrs:  cloned,
		secret: clonedSecret,
		label:  label,
	}
	f.items[path] = append(f.items[path], it)
	f.itemByPath[itemPath] = it
	return itemPath, nil
}

func (f *fakeSSOps) deleteItem(item dbus.ObjectPath) error {
	if err, ok := f.deleteErrs[item]; ok && err != nil {
		return err
	}
	for coll, list := range f.items {
		newList := list[:0]
		for _, it := range list {
			if it.path == item {
				delete(f.itemByPath, item)
				continue
			}
			newList = append(newList, it)
		}
		f.items[coll] = newList
	}
	return nil
}

// attrsEqual returns true when two attribute maps have the same keys and values.
func attrsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// addItem records the canonical {service=charly/enc, username=immich-ml} entry
// under coll with itemPath as its path. Legacy helper for credential-keyring
// tests; for arbitrary attribute shapes (GPG keystore tests) use
// addItemWithAttrs.
func (f *fakeSSOps) addItem(coll dbus.ObjectPath, itemPath dbus.ObjectPath) {
	f.addItemWithAttrs(coll, map[string]string{
		"service":  "charly/enc",
		"username": "immich-ml",
	}, itemPath, nil, "")
}

// addItemWithAttrs records an arbitrary item under coll, retrievable via
// searchItemByAttrs (subset match) and getSecret (by path).
func (f *fakeSSOps) addItemWithAttrs(coll dbus.ObjectPath, attrs map[string]string, itemPath dbus.ObjectPath, secret []byte, label string) {
	cloned := make(map[string]string, len(attrs))
	maps.Copy(cloned, attrs)
	clonedSecret := make([]byte, len(secret))
	copy(clonedSecret, secret)
	it := &fakeItem{
		path:   itemPath,
		attrs:  cloned,
		secret: clonedSecret,
		label:  label,
	}
	f.items[coll] = append(f.items[coll], it)
	f.itemByPath[itemPath] = it
}

// --- Test cases ---

// TestFindItem_DefaultAliasHealthy: default alias resolves to a healthy
// collection that contains the item. Expect immediate match via alias,
// no iteration.
func TestFindItem_DefaultAliasHealthy(t *testing.T) {
	f := newFakeSSOps()
	const defaultPath = dbus.ObjectPath("/org/freedesktop/secrets/collection/login")
	f.aliasMap["default"] = defaultPath
	f.collectionList = []dbus.ObjectPath{defaultPath}
	f.labels[defaultPath] = "Login"
	f.addItem(defaultPath, "/items/pw1")

	item, label, err := findItemAcrossCollections(f, "charly/enc", "immich-ml", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item != "/items/pw1" {
		t.Errorf("item = %s, want /items/pw1", item)
	}
	if label != "Login" {
		t.Errorf("label = %q, want Login", label)
	}
}

// TestFindItem_DefaultAliasBroken_FallbackToIteration: mirrors the real
// KeePassXC bug. Default alias points at a broken stub collection; the
// real credential is in a sibling collection. Expect the broken collection
// to be skipped and the item to be found in the healthy sibling.
func TestFindItem_DefaultAliasBroken_FallbackToIteration(t *testing.T) {
	f := newFakeSSOps()
	const stub = dbus.ObjectPath("/org/freedesktop/secrets/collection/user")
	const real = dbus.ObjectPath("/org/freedesktop/secrets/collection/user_deb4")
	f.aliasMap["default"] = stub
	f.collectionList = []dbus.ObjectPath{stub, real}
	f.labels[stub] = ""
	f.labels[real] = "opencharly"
	f.healthErrs[stub] = errors.New("Input/output error") // broken stub
	f.addItem(real, "/items/real-pw")

	item, label, err := findItemAcrossCollections(f, "charly/enc", "immich-ml", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item != "/items/real-pw" {
		t.Errorf("item = %s, want /items/real-pw", item)
	}
	if label != "opencharly" {
		t.Errorf("label = %q, want opencharly", label)
	}
}

// TestFindItem_PreferLabel_SelectsByLabel: when the default alias is
// healthy but the caller asked for a specific label, both the alias AND the
// preferred-label collection get tried. The alias target is checked first
// (it's higher priority), then the label match. If the item is in the
// label collection, it's found.
func TestFindItem_PreferLabel_SelectsByLabel(t *testing.T) {
	f := newFakeSSOps()
	const aliasTarget = dbus.ObjectPath("/collections/default")
	const labelTarget = dbus.ObjectPath("/collections/opencharly")
	f.aliasMap["default"] = aliasTarget
	f.collectionList = []dbus.ObjectPath{aliasTarget, labelTarget}
	f.labels[aliasTarget] = "Default"
	f.labels[labelTarget] = "opencharly"
	// Item only in the label collection, not in the default.
	f.addItem(labelTarget, "/items/in-opencharly")

	item, label, err := findItemAcrossCollections(f, "charly/enc", "immich-ml", "opencharly")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item != "/items/in-opencharly" {
		t.Errorf("item = %s, want /items/in-opencharly", item)
	}
	if label != "opencharly" {
		t.Errorf("label = %q, want opencharly", label)
	}
}

// TestFindItem_AllCollectionsBroken_ReturnsAllBroken: every collection is
// unhealthy. Expect ErrSSAllBroken, not ErrSSNotFound.
func TestFindItem_AllCollectionsBroken_ReturnsAllBroken(t *testing.T) {
	f := newFakeSSOps()
	const c1 = dbus.ObjectPath("/collections/c1")
	const c2 = dbus.ObjectPath("/collections/c2")
	f.aliasMap["default"] = c1
	f.collectionList = []dbus.ObjectPath{c1, c2}
	f.healthErrs[c1] = errors.New("I/O error")
	f.healthErrs[c2] = errors.New("I/O error")

	_, _, err := findItemAcrossCollections(f, "charly/enc", "immich-ml", "")
	if !errors.Is(err, ErrSSAllBroken) {
		t.Errorf("err = %v, want ErrSSAllBroken", err)
	}
}

// TestFindItem_NotFoundAnywhere_ReturnsNotFound: collections are healthy
// but the item is not in any of them. Expect ErrSSNotFound.
func TestFindItem_NotFoundAnywhere_ReturnsNotFound(t *testing.T) {
	f := newFakeSSOps()
	const c1 = dbus.ObjectPath("/collections/c1")
	const c2 = dbus.ObjectPath("/collections/c2")
	f.aliasMap["default"] = c1
	f.collectionList = []dbus.ObjectPath{c1, c2}
	f.labels[c1] = "Login"
	f.labels[c2] = "Work"

	_, _, err := findItemAcrossCollections(f, "charly/enc", "immich-ml", "")
	if !errors.Is(err, ErrSSNotFound) {
		t.Errorf("err = %v, want ErrSSNotFound", err)
	}
}

// TestFindItem_SearchErrorCountsAsBroken: a collection is healthy but
// SearchItems fails with an I/O error mid-search. If every candidate errors
// that way, result is ErrSSAllBroken; if at least one succeeds with
// not-found, result is ErrSSNotFound.
func TestFindItem_SearchErrorCountsAsBroken(t *testing.T) {
	f := newFakeSSOps()
	const c1 = dbus.ObjectPath("/collections/c1")
	const c2 = dbus.ObjectPath("/collections/c2")
	f.aliasMap["default"] = c1
	f.collectionList = []dbus.ObjectPath{c1, c2}
	f.labels[c1] = "Login"
	f.labels[c2] = "Work"
	f.searchErrs[c1] = fmt.Errorf("I/O error")
	// c2 is healthy but has no matching item

	_, _, err := findItemAcrossCollections(f, "charly/enc", "immich-ml", "")
	if !errors.Is(err, ErrSSNotFound) {
		t.Errorf("err = %v, want ErrSSNotFound (at least one search succeeded)", err)
	}

	// Now make c2 also error
	f.searchErrs[c2] = fmt.Errorf("I/O error")
	_, _, err = findItemAcrossCollections(f, "charly/enc", "immich-ml", "")
	if !errors.Is(err, ErrSSAllBroken) {
		t.Errorf("err = %v, want ErrSSAllBroken (every search errored)", err)
	}
}

// TestFindItem_UnlockFailureCountsAsBroken: a collection is healthy but
// Unlock fails (prompt required, not supported in non-interactive path).
// Should count as a search error for bookkeeping.
func TestFindItem_UnlockFailureCountsAsBroken(t *testing.T) {
	f := newFakeSSOps()
	const c1 = dbus.ObjectPath("/collections/c1")
	f.aliasMap["default"] = c1
	f.collectionList = []dbus.ObjectPath{c1}
	f.labels[c1] = "Login"
	f.unlockErrs[c1] = errors.New("prompt required")

	_, _, err := findItemAcrossCollections(f, "charly/enc", "immich-ml", "")
	if !errors.Is(err, ErrSSAllBroken) {
		t.Errorf("err = %v, want ErrSSAllBroken (unlock failed on only candidate)", err)
	}
}

// TestFindItem_DefaultAliasUnsetButIterationFinds: the default alias is
// not set (readAlias returns empty), iteration walks the collection list
// and finds the item. This exercises the case where `/aliases/default` is
// literally unset (not broken).
func TestFindItem_DefaultAliasUnsetButIterationFinds(t *testing.T) {
	f := newFakeSSOps()
	const c1 = dbus.ObjectPath("/collections/c1")
	f.aliasMap["default"] = "" // unset (readAlias returns empty path, no error)
	f.collectionList = []dbus.ObjectPath{c1}
	f.labels[c1] = "Only"
	f.addItem(c1, "/items/found")

	item, label, err := findItemAcrossCollections(f, "charly/enc", "immich-ml", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item != "/items/found" {
		t.Errorf("item = %s, want /items/found", item)
	}
	if label != "Only" {
		t.Errorf("label = %q, want Only", label)
	}
}

// TestFindItem_DefaultAliasDeduped: when the default alias AND the
// concrete collection path both appear in the candidate list, the concrete
// path should NOT be searched twice (if the alias already resolved to the
// same path). Note: our dedup key is the object path string, so an alias
// path like "/aliases/default" is distinct from a concrete path like
// "/collection/user_deb4" even when they route to the same collection.
// The implementation resolves the alias to the concrete path via readAlias
// before adding to candidates, which fixes this.
func TestFindItem_DefaultAliasDeduped(t *testing.T) {
	f := newFakeSSOps()
	const real = dbus.ObjectPath("/collections/real")
	f.aliasMap["default"] = real // resolves to the concrete path
	f.collectionList = []dbus.ObjectPath{real}
	f.labels[real] = "Login"
	f.addItem(real, "/items/pw")

	// If iteration happened twice, unlock would be called twice. We track
	// that via a counter in a wrapper.
	var unlockCount int
	wrap := &countingOps{fakeSSOps: f, unlockCount: &unlockCount}

	item, _, err := findItemAcrossCollections(wrap, "charly/enc", "immich-ml", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item != "/items/pw" {
		t.Errorf("item = %s, want /items/pw", item)
	}
	if unlockCount != 1 {
		t.Errorf("unlockCount = %d, want 1 (candidate deduped)", unlockCount)
	}
}

// TestFindItem_LockedCollection_ReturnsInteractiveUnlock: a single healthy
// collection whose unlock returns ErrSSInteractiveUnlockRequired. Expect
// ErrSSInteractiveUnlockRequired (not ErrSSAllBroken).
func TestFindItem_LockedCollection_ReturnsInteractiveUnlock(t *testing.T) {
	f := newFakeSSOps()
	const c1 = dbus.ObjectPath("/collections/c1")
	f.aliasMap["default"] = c1
	f.collectionList = []dbus.ObjectPath{c1}
	f.labels[c1] = "user"
	f.unlockErrs[c1] = fmt.Errorf("%w: %s", ErrSSInteractiveUnlockRequired, c1)

	_, _, err := findItemAcrossCollections(f, "charly/enc", "immich-ml", "")
	if !errors.Is(err, ErrSSInteractiveUnlockRequired) {
		t.Errorf("err = %v, want ErrSSInteractiveUnlockRequired", err)
	}
}

// TestFindItem_MixLockedAndBroken_ReturnsAllBroken: one candidate is locked
// (ErrSSInteractiveUnlockRequired), another is broken (I/O error). The mix
// should return ErrSSAllBroken because not all failures are recoverable.
func TestFindItem_MixLockedAndBroken_ReturnsAllBroken(t *testing.T) {
	f := newFakeSSOps()
	const c1 = dbus.ObjectPath("/collections/c1")
	const c2 = dbus.ObjectPath("/collections/c2")
	f.aliasMap["default"] = c1
	f.collectionList = []dbus.ObjectPath{c1, c2}
	f.labels[c1] = "locked-one"
	f.labels[c2] = "broken-one"
	f.unlockErrs[c1] = fmt.Errorf("%w: %s", ErrSSInteractiveUnlockRequired, c1)
	f.unlockErrs[c2] = errors.New("I/O error")

	_, _, err := findItemAcrossCollections(f, "charly/enc", "immich-ml", "")
	if !errors.Is(err, ErrSSAllBroken) {
		t.Errorf("err = %v, want ErrSSAllBroken (mix of locked + broken)", err)
	}
}

type countingOps struct {
	*fakeSSOps
	unlockCount *int
}

func (o *countingOps) unlock(path dbus.ObjectPath) error {
	*o.unlockCount++
	return o.fakeSSOps.unlock(path)
}
