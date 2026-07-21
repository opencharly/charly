package main

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// package plugin verb — installed-present, version match, and absent-as-expected paths.
// Now an extracted state-provision (typed-step) verb, a dedicated builtin plugin unit
// dispatched IN-PROCESS via the CheckVerbProvider RunVerb path (TestMain loads its schema);
// authored as plugin: package + plugin_input (package + installed/version/package_map). The
// package/installed/version/package_map fields left the closed #Op.
func TestRunner_Package(t *testing.T) {
	t.Run("installed via rpm", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "rpm -q 'redis'", stdout: "INSTALLED", exit: 0},
		}
		res := r.Run(context.Background(), []spec.Op{
			{Plugin: "package", PluginInput: map[string]any{"package": "redis", "installed": true}},
		})
		if res[0].Status != spec.StatusPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})

	t.Run("installed mismatch", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "rpm -q 'redis'", stdout: "ABSENT", exit: 0},
		}
		res := r.Run(context.Background(), []spec.Op{
			{Plugin: "package", PluginInput: map[string]any{"package": "redis", "installed": true}},
		})
		if res[0].Status != spec.StatusFail {
			t.Errorf("expected fail, got %+v", res[0])
		}
	})

	t.Run("version list match", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "rpm -q 'redis' >/dev/null", stdout: "INSTALLED", exit: 0},
			{matchPrefix: "rpm -q --qf '%{VERSION}", stdout: "7.0.5\n", exit: 0},
		}
		res := r.Run(context.Background(), []spec.Op{
			{Plugin: "package", PluginInput: map[string]any{"package": "redis", "version": []any{"7.0.5", "7.0.6"}}},
		})
		if res[0].Status != spec.StatusPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})
}

// service plugin verb — running true/false. Now an extracted state-provision verb, a
// dedicated builtin plugin unit dispatched IN-PROCESS via the CheckVerbProvider RunVerb
// path (TestMain loads its schema); authored as plugin: service + plugin_input (service +
// running/enabled). The service/running/enabled fields left the closed #Op.
func TestRunner_Service(t *testing.T) {
	t.Run("running true via supervisorctl", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "supervisorctl status 'jupyter'", exit: 0},
		}
		res := r.Run(context.Background(), []spec.Op{
			{Plugin: "service", PluginInput: map[string]any{"service": "jupyter", "running": true}},
		})
		if res[0].Status != spec.StatusPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})

	t.Run("running mismatch", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "supervisorctl status 'jupyter'", exit: 1},
		}
		res := r.Run(context.Background(), []spec.Op{
			{Plugin: "service", PluginInput: map[string]any{"service": "jupyter", "running": true}},
		})
		if res[0].Status != spec.StatusFail {
			t.Errorf("expected fail, got %+v", res[0])
		}
	})
}

// process plugin verb — pgrep exit status, now a dedicated built-in plugin unit
// dispatched IN-PROCESS via the CheckVerbProvider RunVerb path (keeping the live
// *Runner the pgrep probe needs). Authored as plugin: process + plugin_input
// (TestMain loads its schema).
func TestRunner_ProcessPlugin(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{{matchPrefix: "pgrep -x 'redis-server'", exit: 0}}
		res := r.Run(context.Background(), []spec.Op{
			{Plugin: "process", PluginInput: map[string]any{"process": "redis-server"}},
		})
		if res[0].Status != spec.StatusPass {
			t.Errorf("got %+v", res[0])
		}
	})
	t.Run("expected absent", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{{matchPrefix: "pgrep -x 'worm'", exit: 1}}
		res := r.Run(context.Background(), []spec.Op{
			{Plugin: "process", PluginInput: map[string]any{"process": "worm", "running": false}},
		})
		if res[0].Status != spec.StatusPass {
			t.Errorf("got %+v", res[0])
		}
	})
}

// dns plugin verb — host-side resolution for a guaranteed-resolvable hostname,
// and expected-unresolvable for a never-assigned TLD. Now a dedicated built-in plugin
// unit dispatched IN-PROCESS via the CheckVerbProvider RunVerb path (TestMain loads its
// schema); authored as plugin: dns + plugin_input.
func TestRunner_DNSPlugin(t *testing.T) {
	t.Run("resolvable localhost", func(t *testing.T) {
		r, _ := newFakeRunner(t, RunModeLive)
		res := r.Run(context.Background(), []spec.Op{
			{Plugin: "dns", PluginInput: map[string]any{"dns": "localhost"}},
		})
		if res[0].Status != spec.StatusPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})
	t.Run("unresolvable as expected", func(t *testing.T) {
		r, _ := newFakeRunner(t, RunModeLive)
		res := r.Run(context.Background(), []spec.Op{
			{Plugin: "dns", PluginInput: map[string]any{"dns": "this-host-will-never-exist.invalid", "resolvable": false}},
		})
		if res[0].Status != spec.StatusPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})
}

// user verb — getent output parsing. Now an extracted state-provision verb, a dedicated
// builtin plugin unit dispatched IN-PROCESS via the CheckVerbProvider RunVerb path (TestMain
// loads its schema); authored as plugin: user + plugin_input.
func TestRunner_User(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeLive)
	fake.responses = []fakeResponse{
		{matchPrefix: "getent passwd 'alice'", stdout: "alice:x:1000:1000:Alice:/home/alice:/bin/bash\n", exit: 0},
	}
	res := r.Run(context.Background(), []spec.Op{
		{Plugin: "user", PluginInput: map[string]any{"user": "alice", "uid": 1000, "home": "/home/alice"}},
	})
	if res[0].Status != spec.StatusPass {
		t.Errorf("expected pass, got %+v", res[0])
	}
}

func TestRunner_User_UIDMismatch(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeLive)
	fake.responses = []fakeResponse{
		{matchPrefix: "getent passwd 'alice'", stdout: "alice:x:1500:1500:Alice:/home/alice:/bin/bash\n", exit: 0},
	}
	res := r.Run(context.Background(), []spec.Op{
		{Plugin: "user", PluginInput: map[string]any{"user": "alice", "uid": 1000}},
	})
	if res[0].Status != spec.StatusFail {
		t.Errorf("expected fail, got %+v", res[0])
	}
}

// unix_group verb — getent group parsing. Now the FIRST extracted state-provision verb,
// a dedicated builtin plugin unit dispatched IN-PROCESS via the CheckVerbProvider RunVerb
// path (TestMain loads its schema); authored as plugin: unix_group + plugin_input.
func TestRunner_Group(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeLive)
	fake.responses = []fakeResponse{
		{matchPrefix: "getent group 'docker'", stdout: "docker:x:999:alice,bob\n", exit: 0},
	}
	res := r.Run(context.Background(), []spec.Op{
		{Plugin: "unix_group", PluginInput: map[string]any{"unix_group": "docker", "gid": 999}},
	})
	if res[0].Status != spec.StatusPass {
		t.Errorf("got %+v", res[0])
	}
}

// interface plugin verb — presence + MTU check. Now a dedicated built-in plugin unit
// dispatched IN-PROCESS via the CheckVerbProvider RunVerb path (TestMain loads its
// schema); authored as plugin: interface + plugin_input.
func TestRunner_Interface(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeLive)
	fake.responses = []fakeResponse{
		{matchPrefix: "ip -o addr show 'eth0'", stdout: "2: eth0 inet 10.0.0.5/24\n", exit: 0},
		{matchPrefix: "ip -o link show 'eth0'", stdout: "1500\n", exit: 0},
	}
	res := r.Run(context.Background(), []spec.Op{
		{Plugin: "interface", PluginInput: map[string]any{"interface": "eth0", "mtu": 1500, "addrs": []any{"10.0.0.5/24"}}},
	})
	if res[0].Status != spec.StatusPass {
		t.Errorf("got %+v", res[0])
	}
}

// kernel-param verb — scalar match via the value matcher. Now an extracted state-provision
// verb, a dedicated builtin plugin unit dispatched IN-PROCESS via the CheckVerbProvider
// RunVerb path (TestMain loads its schema); authored as plugin: kernel-param + plugin_input.
// The CHECK reads /proc/sys/<key-as-slashes> directly (no procps-ng `sysctl` dependency).
func TestRunner_KernelParam(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeLive)
	fake.responses = []fakeResponse{
		{matchPrefix: "cat '/proc/sys/net/ipv4/ip_forward'", stdout: "1\n", exit: 0},
	}
	res := r.Run(context.Background(), []spec.Op{
		{Plugin: "kernel-param", PluginInput: map[string]any{"kernel-param": "net.ipv4.ip_forward", "value": "1"}},
	})
	if res[0].Status != spec.StatusPass {
		t.Errorf("got %+v", res[0])
	}
}

// mount verb — findmnt parsing + filesystem check. Now an extracted state-provision verb,
// a dedicated builtin plugin unit dispatched IN-PROCESS via the CheckVerbProvider RunVerb
// path (TestMain loads its schema); authored as plugin: mount + plugin_input.
func TestRunner_Mount(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeLive)
	fake.responses = []fakeResponse{
		{matchPrefix: "findmnt -n -o SOURCE,FSTYPE,OPTIONS '/data'",
			stdout: "/dev/sda1 ext4 rw,relatime\n", exit: 0},
	}
	res := r.Run(context.Background(), []spec.Op{
		{Plugin: "mount", PluginInput: map[string]any{"mount": "/data", "filesystem": "ext4", "mount_source": "/dev/sda1"}},
	})
	if res[0].Status != spec.StatusPass {
		t.Errorf("got %+v", res[0])
	}
}

// addr plugin verb — host-side dial against a real httptest listener. Now a dedicated
// built-in plugin unit dispatched IN-PROCESS via the CheckVerbProvider RunVerb path
// (TestMain loads its schema); authored as plugin: addr + plugin_input.
func TestRunner_Addr(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()
	// Extract host:port from the test server URL.
	u := strings.TrimPrefix(srv.URL, "http://")

	r, _ := newFakeRunner(t, RunModeLive)
	res := r.Run(context.Background(), []spec.Op{{Plugin: "addr", PluginInput: map[string]any{"addr": u}}})
	if res[0].Status != spec.StatusPass {
		t.Errorf("expected reachable, got %+v", res[0])
	}

	// Unreachable — pick a high port nothing is on. net.Listen gives us one safely.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close() // free the port
	res = r.Run(context.Background(), []spec.Op{
		{Plugin: "addr", PluginInput: map[string]any{"addr": addr, "reachable": false}},
	})
	if res[0].Status != spec.StatusPass {
		t.Errorf("expected unreachable-as-expected, got %+v", res[0])
	}
}

// matching plugin verb — pure in-process value matching, now a dedicated built-in
// plugin unit dispatched through the provider registry (TestMain loads its schema).
func TestRunner_MatchingPlugin(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeLive)
	res := r.Run(context.Background(), []spec.Op{
		{Plugin: "matching", PluginInput: map[string]any{
			"matching": "hello world",
			"contains": map[string]any{"contains": "world"},
		}},
	})
	if res[0].Status != spec.StatusPass {
		t.Errorf("got %+v", res[0])
	}
}
