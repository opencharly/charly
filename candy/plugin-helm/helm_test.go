package helm

import (
	"reflect"
	"strings"
	"testing"
)

// flattenValues must be deterministic (sorted keys) and render nested maps as dotted
// keys, lists as indexed keys, and scalars via scalarString — the exact `helm --set`
// argument shape.
func TestFlattenValues(t *testing.T) {
	cases := []struct {
		name   string
		in     map[string]any
		expect []string
	}{
		{
			name:   "empty",
			in:     map[string]any{},
			expect: nil,
		},
		{
			name:   "flat scalars",
			in:     map[string]any{"replicaCount": 2, "image.tag": "v1"},
			expect: []string{"image.tag=v1", "replicaCount=2"},
		},
		{
			name: "nested map + list",
			in: map[string]any{
				"service": map[string]any{
					"type": "ClusterIP",
					"ports": []any{
						map[string]any{"port": 80},
						map[string]any{"port": 443},
					},
				},
			},
			expect: []string{"service.ports[0].port=80", "service.ports[1].port=443", "service.type=ClusterIP"},
		},
		{
			name:   "bool and nil",
			in:     map[string]any{"wait": true, "empty": nil},
			expect: []string{"empty=", "wait=true"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := flattenValues(tc.in)
			if !reflect.DeepEqual(got, tc.expect) {
				t.Errorf("flattenValues(%v) = %v, want %v", tc.in, got, tc.expect)
			}
		})
	}
}

// scalarString must render the leaf types helm --set accepts.
func TestScalarString(t *testing.T) {
	cases := []struct {
		in     any
		expect string
	}{
		{"v1", "v1"},
		{true, "true"},
		{int(2), "2"},
		{int64(3), "3"},
		{float64(1.5), "1.5"},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := scalarString(tc.in); got != tc.expect {
			t.Errorf("scalarString(%v) = %q, want %q", tc.in, got, tc.expect)
		}
	}
}

// kubeconfigPrelude must prefer the operator's KUBECONFIG, then the k3s guest default,
// then ~/.kube/config — and never clobber an already-set KUBECONFIG.
func TestKubeconfigPrelude(t *testing.T) {
	pre := kubeconfigPrelude
	if !strings.Contains(pre, `export KUBECONFIG="${KUBECONFIG:-}"`) {
		t.Errorf("prelude must preserve an existing KUBECONFIG: %q", pre)
	}
	if !strings.Contains(pre, "/etc/rancher/k3s/k3s.yaml") {
		t.Errorf("prelude must fall back to the k3s guest kubeconfig: %q", pre)
	}
	if !strings.Contains(pre, "$HOME/.kube/config") {
		t.Errorf("prelude must fall back to ~/.kube/config: %q", pre)
	}
}
