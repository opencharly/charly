package main

import (
	"strings"
	"testing"

	"github.com/opencharly/charly/charly/plugin/kit"
	"github.com/opencharly/charly/charly/plugin/sdk"
	"github.com/opencharly/charly/charly/spec"
)

// methods_test.go covers the PLUGIN-side helpers ported out-of-process from charly/dbus.go
// (the deleted host-side DbusCmd): the typed-arg → GVariant conversion (ported from
// parseDbusTypedValue) and the required-modifier check that moved here from the host's
// former in-proc live-verb contract. The venue-driving methods (list/call/introspect/notify)
// need a live executor reverse channel and are exercised by the R10 bed (the sway-browser-vnc
// `dbus: list`), not these unit tests.

// TestGvariantArg mirrors the in-tree TestParseDbusArgs: the same `type:value` vocabulary,
// now converted to GVariant text for gdbus.
func TestGvariantArg(t *testing.T) {
	cases := []struct {
		input   string
		want    string // the GVariant token inside the shell single-quotes ("" when wantErr)
		wantErr bool
	}{
		{"string:hello", `"hello"`, false},
		{`string:say "hi"`, `"say \"hi\""`, false},
		{"uint32:42", "@u 42", false},
		{"int32:-1", "@i -1", false},
		{"boolean:true", "true", false},
		{"bool:false", "false", false},
		{"int64:9999999999", "@x 9999999999", false},
		{"uint64:0", "@t 0", false},
		{"double:3.14", "@d 3.14", false},
		{"invalid", "", true},           // no colon
		{"unknown:value", "", true},     // unsupported type
		{"uint32:notanumber", "", true}, // bad value
		{"int32:abc", "", true},         // bad value
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := gvariantArg(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for %q, got %q", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if want := kit.ShellQuote(tc.want); got != want {
				t.Errorf("gvariantArg(%q) = %q, want %q", tc.input, got, want)
			}
		})
	}
}

// TestGvariantArgsMultiple proves a mixed arg list converts in order.
func TestGvariantArgsMultiple(t *testing.T) {
	args := []string{"string:charly", "uint32:0", "string:title", "int32:-1"}
	got, err := gvariantArgs(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{kit.ShellQuote(`"charly"`), kit.ShellQuote("@u 0"), kit.ShellQuote(`"title"`), kit.ShellQuote("@i -1")}
	if len(got) != len(want) {
		t.Fatalf("expected %d tokens, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGvariantArgsEmpty(t *testing.T) {
	got, err := gvariantArgs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 tokens, got %d", len(got))
	}
}

// TestCheckRequiredModifiers mirrors the in-tree dbusMethods Required specs that moved here:
// call needs dest/path/method, introspect needs dest/path, notify needs text; list needs nothing.
func TestCheckRequiredModifiers(t *testing.T) {
	cases := []struct {
		method  string
		op      spec.Op
		wantErr string // substring; "" means no error
	}{
		{"list", spec.Op{Dbus: "list"}, ""},
		{"call", spec.Op{Dbus: "call"}, "dest"},
		{"call", spec.Op{Dbus: "call", Dest: "d", Path: "/p", Method: "m"}, ""},
		{"introspect", spec.Op{Dbus: "introspect"}, "dest"},
		{"introspect", spec.Op{Dbus: "introspect", Dest: "d", Path: "/p"}, ""},
		{"notify", spec.Op{Dbus: "notify"}, "text"},
		{"notify", spec.Op{Dbus: "notify", Text: "title"}, ""},
	}
	for _, tc := range cases {
		err := sdk.CheckRequiredModifiers(tc.method, &tc.op, requiredModifiers, modifierZero)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", tc.method, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: expected error containing %q, got %v", tc.method, tc.wantErr, err)
		}
	}
}
