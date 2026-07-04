package k8sgen

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestCheckToProbe_HTTP covers the http: → httpGet shape.
func TestCheckToProbe_HTTP(t *testing.T) {
	tests := []struct {
		name string
		http string
		want map[string]any
	}{
		{
			name: "http with explicit port + path",
			http: "http://example.com:8080/healthz",
			want: map[string]any{"httpGet": map[string]any{"path": "/healthz", "port": 8080, "host": "example.com"}},
		},
		{
			name: "https default port",
			http: "https://api.example.com/ready",
			want: map[string]any{"httpGet": map[string]any{"path": "/ready", "port": 443, "host": "api.example.com"}},
		},
		{
			name: "localhost host elided",
			http: "http://localhost:9090/metrics",
			want: map[string]any{"httpGet": map[string]any{"path": "/metrics", "port": 9090}},
		},
		{
			name: "127.0.0.1 host elided",
			http: "http://127.0.0.1:80/",
			want: map[string]any{"httpGet": map[string]any{"path": "/", "port": 80}},
		},
		{
			name: "no path defaults to /",
			http: "http://example.com:8080",
			want: map[string]any{"httpGet": map[string]any{"path": "/", "port": 8080, "host": "example.com"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkToProbe(&spec.Op{Plugin: "http", PluginInput: map[string]any{"http": tt.http}})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCheckToProbe_Addr covers the addr: → tcpSocket shape.
func TestCheckToProbe_Addr(t *testing.T) {
	tests := []struct {
		name, addr string
		want       map[string]any
	}{
		{
			name: "host:port",
			addr: "example.com:5432",
			want: map[string]any{"tcpSocket": map[string]any{"port": 5432, "host": "example.com"}},
		},
		{
			name: "127.0.0.1 elided",
			addr: "127.0.0.1:6379",
			want: map[string]any{"tcpSocket": map[string]any{"port": 6379}},
		},
		{
			name: "bare port",
			addr: "8080",
			want: map[string]any{"tcpSocket": map[string]any{"port": 8080}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkToProbe(&spec.Op{Plugin: "addr", PluginInput: map[string]any{"addr": tt.addr}})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCheckToProbe_File covers plugin: file (path from plugin_input) → exec test -e.
func TestCheckToProbe_File(t *testing.T) {
	got := checkToProbe(&spec.Op{Plugin: "file", PluginInput: map[string]any{"file": "/etc/ready"}})
	want := map[string]any{"exec": map[string]any{"command": []string{"test", "-e", "/etc/ready"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestCheckToProbe_Command covers command: → exec sh -c.
func TestCheckToProbe_Command(t *testing.T) {
	got := checkToProbe(&spec.Op{Plugin: "command", PluginInput: map[string]any{"command": "redis-cli ping | grep PONG"}})
	want := map[string]any{"exec": map[string]any{"command": []string{"sh", "-c", "redis-cli ping | grep PONG"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestCheckToProbe_NilAndEmpty covers the no-op paths.
func TestCheckToProbe_NilAndEmpty(t *testing.T) {
	if got := checkToProbe(nil); got != nil {
		t.Errorf("nil check: got %v, want nil", got)
	}
	if got := checkToProbe(&spec.Op{}); got != nil {
		t.Errorf("empty check: got %v, want nil", got)
	}
}

// TestCheckToProbe_HTTPPriority documents the priority order: HTTP (read from
// plugin_input) wins over Addr/File/Command when multiple are set (no real check carries
// more than one verb after validation, but the function is robust). file/addr are plugin
// verbs now (one plugin per op), so the residual cross-field case is HTTP vs the Command
// modifier.
func TestCheckToProbe_HTTPPriority(t *testing.T) {
	got := checkToProbe(&spec.Op{Plugin: "http", PluginInput: map[string]any{"http": "http://example.com:80/health"}, Command: "test -e /etc/ready"})
	if _, ok := got["httpGet"]; !ok {
		t.Errorf("expected httpGet to win over command, got %v", got)
	}
}

// TestGenerateTree_Shape is the C8/M13 carve-out safety net: a representative input
// (Deployment workload with ports, env, storage, and an ingress host) generates the
// expected relative-pathed file set, and the key doc fields survive the port
// (Deployment spec.replicas, container image == ImageRef, podSecurityContext
// runAsUser == UID). Proves the moved generator still produces correct manifests.
func TestGenerateTree_Shape(t *testing.T) {
	cluster := spec.K8s{}
	cluster.Ingress.Enabled = true
	in := spec.K8sGenInput{
		DeploymentName: "web",
		ImageRef:       "registry.example.com/web:v1",
		Ports:          []string{"8080", "9090/udp"},
		UID:            1000,
		GID:            1000,
		Cluster:        cluster,
		Deploy: spec.Deploy{
			Replica:    3,
			Env:        []string{"FOO=bar"},
			Kubernetes: &spec.K8sDeploy{Workload: "Deployment"}, // force Deployment despite storage
			Storage:    []spec.DeployStorage{{Name: "data", Size: "5Gi", Path: "/data"}},
			Expose:     &spec.DeployExpose{Host: "web.example.com", Port: "8080"},
		},
	}

	reply, err := GenerateTree(in)
	if err != nil {
		t.Fatalf("GenerateTree: %v", err)
	}

	if reply.OverlayRelPath != "overlays/default" {
		t.Errorf("OverlayRelPath = %q, want overlays/default", reply.OverlayRelPath)
	}

	// Collect the relative paths and index docs by path.
	got := map[string]json.RawMessage{}
	for _, f := range reply.Files {
		got[f.RelPath] = f.Doc
	}
	wantPaths := []string{
		"base/deployment.yaml",
		"base/service.yaml",
		"base/pvc-web-data.yaml",
		"base/ingress.yaml",
		"base/kustomization.yaml",
		"overlays/default/kustomization.yaml",
	}
	for _, p := range wantPaths {
		if _, ok := got[p]; !ok {
			t.Errorf("missing generated file %q (have %v)", p, keys(got))
		}
	}

	// Key doc fields survived the port.
	var dep map[string]any
	if err := json.Unmarshal(got["base/deployment.yaml"], &dep); err != nil {
		t.Fatalf("decode deployment.yaml: %v", err)
	}
	depSpec, _ := dep["spec"].(map[string]any)
	if depSpec == nil {
		t.Fatalf("deployment has no spec: %v", dep)
	}
	if rep, _ := depSpec["replicas"].(float64); rep != 3 {
		t.Errorf("deployment spec.replicas = %v, want 3", depSpec["replicas"])
	}
	tmpl, _ := depSpec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]any)
	if len(containers) != 1 {
		t.Fatalf("want 1 container, got %v", podSpec["containers"])
	}
	c0, _ := containers[0].(map[string]any)
	if img, _ := c0["image"].(string); img != in.ImageRef {
		t.Errorf("container image = %q, want %q", img, in.ImageRef)
	}
	sc, _ := podSpec["securityContext"].(map[string]any)
	if ras, _ := sc["runAsUser"].(float64); int(ras) != in.UID {
		t.Errorf("podSecurityContext.runAsUser = %v, want %d", sc["runAsUser"], in.UID)
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
