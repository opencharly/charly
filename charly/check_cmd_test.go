package main

import "testing"

// TestGuestNestedCheckCmd verifies the guest-side `charly check live` command that
// runVm dispatches for a nested-in-VM pod (Cutover 6 delegation). The host's
// format/section/filter/instance selectors must pass through, single-quoted, so
// the guest produces the same report shape the host would.
func TestGuestNestedCheckCmd(t *testing.T) {
	cases := []struct {
		name     string
		pod      string
		format   string
		section  string
		filter   []string
		instance string
		want     string
	}{
		{
			name:   "minimal (default text)",
			pod:    "selkies-kde",
			format: "text",
			want:   "charly check live 'selkies-kde' --format 'text'",
		},
		{
			name:   "empty format defaults to text",
			pod:    "selkies-kde",
			format: "",
			want:   "charly check live 'selkies-kde' --format 'text'",
		},
		{
			name:     "all selectors pass through",
			pod:      "p",
			format:   "json",
			section:  "deploy",
			filter:   []string{"cdp", "wl"},
			instance: "work",
			want:     "charly check live 'p' --format 'json' --section 'deploy' --filter 'cdp' --filter 'wl' -i 'work'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := guestNestedCheckCmd(tc.pod, tc.format, tc.section, tc.filter, tc.instance)
			if got != tc.want {
				t.Errorf("guestNestedCheckCmd = %q, want %q", got, tc.want)
			}
		})
	}
}
