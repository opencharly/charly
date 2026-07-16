package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// recordedCall captures a single stubbed exec invocation for later assertion.
type recordedCall struct {
	name string
	args []string
}

// fakeRunner is a drop-in replacement for dataCmdRun and dataCmdOutput that
// records every invocation and lets tests supply canned return values via
// the optional handlers.
type fakeRunner struct {
	calls      []recordedCall
	runHandler func(name string, args ...string) error
	outHandler func(name string, args ...string) ([]byte, error)
}

func (f *fakeRunner) run(name string, args ...string) error {
	f.calls = append(f.calls, recordedCall{name: name, args: append([]string{}, args...)})
	if f.runHandler != nil {
		return f.runHandler(name, args...)
	}
	return nil
}

func (f *fakeRunner) output(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, recordedCall{name: name, args: append([]string{}, args...)})
	if f.outHandler != nil {
		return f.outHandler(name, args...)
	}
	return nil, nil
}

// installFakeRunner swaps the package-level command seams for the duration
// of a single test. It is safe against test parallelism only if tests don't
// share the seam, which they don't — each test gets its own fake.
func installFakeRunner(t *testing.T) *fakeRunner {
	t.Helper()
	fake := &fakeRunner{}
	origRun := dataCmdRun
	origOut := dataCmdOutput
	dataCmdRun = fake.run
	dataCmdOutput = fake.output
	t.Cleanup(func() {
		dataCmdRun = origRun
		dataCmdOutput = origOut
	})
	return fake
}

// findRunCall returns the first recorded `podman run` call, or nil if none.
func (f *fakeRunner) findRunCall() *recordedCall {
	for i := range f.calls {
		if f.calls[i].name == "podman" && len(f.calls[i].args) > 0 && f.calls[i].args[0] == "run" {
			return &f.calls[i]
		}
	}
	return nil
}

// containsArg returns true if the call's args contain the given token.
func (c *recordedCall) containsArg(token string) bool {
	return slices.Contains(c.args, token)
}

// containsSubstring returns true if any arg contains the given substring.
// Useful for asserting on the inline bash -c script body.
func (c *recordedCall) containsSubstring(s string) bool {
	for _, a := range c.args {
		if strings.Contains(a, s) {
			return true
		}
	}
	return false
}

func makeJupyterMeta(dataImage bool) *BoxMetadata {
	return &BoxMetadata{
		Box: "jupyter",
		UID: 1000,
		GID: 1000,
		DataEntries: []LabelDataEntry{
			{
				Volume:  "workspace",
				Staging: "/data/workspace/",
				Candy:   "notebook-templates",
			},
		},
		DataImage: dataImage,
	}
}

func TestProvisionDataNoEntries(t *testing.T) {
	fake := installFakeRunner(t)
	meta := &BoxMetadata{Box: "jupyter"}

	n, err := provisionData("podman", "jupyter-img", meta, nil, nil, "jupyter", "", DataProvisionInitial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 seeded, got %d", n)
	}
	if len(fake.calls) != 0 {
		t.Errorf("expected no commands, got %d calls", len(fake.calls))
	}
}

func TestProvisionDataNoVolumes(t *testing.T) {
	fake := installFakeRunner(t)
	meta := makeJupyterMeta(false)

	n, err := provisionData("podman", "jupyter-img", meta, nil, nil, "jupyter", "", DataProvisionInitial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 seeded, got %d", n)
	}
	if len(fake.calls) != 0 {
		t.Errorf("expected no commands, got %d calls", len(fake.calls))
	}
}

func TestProvisionDataBindOnly_InitialEmpty(t *testing.T) {
	fake := installFakeRunner(t)
	hostDir := t.TempDir()
	meta := makeJupyterMeta(false)
	bindMounts := []deploykit.ResolvedBindMount{
		{Name: "workspace", HostPath: hostDir, ContPath: "/workspace"},
	}

	n, err := provisionData("podman", "jupyter-img", meta, bindMounts, nil, "jupyter", "", DataProvisionInitial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 seeded, got %d", n)
	}
	runCall := fake.findRunCall()
	if runCall == nil {
		t.Fatal("expected a `podman run` call, got none")
	}
	if !runCall.containsArg("-v") || !runCall.containsArg(hostDir+":/seed") {
		t.Errorf("expected -v %s:/seed, got args=%v", hostDir, runCall.args)
	}
	if !runCall.containsSubstring("--userns=keep-id:uid=1000,gid=1000") {
		t.Errorf("expected --userns=keep-id:uid=1000,gid=1000 for bind mount, got args=%v", runCall.args)
	}
	if !runCall.containsSubstring("cp -a /data/workspace/.") {
		t.Errorf("expected cp -a /data/workspace/. in bash script, got args=%v", runCall.args)
	}
}

func TestProvisionDataBindOnly_InitialNonEmpty(t *testing.T) {
	fake := installFakeRunner(t)
	hostDir := t.TempDir()
	// Make the dir non-empty so DataProvisionInitial skips it.
	if err := os.WriteFile(filepath.Join(hostDir, "user-file.ipynb"), []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	meta := makeJupyterMeta(false)
	bindMounts := []deploykit.ResolvedBindMount{
		{Name: "workspace", HostPath: hostDir},
	}

	n, err := provisionData("podman", "jupyter-img", meta, bindMounts, nil, "jupyter", "", DataProvisionInitial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 seeded (target not empty), got %d", n)
	}
	if fake.findRunCall() != nil {
		t.Errorf("expected no podman run, got calls=%v", fake.calls)
	}
}

func TestProvisionDataBindOnly_Merge(t *testing.T) {
	fake := installFakeRunner(t)
	hostDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostDir, "existing.ipynb"), []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	meta := makeJupyterMeta(false)
	bindMounts := []deploykit.ResolvedBindMount{{Name: "workspace", HostPath: hostDir}}

	n, err := provisionData("podman", "jupyter-img", meta, bindMounts, nil, "jupyter", "", DataProvisionMerge)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 seeded in merge mode, got %d", n)
	}
	runCall := fake.findRunCall()
	if runCall == nil {
		t.Fatal("expected podman run in merge mode")
	}
	if !runCall.containsSubstring("cp -an") {
		t.Errorf("expected cp -an for merge mode, got args=%v", runCall.args)
	}
}

func TestProvisionDataNamedOnly_InitialEmpty(t *testing.T) {
	fake := installFakeRunner(t)
	// volumeIsEmpty is stubbed to return a tempdir path, which is empty.
	mountDir := t.TempDir()
	fake.outHandler = func(name string, args ...string) ([]byte, error) {
		if name == "podman" && len(args) >= 2 && args[0] == "volume" && args[1] == "inspect" {
			return []byte(mountDir + "\n"), nil
		}
		return nil, nil
	}

	meta := makeJupyterMeta(false)
	namedVolumes := []deploykit.VolumeMount{
		{VolumeName: "charly-jupyter-workspace", ContainerPath: "/workspace"},
	}

	n, err := provisionData("podman", "jupyter-img", meta, nil, namedVolumes, "jupyter", "", DataProvisionInitial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 seeded, got %d", n)
	}
	runCall := fake.findRunCall()
	if runCall == nil {
		t.Fatal("expected podman run for named volume")
	}
	if !runCall.containsArg("charly-jupyter-workspace:/seed") {
		t.Errorf("expected -v charly-jupyter-workspace:/seed, got args=%v", runCall.args)
	}
	// Critical: no --userns=keep-id for named volumes.
	for _, a := range runCall.args {
		if strings.HasPrefix(a, "--userns=") {
			t.Errorf("named volume must NOT get --userns, got %q", a)
		}
	}
}

func TestProvisionDataNamedOnly_InitialNonEmpty(t *testing.T) {
	fake := installFakeRunner(t)
	mountDir := t.TempDir()
	// Put a file so volumeIsEmpty returns false.
	if err := os.WriteFile(filepath.Join(mountDir, "user.ipynb"), []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	fake.outHandler = func(name string, args ...string) ([]byte, error) {
		return []byte(mountDir + "\n"), nil
	}

	meta := makeJupyterMeta(false)
	namedVolumes := []deploykit.VolumeMount{{VolumeName: "charly-jupyter-workspace"}}

	n, err := provisionData("podman", "jupyter-img", meta, nil, namedVolumes, "jupyter", "", DataProvisionInitial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 seeded (named volume non-empty), got %d", n)
	}
	if fake.findRunCall() != nil {
		t.Errorf("expected no podman run, got calls=%v", fake.calls)
	}
}

func TestProvisionDataNamedOnly_Merge(t *testing.T) {
	fake := installFakeRunner(t)
	meta := makeJupyterMeta(false)
	namedVolumes := []deploykit.VolumeMount{{VolumeName: "charly-jupyter-workspace"}}

	n, err := provisionData("podman", "jupyter-img", meta, nil, namedVolumes, "jupyter", "", DataProvisionMerge)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 seeded in merge mode, got %d", n)
	}
	runCall := fake.findRunCall()
	if runCall == nil {
		t.Fatal("expected podman run in merge mode for named volume")
	}
	if !runCall.containsSubstring("cp -an") {
		t.Errorf("expected cp -an, got args=%v", runCall.args)
	}
}

func TestProvisionDataMixed(t *testing.T) {
	fake := installFakeRunner(t)
	bindDir := t.TempDir()
	mountDir := t.TempDir()
	fake.outHandler = func(name string, args ...string) ([]byte, error) {
		return []byte(mountDir + "\n"), nil
	}

	meta := &BoxMetadata{
		Box: "multi",
		UID: 1000,
		GID: 1000,
		DataEntries: []LabelDataEntry{
			{Volume: "workspace", Staging: "/data/workspace/", Candy: "a"},
			{Volume: "models", Staging: "/data/models/", Candy: "b"},
		},
	}
	bindMounts := []deploykit.ResolvedBindMount{
		{Name: "workspace", HostPath: bindDir},
	}
	namedVolumes := []deploykit.VolumeMount{
		{VolumeName: "charly-multi-models"},
	}

	n, err := provisionData("podman", "multi-img", meta, bindMounts, namedVolumes, "multi", "", DataProvisionInitial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 seeded (bind + named), got %d", n)
	}

	var sawBindCall, sawNamedCall bool
	for _, c := range fake.calls {
		if c.name != "podman" || len(c.args) == 0 || c.args[0] != "run" {
			continue
		}
		if c.containsArg(bindDir + ":/seed") {
			sawBindCall = true
			if !c.containsSubstring("--userns=keep-id") {
				t.Errorf("bind run missing keep-id: %v", c.args)
			}
		}
		if c.containsArg("charly-multi-models:/seed") {
			sawNamedCall = true
			for _, a := range c.args {
				if strings.HasPrefix(a, "--userns=") {
					t.Errorf("named run must not have keep-id: %v", c.args)
				}
			}
		}
	}
	if !sawBindCall {
		t.Error("expected a podman run with the bind dir")
	}
	if !sawNamedCall {
		t.Error("expected a podman run with the named volume")
	}
}

func TestProvisionDataUnknownVolumeWarns(t *testing.T) {
	fake := installFakeRunner(t)
	// Capture stderr to assert the warning is printed.
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = oldStderr })

	meta := &BoxMetadata{
		Box: "jupyter",
		DataEntries: []LabelDataEntry{
			{Volume: "typo-name", Staging: "/data/typo/", Candy: "notebook-templates"},
		},
	}
	bindMounts := []deploykit.ResolvedBindMount{{Name: "workspace", HostPath: t.TempDir()}}

	n, err := provisionData("podman", "jupyter-img", meta, bindMounts, nil, "jupyter", "", DataProvisionInitial)
	_ = w.Close()
	captured := make([]byte, 4096)
	nb, _ := r.Read(captured)
	stderrOutput := string(captured[:nb])

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 seeded (unknown volume), got %d", n)
	}
	if fake.findRunCall() != nil {
		t.Errorf("expected no podman run for unknown volume, got calls=%v", fake.calls)
	}
	if !strings.Contains(stderrOutput, "typo-name") {
		t.Errorf("expected stderr to mention unknown volume name, got: %q", stderrOutput)
	}
	if !strings.Contains(stderrOutput, "unknown volume") {
		t.Errorf("expected stderr to say 'unknown volume', got: %q", stderrOutput)
	}
}

func TestProvisionDataScratchImageNamedVolume(t *testing.T) {
	fake := installFakeRunner(t)
	// Simulate the helper image not being present on first check, so
	// ensureSeederHelperImage falls through to `podman pull`.
	fake.runHandler = func(name string, args ...string) error {
		if len(args) >= 2 && args[0] == "image" && args[1] == "exists" {
			return os.ErrNotExist // non-nil → trigger pull
		}
		return nil
	}
	meta := makeJupyterMeta(true) // DataImage = true
	namedVolumes := []deploykit.VolumeMount{{VolumeName: "charly-jupyter-workspace"}}

	n, err := provisionData("podman", "scratch-data-img", meta, nil, namedVolumes, "jupyter", "", DataProvisionMerge)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 seeded, got %d", n)
	}

	// Must have pulled the helper image
	var sawPull, sawRunWithMount bool
	for _, c := range fake.calls {
		if c.name == "podman" && len(c.args) >= 2 && c.args[0] == "pull" && c.args[1] == SeederHelperImage {
			sawPull = true
		}
		if c.name == "podman" && len(c.args) > 0 && c.args[0] == "run" {
			if c.containsSubstring("type=image,src=scratch-data-img") {
				sawRunWithMount = true
			}
			if !c.containsArg(SeederHelperImage) {
				t.Errorf("scratch named-volume run missing helper image %q: %v", SeederHelperImage, c.args)
			}
		}
	}
	if !sawPull {
		t.Error("expected `podman pull` of helper image")
	}
	if !sawRunWithMount {
		t.Error("expected `podman run` with --mount type=image,src=scratch-data-img")
	}
}

func TestProvisionDataScratchImageBindMount(t *testing.T) {
	fake := installFakeRunner(t)
	hostDir := t.TempDir()
	meta := makeJupyterMeta(true)
	bindMounts := []deploykit.ResolvedBindMount{{Name: "workspace", HostPath: hostDir}}

	n, err := provisionData("podman", "scratch-data-img", meta, bindMounts, nil, "jupyter", "", DataProvisionInitial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 seeded, got %d", n)
	}

	// Must NOT use the helper image; uses podman create + podman cp instead.
	var sawCreate, sawCp bool
	for _, c := range fake.calls {
		if c.name != "podman" || len(c.args) == 0 {
			continue
		}
		if c.args[0] == "create" {
			sawCreate = true
		}
		if c.args[0] == "cp" {
			sawCp = true
		}
		if c.containsArg(SeederHelperImage) {
			t.Errorf("scratch bind path must not use helper box: %v", c.args)
		}
	}
	if !sawCreate {
		t.Error("expected `podman create` for scratch bind path")
	}
	if !sawCp {
		t.Error("expected `podman cp` for scratch bind path")
	}
}

func TestProvisionDataEntryDestSubdir(t *testing.T) {
	fake := installFakeRunner(t)
	hostDir := t.TempDir()
	meta := &BoxMetadata{
		Box: "jupyter",
		UID: 1000,
		GID: 1000,
		DataEntries: []LabelDataEntry{
			{
				Volume:  "workspace",
				Staging: "/data/workspace/course/",
				Candy:   "notebook-course",
				Dest:    "course",
			},
		},
	}
	bindMounts := []deploykit.ResolvedBindMount{{Name: "workspace", HostPath: hostDir}}

	n, err := provisionData("podman", "jupyter-img", meta, bindMounts, nil, "jupyter", "", DataProvisionInitial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 seeded, got %d", n)
	}
	runCall := fake.findRunCall()
	if runCall == nil {
		t.Fatal("expected podman run")
	}
	if !runCall.containsSubstring("mkdir -p \"/seed/course\"") {
		t.Errorf("expected mkdir -p \"/seed/course\" in bash script, got args=%v", runCall.args)
	}
	if !runCall.containsSubstring("cp -a /data/workspace/course/.") {
		t.Errorf("expected cp -a /data/workspace/course/. in bash script, got args=%v", runCall.args)
	}

	// Confirm the host subdirectory was created.
	if _, statErr := os.Stat(filepath.Join(hostDir, "course")); statErr != nil {
		t.Errorf("expected host subdir to be created: %v", statErr)
	}
}
