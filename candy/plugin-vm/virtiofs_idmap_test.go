package main

import (
	"os"
	"path/filepath"
	"testing"

	libvirtxml "libvirt.org/go/libvirtxml"
)

// D4: the guest-user idmap maps the guest's primary id (1000) to the host
// operator, and every other guest id into the operator's subordinate-ID range.
// This is what makes a rootless passthrough virtiofs share owned by the guest
// user instead of guest-root (libvirt's default).
func TestGuestOwnerIDMap(t *testing.T) {
	got := guestOwnerIDMap(1000, 1000, 100000, 65536)
	want := []libvirtxml.DomainFilesystemIDMapEntry{
		{Start: 0, Target: 100000, Count: 1000},     // guest 0-999  → subuid 100000-100999
		{Start: 1000, Target: 1000, Count: 1},       // guest 1000   → host operator 1000
		{Start: 1001, Target: 101000, Count: 64536}, // guest 1001+ → subuid 101000-165535
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestGuestOwnerIDMapInvalid(t *testing.T) {
	// guestID 0 (guest-root === host operator; nothing to remap) → nil.
	if guestOwnerIDMap(0, 1000, 100000, 65536) != nil {
		t.Error("guestID 0 should return nil (no partition)")
	}
	// guestID outside the subordinate range → nil.
	if guestOwnerIDMap(70000, 1000, 100000, 65536) != nil {
		t.Error("guestID >= subCount should return nil")
	}
}

func TestSubIDRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subuid")
	if err := os.WriteFile(path, []byte("root:0:65536\ntestuser:100000:65536\n# comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Match by name.
	if s, c, ok := subIDRange(path, "testuser", 1000); !ok || s != 100000 || c != 65536 {
		t.Errorf("by-name = (%d,%d,%v), want (100000,65536,true)", s, c, ok)
	}
	// Match by uid string when name doesn't match.
	pathUID := filepath.Join(dir, "subuid2")
	_ = os.WriteFile(pathUID, []byte("1000:200000:65536\n"), 0o644)
	if s, c, ok := subIDRange(pathUID, "someone", 1000); !ok || s != 200000 || c != 65536 {
		t.Errorf("by-uid = (%d,%d,%v), want (200000,65536,true)", s, c, ok)
	}
	// Missing entry → ok=false.
	if _, _, ok := subIDRange(path, "nobody", 4242); ok {
		t.Error("missing user should report ok=false")
	}
	// Unreadable file → ok=false.
	if _, _, ok := subIDRange(filepath.Join(dir, "nope"), "testuser", 1000); ok {
		t.Error("unreadable file should report ok=false")
	}
}

func virtiofsFS(accessMode string, idmap *libvirtxml.DomainFilesystemIDMap) libvirtxml.DomainFilesystem {
	return libvirtxml.DomainFilesystem{
		AccessMode: accessMode,
		Driver:     &libvirtxml.DomainFilesystemDriver{Type: "virtiofs"},
		Source:     &libvirtxml.DomainFilesystemSource{Mount: &libvirtxml.DomainFilesystemSourceMount{Dir: "/home/user"}},
		Target:     &libvirtxml.DomainFilesystemTarget{Dir: "workspace"},
		IDMap:      idmap,
	}
}

// D4: applyVirtiofsIdmap sets the idmap on passthrough virtiofs shares that
// don't already declare one, and leaves everything else untouched.
func TestApplyVirtiofsIdmap(t *testing.T) {
	uidMap := guestOwnerIDMap(1000, 1000, 100000, 65536)
	gidMap := guestOwnerIDMap(1000, 1000, 100000, 65536)
	existing := &libvirtxml.DomainFilesystemIDMap{UID: []libvirtxml.DomainFilesystemIDMapEntry{{Start: 5, Target: 5, Count: 1}}}

	d := &libvirtxml.Domain{Devices: &libvirtxml.DomainDeviceList{Filesystems: []libvirtxml.DomainFilesystem{
		virtiofsFS("passthrough", nil),                           // 0: should get the idmap
		virtiofsFS("", nil),                                      // 1: empty accessmode == passthrough default → idmap
		virtiofsFS("mapped", nil),                                // 2: non-passthrough → untouched
		virtiofsFS("passthrough", existing),                      // 3: author idmap wins → untouched
		{Driver: &libvirtxml.DomainFilesystemDriver{Type: "9p"}}, // 4: not virtiofs → untouched
	}}}
	applyVirtiofsIdmap(d, uidMap, gidMap)

	fs := d.Devices.Filesystems
	if fs[0].IDMap == nil || len(fs[0].IDMap.UID) != 3 {
		t.Errorf("passthrough virtiofs[0] did not get the guest idmap: %+v", fs[0].IDMap)
	}
	if fs[1].IDMap == nil {
		t.Error("empty-accessmode virtiofs[1] (passthrough default) did not get the idmap")
	}
	if fs[2].IDMap != nil {
		t.Error("mapped virtiofs[2] should be left to libvirt's default")
	}
	if fs[3].IDMap != existing {
		t.Error("author-declared idmap[3] must win")
	}
	if fs[4].IDMap != nil {
		t.Error("9p filesystem[4] should be untouched")
	}
}

func TestDomainHasUnmappedPassthroughVirtiofs(t *testing.T) {
	none := &libvirtxml.Domain{Devices: &libvirtxml.DomainDeviceList{}}
	if domainHasUnmappedPassthroughVirtiofs(none) {
		t.Error("no filesystems → false")
	}
	yes := &libvirtxml.Domain{Devices: &libvirtxml.DomainDeviceList{Filesystems: []libvirtxml.DomainFilesystem{
		virtiofsFS("passthrough", nil),
	}}}
	if !domainHasUnmappedPassthroughVirtiofs(yes) {
		t.Error("unmapped passthrough virtiofs → true")
	}
}
