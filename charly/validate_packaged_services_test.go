package main

import "testing"

// TestCandyHasOrphanPackaged guards the preserve_user-warning suppression:
// the warning must fire ONLY for a use_packaged service with no same-name
// custom-exec sibling (genuinely dropped under supervisord), and stay silent
// for the canonical mixed-form polymorphism pattern (sshd) where the exec
// sibling provides the supervisord fallback.
func TestCandyHasOrphanPackaged(t *testing.T) {
	tests := []struct {
		name  string
		layer *Candy
		want  bool
	}{
		{
			name:  "nil layer",
			layer: nil,
			want:  false,
		},
		{
			name: "mixed-form (packaged + same-name exec sibling) — sshd pattern — no orphan",
			layer: &Candy{service: []ServiceEntry{
				{Name: "sshd", UsePackaged: "sshd.service"},
				{Name: "sshd", Exec: "/usr/local/bin/sshd-wrapper"},
			}},
			want: false,
		},
		{
			name: "packaged-only (no exec sibling) — postgresql pattern — orphan",
			layer: &Candy{service: []ServiceEntry{
				{Name: "postgresql", UsePackaged: "postgresql.service"},
			}},
			want: true,
		},
		{
			name: "packaged with a DIFFERENT-name exec sibling — still orphan",
			layer: &Candy{service: []ServiceEntry{
				{Name: "postgresql", UsePackaged: "postgresql.service"},
				{Name: "other", Exec: "/bin/other"},
			}},
			want: true,
		},
		{
			name: "custom-only — no packaged — no orphan",
			layer: &Candy{service: []ServiceEntry{
				{Name: "svc", Exec: "svc serve"},
			}},
			want: false,
		},
		{
			name:  "no services — no orphan",
			layer: &Candy{},
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := candyHasOrphanPackaged(tt.layer); got != tt.want {
				t.Errorf("candyHasOrphanPackaged() = %v, want %v", got, tt.want)
			}
		})
	}
}
