package proc

import (
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

func TestSortedEnvPairs(t *testing.T) {
	got := SortedEnvPairs(map[string]string{"B": "two words", "A": "1", "C": ""})
	want := []string{"A=1", "B=two words", "C="}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("SortedEnvPairs = %#v, want %#v", got, want)
	}
	if out := SortedEnvPairs(nil); len(out) != 0 {
		t.Fatalf("SortedEnvPairs(nil) = %#v, want empty", out)
	}
}

func TestRemoteLaunchCommand(t *testing.T) {
	tests := []struct {
		name   string
		launch spec.ProcessLaunch
		want   string
	}{
		{
			name:   "argv only",
			launch: spec.ProcessLaunch{Argv: []string{"charly", "__agent-target", "serve", "--stdio"}},
			want:   "'charly' '__agent-target' 'serve' '--stdio'",
		},
		{
			name: "working dir and env stay target-side",
			launch: spec.ProcessLaunch{
				Argv:       []string{"run", "a b"},
				WorkingDir: "/work dir",
				Env:        map[string]string{"TOKEN": "a b'$", "A": "1"},
			},
			want: `cd '/work dir' && env 'A=1' 'TOKEN=a b'\''$' 'run' 'a b'`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RemoteLaunchCommand(tc.launch); got != tc.want {
				t.Fatalf("RemoteLaunchCommand = %q, want %q", got, tc.want)
			}
		})
	}
}
