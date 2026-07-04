package vm

import (
	"testing"
)

func TestExtractGraphicsSocketPaths(t *testing.T) {
	cases := []struct {
		name string
		xml  string
		want []string
	}{
		{
			name: "single socket listener (single-quoted)",
			xml: `<domain><devices><graphics type='spice'>
				<listen type='socket' socket='/home/u/.config/libvirt/qemu/lib/domain-7-vm/spice.sock'/>
			</graphics></devices></domain>`,
			want: []string{"/home/u/.config/libvirt/qemu/lib/domain-7-vm/spice.sock"},
		},
		{
			name: "double-quoted socket attr",
			xml:  `<listen type="socket" socket="/tmp/spice.sock"/>`,
			want: []string{"/tmp/spice.sock"},
		},
		{
			name: "address listener — not included",
			xml:  `<listen type='address' address='127.0.0.1'/>`,
			want: nil,
		},
		{
			name: "socket listener with no path (libvirt auto-allocates later)",
			xml:  `<listen type='socket'/>`,
			want: nil,
		},
		{
			name: "multiple listeners mixed",
			xml: `<graphics type='spice'>
				<listen type='socket' socket='/tmp/a.sock'/>
				<listen type='address' address='127.0.0.1'/>
				<listen type='socket' socket='/tmp/b.sock'/>
			</graphics>`,
			want: []string{"/tmp/a.sock", "/tmp/b.sock"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractGraphicsSocketPaths(tc.xml)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d paths, want %d: got=%v want=%v", len(got), len(tc.want), got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("path[%d]: got %q want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
