package exec

import (
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/spec/spec"
	"github.com/opencharly/spec/testkit"
)

func TestShellExecutorStartProcessPreservesArgvAndPipes(t *testing.T) {
	p, err := (ShellExecutor{}).StartProcess(context.Background(), spec.ProcessLaunch{Argv: []string{"sh", "-c", `IFS= read -r line; printf '%s|%s|%s' "$1" "$line" "$CHARLY_PROCESS_TEST"; printf diagnostic >&2`, "sh", "space $' literal"}, Env: spec.StrMap{"CHARLY_PROCESS_TEST": "environment value"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(p.Stdin(), "payload with spaces\n"); err != nil {
		t.Fatal(err)
	}
	_ = p.Stdin().Close()
	out, err := io.ReadAll(p.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	diagnostic, err := io.ReadAll(p.Stderr())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), "space $' literal|payload with spaces|environment value"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := string(diagnostic); got != "diagnostic" {
		t.Fatalf("stderr = %q", got)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
}

func TestSSHProcessExecutorsPreserveRemoteLaunchAndPipes(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("OpenSSH client unavailable")
	}
	server := testkit.StartSSHProcessServer(t, func(command string) *exec.Cmd {
		return exec.Command("sh", "-c", command)
	})
	t.Setenv("HOME", server.Home)
	remoteDir := t.TempDir()
	sshOptions := []string{
		"-i", server.IdentityFile,
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
	}
	launch := spec.ProcessLaunch{
		Argv:       []string{"sh", "-c", `IFS= read -r line; printf '%s|%s|%s|%s' "$1" "$line" "$CHARLY_PROCESS_TEST" "$PWD"; printf diagnostic >&2`, "sh", "space $' literal"},
		WorkingDir: remoteDir,
		Env:        spec.StrMap{"CHARLY_PROCESS_TEST": "environment value"},
	}
	tests := []struct {
		name  string
		start func(context.Context, spec.ProcessLaunch) (spec.Process, error)
	}{
		{
			name: "direct",
			start: (&SSHExecutor{
				User: "agent", Host: server.Address, Port: int(server.Port), Args: sshOptions,
			}).StartProcess,
		},
		{
			name: "nested",
			start: (&NestedExecutor{
				Parent: ShellExecutor{},
				Jump: NestedJump{
					Kind: JumpSSH, Target: "agent@" + server.Address,
					ExtraArgs: append([]string{"-p", strconv.FormatInt(server.Port, 10)}, sshOptions...),
				},
			}).StartProcess,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Bounded: this subtest drives the real OpenSSH client over a real
			// TCP loopback connection, unlike every other StartProcess test in
			// this file (which stay fully local and synchronous). Without a
			// deadline here, a stalled handshake or a stuck exchange on the
			// in-process x/crypto/ssh server has nothing to unblock it — the
			// goroutine parks in startCommandProcess's cmd.Wait() until Go's
			// own default test-binary panic (10m) finally kills the run. A
			// bounded context makes exec.CommandContext kill the ssh process
			// and fail the test promptly instead, mirroring the deadline every
			// sibling real-subprocess test in this module already carries
			// (transport.TestGRPCOverRealOpenSSH, TestGRPCOverProcessTransports).
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			process, err := tc.start(ctx, launch)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(process.Stdin(), "payload with spaces\n"); err != nil {
				t.Fatal(err)
			}
			_ = process.Stdin().Close()
			stdout, err := io.ReadAll(process.Stdout())
			if err != nil {
				t.Fatal(err)
			}
			stderr, err := io.ReadAll(process.Stderr())
			if err != nil {
				t.Fatal(err)
			}
			if err := process.Wait(); err != nil {
				t.Fatal(err)
			}
			want := "space $' literal|payload with spaces|environment value|" + remoteDir
			if got := string(stdout); got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
			if got := string(stderr); got != "diagnostic" {
				t.Fatalf("stderr = %q, want diagnostic", got)
			}
			if err := process.Close(); err != nil {
				t.Fatalf("idempotent close: %v", err)
			}
		})
	}
}

type recordingProcessExecutor struct{ argv []string }

func (r *recordingProcessExecutor) StartProcess(_ context.Context, launch spec.ProcessLaunch) (spec.Process, error) {
	r.argv = append([]string(nil), launch.Argv...)
	return nil, nil
}

type processOnlyDeployExecutor struct {
	spec.DeployExecutor
	*recordingProcessExecutor
}

func TestNestedExecutorStartProcessComposesEveryHopAsArgv(t *testing.T) {
	recorder := &recordingProcessExecutor{}
	root := &processOnlyDeployExecutor{recordingProcessExecutor: recorder}
	container := &NestedExecutor{Parent: root, Jump: NestedJump{Kind: JumpPodmanExec, Target: "box", ExtraArgs: []string{"--env", "A=B C"}}}
	leaf := &NestedExecutor{Parent: container, Jump: NestedJump{Kind: JumpSSH, Target: "agent@inner"}}
	_, err := leaf.StartProcess(context.Background(), spec.ProcessLaunch{Argv: []string{"charly", "__agent-target", "serve", "--stdio"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"podman", "exec", "-i", "--env", "A=B C", "box", "ssh", "-T", "-o", "LogLevel=ERROR", "agent@inner", "'charly' '__agent-target' 'serve' '--stdio'"}
	if got := strings.Join(recorder.argv, "\x00"); got != strings.Join(want, "\x00") {
		t.Fatalf("argv = %#v, want %#v", recorder.argv, want)
	}
}

func TestNestedExecutorStartProcessRejectsVirshConsole(t *testing.T) {
	root := &processOnlyDeployExecutor{recordingProcessExecutor: &recordingProcessExecutor{}}
	nested := &NestedExecutor{Parent: root, Jump: NestedJump{Kind: JumpVirshConsole, Target: "vm"}}
	if _, err := nested.StartProcess(context.Background(), spec.ProcessLaunch{Argv: []string{"charly"}}); err == nil || !strings.Contains(err.Error(), spec.ErrNotSupported.Error()) {
		t.Fatalf("error = %v, want ErrNotSupported", err)
	}
}
