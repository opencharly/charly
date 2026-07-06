package sidecarkind

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

func mustBody(t *testing.T, s spec.Sidecar) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal sidecar: %v", err)
	}
	return b
}

func TestMergeSidecar_EnvMerge(t *testing.T) {
	base := map[string]spec.Sidecar{
		"tailscale": {
			Image: "tailscale:base",
			Env:   spec.StrMap{"TS_STATE_DIR": "/var/lib/tailscale", "TS_USERSPACE": "false", "TS_ACCEPT_DNS": "true"},
		},
	}
	overlay := map[string]spec.Sidecar{
		"tailscale": {Env: spec.StrMap{"TS_HOSTNAME": "my-app", "TS_USERSPACE": "true"}},
	}
	ts := mergeSidecar(base, overlay)["tailscale"]
	if ts.Image != "tailscale:base" {
		t.Errorf("image = %q, want tailscale:base", ts.Image)
	}
	if ts.Env["TS_STATE_DIR"] != "/var/lib/tailscale" {
		t.Error("TS_STATE_DIR should be preserved from base")
	}
	if ts.Env["TS_HOSTNAME"] != "my-app" {
		t.Error("TS_HOSTNAME should be added from overlay")
	}
	if ts.Env["TS_USERSPACE"] != "true" {
		t.Error("TS_USERSPACE should be overridden by overlay")
	}
	if ts.Env["TS_ACCEPT_DNS"] != "true" {
		t.Error("TS_ACCEPT_DNS should be preserved from base")
	}
}

func TestMergeSidecar_NilInputs(t *testing.T) {
	if result := mergeSidecar(nil, nil); result != nil {
		t.Error("nil+nil should return nil")
	}
	if result := mergeSidecar(nil, map[string]spec.Sidecar{"a": {Image: "x"}}); result["a"].Image != "x" {
		t.Error("nil base + overlay should return overlay")
	}
	if result := mergeSidecar(map[string]spec.Sidecar{"a": {Image: "x"}}, nil); result["a"].Image != "x" {
		t.Error("base + nil overlay should return copy of base")
	}
}

// TestMergeSidecar_Parameter guards the Parameter map merge — regression for the
// bug where a deploy's `parameter.tailnet` was silently dropped during merge.
func TestMergeSidecar_Parameter(t *testing.T) {
	base := map[string]spec.Sidecar{"tailscale": {Parameter: spec.StrMap{"tailnet": ""}}}
	overlay := map[string]spec.Sidecar{"tailscale": {Parameter: spec.StrMap{"tailnet": "armadillo-quail.ts.net"}}}
	if got := mergeSidecar(base, overlay)["tailscale"].Parameter["tailnet"]; got != "armadillo-quail.ts.net" {
		t.Errorf("merge dropped deploy parameter: got %q", got)
	}
	base2 := map[string]spec.Sidecar{"x": {Parameter: spec.StrMap{"a": "default-a"}}}
	overlay2 := map[string]spec.Sidecar{"x": {Parameter: spec.StrMap{"b": "deploy-b"}}}
	merged2 := mergeSidecar(base2, overlay2)
	if merged2["x"].Parameter["a"] != "default-a" {
		t.Errorf("template default lost: got %q", merged2["x"].Parameter["a"])
	}
	if merged2["x"].Parameter["b"] != "deploy-b" {
		t.Errorf("deploy value lost: got %q", merged2["x"].Parameter["b"])
	}
}

func TestResolveEach_Naming(t *testing.T) {
	defs := map[string]spec.Sidecar{
		"tailscale": {
			Image:    "ts:latest",
			Env:      spec.StrMap{"TS_HOSTNAME": "test"},
			Volume:   []spec.SidecarVolume{{Name: "state", Path: "/var/lib/tailscale"}},
			Secret:   []spec.SidecarSecret{{Name: "ts-authkey", Env: "TS_AUTHKEY"}},
			Security: &spec.Security{CapAdd: []string{"NET_ADMIN"}},
		},
	}
	resolved, err := resolveEach(defs, "my-app", "")
	if err != nil {
		t.Fatalf("resolveEach: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 sidecar, got %d", len(resolved))
	}
	sc := resolved[0]
	if sc.Volume[0].VolumeName != "charly-my-app-tailscale-state" {
		t.Errorf("volume name = %q, want charly-my-app-tailscale-state", sc.Volume[0].VolumeName)
	}
	if sc.Secret[0].Name != "charly-my-app-tailscale-ts-authkey" {
		t.Errorf("secret name = %q, want charly-my-app-tailscale-ts-authkey", sc.Secret[0].Name)
	}
}

func TestSidecarEnvKey(t *testing.T) {
	sidecars := map[string]spec.Sidecar{
		"tailscale": {
			Env:    spec.StrMap{"TS_STATE_DIR": "/var/lib/tailscale"},
			Secret: []spec.SidecarSecret{{Name: "ts-authkey", Env: "TS_AUTHKEY"}},
		},
	}
	keys := sidecarEnvKey(sidecars)
	for _, k := range []string{"TS_STATE_DIR", "TS_AUTHKEY", "TS_HOSTNAME", "TS_EXTRA_ARGS"} {
		if keys[k] != "tailscale" {
			t.Errorf("%s should map to tailscale, got %q", k, keys[k])
		}
	}
}

// TestRenderEnvFrom exercises the env-var name resolution the tailscale sidecar uses
// for multi-tailnet .secrets storage.
func TestRenderEnvFrom(t *testing.T) {
	for _, tc := range []struct {
		name, envFrom, envFallback string
		params                     spec.StrMap
		wantHostEnv                string
		wantErr                    bool
		errContains                string
	}{
		{name: "legacy_default_falls_back", envFrom: "", envFallback: "TS_AUTHKEY", wantHostEnv: "TS_AUTHKEY"},
		{name: "tailnet_armadillo", envFrom: "TS_AUTHKEY_{{.Parameter.tailnet | tailnetEnvSuffix}}", envFallback: "TS_AUTHKEY", params: spec.StrMap{"tailnet": "armadillo-quail.ts.net"}, wantHostEnv: "TS_AUTHKEY_ARMADILLO_QUAIL_TS_NET"},
		{name: "missing_param_errors", envFrom: "TS_AUTHKEY_{{.Parameter.tailnet | tailnetEnvSuffix}}", envFallback: "TS_AUTHKEY", wantErr: true, errContains: "parameter \"tailnet\" which is unset"},
		{name: "empty_param_treated_missing", envFrom: "TS_AUTHKEY_{{.Parameter.tailnet | tailnetEnvSuffix}}", envFallback: "TS_AUTHKEY", params: spec.StrMap{"tailnet": ""}, wantErr: true, errContains: "which is unset"},
		{name: "special_chars_normalized", envFrom: "PREFIX_{{.Parameter.x | tailnetEnvSuffix}}", envFallback: "PREFIX", params: spec.StrMap{"x": "weird/name.with-stuff:and stuff"}, wantHostEnv: "PREFIX_WEIRD_NAME_WITH_STUFF_AND_STUFF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderEnvFrom(spec.SidecarSecret{Name: "test", Env: tc.envFallback, EnvFrom: tc.envFrom}, tc.params)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (result %q)", tc.errContains, got)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantHostEnv {
				t.Errorf("got %q, want %q", got, tc.wantHostEnv)
			}
		})
	}
}

func TestExtractParameterRefs(t *testing.T) {
	for _, tc := range []struct {
		name, tmpl string
		want       []string
	}{
		{"single_ref", "TS_AUTHKEY_{{.Parameter.tailnet | tailnetEnvSuffix}}", []string{"tailnet"}},
		{"two_refs", "{{.Parameter.a}}_{{.Parameter.b}}", []string{"a", "b"}},
		{"no_refs", "static-string", []string{}},
		{"empty", "", []string{}},
		{"ref_then_func", "{{.Parameter.tailnet | tailnetEnvSuffix}}_suffix", []string{"tailnet"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := extractParameterRefs(tc.tmpl)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d refs (%v), want %d (%v)", len(got), got, len(tc.want), tc.want)
			}
			for _, name := range tc.want {
				if _, ok := got[name]; !ok {
					t.Errorf("missing %q in %v", name, got)
				}
			}
		})
	}
}

// TestResolveSidecars_EndToEnd proves the full OpResolve behavior the kernel used to
// do inline (Cutover D): CLI env-flag routing (sidecar keys → the sidecar, app keys
// stay in AppEnv) + embedded<deploy merge + volume/secret resolution.
func TestResolveSidecars_EndToEnd(t *testing.T) {
	in := spec.SidecarResolveInput{
		EmbeddedTemplates: map[string]json.RawMessage{
			"tailscale": mustBody(t, spec.Sidecar{
				Image:  "ghcr.io/tailscale/tailscale:latest",
				Env:    spec.StrMap{"TS_STATE_DIR": "/var/lib/tailscale"},
				Volume: []spec.SidecarVolume{{Name: "state", Path: "/var/lib/tailscale"}},
			}),
		},
		DeployOverrides: map[string]json.RawMessage{
			"tailscale": mustBody(t, spec.Sidecar{Env: spec.StrMap{"TS_HOSTNAME": "my-app"}}),
		},
		CliEnv:   []string{"TS_EXTRA_ARGS=--advertise-routes", "APP_VAR=x"},
		Box:      "my-app",
		Instance: "",
	}
	reply, err := resolveSidecars(in)
	if err != nil {
		t.Fatalf("resolveSidecars: %v", err)
	}
	// App-only env: TS_EXTRA_ARGS routed to the sidecar (well-known TS_ prefix), APP_VAR stays.
	if len(reply.AppEnv) != 1 || reply.AppEnv[0] != "APP_VAR=x" {
		t.Errorf("AppEnv = %v, want [APP_VAR=x]", reply.AppEnv)
	}
	// The routed var is folded into the persisted override.
	var persisted spec.Sidecar
	if err := json.Unmarshal(reply.PersistOverrides["tailscale"], &persisted); err != nil {
		t.Fatalf("decode persisted override: %v", err)
	}
	if persisted.Env["TS_EXTRA_ARGS"] != "--advertise-routes" {
		t.Errorf("routed env not persisted: %v", persisted.Env)
	}
	// The resolved sidecar merged embedded image + deploy env + named the volume.
	if len(reply.Sidecars) != 1 {
		t.Fatalf("expected 1 resolved sidecar, got %d", len(reply.Sidecars))
	}
	sc := reply.Sidecars[0]
	if sc.Image != "ghcr.io/tailscale/tailscale:latest" {
		t.Errorf("image = %q, want the embedded template's", sc.Image)
	}
	if sc.Env["TS_HOSTNAME"] != "my-app" || sc.Env["TS_STATE_DIR"] != "/var/lib/tailscale" {
		t.Errorf("merged env = %v", sc.Env)
	}
	if len(sc.Volume) != 1 || sc.Volume[0].VolumeName != "charly-my-app-tailscale-state" {
		t.Errorf("volume = %v, want charly-my-app-tailscale-state", sc.Volume)
	}
}
