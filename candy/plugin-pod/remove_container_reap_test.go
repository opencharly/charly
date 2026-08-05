package pod

// remove_container_reap_test.go — regression guard for the quadlet-teardown orphan.
//
// `charly remove` in quadlet mode used to rely entirely on a discarded-error `systemctl stop` to
// reap the container, then deleted the quadlet file regardless. A container that survived the stop
// was orphaned permanently: with its unit gone, nothing would ever remove it, and it kept its
// netns — and therefore its aardvark-dns registration — alive, breaking container-name resolution
// host-wide for unrelated deploys. These tests pin the verify-then-remove behaviour that closes it.

import (
	"errors"
	"reflect"
	"testing"
)

func TestSidecarContainerNames(t *testing.T) {
	got := sidecarContainerNames("charly-app", []string{"tailscale", "proxy"})
	want := []string{"charly-app-tailscale", "charly-app-proxy"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sidecarContainerNames = %v, want %v", got, want)
	}
	if got := sidecarContainerNames("charly-app", nil); len(got) != 0 {
		t.Errorf("no sidecars should yield no container names, got %v", got)
	}
}

// TestEnsureContainersRemoved_ReapsOnlySurvivors: a container systemd already reaped costs one
// existence check and is NOT re-removed; a survivor is force-removed.
func TestEnsureContainersRemoved_ReapsOnlySurvivors(t *testing.T) {
	origExists, origRemove := containerExists, removeContainer
	defer func() { containerExists, removeContainer = origExists, origRemove }()

	alive := map[string]bool{"charly-app-tailscale": true}
	var checked, removed []string
	containerExists = func(_, name string) bool {
		checked = append(checked, name)
		return alive[name]
	}
	removeContainer = func(_, name string) error {
		removed = append(removed, name)
		return nil
	}

	ensureContainersRemoved("podman", []string{"charly-app", "charly-app-tailscale", ""})

	if want := []string{"charly-app", "charly-app-tailscale"}; !reflect.DeepEqual(checked, want) {
		t.Errorf("existence-checked %v, want %v (the empty name must be skipped)", checked, want)
	}
	if want := []string{"charly-app-tailscale"}; !reflect.DeepEqual(removed, want) {
		t.Errorf("removed %v, want %v (only the survivor)", removed, want)
	}
}

// TestEnsureContainersRemoved_ContinuesPastFailure: one container that refuses to be removed must
// not abort the reaping of the rest — teardown still has files and volumes to clean up.
func TestEnsureContainersRemoved_ContinuesPastFailure(t *testing.T) {
	origExists, origRemove := containerExists, removeContainer
	defer func() { containerExists, removeContainer = origExists, origRemove }()

	containerExists = func(_, _ string) bool { return true }
	var removed []string
	removeContainer = func(_, name string) error {
		removed = append(removed, name)
		if name == "charly-app" {
			return errors.New("device or resource busy")
		}
		return nil
	}

	ensureContainersRemoved("podman", []string{"charly-app", "charly-app-tailscale"})

	if want := []string{"charly-app", "charly-app-tailscale"}; !reflect.DeepEqual(removed, want) {
		t.Errorf("removed %v, want %v — a failure on the first must not stop the second", removed, want)
	}
}
