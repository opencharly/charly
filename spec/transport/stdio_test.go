package transport

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
	"github.com/opencharly/spec/testkit"
)

type echoProvider struct{ pb.UnimplementedProviderServer }

func (echoProvider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetClass() == "fixture-state" {
		workingDir, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		result, err := json.Marshal(struct {
			WorkingDir  string `json:"working_dir"`
			Environment string `json:"environment"`
		}{WorkingDir: workingDir, Environment: os.Getenv("CHARLY_TARGETKIT_REMOTE_SENTINEL")})
		if err != nil {
			return nil, err
		}
		return &pb.InvokeReply{ResultJson: result}, nil
	}
	return &pb.InvokeReply{ResultJson: append([]byte("echo:"), req.GetParamsJson()...)}, nil
}

func TestTargetkitHelperProcess(t *testing.T) {
	if os.Getenv("CHARLY_TARGETKIT_HELPER") != "1" {
		return
	}
	err := ServeStdio(os.Stdin, os.Stdout, func(server *grpc.Server) {
		pb.RegisterProviderServer(server, echoProvider{})
	})
	if err != nil {
		_, _ = io.WriteString(os.Stderr, err.Error())
		os.Exit(2)
	}
	os.Exit(0)
}

// TestGRPCOverProcessTransports exercises real HTTP/2 gRPC frames over both
// supported process transports. The SSH case replaces only the ssh executable
// with the test process; StdioCommand still has to construct and pass the exact
// fixed remote endpoint argv. This catches regressions hidden by argv-only unit
// tests.
func TestGRPCOverProcessTransports(t *testing.T) {
	execDir := t.TempDir()
	remoteOnlyDir := filepath.Join(t.TempDir(), "remote-only-missing")
	const localEnvKey = "CHARLY_TARGETKIT_LOCAL_SENTINEL"
	const remoteEnvKey = "CHARLY_TARGETKIT_REMOTE_ONLY_SENTINEL"
	cases := []struct {
		name         string
		target       spec.TargetSpec
		check        func(*testing.T, string, []string)
		wantDir      string
		wantEnvKey   string
		wantEnvValue string
		forbiddenEnv string
	}{
		{
			name: "exec",
			target: spec.TargetSpec{WorkingDir: execDir, Hops: []spec.TargetHop{
				{Transport: "exec", Env: spec.StrMap{localEnvKey: "local"}},
				{Transport: "grpc"},
			}},
			check: func(t *testing.T, name string, args []string) {
				if name != "charly" || !reflect.DeepEqual(args, []string{"__agent-target", "serve", "--stdio"}) {
					t.Fatalf("exec endpoint = %q %q", name, args)
				}
			},
			wantDir:      execDir,
			wantEnvKey:   localEnvKey,
			wantEnvValue: "local",
		},
		{
			name: "ssh",
			target: spec.TargetSpec{WorkingDir: remoteOnlyDir, Hops: []spec.TargetHop{
				{Transport: "ssh", Address: "box", User: "agent", Env: spec.StrMap{remoteEnvKey: "remote"}},
				{Transport: "grpc"},
				{Transport: "tmux"},
			}},
			check: func(t *testing.T, name string, args []string) {
				remote := "cd " + spec.ShellQuote(remoteOnlyDir) + " && env " + spec.ShellQuote(remoteEnvKey+"=remote") + " 'charly' '__agent-target' 'serve' '--stdio'"
				want := []string{"-T", "agent@box", remote}
				if name != "ssh" || !reflect.DeepEqual(args, want) {
					t.Fatalf("ssh endpoint = %q %q, want ssh %q", name, args, want)
				}
			},
			forbiddenEnv: remoteEnvKey + "=remote",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var launched *exec.Cmd
			conn, client, err := DialProvider(ctx, tc.target, DialOptions{
				Command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
					tc.check(t, name, args)
					cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTargetkitHelperProcess$")
					cmd.Env = append(os.Environ(), "CHARLY_TARGETKIT_HELPER=1", localEnvKey+"=factory")
					launched = cmd
					return cmd
				},
			})
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			if launched == nil {
				t.Fatal("dial did not construct a carrier process")
			}
			if launched.Dir != tc.wantDir {
				t.Fatalf("local carrier cwd = %q, want %q", launched.Dir, tc.wantDir)
			}
			if !containsEnv(launched.Env, "CHARLY_TARGETKIT_HELPER=1") {
				t.Fatal("target env overlay discarded the command factory environment")
			}
			if tc.wantEnvKey != "" {
				if got := effectiveEnv(launched.Env, tc.wantEnvKey); got != tc.wantEnvValue {
					t.Fatalf("local carrier env %s = %q, want %q", tc.wantEnvKey, got, tc.wantEnvValue)
				}
			}
			if tc.forbiddenEnv != "" && containsEnv(launched.Env, tc.forbiddenEnv) {
				t.Fatalf("remote-only env leaked to local carrier: %q", tc.forbiddenEnv)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Errorf("close target connection: %v", err)
				}
			})
			reply, err := client.Invoke(ctx, &pb.InvokeRequest{Class: "fixture", Reserved: "echo", ParamsJson: []byte("payload")})
			if err != nil {
				t.Fatalf("invoke: %v", err)
			}
			if got, want := string(reply.GetResultJson()), "echo:payload"; got != want {
				t.Fatalf("reply = %q, want %q", got, want)
			}
		})
	}
}

func containsEnv(env []string, want string) bool {
	for _, pair := range env {
		if pair == want {
			return true
		}
	}
	return false
}

func effectiveEnv(env []string, key string) string {
	prefix := key + "="
	value := ""
	for _, pair := range env {
		if strings.HasPrefix(pair, prefix) {
			value = strings.TrimPrefix(pair, prefix)
		}
	}
	return value
}

func TestGRPCOverRealOpenSSH(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("OpenSSH client unavailable")
	}
	server := testkit.StartSSHProcessServer(t, func(command string) *exec.Cmd {
		return exec.Command("sh", "-c", command)
	})
	t.Setenv("HOME", server.Home)
	remoteDir := t.TempDir()
	remoteCharly := filepath.Join(t.TempDir(), "charly")
	remoteScript := "#!/bin/sh\nexec " + spec.ShellQuote(os.Args[0]) + " -test.run=^TestTargetkitHelperProcess$\n"
	if err := os.WriteFile(remoteCharly, []byte(remoteScript), 0o700); err != nil {
		t.Fatal(err)
	}
	const remoteValue = "remote value with spaces"
	target := spec.TargetSpec{WorkingDir: remoteDir, Hops: []spec.TargetHop{
		{Transport: "ssh", Address: server.Address, User: "agent", Port: server.Port, IdentityFile: server.IdentityFile, Env: spec.StrMap{
			"CHARLY_TARGETKIT_HELPER":          "1",
			"CHARLY_TARGETKIT_REMOTE_SENTINEL": remoteValue,
		}, Options: spec.StrMap{
			"IdentitiesOnly": "yes", "LogLevel": "ERROR", "StrictHostKeyChecking": "no", "UserKnownHostsFile": "/dev/null",
		}},
		{Transport: "grpc"},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var launched *exec.Cmd
	conn, client, err := DialProvider(ctx, target, DialOptions{
		RemoteCharlyBinary: remoteCharly,
		Stderr:             io.Discard,
		Command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Env = append(os.Environ(), "CHARLY_TARGETKIT_FACTORY_SENTINEL=preserved")
			launched = cmd
			return cmd
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if launched == nil {
		t.Fatal("real OpenSSH carrier was not launched")
	}
	if launched.Dir != "" {
		t.Fatalf("remote working directory leaked to local OpenSSH carrier: %q", launched.Dir)
	}
	if got := effectiveEnv(launched.Env, "CHARLY_TARGETKIT_REMOTE_SENTINEL"); got != "" {
		t.Fatalf("remote environment leaked to local OpenSSH carrier: %q", got)
	}
	if got := effectiveEnv(launched.Env, "CHARLY_TARGETKIT_FACTORY_SENTINEL"); got != "preserved" {
		t.Fatalf("OpenSSH command factory environment = %q, want preserved", got)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close SSH target connection: %v", err)
		}
	})
	reply, err := client.Invoke(ctx, &pb.InvokeRequest{Class: "fixture-state", Reserved: "echo"})
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		WorkingDir  string `json:"working_dir"`
		Environment string `json:"environment"`
	}
	if err := json.Unmarshal(reply.GetResultJson(), &state); err != nil {
		t.Fatal(err)
	}
	if state.WorkingDir != remoteDir {
		t.Fatalf("remote working directory = %q, want %q", state.WorkingDir, remoteDir)
	}
	if state.Environment != remoteValue {
		t.Fatalf("remote environment = %q, want %q", state.Environment, remoteValue)
	}
}

func TestSSHGRPCCommandIsFixedArgv(t *testing.T) {
	target := spec.TargetSpec{Hops: []spec.TargetHop{
		{Transport: "ssh", Address: "box.example", User: "agent", Port: 2222, IdentityFile: "/keys/id"},
		{Transport: "grpc"},
		{Transport: "tmux"},
	}}
	got, err := StdioCommand(target, DialOptions{CharlyBinary: "/usr/bin/charly"})
	if err != nil {
		t.Fatal(err)
	}
	// CharlyBinary is a controller-local path and must never be sent to the
	// remote box. The remote endpoint resolves its own installation via PATH.
	want := []string{"ssh", "-T", "-p", "2222", "-i", "/keys/id", "agent@box.example", "'charly' '__agent-target' 'serve' '--stdio'"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
}

func TestSSHGRPCCommandUsesReplicatedRemoteEndpoint(t *testing.T) {
	target := spec.TargetSpec{Hops: []spec.TargetHop{
		{Transport: "ssh", Address: "box.example", User: "agent"},
		{Transport: "grpc"},
		{Transport: "tmux"},
	}}
	got, err := StdioCommand(target, DialOptions{
		CharlyBinary:       "/controller/worktree/bin/charly",
		RemoteCharlyBinary: "/tmp/charly-2026.200.1200",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ssh", "-T", "agent@box.example", "'/tmp/charly-2026.200.1200' '__agent-target' 'serve' '--stdio'"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
}

func TestSSHCommandRendersWorkingDirectoryAndEnvironment(t *testing.T) {
	target := spec.TargetSpec{WorkingDir: "/work dir", Hops: []spec.TargetHop{{Transport: "ssh", Address: "box", Env: spec.StrMap{"TOKEN": "a b'$"}}, {Transport: "grpc"}}}
	got, err := StdioCommand(target, DialOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantRemote := "cd '/work dir' && env 'TOKEN=a b'\\''$' 'charly' '__agent-target' 'serve' '--stdio'"
	if got[len(got)-1] != wantRemote {
		t.Fatalf("remote command = %q, want %q", got[len(got)-1], wantRemote)
	}
}

func TestGenericRouteCrossProduct(t *testing.T) {
	routes := []spec.TargetSpec{
		{Hops: []spec.TargetHop{{Transport: "exec"}, {Transport: "grpc"}}},
		{Hops: []spec.TargetHop{{Transport: "exec"}, {Transport: "grpc"}, {Transport: "tmux"}}},
		{Hops: []spec.TargetHop{{Transport: "ssh", Address: "box"}, {Transport: "grpc"}}},
		{Hops: []spec.TargetHop{{Transport: "ssh", Address: "box"}, {Transport: "grpc"}, {Transport: "exec"}, {Transport: "grpc"}, {Transport: "tmux"}}},
	}
	for _, route := range routes {
		if _, err := StdioCommand(route, DialOptions{}); err != nil {
			t.Fatalf("route %#v: %v", route.Hops, err)
		}
	}
}

func TestRouteRequiresGRPC(t *testing.T) {
	_, err := StdioCommand(spec.TargetSpec{Hops: []spec.TargetHop{{Transport: "ssh", Address: "box"}, {Transport: "tmux"}}}, DialOptions{})
	if err == nil {
		t.Fatal("route without grpc accepted")
	}
}
