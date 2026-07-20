package main

import "testing"

// The venue transfer tarball name must be unique per deploy invocation: two
// concurrent deploys of the same candy to the same venue must never share it
// (the second PutFile would overwrite the first's tarball before its extract
// runs).
func TestVenueBuilderTarNameUniquePerScope(t *testing.T) {
	a := venueBuilderTarName("mycandy", "charly-venue-builder-tar-aaa111")
	b := venueBuilderTarName("mycandy", "charly-venue-builder-tar-bbb222")
	if a == b {
		t.Fatalf("same candy in different scopes must not share a venue tarball name: %q", a)
	}
	if got := venueBuilderTarName("mycandy", "charly-venue-builder-tar-aaa111"); got != a {
		t.Fatalf("same scope must be stable: %q vs %q", got, a)
	}
	want := "/tmp/charly-builder-mycandy-charly-venue-builder-tar-aaa111.tar.gz"
	if a != want {
		t.Fatalf("unexpected name: got %q want %q", a, want)
	}
	if other := venueBuilderTarName("othercandy", "charly-venue-builder-tar-aaa111"); other == a {
		t.Fatalf("different candies must not share a venue tarball name: %q", other)
	}
}
