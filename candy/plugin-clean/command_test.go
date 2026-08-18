package clean

import (
	"testing"
)

// TestCleanCategories covers the --images/--check/--deep flag-resolution logic: the pre-existing
// "any one of --images/--check given alone suppresses the other default categories" behavior stays
// unchanged, and --deep NEVER fires implicitly on a plain `charly clean` (R5: no default-behavior
// change) but joins the same "an explicit category was given" gate as --images/--check.
func TestCleanCategories(t *testing.T) {
	cases := []struct {
		name                            string
		images, check, deep             bool
		wantImages, wantCheck, wantDeep bool
	}{
		{name: "no flags: full default sweep, deep excluded",
			images: false, check: false, deep: false,
			wantImages: true, wantCheck: true, wantDeep: false},
		{name: "--images alone: only images",
			images: true, check: false, deep: false,
			wantImages: true, wantCheck: false, wantDeep: false},
		{name: "--check alone: only check",
			images: false, check: true, deep: false,
			wantImages: false, wantCheck: true, wantDeep: false},
		{name: "--images + --check: both",
			images: true, check: true, deep: false,
			wantImages: true, wantCheck: true, wantDeep: false},
		{name: "--deep alone: only deep",
			images: false, check: false, deep: true,
			wantImages: false, wantCheck: false, wantDeep: true},
		{name: "--deep + --images: both, check excluded",
			images: true, check: false, deep: true,
			wantImages: true, wantCheck: false, wantDeep: true},
		{name: "--deep + --check: both, images excluded",
			images: false, check: true, deep: true,
			wantImages: false, wantCheck: true, wantDeep: true},
		{name: "all three flags: all categories",
			images: true, check: true, deep: true,
			wantImages: true, wantCheck: true, wantDeep: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotImages, gotCheck, gotDeep := cleanCategories(c.images, c.check, c.deep)
			if gotImages != c.wantImages || gotCheck != c.wantCheck || gotDeep != c.wantDeep {
				t.Errorf("cleanCategories(%v,%v,%v) = (%v,%v,%v), want (%v,%v,%v)",
					c.images, c.check, c.deep,
					gotImages, gotCheck, gotDeep,
					c.wantImages, c.wantCheck, c.wantDeep)
			}
		})
	}
}
