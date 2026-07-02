package main

import (
	"reflect"
	"testing"
)

// Covers the build-time subset: every BoxMetadata field that should populate
// into Env, and negative checks for runtime-only vars (must not be present).
func TestResolveTestVarsBuild(t *testing.T) {
	meta := &BoxMetadata{
		Box:       "redis-ml",
		User:      "user",
		Home:      "/home/user",
		UID:       1000,
		GID:       1000,
		DNS:       "redis.example.com",
		AcmeEmail: "ops@example.com",
	}
	r := ResolveCheckVarsBuild(meta)

	if r.HasRuntime {
		t.Error("build-time resolver must not claim runtime")
	}
	want := map[string]string{
		"IMAGE":      "redis-ml",
		"USER":       "user",
		"HOME":       "/home/user",
		"UID":        "1000",
		"GID":        "1000",
		"DNS":        "redis.example.com",
		"ACME_EMAIL": "ops@example.com",
	}
	if !reflect.DeepEqual(r.Env, want) {
		t.Errorf("env mismatch\ngot:  %v\nwant: %v", r.Env, want)
	}
	// Runtime-only keys must be absent.
	for _, key := range []string{"CONTAINER_IP", "CONTAINER_NAME", "HOST_PORT:6379", "VOLUME_PATH:workspace", "ENV_TOKEN"} {
		if _, ok := r.Env[key]; ok {
			t.Errorf("build-time resolver should not populate %q", key)
		}
	}
}

// Nil metadata must not panic; result is an empty map.
func TestResolveTestVarsBuild_Nil(t *testing.T) {
	r := ResolveCheckVarsBuild(nil)
	if r == nil {
		t.Fatal("nil resolver")
	}
	if len(r.Env) != 0 {
		t.Errorf("expected empty env, got %v", r.Env)
	}
}

// Exercises the full runtime path with a swapped InspectContainer. Confirms
// each variable category: container name/IP, HOST_PORT mapping, VOLUME_PATH
// (both named-volume and bind-mount lookup paths), and ENV_<NAME>.
func TestResolveTestVarsRuntime(t *testing.T) {
	origInspect := InspectContainer
	defer func() { InspectContainer = origInspect }()

	InspectContainer = func(engine, name string) (*ContainerInspection, error) {
		return &ContainerInspection{
			Name: "/charly-redis-ml",
			Config: InspectConfig{
				Hostname: "charly-redis-ml",
				Env: []string{
					"REDIS_URL=redis://127.0.0.1:6379",
					"TOKEN=secret",
					"EMPTY=",
					"NOEQUALS", // should be ignored
				},
			},
			NetworkSettings: InspectNetwork{
				Networks: map[string]InspectNetworkBind{
					"charly": {IPAddress: "10.88.0.12"},
				},
				Ports: map[string][]InspectPortBind{
					"6379/tcp": {{HostIp: "0.0.0.0", HostPort: "16379"}},
					"9000/tcp": {}, // declared but unpublished → skipped
				},
			},
			Mounts: []InspectMount{
				{
					Type:        "volume",
					Name:        "charly-redis-ml-data",
					Source:      "/var/lib/containers/storage/volumes/charly-redis-ml-data/_data",
					Destination: "/data",
				},
				{
					Type:        "bind",
					Source:      "/tmp/ws-test",
					Destination: "/workspace",
				},
			},
		}, nil
	}

	meta := &BoxMetadata{
		Box:  "redis-ml",
		User: "user",
		Home: "/home/user",
		Volume: []VolumeMount{
			{VolumeName: "charly-redis-ml-data", ContainerPath: "/data"},
			{VolumeName: "charly-redis-ml-workspace", ContainerPath: "/workspace"},
		},
	}

	r, err := ResolveCheckVarsRuntime(meta, nil, "podman", "redis-ml", "charly-redis-ml", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.HasRuntime {
		t.Error("HasRuntime should be true when inspect succeeds")
	}

	cases := []struct {
		key, want string
	}{
		{"IMAGE", "redis-ml"},
		{"CONTAINER_NAME", "charly-redis-ml"},
		{"CONTAINER_IP", "10.88.0.12"},
		{"HOST_PORT:6379", "16379"},
		{"VOLUME_PATH:data", "/var/lib/containers/storage/volumes/charly-redis-ml-data/_data"},
		{"VOLUME_CONTAINER_PATH:data", "/data"},
		{"VOLUME_PATH:workspace", "/tmp/ws-test"},
		{"VOLUME_CONTAINER_PATH:workspace", "/workspace"},
		{"ENV_REDIS_URL", "redis://127.0.0.1:6379"},
		{"ENV_TOKEN", "secret"},
		{"ENV_EMPTY", ""},
	}
	for _, tc := range cases {
		got, ok := r.Env[tc.key]
		if !ok {
			t.Errorf("missing key %q", tc.key)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.key, got, tc.want)
		}
	}

	// 9000 declared but unpublished — must not appear.
	if _, ok := r.Env["HOST_PORT:9000"]; ok {
		t.Error("unpublished port should not produce HOST_PORT")
	}
	// Malformed env without '=' must not appear.
	if _, ok := r.Env["ENV_NOEQUALS"]; ok {
		t.Error("malformed env line should be skipped")
	}
}

// If inspect fails, the resolver still returns the build-time subset and
// HasRuntime=false so the caller can report skip reasons on runtime vars.
func TestResolveTestVarsRuntime_InspectFails(t *testing.T) {
	origInspect := InspectContainer
	defer func() { InspectContainer = origInspect }()

	InspectContainer = func(engine, name string) (*ContainerInspection, error) {
		return nil, errStub("inspect failed")
	}

	meta := &BoxMetadata{Box: "jupyter", User: "user", Home: "/home/user"}
	r, err := ResolveCheckVarsRuntime(meta, nil, "podman", "jupyter", "charly-jupyter", "")
	if err == nil {
		t.Fatal("expected error from failed inspect")
	}
	if r == nil {
		t.Fatal("resolver should be non-nil even on error")
	}
	if r.HasRuntime {
		t.Error("HasRuntime should be false when inspect failed")
	}
	if r.Env["IMAGE"] != "jupyter" {
		t.Errorf("build-time vars should still be populated, got %v", r.Env)
	}
	if _, ok := r.Env["CONTAINER_IP"]; ok {
		t.Error("runtime vars must not be populated on failure")
	}
}

// Top-level IPAddress (docker bridge) path.
func TestRuntimeVars_DockerBridgeIP(t *testing.T) {
	origInspect := InspectContainer
	defer func() { InspectContainer = origInspect }()

	InspectContainer = func(engine, name string) (*ContainerInspection, error) {
		return &ContainerInspection{
			Name: "/foo",
			NetworkSettings: InspectNetwork{
				IPAddress: "172.17.0.2",
			},
		}, nil
	}
	r, _ := ResolveCheckVarsRuntime(&BoxMetadata{Box: "foo"}, nil, "docker", "foo", "foo", "")
	if r.Env["CONTAINER_IP"] != "172.17.0.2" {
		t.Errorf("docker-style top-level IP not picked up: %v", r.Env)
	}
}

// Exercises the full substitution pipeline: resolver output feeds ExpandTestVars.
func TestResolver_EndToEndExpansion(t *testing.T) {
	origInspect := InspectContainer
	defer func() { InspectContainer = origInspect }()

	InspectContainer = func(engine, name string) (*ContainerInspection, error) {
		return &ContainerInspection{
			Name: "/charly-redis",
			NetworkSettings: InspectNetwork{
				Networks: map[string]InspectNetworkBind{"charly": {IPAddress: "10.0.0.5"}},
				Ports: map[string][]InspectPortBind{
					"6379/tcp": {{HostPort: "16379"}},
				},
			},
		}, nil
	}
	r, err := ResolveCheckVarsRuntime(&BoxMetadata{Box: "redis", User: "u", Home: "/home/u"}, nil, "podman", "redis", "charly-redis", "")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	in := "redis-cli -h ${CONTAINER_IP} -p ${HOST_PORT:6379} ${HOME}/.rdb"
	got, missing := ExpandTestVars(in, r.Env)
	want := "redis-cli -h 10.0.0.5 -p 16379 /home/u/.rdb"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if len(missing) != 0 {
		t.Errorf("unexpected missing: %v", missing)
	}
}

// errStub is a tiny error type so tests don't pull in fmt just to build one.
type errStub string

func (e errStub) Error() string { return string(e) }

// DEPLOY_NAME is seeded (sanitized) in the runtime resolver so deploy-scope
// checks can address their own cluster via cluster: "${DEPLOY_NAME}" — the
// k3s-server fix. A colon-prefixed VM deploy name is sanitized identically to
// K3sPostProvision's ClusterProfile naming (vm:k3s-vm -> vm-k3s-vm).
func TestResolveTestVarsRuntime_DeployName(t *testing.T) {
	origInspect := InspectContainer
	defer func() { InspectContainer = origInspect }()
	InspectContainer = func(engine, name string) (*ContainerInspection, error) {
		return &ContainerInspection{Name: "/charly-x"}, nil
	}
	r, err := ResolveCheckVarsRuntime(&BoxMetadata{Box: "x"}, nil, "podman", "redis-ml", "charly-x", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := r.Env["DEPLOY_NAME"]; got != "redis-ml" {
		t.Errorf("DEPLOY_NAME = %q, want redis-ml", got)
	}
	r2, _ := ResolveCheckVarsRuntime(&BoxMetadata{Box: "x"}, nil, "podman", "vm:k3s-vm", "charly-x", "")
	if got := r2.Env["DEPLOY_NAME"]; got != "vm-k3s-vm" {
		t.Errorf("DEPLOY_NAME = %q, want vm-k3s-vm", got)
	}
}
