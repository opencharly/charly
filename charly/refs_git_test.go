package main

import "testing"

// TestPickResolvedCommit guards the Bug-2 fix: an annotated tag must resolve to
// the underlying COMMIT (refs/tags/X^{}), never the tag OBJECT (refs/tags/X).
// Returning the tag object made a later `git clone --depth 1 --branch <tag>`
// emit git's "refs/tags/X <sha> is not a commit!" warning.
func TestPickResolvedCommit(t *testing.T) {
	const tagObj = "c85de9810981f6655e8f9a5d2307460c0456d780"
	const commit = "2d731456b0b8cfbe2e19b64de75b4d652d2fc94c"
	cases := []struct {
		name      string
		lines     []string
		ref, want string
	}{
		{"annotated tag prefers the peeled commit, not the tag object",
			[]string{tagObj + "\trefs/tags/v1.0.0", commit + "\trefs/tags/v1.0.0^{}"}, "v1.0.0", commit},
		{"peeled line first still wins",
			[]string{commit + "\trefs/tags/v1.0.0^{}", tagObj + "\trefs/tags/v1.0.0"}, "v1.0.0", commit},
		{"lightweight tag (no peel) returns its direct sha",
			[]string{commit + "\trefs/tags/v1.0.0"}, "v1.0.0", commit},
		{"branch returns the head sha",
			[]string{commit + "\trefs/heads/main"}, "main", commit},
		{"ref absent returns empty",
			[]string{commit + "\trefs/tags/v9.9.9"}, "v1.0.0", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickResolvedCommit(c.lines, c.ref); got != c.want {
				t.Errorf("pickResolvedCommit(%v, %q) = %q, want %q", c.lines, c.ref, got, c.want)
			}
		})
	}
}
