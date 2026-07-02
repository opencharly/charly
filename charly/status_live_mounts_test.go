package main

import (
	"reflect"
	"testing"
)

// TestParseInspect_LiveMounts asserts the JSON .Mounts[] block from a
// realistic `podman inspect` blob is parsed into MountInfo correctly and
// that mountsFromInspect handles missing/empty Mounts gracefully. Mirrors
// the actual immich quadlet's live mount layout — three encrypted FUSE
// binds + workspace bind + two named volumes — so it doubles as the JSON-
// shape regression guard for the data flow that feeds collectOne when a
// running container is queried.
func TestParseInspect_LiveMounts(t *testing.T) {
	// Realistic blob shape; both podman + docker emit Mounts[] with these
	// fields. Trimmed to just the fields charly status reads.
	blob := []byte(`[{
		"Name": "/charly-immich",
		"HostConfig": {"NetworkMode": "charly"},
		"Mounts": [
			{"Type": "bind", "Name": "", "Source": "/home/u/.local/share/charly/encrypted/charly-immich-library/plain", "Destination": "/home/user/.immich/library"},
			{"Type": "bind", "Name": "", "Source": "/home/u/.local/share/charly/encrypted/charly-immich-cache/plain", "Destination": "/home/user/.immich/cache"},
			{"Type": "bind", "Name": "", "Source": "/home/u/.local/share/charly/encrypted/charly-immich-pgdata/plain", "Destination": "/home/user/.postgresql/data"},
			{"Type": "bind", "Name": "", "Source": "/home/u/projects/charly", "Destination": "/workspace"},
			{"Type": "volume", "Name": "charly-immich-import", "Source": "/var/lib/containers/storage/volumes/charly-immich-import/_data", "Destination": "/home/user/.immich/import"},
			{"Type": "volume", "Name": "charly-immich-external", "Source": "/var/lib/containers/storage/volumes/charly-immich-external/_data", "Destination": "/home/user/.immich/external"}
		]
	}]`)
	rows, err := parseInspect(blob)
	if err != nil {
		t.Fatalf("parseInspect: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if got := len(rows[0].Mounts); got != 6 {
		t.Fatalf("got %d mounts, want 6", got)
	}

	// Index by Destination for clear assertions.
	byDest := map[string]MountInfo{}
	for _, m := range rows[0].Mounts {
		byDest[m.Destination] = m
	}

	if m, ok := byDest["/home/user/.immich/library"]; !ok || m.Type != "bind" || !isEncryptedPlainPath(m.Source) {
		t.Errorf("library mount: got type=%q source=%q (encrypted-plain check=%v); want bind+isEncryptedPlainPath=true",
			m.Type, m.Source, isEncryptedPlainPath(m.Source))
	}
	if m, ok := byDest["/workspace"]; !ok || m.Type != "bind" || isEncryptedPlainPath(m.Source) {
		t.Errorf("workspace mount: got type=%q source=%q (encrypted-plain check=%v); want bind+isEncryptedPlainPath=false",
			m.Type, m.Source, isEncryptedPlainPath(m.Source))
	}
	if m, ok := byDest["/home/user/.immich/import"]; !ok || m.Type != "volume" || m.Name != "charly-immich-import" {
		t.Errorf("import mount: got type=%q name=%q; want type=volume name=charly-immich-import",
			m.Type, m.Name)
	}

	// End-to-end: feed parsed mounts through formatLiveMounts and assert
	// the (enc) suffix lands on the three encrypted binds and nowhere else.
	out := formatLiveMounts(rows[0].Mounts)
	encCount := 0
	for _, line := range out {
		if reflect.DeepEqual(line[len(line)-6:], "(enc) "[:5]) || (len(line) >= 5 && line[len(line)-5:] == "(enc)") {
			encCount++
		}
	}
	if encCount != 3 {
		t.Errorf("got %d (enc)-suffixed lines, want 3 (library + cache + pgdata)\nfull output:\n%v", encCount, out)
	}
}

// TestMountsFromInspect_MissingMountsField covers the defensive path —
// some inspect blobs (e.g., for created-but-never-started containers, or
// older podman versions) lack the Mounts key entirely. Must return nil
// without panicking.
func TestMountsFromInspect_MissingMountsField(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]any
	}{
		{"no Mounts key", map[string]any{"Name": "/charly-foo"}},
		{"Mounts is null", map[string]any{"Mounts": nil}},
		{"Mounts is empty array", map[string]any{"Mounts": []any{}}},
		{"Mounts contains non-map garbage", map[string]any{"Mounts": []any{"string", 42, nil}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mountsFromInspect(tc.raw)
			if len(got) != 0 {
				t.Errorf("got %d mounts, want 0; %v", len(got), got)
			}
		})
	}
}

// TestIsEncryptedPlainPath asserts the gocryptfs-plain-dir detection
// used to flag live mounts as encryption FUSE mountpoints in the status
// display. Path-only — must NOT match volume-name strings or unrelated
// paths that happen to share a substring.
func TestIsEncryptedPlainPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "canonical charly gocryptfs plain dir",
			path: "/home/user/.local/share/charly/encrypted/charly-immich-library/plain",
			want: true,
		},
		{
			name: "explicit-storage encrypted path",
			path: "/mnt/nas/encrypted/charly-app-data/plain",
			want: true,
		},
		{
			name: "regular bind-mount source",
			path: "/home/user/project",
			want: false,
		},
		{
			name: "named-volume mountpoint",
			path: "/var/lib/containers/storage/volumes/charly-immich-cache/_data",
			want: false,
		},
		{
			name: "ends in plain but not under /encrypted/",
			path: "/var/lib/myapp/data/plain",
			want: false,
		},
		{
			name: "under /encrypted/ but not the plain dir (e.g. cipher)",
			path: "/home/user/.local/share/charly/encrypted/charly-foo/cipher",
			want: false,
		},
		{
			name: "empty path",
			path: "",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isEncryptedPlainPath(tc.path); got != tc.want {
				t.Errorf("isEncryptedPlainPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestFormatLiveMounts covers the live-mount renderer: exact output
// strings for typical bind / volume / encrypted-FUSE mount mixes.
// This is the function that distinguishes the OCI-label default
// volume from a `--bind`/`--encrypt` deploy override in `charly status`.
func TestFormatLiveMounts(t *testing.T) {
	cases := []struct {
		name   string
		mounts []MountInfo
		want   []string
	}{
		{
			name:   "empty",
			mounts: nil,
			want:   []string{},
		},
		{
			name: "named volume",
			mounts: []MountInfo{
				{Type: "volume", Name: "charly-immich-import", Source: "/var/lib/containers/storage/volumes/charly-immich-import/_data", Destination: "/home/user/.immich/import"},
			},
			want: []string{
				"charly-immich-import: /var/lib/containers/storage/volumes/charly-immich-import/_data -> /home/user/.immich/import",
			},
		},
		{
			name: "plain bind mount",
			mounts: []MountInfo{
				{Type: "bind", Name: "", Source: "/home/user/charly", Destination: "/workspace"},
			},
			want: []string{
				"bind: /home/user/charly -> /workspace",
			},
		},
		{
			name: "encrypted FUSE bind — gets the (enc) suffix",
			mounts: []MountInfo{
				{Type: "bind", Name: "", Source: "/home/user/.local/share/charly/encrypted/charly-immich-library/plain", Destination: "/home/user/.immich/library"},
			},
			want: []string{
				"bind: /home/user/.local/share/charly/encrypted/charly-immich-library/plain -> /home/user/.immich/library (enc)",
			},
		},
		{
			name: "mixed: plain bind + encrypted bind + named volume",
			mounts: []MountInfo{
				{Type: "bind", Source: "/home/u/proj", Destination: "/workspace"},
				{Type: "bind", Source: "/home/u/.local/share/charly/encrypted/charly-app-data/plain", Destination: "/data"},
				{Type: "volume", Name: "charly-app-cache", Source: "/v/charly-app-cache/_data", Destination: "/cache"},
			},
			want: []string{
				"bind: /home/u/proj -> /workspace",
				"bind: /home/u/.local/share/charly/encrypted/charly-app-data/plain -> /data (enc)",
				"charly-app-cache: /v/charly-app-cache/_data -> /cache",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatLiveMounts(tc.mounts)
			// Normalize empty for slice equality.
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("formatLiveMounts(%q):\n  got:  %v\n  want: %v", tc.name, got, tc.want)
			}
		})
	}
}
