package main

import (
	"testing"
)

// TestBundleNode_LifecycleAloneDoesNotAuthorize verifies the
// long-standing anti-derivation invariant: lifecycle: dev does NOT
// imply disposable: true.
func TestBundleNode_LifecycleAloneDoesNotAuthorize(t *testing.T) {
	for _, tier := range []string{"scratch", "dev", "test", "qa", "staging", "prod"} {
		node := BundleNode{Lifecycle: tier}
		if node.IsDisposable() {
			t.Errorf("lifecycle=%q must NOT make a deploy disposable", tier)
		}
	}
}

// TestBundleNode_EphemeralImpliesDisposable verifies the load-
// bearing exception: ephemeral: ... DOES imply disposable: true.
func TestBundleNode_EphemeralImpliesDisposable(t *testing.T) {
	tests := []struct {
		name string
		node BundleNode
		want bool
	}{
		{
			name: "ephemeral block-form implies disposable",
			node: BundleNode{
				Ephemeral: &EphemeralLifetime{TTL: "30m"},
			},
			want: true,
		},
		{
			name: "ephemeral with all defaults still implies disposable",
			node: BundleNode{
				Ephemeral: &EphemeralLifetime{},
			},
			want: true,
		},
		{
			name: "no ephemeral block, no disposable → not disposable",
			node: BundleNode{},
			want: false,
		},
		{
			name: "explicit disposable + no ephemeral → disposable",
			node: BundleNode{Disposable: new(true)},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.node.IsDisposable(); got != tt.want {
				t.Errorf("IsDisposable() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestBundleNode_IsEphemeral verifies the IsEphemeral check tracks
// EphemeralLifetime presence.
func TestBundleNode_IsEphemeral(t *testing.T) {
	tests := []struct {
		name string
		node BundleNode
		want bool
	}{
		{name: "no block", node: BundleNode{}, want: false},
		{name: "block with ttl", node: BundleNode{Ephemeral: &EphemeralLifetime{TTL: "1h"}}, want: true},
		{name: "block with empty fields", node: BundleNode{Ephemeral: &EphemeralLifetime{}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.node.IsEphemeral(); got != tt.want {
				t.Errorf("IsEphemeral() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestEphemeralLifetime_EffectiveTTL covers the parsing + default.
func TestEphemeralLifetime_EffectiveTTL(t *testing.T) {
	tests := []struct {
		name string
		eph  *EphemeralLifetime
		want string
	}{
		{name: "nil → 0", eph: nil, want: "0s"},
		{name: "empty → default 1h", eph: &EphemeralLifetime{}, want: "1h0m0s"},
		{name: "30m parses", eph: &EphemeralLifetime{TTL: "30m"}, want: "30m0s"},
		{name: "invalid → default", eph: &EphemeralLifetime{TTL: "not-a-duration"}, want: "1h0m0s"},
		{name: "negative → default", eph: &EphemeralLifetime{TTL: "-5m"}, want: "1h0m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.eph.EffectiveTTL().String()
			if got != tt.want {
				t.Errorf("EffectiveTTL() = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestEphemeralLifetime_EffectiveNamingPattern covers the default fall-through.
func TestEphemeralLifetime_EffectiveNamingPattern(t *testing.T) {
	defaultPat := "{{.Source}}-eph-{{.UUID6}}"
	if got := (*EphemeralLifetime)(nil).EffectiveNamingPattern(); got != defaultPat {
		t.Errorf("nil.EffectiveNamingPattern() = %q, want %q", got, defaultPat)
	}
	if got := (&EphemeralLifetime{}).EffectiveNamingPattern(); got != defaultPat {
		t.Errorf("empty.EffectiveNamingPattern() = %q, want %q", got, defaultPat)
	}
	custom := "test-{{.UUID6}}"
	if got := (&EphemeralLifetime{NamingPattern: custom}).EffectiveNamingPattern(); got != custom {
		t.Errorf("custom.EffectiveNamingPattern() = %q, want %q", got, custom)
	}
}

// TestRenderNamingPattern covers the template rendering for instance
// names.
func TestRenderNamingPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		source  string
		id      string
		want    string
		wantErr bool
	}{
		{name: "default pattern", pattern: "{{.Source}}-eph-{{.UUID6}}", source: "arch-test", id: "abcdef", want: "arch-test-eph-abcdef"},
		{name: "literal", pattern: "fixed", source: "x", id: "y", want: "fixed"},
		{name: "bad template", pattern: "{{.Bad", source: "x", id: "y", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := renderNamingPattern(tt.pattern, tt.source, tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr = %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNewEphemeralID verifies the ID is six hex characters.
func TestNewEphemeralID(t *testing.T) {
	id, err := newEphemeralID()
	if err != nil {
		t.Fatalf("newEphemeralID error: %v", err)
	}
	if len(id) != 6 {
		t.Errorf("ID length = %d, want 6 (got %q)", len(id), id)
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("non-hex character in ID %q: %c", id, c)
		}
	}
	// Two successive IDs should differ overwhelmingly often.
	id2, _ := newEphemeralID()
	if id == id2 {
		t.Errorf("two consecutive IDs match (statistically vanishing probability): %q == %q", id, id2)
	}
}

// TestValidateVmNamingGuard verifies the -eph- infix is reserved.
func TestValidateVmNamingGuard(t *testing.T) {
	tests := []struct {
		name        string
		shouldError bool
	}{
		{name: "arch", shouldError: false},
		{name: "arch-test", shouldError: false},
		{name: "fedora-coder", shouldError: false},
		{name: "arch-eph-abc", shouldError: true},
		{name: "test-eph-deadbeef", shouldError: true},
		{name: "-eph-", shouldError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := &ValidationError{}
			ValidateVmNamingGuard(tt.name, errs)
			has := errs.HasErrors()
			if has != tt.shouldError {
				t.Errorf("ValidateVmNamingGuard(%q) errors=%v, want %v", tt.name, has, tt.shouldError)
			}
		})
	}
}
