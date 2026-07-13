package main

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/enginekit"
)

// NOTE: the parseInspect / mountsFromInspect white-box tests (formerly here)
// moved to the enginekit package with the engine-parsing code they exercise
// (chunk 1 relocated those functions to sdk/enginekit as unexported symbols).
// This file retains only the tests for the package-main helpers isEncryptedPlainPath
// and formatLiveMounts.

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
		mounts []enginekit.MountInfo
		want   []string
	}{
		{
			name:   "empty",
			mounts: nil,
			want:   []string{},
		},
		{
			name: "named volume",
			mounts: []enginekit.MountInfo{
				{Type: "volume", Name: "charly-immich-import", Source: "/var/lib/containers/storage/volumes/charly-immich-import/_data", Destination: "/home/user/.immich/import"},
			},
			want: []string{
				"charly-immich-import: /var/lib/containers/storage/volumes/charly-immich-import/_data -> /home/user/.immich/import",
			},
		},
		{
			name: "plain bind mount",
			mounts: []enginekit.MountInfo{
				{Type: "bind", Name: "", Source: "/home/user/charly", Destination: "/workspace"},
			},
			want: []string{
				"bind: /home/user/charly -> /workspace",
			},
		},
		{
			name: "encrypted FUSE bind — gets the (enc) suffix",
			mounts: []enginekit.MountInfo{
				{Type: "bind", Name: "", Source: "/home/user/.local/share/charly/encrypted/charly-immich-library/plain", Destination: "/home/user/.immich/library"},
			},
			want: []string{
				"bind: /home/user/.local/share/charly/encrypted/charly-immich-library/plain -> /home/user/.immich/library (enc)",
			},
		},
		{
			name: "mixed: plain bind + encrypted bind + named volume",
			mounts: []enginekit.MountInfo{
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
