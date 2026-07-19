package main

import (
	"reflect"
	"testing"
)

func TestSSHCmdArgsRendersDocumentedPortForms(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		want         []string
	}{
		{name: "hostname", target: "agent@box.example:2222", want: []string{"-p", "2222", "agent@box.example", "'charly' 'agent' 'runtime' 'list'"}},
		{name: "ipv4", target: "127.0.0.1:22022", want: []string{"-p", "22022", "127.0.0.1", "'charly' 'agent' 'runtime' 'list'"}},
		{name: "ipv6", target: "agent@[::1]:2222", want: []string{"-p", "2222", "agent@[::1]", "'charly' 'agent' 'runtime' 'list'"}},
		{name: "no-port", target: "agent@box.example", want: []string{"agent@box.example", "'charly' 'agent' 'runtime' 'list'"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sshCmdArgs(tc.target, []string{"agent", "runtime", "list"})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ssh args = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestSSHCmdArgsCarriesExplicitConnectionPolicyAndQuotesRemoteArguments(t *testing.T) {
	got, err := sshCmdArgsWithEndpoint("user@box:2222", "/tmp/charly endpoint", "/keys/id", []string{"StrictHostKeyChecking=no"}, []string{"agent", "value with spaces"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-p", "2222", "-i", "/keys/id", "-o", "StrictHostKeyChecking=no", "user@box", "'/tmp/charly endpoint' 'agent' 'value with spaces'"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ssh args = %#v, want %#v", got, want)
	}
}

func TestSSHCmdArgsRejectsInvalidPorts(t *testing.T) {
	for _, target := range []string{"box:0", "box:70000", "box:not-a-port", "user@"} {
		if _, err := sshCmdArgs(target, nil); err == nil {
			t.Errorf("ssh target %q unexpectedly passed", target)
		}
	}
}
