package docs

import "testing"

// resolver used by the tests: two real skills and one reference file.
func testResolver() linkResolver {
	return linkResolver{
		skillPages: map[string]string{
			"check:check":     "/recipes/check/check/",
			"internals:go":    "/recipes/internals/go/",
			"image:layer":     "/recipes/image/layer/",
			"selkies:selkies": "/recipes/selkies/selkies/",
		},
		refPages: map[string]string{
			"check/check/beds-and-r10": "/recipes/check/check/beds-and-r10/",
		},
	}
}

func TestRewriteSkillReferences(t *testing.T) {
	lr := testResolver()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			// 3745 of the corpus's 3788 references are inside a code span. The link must WRAP
			// the span — injecting it inside would render the markdown literally.
			name: "backticked reference wraps the code span",
			in:   "See `/charly-check:check` for detail.",
			want: "See [`/charly-check:check`](/recipes/check/check/) for detail.",
		},
		{
			name: "bare reference becomes a plain link",
			in:   "verification: see /charly-check:check",
			want: "verification: see [/charly-check:check](/recipes/check/check/)",
		},
		{
			name: "two references on one line both rewrite",
			in:   "`/charly-internals:go` and `/charly-image:layer`",
			want: "[`/charly-internals:go`](/recipes/internals/go/) and [`/charly-image:layer`](/recipes/image/layer/)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, dangling := lr.rewrite(tc.in, "check", "check", "test.md")
			if len(dangling) != 0 {
				t.Fatalf("unexpected dangling refs: %v", dangling)
			}
			if got != tc.want {
				t.Errorf("rewrite:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// TestRewriteLeavesNonReferencesAlone locks in the three guards. Each case is a REAL shape from
// the corpus that a naive `/charly-<word>:<word>` match would corrupt into a broken link.
func TestRewriteLeavesNonReferencesAlone(t *testing.T) {
	lr := testResolver()
	cases := []struct {
		name string
		in   string
	}{
		{
			// Guard 1 (skill part must start with a letter): host:port strings.
			name: "redis url with port",
			in:   "consumers receive `redis://charly-redis:6379`",
		},
		{
			name: "jupyter url with port",
			in:   `{"url":"http://charly-jupyter:8888"}`,
		},
		{
			// Guard 2 (no `/` before): a URL authority.
			name: "scheme-relative authority",
			in:   "http://charly-check:check",
		},
		{
			// Guard 2 (no word character before): a container image tag. This one is why the
			// guard checks word characters and not just `/` — `localhost` ends in a letter.
			name: "container image tag",
			in:   "podman run localhost/charly-selkies-kde:latest selkies-kde",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, dangling := lr.rewrite(tc.in, "check", "check", "test.md")
			if got != tc.in {
				t.Errorf("input was rewritten but should have been left alone:\n got: %q\nwant: %q", got, tc.in)
			}
			if len(dangling) != 0 {
				t.Errorf("input was reported as a dangling reference: %v", dangling)
			}
		})
	}
}

func TestRewriteReferencePaths(t *testing.T) {
	lr := testResolver()
	in := "Full detail: `references/beds-and-r10.md`."
	want := "Full detail: [`references/beds-and-r10.md`](/recipes/check/check/beds-and-r10/)."
	got, dangling := lr.rewrite(in, "check", "check", "test.md")
	if len(dangling) != 0 {
		t.Fatalf("unexpected dangling refs: %v", dangling)
	}
	if got != want {
		t.Errorf("rewrite:\n got: %q\nwant: %q", got, want)
	}
}

// TestDanglingReferenceIsReported is the check that makes the docs build a corpus-wide integrity
// gate: an unresolvable reference must FAIL rather than emit a silently broken link.
func TestDanglingReferenceIsReported(t *testing.T) {
	lr := testResolver()
	got, dangling := lr.rewrite("see `/charly-internals:no-such-skill`", "check", "check", "test.md")
	if len(dangling) != 1 {
		t.Fatalf("expected 1 dangling ref, got %d (%v)", len(dangling), dangling)
	}
	if dangling[0].Ref != "/charly-internals:no-such-skill" {
		t.Errorf("unexpected ref: %q", dangling[0].Ref)
	}
	if got != "see `/charly-internals:no-such-skill`" {
		t.Errorf("dangling reference should be left as authored, got %q", got)
	}
	if err := danglingError(dangling); err == nil {
		t.Error("danglingError returned nil for a dangling reference")
	}
}

func TestDanglingErrorNilWhenClean(t *testing.T) {
	if err := danglingError(nil); err != nil {
		t.Errorf("expected nil error for a clean corpus, got %v", err)
	}
}
