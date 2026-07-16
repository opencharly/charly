package check

import "testing"

func TestFindCharlyForCheckPrefersHostBinary(t *testing.T) {
	t.Setenv("CHARLY_BIN", "/tmp/fresh-charly")
	if got := findCharlyForCheck(); got != "/tmp/fresh-charly" {
		t.Fatalf("findCharlyForCheck() = %q, want host CHARLY_BIN", got)
	}
}
